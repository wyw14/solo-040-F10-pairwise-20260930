package hydraulics

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/wyw14/cry-96/internal/gate"
	"github.com/wyw14/cry-96/internal/journal"
	"github.com/wyw14/cry-96/internal/model"
)

// Controller coordinates valve commands, level windows and ramp operations.
type Controller struct {
	mu       sync.RWMutex
	commands *gate.Commands
	recorder *journal.Recorder
	windows  map[string]*LevelWindow
	configs  map[string]model.LevelConfig
	ramp     *Ramp
	now      func() time.Time
}

func NewController(commands *gate.Commands, recorder *journal.Recorder, ramp *Ramp, now func() time.Time) *Controller {
	if now == nil {
		now = time.Now
	}
	return &Controller{
		commands: commands, recorder: recorder,
		windows: make(map[string]*LevelWindow), configs: make(map[string]model.LevelConfig),
		ramp: ramp, now: now,
	}
}

func (c *Controller) window(chamberID string) *LevelWindow {
	c.mu.Lock()
	defer c.mu.Unlock()
	window := c.windows[chamberID]
	if window == nil {
		window = NewLevelWindow()
		c.windows[chamberID] = window
	}
	return window
}

// Config returns the chamber's stability configuration or the default.
func (c *Controller) Config(chamberID string) model.LevelConfig {
	c.mu.RLock()
	config, ok := c.configs[chamberID]
	c.mu.RUnlock()
	if !ok {
		return model.DefaultLevelConfig()
	}
	return config
}

// Configure stores a validated stability configuration for a chamber.
func (c *Controller) Configure(chamberID string, config model.LevelConfig) error {
	if chamberID == "" {
		return errors.New("chamber id is required")
	}
	if err := config.Validate(); err != nil {
		return err
	}
	c.mu.Lock()
	c.configs[chamberID] = config
	c.mu.Unlock()
	return nil
}

// BeginCycle binds an empty window the moment a new transit generation
// starts, so samples from the previous lockage cannot survive into it.
func (c *Controller) BeginCycle(cycle model.TransitCycle) error {
	if cycle.ChamberID == "" {
		return errors.New("chamber id is required")
	}
	c.window(cycle.ChamberID).Begin(cycle.ID, cycle.Generation)
	return nil
}

// BeginObservation starts an empty window for the generation when the
// chamber becomes sealed and equalization begins: readings taken while
// admitting cannot count as stability evidence. A missing binding (for
// example after a restart replay) is created on the spot.
func (c *Controller) BeginObservation(cycle model.TransitCycle) error {
	if cycle.ChamberID == "" {
		return errors.New("chamber id is required")
	}
	c.window(cycle.ChamberID).BeginFresh(cycle.ID, cycle.Generation)
	return nil
}

// Observe records one field reading. It is accepted only while the
// chamber's window is bound to the sample's generation; foreign, stale-order
// or unbound samples are rejected instead of influencing a decision.
func (c *Controller) Observe(chamberID string, sample model.LevelSample) (model.LevelAssessment, error) {
	if chamberID == "" {
		return model.LevelAssessment{}, errors.New("chamber id is required")
	}
	if sample.Quality == "" {
		sample.Quality = model.QualityOK
	}
	c.mu.RLock()
	window := c.windows[chamberID]
	c.mu.RUnlock()
	if window == nil {
		return model.LevelAssessment{}, errors.New("no active level window for transit")
	}
	id, generation := window.Identity()
	if sample.CycleID != id || sample.Generation != generation {
		return model.LevelAssessment{}, errors.New("sample belongs to another transit generation")
	}
	if err := window.Add(sample); err != nil {
		return model.LevelAssessment{}, err
	}
	return c.Assessment(chamberID, model.StageLeveling), nil
}

// Assessment evaluates the chamber's current evidence. Stability rules are
// applied while the chamber is sealed (when the release gate is requested)
// and during the subsequent leveling stage; elsewhere release is held. The
// reason explains every delay.
func (c *Controller) Assessment(chamberID string, stage model.TransitStage) model.LevelAssessment {
	config := c.Config(chamberID)
	assessment := model.LevelAssessment{
		ChamberID: chamberID, Config: config,
		Decision: model.LevelHold, EvaluatedAt: c.now().UTC(),
		RecentSamples: []model.LevelSample{},
	}
	c.mu.RLock()
	window := c.windows[chamberID]
	c.mu.RUnlock()
	if window != nil {
		assessment.CycleID, assessment.Generation = window.Identity()
	}
	if stage != model.StageSealed && stage != model.StageLeveling {
		assessment.Reason = reasonNotObserving
		if window != nil {
			c.fillEvidence(&assessment, window, config, evaluation{})
		}
		return assessment
	}
	if window == nil {
		assessment.Reason = reasonNoWindow
		return assessment
	}
	verd := window.evaluate(config, c.now())
	c.fillEvidence(&assessment, window, config, verd)
	assessment.Decision = verd.decision
	assessment.Reason = verd.reason
	return assessment
}

func (c *Controller) fillEvidence(assessment *model.LevelAssessment, window *LevelWindow, config model.LevelConfig, verd evaluation) {
	samples := window.Samples()
	assessment.RecentSamples = append(assessment.RecentSamples, samples...)
	if len(assessment.RecentSamples) > config.MinimumSamples {
		assessment.RecentSamples = assessment.RecentSamples[len(assessment.RecentSamples)-config.MinimumSamples:]
	}
	assessment.SampleCount = len(samples)
	assessment.UsableCount = verd.usable
	assessment.SpanMS = int64(verd.span / time.Millisecond)
	assessment.SwingM = verd.swing
	assessment.AgeMS = int64(verd.age / time.Millisecond)
	if verd.latest != nil {
		latest := *verd.latest
		assessment.LatestMeters = latest.Meters
		assessment.LatestQuality = latest.Quality
		latestAt := latest.ObservedAt
		assessment.LatestAt = &latestAt
	} else if len(samples) > 0 {
		latest := samples[len(samples)-1]
		assessment.LatestMeters = latest.Meters
		assessment.LatestQuality = latest.Quality
		latestAt := latest.ObservedAt
		assessment.LatestAt = &latestAt
		assessment.AgeMS = int64(c.now().Sub(latestAt) / time.Millisecond)
	}
}

// RecentSamples exposes the current generation's readings, newest last.
func (c *Controller) RecentSamples(chamberID string) []model.LevelSample {
	return c.WindowSamples(chamberID)
}

func (c *Controller) IssueValve(ctx context.Context, cycle model.TransitCycle, target float64) error {
	device := model.DeviceFillValve
	if target < 0 {
		device = model.DeviceDrainValve
		target = -target
	}
	command, err := c.commands.Issue(cycle, device, "set_opening", target, c.now())
	if err != nil {
		return err
	}
	_, err = c.recorder.Record(ctx, cycle.ChamberID, model.EventCommandQueued, cycle.ID, cycle.Generation, command)
	return err
}

func (c *Controller) RampTo(ctx context.Context, cycle model.TransitCycle, targets []float64) (RampResult, error) {
	return c.ramp.Run(ctx, cycle, targets, c.IssueValve)
}

func (c *Controller) WindowSamples(chamberID string) []model.LevelSample {
	c.mu.RLock()
	window := c.windows[chamberID]
	c.mu.RUnlock()
	if window == nil {
		return nil
	}
	return window.Samples()
}
