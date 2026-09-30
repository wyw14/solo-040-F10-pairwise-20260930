package model

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type DeviceKind string

const (
	DeviceUpstreamGate   DeviceKind = "upstream_gate"
	DeviceDownstreamGate DeviceKind = "downstream_gate"
	DeviceFillValve      DeviceKind = "fill_valve"
	DeviceDrainValve     DeviceKind = "drain_valve"
)

type CommandState string

const (
	CommandQueued    CommandState = "queued"
	CommandAccepted  CommandState = "accepted"
	CommandExecuting CommandState = "executing"
	CommandSettled   CommandState = "settled"
	CommandCancelled CommandState = "cancelled"
	CommandFailed    CommandState = "failed"
)

func (s CommandState) Terminal() bool {
	return s == CommandSettled || s == CommandCancelled || s == CommandFailed
}

type DeviceCommand struct {
	ID         uuid.UUID    `json:"id"`
	CycleID    uuid.UUID    `json:"cycle_id"`
	ChamberID  string       `json:"chamber_id"`
	Generation uint64       `json:"generation"`
	Epoch      uint64       `json:"epoch"`
	Device     DeviceKind   `json:"device"`
	Action     string       `json:"action"`
	Target     float64      `json:"target,omitempty"`
	State      CommandState `json:"state"`
	CreatedAt  time.Time    `json:"created_at"`
	UpdatedAt  time.Time    `json:"updated_at"`
}

func NewDeviceCommand(cycle TransitCycle, epoch uint64, device DeviceKind, action string, target float64, now time.Time) (DeviceCommand, error) {
	if cycle.ID == uuid.Nil || cycle.ChamberID == "" {
		return DeviceCommand{}, errors.New("command requires a transit cycle")
	}
	if epoch == 0 {
		return DeviceCommand{}, errors.New("command epoch must be positive")
	}
	if device == "" || action == "" {
		return DeviceCommand{}, errors.New("device and action are required")
	}
	return DeviceCommand{
		ID: uuid.New(), CycleID: cycle.ID, ChamberID: cycle.ChamberID,
		Generation: cycle.Generation, Epoch: epoch, Device: device,
		Action: action, Target: target, State: CommandQueued,
		CreatedAt: now.UTC(), UpdatedAt: now.UTC(),
	}, nil
}

func (c *DeviceCommand) SetState(next CommandState, now time.Time) error {
	if c.State.Terminal() {
		return fmt.Errorf("command %s is already terminal", c.ID)
	}
	c.State = next
	c.UpdatedAt = now.UTC()
	return nil
}

type DeviceAck struct {
	CommandID  uuid.UUID  `json:"command_id"`
	CycleID    uuid.UUID  `json:"cycle_id"`
	ChamberID  string     `json:"chamber_id"`
	Generation uint64     `json:"generation"`
	Epoch      uint64     `json:"epoch"`
	Device     DeviceKind `json:"device"`
	Settled    bool       `json:"settled"`
	ObservedAt time.Time  `json:"observed_at"`
}

func (a DeviceAck) Matches(command DeviceCommand) bool {
	return a.CommandID == command.ID && a.CycleID == command.CycleID &&
		a.Generation == command.Generation && a.Epoch == command.Epoch &&
		a.ChamberID == command.ChamberID && a.Device == command.Device
}

type GatePosition string

const (
	GateOpen    GatePosition = "open"
	GateOpening GatePosition = "opening"
	GateClosed  GatePosition = "closed"
	GateClosing GatePosition = "closing"
)

func (p GatePosition) Sealed() bool { return p == GateClosed }

// SampleQuality marks whether a field reading is trustworthy.
type SampleQuality string

const (
	// QualityOK is the only quality that may support a release decision.
	QualityOK SampleQuality = "ok"
	// QualityAbnormal marks a reading the sensor flagged as anomalous.
	QualityAbnormal SampleQuality = "abnormal"
	// QualityMissing marks a gap where a reading was expected but never arrived.
	QualityMissing SampleQuality = "missing"
)

