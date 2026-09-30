package interlock_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wyw14/cry-96/internal/gate"
	"github.com/wyw14/cry-96/internal/interlock"
	"github.com/wyw14/cry-96/internal/journal"
	"github.com/wyw14/cry-96/internal/model"
)

func cycleAtStage(t *testing.T, stage model.TransitStage) model.TransitCycle {
	t.Helper()
	cycle, err := model.NewTransitCycle("chamber-1", uuid.New(), model.DirectionUpstream, 1, time.Now())
	if err != nil {
		t.Fatalf("cycle: %v", err)
	}
	cycle.Stage = stage
	return cycle
}

func newCoordinator(releaseCheck func(model.TransitCycle) error) *interlock.Coordinator {
	now := time.Now
	recorder := journal.NewRecorder(journal.NewMemoryStore(), now)
	gateState := gate.NewState("chamber-1")
	gates := gate.NewController(gateState, gate.NewCommands(), recorder, now)
	return interlock.NewCoordinator(
		interlock.DefaultPolicy(), interlock.NewArbiter(), gates,
		func(string) float64 { return 6 }, func(string) bool { return false },
		releaseCheck,
	)
}

func TestOpenDuringLevelingReportsDelayReasons(t *testing.T) {
	delay := errors.New("gate open delayed: insufficient samples: 1 of 4 required")
	coordinator := newCoordinator(func(model.TransitCycle) error { return delay })

	_, err := coordinator.RequestOpen(context.Background(), cycleAtStage(t, model.StageLeveling), model.DirectionDownstream, "test")
	if err == nil || !strings.Contains(err.Error(), "insufficient samples") {
		t.Fatalf("expected delay reasons, got %v", err)
	}
}

func TestOpenDuringLevelingAllowedWhenStable(t *testing.T) {
	coordinator := newCoordinator(func(model.TransitCycle) error { return nil })
	command, err := coordinator.RequestOpen(context.Background(), cycleAtStage(t, model.StageLeveling), model.DirectionDownstream, "test")
	if err != nil {
		t.Fatalf("stable level must allow open: %v", err)
	}
	if command.Action != "open" {
		t.Fatalf("expected open command, got %q", command.Action)
	}
}

func TestReleaseCheckSkippedOutsideLeveling(t *testing.T) {
	called := false
	coordinator := newCoordinator(func(model.TransitCycle) error {
		called = true
		return errors.New("should not run")
	})
	if _, err := coordinator.RequestOpen(context.Background(), cycleAtStage(t, model.StagePlanned), model.DirectionUpstream, "test"); err != nil {
		t.Fatalf("admission open must not require level stability: %v", err)
	}
	if called {
		t.Fatal("release check must not run outside the leveling stage")
	}
}
