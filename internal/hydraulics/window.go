package hydraulics

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wyw14/cry-96/internal/model"
)

// retainSamples bounds the raw per-generation buffer; model.LevelConfig
// rejects configured minimum sample counts above it.
const retainSamples = model.LevelWindowCapacity

// Stable evaluation reason codes, exposed verbatim to operators and clients.
const (
	reasonNoWindow     = "leveling observation window is not active for this transit"
	reasonForeign      = "water level evidence belongs to another transit generation"
	reasonInsufficient = "insufficient level samples"
	reasonAbnormal     = "abnormal level sample in observation window"
	reasonMissing      = "missing level sample in observation window"
	reasonStale        = "latest level observation exceeds freshness limit"
	reasonFuture       = "level sample timestamp is in the future"
	reasonSpan         = "observation time span shorter than configured minimum"
	reasonSwing        = "water level swing exceeds configured tolerance"
	reasonNotObserving = "chamber is not sealed for level observation"
)

// LevelWindow owns a bounded sample sequence for one transit generation.
// Samples from any earlier cycle or generation are rejected on Add and can
// never contribute to a stability decision.
type LevelWindow struct {
	mu         sync.RWMutex
	cycleID    uuid.UUID
	generation uint64
	samples    []model.LevelSample
}

func NewLevelWindow() *LevelWindow {
	return &LevelWindow{}
}

// Begin binds the window to a transit generation, discarding every sample
// that belonged to the previous binding.
func (w *LevelWindow) Begin(cycleID uuid.UUID, generation uint64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cycleID != cycleID || w.generation != generation {
		w.cycleID = cycleID
		w.generation = generation
		w.samples = nil
	}
}

// BeginFresh binds the window and always empties it, used when a new
// observation phase (leveling) starts within the same generation.
func (w *LevelWindow) BeginFresh(cycleID uuid.UUID, generation uint64) {
	w.mu.Lock()
	w.cycleID = cycleID
	w.generation = generation
	w.samples = nil
	w.mu.Unlock()
}

func (w *LevelWindow) Add(sample model.LevelSample) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cycleID == uuid.Nil {
		return errors.New("no active level window for transit")
	}
	if sample.CycleID != w.cycleID || sample.Generation != w.generation {
		return errors.New(reasonForeign)
	}
	if len(w.samples) > 0 {
		if sample.ObservedAt.Before(w.samples[len(w.samples)-1].ObservedAt) {
			return errors.New("sample observed_at is older than the latest window sample")
		}
	}
	w.samples = append(w.samples, sample)
	if len(w.samples) > retainSamples {
		w.samples = append([]model.LevelSample(nil), w.samples[len(w.samples)-retainSamples:]...)
	}
	return nil
}

func (w *LevelWindow) Identity() (uuid.UUID, uint64) {
	w.mu.RLock()
	id, generation := w.cycleID, w.generation
	w.mu.RUnlock()
	return id, generation
}

func (w *LevelWindow) Samples() []model.LevelSample {
	w.mu.RLock()
	result := append([]model.LevelSample(nil), w.samples...)
	w.mu.RUnlock()
	return result
}

// evaluation is the internal verdict computed under the window lock.
type evaluation struct {
	decision model.LevelDecision
	reason   string
	usable   int
	span     time.Duration
	swing    float64
	latest   *model.LevelSample
	age      time.Duration
}

// evaluate applies the chamber configuration against the most recent
// MinimumSamples samples. Any abnormal or missing reading in that set, a
// stale/missing latest reading, an insufficient span or an excessive swing
// yields Hold; only an all-usable, fresh, spanning, quiet set releases.
func (w *LevelWindow) evaluate(config model.LevelConfig, now time.Time) evaluation {
	w.mu.RLock()
	defer w.mu.RUnlock()

	result := evaluation{decision: model.LevelHold}
	if w.cycleID == uuid.Nil {
		result.reason = reasonNoWindow
		return result
	}

	if len(w.samples) < config.MinimumSamples {
		usable := 0
		for _, sample := range w.samples {
			if sample.Quality.Usable() {
				usable++
			}
		}
		result.usable = usable
		result.reason = fmt.Sprintf("%s: have %d, need %d", reasonInsufficient, len(w.samples), config.MinimumSamples)
		return result
	}

	window := append([]model.LevelSample(nil), w.samples[len(w.samples)-config.MinimumSamples:]...)

	latest := &window[len(window)-1]
	result.latest = latest
	result.age = now.Sub(latest.ObservedAt)
	if result.age < 0 {
		result.reason = fmt.Sprintf("%s: observed_at %s is ahead of evaluation time %s", reasonFuture, latest.ObservedAt.Format(time.RFC3339), now.Format(time.RFC3339))
		return result
	}
	if result.age > config.MaxObservationAge() {
		result.reason = fmt.Sprintf("%s: latest reading %.0fs old, limit %.0fs", reasonStale, result.age.Seconds(), config.MaxObservationAge().Seconds())
		return result
	}

	oldest := window[0].ObservedAt
	result.span = latest.ObservedAt.Sub(oldest)

	minimum, maximum := window[0].Meters, window[0].Meters
	usable := 0
	for _, sample := range window {
		if sample.Quality.Usable() {
			usable++
		}
		if sample.Meters < minimum {
			minimum = sample.Meters
		}
		if sample.Meters > maximum {
			maximum = sample.Meters
		}
	}
	result.usable = usable
	result.swing = maximum - minimum

	// Report the most recent bad reading first: it describes the current
	// sensor condition rather than an older glitch already superseded.
	for index := len(window) - 1; index >= 0; index-- {
		sample := window[index]
		switch sample.Quality {
		case model.QualityAbnormal:
			result.reason = fmt.Sprintf("%s at %s (%.3f m)", reasonAbnormal, sample.ObservedAt.Format(time.RFC3339), sample.Meters)
			return result
		case model.QualityMissing:
			result.reason = fmt.Sprintf("%s expected at %s", reasonMissing, sample.ObservedAt.Format(time.RFC3339))
			return result
		}
	}

	if result.span < config.MinimumSpan() {
		result.reason = fmt.Sprintf("%s: %.1fs observed, need %.1fs", reasonSpan, result.span.Seconds(), config.MinimumSpan().Seconds())
		return result
	}
	if result.swing > config.SwingToleranceM {
		result.reason = fmt.Sprintf("%s: %.3f m spread over %d samples, limit %.3f m", reasonSwing, result.swing, len(window), config.SwingToleranceM)
		return result
	}

	result.decision = model.LevelRelease
	return result
}
