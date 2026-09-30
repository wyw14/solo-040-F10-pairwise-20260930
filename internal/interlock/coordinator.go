package interlock

import (
	"context"
	"fmt"
	"time"

	"github.com/wyw14/cry-96/internal/gate"
	"github.com/wyw14/cry-96/internal/model"
)

// Coordinator keeps policy evaluation and command reservation in one flow.
type Coordinator struct {
	policy       Policy
	arbiter      *Arbiter
	gates        *gate.Controller
	level        func(string) float64
	emergency    func(string) bool
	releaseCheck func(model.TransitCycle) error
}

func NewCoordinator(policy Policy, arbiter *Arbiter, gates *gate.Controller, level func(string) float64, emergency func(string) bool, releaseCheck func(model.TransitCycle) error) *Coordinator {
	return &Coordinator{policy: policy, arbiter: arbiter, gates: gates, level: level, emergency: emergency, releaseCheck: releaseCheck}
}

func (c *Coordinator) Snapshot(chamberID string) EquipmentSnapshot {
	level := 0.0
	if c.level != nil {
		level = c.level(chamberID)
	}
	emergency := false
	if c.emergency != nil {
		emergency = c.emergency(chamberID)
	}
	return EquipmentSnapshot{
		ChamberID: chamberID, Gates: c.gates.State().Snapshot(chamberID),
		LevelM: level, Emergency: emergency,
	}
}

func (c *Coordinator) RequestOpen(ctx context.Context, cycle model.TransitCycle, direction model.Direction, owner string) (model.DeviceCommand, error) {
	if err := c.policy.ValidateOpen(c.Snapshot(cycle.ChamberID), direction); err != nil {
		return model.DeviceCommand{}, err
	}
	// Opening a gate while the chamber is leveling releases vessels, so the
	// water level must first prove stable; the check explains any delay.
	if cycle.Stage == model.StageLeveling && c.releaseCheck != nil {
		if err := c.releaseCheck(cycle); err != nil {
			return model.DeviceCommand{}, err
		}
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
	if err := c.policy.ValidateClose(c.Snapshot(cycle.ChamberID), direction); err != nil {
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
