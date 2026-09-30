package hydraulics

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wyw14/cry-96/internal/gate"
	"github.com/wyw14/cry-96/internal/journal"
	"github.com/wyw14/cry-96/internal/model"
)

// ErrNoLevelWindow reports that no window accepts samples for the transit.
var ErrNoLevelWindow = errors.New("no active level window for transit")

// Controller coordinates valve commands, level windows and ramp operations.
type Controller struct {
	mu       sync.RWMutex
	commands *gate.Commands
	recorder *journal.Recorder
	windows  map[string]*LevelWindow
	policies map[string]LevelPolicy
	ramp     *Ramp
	now      func() time.Time
}

func NewController(commands *gate.Commands, recorder *journal.Recorder, ramp *Ramp, policies map[string]LevelPolicy, now func() time.Time) *Controller {
	if now == nil {
		now = time.Now
	}
	controller := &Controller{
		commands: commands, recorder: recorder, windows: make(map[string]*LevelWindow),
		policies: make(map[string]LevelPolicy, len(policies)), ramp: ramp, now: now,
	}
	for chamberID, policy := range policies {
		controller.policies[chamberID] = policy.normalized()
	}
	return controller
}

// Policy returns the stability policy configured for a chamber, falling back
// to the default when the chamber has no explicit configuration.
func (c *Controller) Policy(chamberID string) LevelPolicy {
	c.mu.RLock()
	policy, ok := c.policies[chamberID]
	c.mu.RUnlock()
	if !ok {
		policy = DefaultLevelPolicy()
	}
	return policy.normalized()
}

func (c *Controller) policyLocked(chamberID string) LevelPolicy {
	policy, ok := c.policies[chamberID]
	if !ok {
		policy = DefaultLevelPolicy()
	}
	return policy.normalized()
}

func (c *Controller) window(chamberID string) (*LevelWindow, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	window := c.windows[chamberID]
	if window != nil {
		return window, nil
	}
	created, err := NewLevelWindow(c.policyLocked(chamberID).windowCapacity())
	if err != nil {
		return nil, err
	}
	c.windows[chamberID] = created
	return created, nil
}

func (c *Controller) BeginLeveling(cycle model.TransitCycle) error {
	window, err := c.window(cycle.ChamberID)
	if err != nil {
		return err
	}
	window.Begin(cycle.ID, cycle.Generation)
	return nil
}

// ResetChamber drops every sample held for a chamber. Called when a transit
// ends or a new one starts, so old samples never join a later judgement.
func (c *Controller) ResetChamber(chamberID string) {
	c.mu.RLock()
	window := c.windows[chamberID]
	c.mu.RUnlock()
	if window != nil {
		window.Reset()
	}
}

// Observe validates and records a sample, then evaluates the window.
func (c *Controller) Observe(chamberID string, sample model.LevelSample) (LevelVerdict, error) {
	window, err := c.windowForCycle(sample.CycleID, sample.Generation)
	if err != nil {
		return LevelVerdict{}, err
	}
	policy := c.Policy(chamberID)
	if err := window.Add(sample, policy, c.now()); err != nil {
		return LevelVerdict{}, err
	}
	return window.Verdict(policy, c.now()), nil
}

func (c *Controller) windowForCycle(cycleID uuid.UUID, generation uint64) (*LevelWindow, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, window := range c.windows {
		id, currentGeneration := window.Identity()
		if id == cycleID && currentGeneration == generation {
			return window, nil
		}
	}
	return nil, ErrNoLevelWindow
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

// PolicyView is the JSON-friendly form of a chamber's level policy.
type PolicyView struct {
	MinSamples          int     `json:"min_samples"`
	MinSpanSeconds      float64 `json:"min_span_seconds"`
	FluctuationM        float64 `json:"fluctuation_m"`
	MaxSampleAgeSeconds float64 `json:"max_sample_age_seconds"`
}

func NewPolicyView(policy LevelPolicy) PolicyView {
	return PolicyView{
		MinSamples:          policy.MinSamples,
		MinSpanSeconds:      policy.MinSpan.Seconds(),
		FluctuationM:        policy.FluctuationM,
		MaxSampleAgeSeconds: policy.MaxSampleAge.Seconds(),
	}
}

// WindowStatus is the operator-facing view of one chamber's level window.
type WindowStatus struct {
	ChamberID  string              `json:"chamber_id"`
	CycleID    string              `json:"cycle_id,omitempty"`
	Generation uint64              `json:"generation,omitempty"`
	Policy     PolicyView          `json:"policy"`
	Samples    []model.LevelSample `json:"samples"`
	Verdict    LevelVerdict        `json:"verdict"`
}

// WindowStatus reports samples and the stability verdict for the given
// transit identity. Samples from any other cycle or generation are never
// returned and never take part in the verdict.
func (c *Controller) WindowStatus(chamberID string, cycleID uuid.UUID, generation uint64) WindowStatus {
	policy := c.Policy(chamberID)
	status := WindowStatus{
		ChamberID: chamberID,
		Policy:    NewPolicyView(policy),
		Samples:   []model.LevelSample{},
	}
	c.mu.RLock()
	window := c.windows[chamberID]
	c.mu.RUnlock()
	if window != nil {
		id, windowGeneration := window.Identity()
		if id == cycleID && windowGeneration == generation {
			status.CycleID = id.String()
			status.Generation = windowGeneration
			status.Samples = window.Samples()
		}
	}
	status.Verdict = evaluateSamples(status.Samples, policy, c.now())
	return status
}
