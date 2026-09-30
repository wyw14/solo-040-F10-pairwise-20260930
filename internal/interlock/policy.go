package interlock

import (
	"errors"
	"fmt"

	"github.com/wyw14/cry-96/internal/model"
)

type EquipmentSnapshot struct {
	ChamberID string                                 `json:"chamber_id"`
	Stage     model.TransitStage                     `json:"stage"`
	Gates     map[model.Direction]model.GatePosition `json:"gates"`
	// LevelM is the most recent usable reading; LevelMPresent reports whether
	// the window holds any reading at all. A window whose readings are all
	// abnormal or missing can never support a release.
	LevelM        float64 `json:"level_m"`
	LevelMUsable  bool    `json:"level_m_usable"`
	LevelMPresent bool    `json:"level_m_present"`
	// Level carries the configured stability verdict while sealed/leveling,
	// which is when gate release depends on it.
	Level     *model.LevelAssessment `json:"level,omitempty"`
	Emergency bool                   `json:"emergency"`
}

type Policy struct {
	MinimumLevel float64
	MaximumLevel float64
}

func DefaultPolicy() Policy { return Policy{MinimumLevel: 0, MaximumLevel: 12} }

func (p Policy) ValidateOpen(snapshot EquipmentSnapshot, direction model.Direction) error {
	if snapshot.Emergency {
		return fmt.Errorf("interlock emergency active chamber=%s", snapshot.ChamberID)
	}
	// Equalization finishes while the chamber is sealed; the gate request
	// that releases the vessel is issued at sealed and its ack only then
	// advances the transit to leveling. Both points must show a release
	// verdict, so the open is refused here with the delay reason.
	if snapshot.Stage == model.StageSealed || snapshot.Stage == model.StageLeveling {
		if snapshot.Level == nil {
			return errors.New("water level stability has not been assessed")
		}
		if snapshot.Level.Decision != model.LevelRelease {
			reason := snapshot.Level.Reason
			if reason == "" {
				reason = "water level not confirmed stable"
			}
			return fmt.Errorf("gate release delayed: %s", reason)
		}
		if snapshot.Level.LatestMeters < p.MinimumLevel || snapshot.Level.LatestMeters > p.MaximumLevel {
			return fmt.Errorf("water level %.2f outside safe window", snapshot.Level.LatestMeters)
		}
	} else {
		// Entry-side gates (planned/admitting/releasing) historically did
		// not require level telemetry, so an empty window is allowed. But
		// readings that exist and are all abnormal or missing must never
		// produce a releasable conclusion.
		if snapshot.LevelMPresent && !snapshot.LevelMUsable {
			return errors.New("abnormal or missing water level sample blocks gate release")
		}
		if snapshot.LevelMUsable && (snapshot.LevelM < p.MinimumLevel || snapshot.LevelM > p.MaximumLevel) {
			return fmt.Errorf("water level %.2f outside safe window", snapshot.LevelM)
		}
	}
	opposite := snapshot.Gates[direction.Opposite()]
	if opposite != model.GateClosed {
		return fmt.Errorf("opposite gate is %s", opposite)
	}
	return nil
}

func (p Policy) ValidateClose(snapshot EquipmentSnapshot, direction model.Direction) error {
	position := snapshot.Gates[direction]
	if position == model.GateClosed || position == model.GateClosing {
		return fmt.Errorf("gate %s already closing or closed", direction)
	}
	return nil
}
