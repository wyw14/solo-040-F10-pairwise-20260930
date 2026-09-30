package cycle_test

import (
	"context"
	"testing"
	"time"

	"github.com/wyw14/cry-96/internal/cycle"
	"github.com/wyw14/cry-96/internal/hydraulics"
	"github.com/wyw14/cry-96/internal/journal"
	"github.com/wyw14/cry-96/internal/model"
)

func newPlan(t *testing.T, chamberID string) model.ConvoyPlan {
	t.Helper()
	vessel, err := model.NewVessel("River Star", "RS-204", 86)
	if err != nil {
		t.Fatalf("vessel: %v", err)
	}
	plan, err := model.NewConvoyPlan(chamberID, model.DirectionUpstream, []model.Vessel{vessel}, time.Now())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	return plan
}

func setup(t *testing.T) (*cycle.Coordinator, *hydraulics.Controller) {
	t.Helper()
	now := time.Now
	recorder := journal.NewRecorder(journal.NewMemoryStore(), now)
	hydraulicsController := hydraulics.NewController(nil, recorder, nil, nil, now)
	return cycle.NewCoordinator(nil, hydraulicsController, recorder, now), hydraulicsController
}

func TestStartDropsPreviousTransitSamples(t *testing.T) {
	coordinator, hydraulicsController := setup(t)
	ctx := context.Background()

	first, err := coordinator.Start(ctx, newPlan(t, "chamber-1"))
	if err != nil {
		t.Fatalf("start first: %v", err)
	}
	if err := hydraulicsController.BeginLeveling(first); err != nil {
		t.Fatalf("begin leveling: %v", err)
	}
	reading := model.LevelSample{CycleID: first.ID, Generation: first.Generation, Meters: 6, ObservedAt: time.Now()}
	if _, err := hydraulicsController.Observe("chamber-1", reading); err != nil {
		t.Fatalf("observe: %v", err)
	}
	if _, err := coordinator.Cancel(ctx, "chamber-1", "operator"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	status := hydraulicsController.WindowStatus("chamber-1", first.ID, first.Generation)
	if len(status.Samples) != 0 {
		t.Fatalf("cancel must drop samples, got %d", len(status.Samples))
	}

	second, err := coordinator.Start(ctx, newPlan(t, "chamber-1"))
	if err != nil {
		t.Fatalf("start second: %v", err)
	}
	if second.Generation != first.Generation+1 {
		t.Fatalf("expected new generation, got %d after %d", second.Generation, first.Generation)
	}
	status = hydraulicsController.WindowStatus("chamber-1", second.ID, second.Generation)
	if len(status.Samples) != 0 || status.Verdict.Stable {
		t.Fatalf("new transit must not inherit samples: %+v", status)
	}
	if _, err := hydraulicsController.Observe("chamber-1", reading); err == nil {
		t.Fatal("samples from the previous transit must be rejected")
	}
}

func TestHandoffCompletionDropsSamples(t *testing.T) {
	coordinator, hydraulicsController := setup(t)
	ctx := context.Background()

	releasing, err := model.NewTransitCycle("chamber-1", newPlan(t, "chamber-1").ID, model.DirectionUpstream, 1, time.Now())
	if err != nil {
		t.Fatalf("cycle: %v", err)
	}
	releasing.Stage = model.StageReleasing
	coordinator.Restore(releasing)
	if err := hydraulicsController.BeginLeveling(releasing); err != nil {
		t.Fatalf("begin leveling: %v", err)
	}
	reading := model.LevelSample{CycleID: releasing.ID, Generation: releasing.Generation, Meters: 6, ObservedAt: time.Now()}
	if _, err := hydraulicsController.Observe("chamber-1", reading); err != nil {
		t.Fatalf("observe: %v", err)
	}
	if err := coordinator.ApplySignalHandoff(ctx, releasing.ID, releasing.Generation); err != nil {
		t.Fatalf("handoff: %v", err)
	}
	status := hydraulicsController.WindowStatus("chamber-1", releasing.ID, releasing.Generation)
	if len(status.Samples) != 0 || status.Verdict.Stable {
		t.Fatalf("completed transit must not keep samples: %+v", status)
	}
	if _, err := hydraulicsController.Observe("chamber-1", reading); err == nil {
		t.Fatal("samples for a completed transit must be rejected")
	}
}
