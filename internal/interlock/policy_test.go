package interlock

import (
	"strings"
	"testing"
	"time"

	"github.com/wyw14/cry-96/internal/model"
)

func gates(upstream, downstream model.GatePosition) map[model.Direction]model.GatePosition {
	return map[model.Direction]model.GatePosition{
		model.DirectionUpstream:   upstream,
		model.DirectionDownstream: downstream,
	}
}

func releaseAssessment() *model.LevelAssessment {
	return &model.LevelAssessment{
		ChamberID: "chamber-1", Decision: model.LevelRelease,
		LatestMeters: 5, LatestQuality: model.QualityOK,
		EvaluatedAt: time.Now().UTC(),
	}
}

func TestValidateOpenRequiresStableLevelWhileSealed(t *testing.T) {
	policy := DefaultPolicy()
	closedGates := gates(model.GateClosed, model.GateClosed)

	for _, stage := range []model.TransitStage{model.StageSealed, model.StageLeveling} {
		t.Run("releases on stable verdict/"+string(stage), func(t *testing.T) {
			snapshot := EquipmentSnapshot{
				ChamberID: "chamber-1", Stage: stage, Gates: closedGates,
				Level: releaseAssessment(),
			}
			if err := policy.ValidateOpen(snapshot, model.DirectionDownstream); err != nil {
				t.Fatalf("expected release, got %v", err)
			}
		})
	}

	t.Run("delays with reason while holding", func(t *testing.T) {
		hold := releaseAssessment()
		hold.Decision = model.LevelHold
		hold.Reason = "insufficient level samples: have 2, need 4"
		snapshot := EquipmentSnapshot{
			ChamberID: "chamber-1", Stage: model.StageSealed, Gates: closedGates, Level: hold,
		}
		err := policy.ValidateOpen(snapshot, model.DirectionDownstream)
		if err == nil {
			t.Fatal("hold verdict must not open the gate")
		}
		if !strings.Contains(err.Error(), "delayed") || !strings.Contains(err.Error(), hold.Reason) {
			t.Fatalf("delay reason not surfaced: %v", err)
		}
	})

	t.Run("missing assessment never releases", func(t *testing.T) {
		snapshot := EquipmentSnapshot{
			ChamberID: "chamber-1", Stage: model.StageSealed, Gates: closedGates,
		}
		if err := policy.ValidateOpen(snapshot, model.DirectionDownstream); err == nil {
			t.Fatal("missing assessment must not open the gate")
		}
	})

	t.Run("abnormal verdict never releases", func(t *testing.T) {
		assessment := releaseAssessment()
		assessment.LatestQuality = model.QualityAbnormal
		assessment.Decision = model.LevelHold
		assessment.Reason = "abnormal level sample in observation window"
		snapshot := EquipmentSnapshot{
			ChamberID: "chamber-1", Stage: model.StageSealed, Gates: closedGates, Level: assessment,
		}
		if err := policy.ValidateOpen(snapshot, model.DirectionDownstream); err == nil {
			t.Fatal("abnormal evidence must not open the gate")
		}
	})

	t.Run("emergency overrides release", func(t *testing.T) {
		snapshot := EquipmentSnapshot{
			ChamberID: "chamber-1", Stage: model.StageSealed, Gates: closedGates,
			Level: releaseAssessment(), Emergency: true,
		}
		if err := policy.ValidateOpen(snapshot, model.DirectionDownstream); err == nil {
			t.Fatal("emergency must block release")
		}
	})
}

func TestValidateOpenOutsideObservationStage(t *testing.T) {
	policy := DefaultPolicy()
	closedGates := gates(model.GateClosed, model.GateClosed)

	t.Run("empty window is allowed on entry side", func(t *testing.T) {
		err := policy.ValidateOpen(EquipmentSnapshot{
			ChamberID: "chamber-1", Stage: model.StagePlanned, Gates: closedGates,
		}, model.DirectionUpstream)
		if err != nil {
			t.Fatalf("entry gate with no telemetry should pass, got %v", err)
		}
	})

	t.Run("present but unusable reading blocks", func(t *testing.T) {
		err := policy.ValidateOpen(EquipmentSnapshot{
			ChamberID: "chamber-1", Stage: model.StagePlanned, Gates: closedGates,
			LevelMPresent: true,
		}, model.DirectionUpstream)
		if err == nil || !strings.Contains(err.Error(), "abnormal or missing") {
			t.Fatalf("bad-quality reading must block, got %v", err)
		}
	})

	t.Run("usable reading within range passes", func(t *testing.T) {
		err := policy.ValidateOpen(EquipmentSnapshot{
			ChamberID: "chamber-1", Stage: model.StagePlanned, Gates: closedGates,
			LevelM: 5, LevelMUsable: true, LevelMPresent: true,
		}, model.DirectionUpstream)
		if err != nil {
			t.Fatalf("usable level within range should pass, got %v", err)
		}
	})

	t.Run("usable reading out of range blocks", func(t *testing.T) {
		err := policy.ValidateOpen(EquipmentSnapshot{
			ChamberID: "chamber-1", Stage: model.StagePlanned, Gates: closedGates,
			LevelM: 99, LevelMUsable: true, LevelMPresent: true,
		}, model.DirectionUpstream)
		if err == nil {
			t.Fatal("out-of-range level must block")
		}
	})
}