func (q SampleQuality) Usable() bool { return q == QualityOK }

type LevelSample struct {
	CycleID    uuid.UUID     `json:"cycle_id"`
	Generation uint64        `json:"generation"`
	Meters     float64       `json:"meters"`
	Quality    SampleQuality `json:"quality"`
	ObservedAt time.Time     `json:"observed_at"`
}

// LevelWindowCapacity bounds how many raw samples a chamber retains for a
// transit generation; configured minimum sample counts must stay within it.
const LevelWindowCapacity = 64

// LevelConfig tunes the stability decision for one chamber.
type LevelConfig struct {
	// MinimumSamples is the number of usable samples the window must hold.
	MinimumSamples int `json:"minimum_samples"`
	// MinimumSpan is the time the oldest-to-newest usable samples must cover.
	MinimumSpanMS int64 `json:"minimum_span_ms"`
	// SwingToleranceM is the maximum allowed max-min spread, in meters.
	SwingToleranceM float64 `json:"swing_tolerance_m"`
	// MaxObservationAgeMS bounds how old the newest sample may be at decision time.
	MaxObservationAgeMS int64 `json:"max_observation_age_ms"`
}

// DefaultLevelConfig preserves the historic four-sample / 0.05 m behaviour
// while adding a span and freshness requirement.
func DefaultLevelConfig() LevelConfig {
	return LevelConfig{
		MinimumSamples:      4,
		MinimumSpanMS:       3000,
		SwingToleranceM:     0.05,
		MaxObservationAgeMS: 10000,
	}
}

func (c LevelConfig) MinimumSpan() time.Duration {
	return time.Duration(c.MinimumSpanMS) * time.Millisecond
}

func (c LevelConfig) MaxObservationAge() time.Duration {
	return time.Duration(c.MaxObservationAgeMS) * time.Millisecond
}

func (c LevelConfig) Validate() error {
	if c.MinimumSamples < 2 {
		return errors.New("minimum_samples must be at least two")
	}
	if c.MinimumSamples > LevelWindowCapacity {
		return fmt.Errorf("minimum_samples must not exceed %d", LevelWindowCapacity)
	}
	if c.MinimumSpanMS < 0 {
		return errors.New("minimum_span_ms must not be negative")
	}
	if c.SwingToleranceM <= 0 {
		return errors.New("swing_tolerance_m must be positive")
	}
	if c.MaxObservationAgeMS <= 0 {
		return errors.New("max_observation_age_ms must be positive")
	}
	if c.MinimumSpanMS > c.MaxObservationAgeMS {
		return errors.New("minimum_span_ms must not exceed max_observation_age_ms")
	}
	return nil
}

// LevelDecision explains whether gate release may proceed.
type LevelDecision string

const (
	// LevelHold blocks release; Reason explains the delay.
	LevelHold LevelDecision = "hold"
	// LevelRelease means the level is confirmed stable and current.
	LevelRelease LevelDecision = "release"
)

// LevelAssessment is the operator-visible stability verdict with its evidence.
type LevelAssessment struct {
	ChamberID     string        `json:"chamber_id"`
	CycleID       uuid.UUID     `json:"cycle_id"`
	Generation    uint64        `json:"generation"`
	Config        LevelConfig   `json:"config"`
	Decision      LevelDecision `json:"decision"`
	Reason        string        `json:"reason,omitempty"`
	SampleCount   int           `json:"sample_count"`
	UsableCount   int           `json:"usable_count"`
	SpanMS        int64         `json:"span_ms"`
	SwingM        float64       `json:"swing_m"`
	LatestMeters  float64       `json:"latest_meters,omitempty"`
	LatestQuality SampleQuality `json:"latest_quality,omitempty"`
	LatestAt      *time.Time    `json:"latest_at,omitempty"`
	AgeMS         int64         `json:"age_ms"`
	RecentSamples []LevelSample `json:"recent_samples"`
	EvaluatedAt   time.Time     `json:"evaluated_at"`
}
