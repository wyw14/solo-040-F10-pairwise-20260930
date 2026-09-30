package interlock

import (
	"context"
	"fmt"
	"time"

	"github.com/wyw14/cry-96/internal/gate"
	"github.com/wyw14/cry-96/internal/model"
)

// LevelEvidence bundles the stability verdict and the latest reading state
// the policy needs, sourced from the hydraulics controller.
type LevelEvidence struct {
	Assessment model.LevelAssessment
	LatestM    float64
	// Usable reports the newest reading is trusted; Present reports whether
	// any reading exists for the current generation.
	Usable  bool
	Present bool
}

// Coordinator keeps policy evaluation and command reservation in one flow.
type Coordinator struct {
	policy    Policy
	arbiter   *Arbiter
	gates     *gate.Controller
	levels    func(chamberID string, stage model.TransitStage) LevelEvidence
	emergency func(string) bool
}

func NewCoordinator(policy Policy, arbiter *Arbiter, gates *gate.Controller, levels func(chamberID string, stage model.TransitStage) LevelEvidence, emergency func(string) bool) *Coordinator {
	return &Coordinator{policy: policy, arbiter: arbiter, gates: gates, levels: levels, emergency: emergency}
}

func (c *Coordinator) Snapshot(chamberID string, stage model.TransitStage) EquipmentSnapshot {
	snapshot := EquipmentSnapshot{
		ChamberID: chamberID, Stage: stage,
		Gates: c.gates.State().Snapshot(chamberID),
	}
	if c.levels != nil {
		evidence := c.levels(chamberID, stage)
		snapshot.LevelM = evidence.LatestM
		snapshot.LevelMUsable = evidence.Usable
		snapshot.LevelMPresent = evidence.Present
		level := evidence.Assessment
		snapshot.Level = &level
	}
	if c.emergency != nil {
		snapshot.Emergency = c.emergency(chamberID)
	}
	return snapshot
}

func (c *Coordinator) RequestOpen(ctx context.Context, cycle model.TransitCycle, direction model.Direction, owner string) (model.DeviceCommand, error) {
	if err := c.policy.ValidateOpen(c.Snapshot(cycle.ChamberID, cycle.Stage), direction); err != nil {
		return model.DeviceCommand{}, err
	}
	var command model.DeviceCommand
	_, err := c.arbiter.Reserve(cycle.ChamberID, direction, owner, func() error {
		issued, issueErr := c.gates.Request(ctx, cycle, direction, true)
		command = issued
		return issueErr
	})
	return command, err
}

func (c *Coordinator) RequestClose(ctx context.Context, cycle model.TransitCycle, direction model.Direction) (model.DeviceCommand, error) {
	if err := c.policy.ValidateClose(c.Snapshot(cycle.ChamberID, cycle.Stage), direction); err != nil {
		return model.DeviceCommand{}, err
	}
	return c.gates.Request(ctx, cycle, direction, false)
}

func (c *Coordinator) Release(chamberID, owner string) error {
	if !c.arbiter.Release(chamberID, owner) {
		return fmt.Errorf("reservation owner %q does not control chamber %s", owner, chamberID)
	}
	return nil
}

func (c *Coordinator) Reservation(chamberID string) (Reservation, bool) {
	return c.arbiter.Current(chamberID)
}

func (c *Coordinator) Observe(chamberID string, direction model.Direction, position model.GatePosition, _ time.Time) error {
	return c.gates.Observe(chamberID, direction, position)
}
