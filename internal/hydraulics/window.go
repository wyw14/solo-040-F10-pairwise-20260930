package hydraulics

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wyw14/cry-96/internal/model"
)

// LevelWindow owns a bounded sample sequence for one transit generation.
type LevelWindow struct {
	mu         sync.RWMutex
	cycleID    uuid.UUID
	generation uint64
	capacity   int
	samples    []model.LevelSample
}

func NewLevelWindow(capacity int) (*LevelWindow, error) {
	if capacity < 3 {
		return nil, errors.New("level window capacity must be at least three")
	}
	return &LevelWindow{capacity: capacity}, nil
}

func (w *LevelWindow) Begin(cycleID uuid.UUID, generation uint64) {
	w.mu.Lock()
	if w.cycleID != cycleID || w.generation != generation {
		w.cycleID = cycleID
		w.generation = generation
		w.samples = nil
	}
	w.mu.Unlock()
}

// Reset drops the window identity and every sample, so a finished transit
// leaves nothing behind for the next one.
func (w *LevelWindow) Reset() {
	w.mu.Lock()
	w.cycleID = uuid.Nil
	w.generation = 0
	w.samples = nil
	w.mu.Unlock()
}

func (w *LevelWindow) Add(sample model.LevelSample, policy LevelPolicy, now time.Time) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if sample.CycleID != w.cycleID || sample.Generation != w.generation {
		return errors.New("sample belongs to another transit generation")
	}
	if err := validateSample(sample, policy, now); err != nil {
		return err
	}
	if latest := len(w.samples) - 1; latest >= 0 && sample.ObservedAt.Before(w.samples[latest].ObservedAt) {
		return errors.New("sample is older than the latest window sample")
	}
	w.samples = append(w.samples, sample)
	if len(w.samples) > w.capacity {
		w.samples = append([]model.LevelSample(nil), w.samples[len(w.samples)-w.capacity:]...)
	}
	return nil
}

// validateSample rejects abnormal observations so they can never support a
// release conclusion.
func validateSample(sample model.LevelSample, policy LevelPolicy, now time.Time) error {
	if math.IsNaN(sample.Meters) || math.IsInf(sample.Meters, 0) {
		return errors.New("sample level is not a finite number")
	}
	if sample.Meters < policy.MinMeters || sample.Meters > policy.MaxMeters {
		return fmt.Errorf("sample level %.3fm outside plausible range [%.1f, %.1f]",
			sample.Meters, policy.MinMeters, policy.MaxMeters)
	}
	if sample.ObservedAt.IsZero() {
		return errors.New("sample observation time is required")
	}
	if sample.ObservedAt.After(now.Add(policy.MaxFutureSkew)) {
		return errors.New("sample observed in the future")
	}
	if now.Sub(sample.ObservedAt) > policy.MaxSampleAge {
		return fmt.Errorf("sample is older than %.0fs", policy.MaxSampleAge.Seconds())
	}
	return nil
}

func (w *LevelWindow) Verdict(policy LevelPolicy, now time.Time) LevelVerdict {
	return evaluateSamples(w.Samples(), policy, now)
}

func (w *LevelWindow) Samples() []model.LevelSample {
	w.mu.RLock()
	result := append([]model.LevelSample(nil), w.samples...)
	w.mu.RUnlock()
	return result
}

func (w *LevelWindow) Identity() (uuid.UUID, uint64) {
	w.mu.RLock()
	id, generation := w.cycleID, w.generation
	w.mu.RUnlock()
	return id, generation
}
