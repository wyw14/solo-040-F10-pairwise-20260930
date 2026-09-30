package hydraulics_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wyw14/cry-96/internal/hydraulics"
	"github.com/wyw14/cry-96/internal/model"
)

var testStart = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

func testPolicy() hydraulics.LevelPolicy {
	return hydraulics.LevelPolicy{
		MinSamples: 3, MinSpan: 20 * time.Second, FluctuationM: 0.05,
		MaxSampleAge: 40 * time.Second,
	}
}

func newController(now *time.Time) *hydraulics.Controller {
	return hydraulics.NewController(nil, nil, nil,
		map[string]hydraulics.LevelPolicy{"chamber-1": testPolicy()},
		func() time.Time { return *now })
}

func beginWindow(t *testing.T, c *hydraulics.Controller, chamberID string) model.TransitCycle {
	t.Helper()
	cycle, err := model.NewTransitCycle(chamberID, uuid.New(), model.DirectionUpstream, 1, testStart)
	if err != nil {
		t.Fatalf("cycle: %v", err)
	}
	if err := c.BeginLeveling(cycle); err != nil {
		t.Fatalf("begin leveling: %v", err)
	}
	return cycle
}

func sample(cycle model.TransitCycle, meters float64, observedAt time.Time) model.LevelSample {
	return model.LevelSample{
		CycleID: cycle.ID, Generation: cycle.Generation,
		Meters: meters, ObservedAt: observedAt,
	}
}

func TestVerdictExplainsEveryUnmetCondition(t *testing.T) {
	now := testStart
	controller := newController(&now)
	cycle := beginWindow(t, controller, "chamber-1")

	verdict, err := controller.Observe("chamber-1", sample(cycle, 6.00, now.Add(-25*time.Second)))
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if verdict.Stable || len(verdict.Reasons) == 0 {
		t.Fatalf("single sample must not be stable: %+v", verdict)
	}
	verdict, _ = controller.Observe("chamber-1", sample(cycle, 6.02, now.Add(-10*time.Second)))
	verdict, _ = controller.Observe("chamber-1", sample(cycle, 6.01, now.Add(-8*time.Second)))
	if verdict.Stable {
		t.Fatalf("span below minimum must not be stable: %+v", verdict)
	}
	joined := strings.Join(verdict.Reasons, "; ")
	if !strings.Contains(joined, "observation span") {
		t.Fatalf("expected span reason, got %q", joined)
	}

	// A fresh window with enough span but too much fluctuation.
	cycle2, err := model.NewTransitCycle("chamber-1", uuid.New(), model.DirectionUpstream, 2, testStart)
	if err != nil {
		t.Fatalf("cycle2: %v", err)
	}
	if err := controller.BeginLeveling(cycle2); err != nil {
		t.Fatalf("begin cycle2: %v", err)
	}
	for _, item := range []struct {
		age    time.Duration
		meters float64
	}{{-30 * time.Second, 6.00}, {-15 * time.Second, 6.10}, {0, 6.02}} {
		if _, err := controller.Observe("chamber-1", sample(cycle2, item.meters, now.Add(item.age))); err != nil {
			t.Fatalf("observe cycle2: %v", err)
		}
	}
	status := controller.WindowStatus("chamber-1", cycle2.ID, cycle2.Generation)
	if status.Verdict.Stable || !strings.Contains(strings.Join(status.Verdict.Reasons, "; "), "fluctuation") {
		t.Fatalf("expected fluctuation reason, got %+v", status.Verdict)
	}

	// Stable now, but samples age out as the clock advances.
	cycle3, _ := model.NewTransitCycle("chamber-1", uuid.New(), model.DirectionUpstream, 3, testStart)
	if err := controller.BeginLeveling(cycle3); err != nil {
		t.Fatalf("begin cycle3: %v", err)
	}
	for _, item := range []struct {
		age    time.Duration
		meters float64
	}{{-30 * time.Second, 6.00}, {-15 * time.Second, 6.01}, {0, 6.02}} {
		if _, err := controller.Observe("chamber-1", sample(cycle3, item.meters, now.Add(item.age))); err != nil {
			t.Fatalf("observe cycle3: %v", err)
		}
	}
	status = controller.WindowStatus("chamber-1", cycle3.ID, cycle3.Generation)
	if !status.Verdict.Stable {
		t.Fatalf("expected stable verdict, got %+v", status.Verdict)
	}
	now = now.Add(45 * time.Second)
	status = controller.WindowStatus("chamber-1", cycle3.ID, cycle3.Generation)
	if status.Verdict.Stable || !strings.Contains(strings.Join(status.Verdict.Reasons, "; "), "older than") {
		t.Fatalf("expected stale-sample reason, got %+v", status.Verdict)
	}
}

func TestAbnormalSamplesNeverEnterWindow(t *testing.T) {
	now := testStart
	controller := newController(&now)
	cycle := beginWindow(t, controller, "chamber-1")

	cases := []struct {
		name   string
		sample model.LevelSample
	}{
		{"out of range", sample(cycle, 99, now)},
		{"negative", sample(cycle, -1, now)},
		{"stale", sample(cycle, 6, now.Add(-41*time.Second))},
		{"future", sample(cycle, 6, now.Add(time.Minute))},
		{"no timestamp", model.LevelSample{CycleID: cycle.ID, Generation: cycle.Generation, Meters: 6}},
		{"wrong generation", model.LevelSample{CycleID: cycle.ID, Generation: 99, Meters: 6, ObservedAt: now}},
	}
	for _, tc := range cases {
		if _, err := controller.Observe("chamber-1", tc.sample); err == nil {
			t.Fatalf("%s: expected rejection", tc.name)
		}
	}
	status := controller.WindowStatus("chamber-1", cycle.ID, cycle.Generation)
	if status.Verdict.SampleCount != 0 || status.Verdict.Stable {
		t.Fatalf("abnormal samples must not produce a release conclusion: %+v", status.Verdict)
	}
}

func TestWindowStatusIgnoresOtherTransits(t *testing.T) {
	now := testStart
	controller := newController(&now)
	cycle := beginWindow(t, controller, "chamber-1")
	if _, err := controller.Observe("chamber-1", sample(cycle, 6, now)); err != nil {
		t.Fatalf("observe: %v", err)
	}
	other, err := model.NewTransitCycle("chamber-1", uuid.New(), model.DirectionDownstream, 2, testStart)
	if err != nil {
		t.Fatalf("other cycle: %v", err)
	}
	status := controller.WindowStatus("chamber-1", other.ID, other.Generation)
	if len(status.Samples) != 0 || status.Verdict.Stable {
		t.Fatalf("samples from another transit must not appear: %+v", status)
	}
}

func TestResetChamberDropsSamples(t *testing.T) {
	now := testStart
	controller := newController(&now)
	cycle := beginWindow(t, controller, "chamber-1")
	if _, err := controller.Observe("chamber-1", sample(cycle, 6, now)); err != nil {
		t.Fatalf("observe: %v", err)
	}
	controller.ResetChamber("chamber-1")
	status := controller.WindowStatus("chamber-1", cycle.ID, cycle.Generation)
	if len(status.Samples) != 0 || status.Verdict.Stable {
		t.Fatalf("reset must drop samples: %+v", status)
	}
	if _, err := controller.Observe("chamber-1", sample(cycle, 6, now)); err == nil {
		t.Fatal("samples for a reset transit must be rejected")
	}
}

func TestPoliciesArePerChamber(t *testing.T) {
	now := testStart
	controller := hydraulics.NewController(nil, nil, nil, map[string]hydraulics.LevelPolicy{
		"chamber-1": {MinSamples: 2, MinSpan: 0, FluctuationM: 0.05, MaxSampleAge: time.Minute},
		"chamber-2": {MinSamples: 4, MinSpan: 0, FluctuationM: 0.05, MaxSampleAge: time.Minute},
	}, func() time.Time { return now })
	first := beginWindow(t, controller, "chamber-1")
	second := beginWindow(t, controller, "chamber-2")
	for _, cycle := range []model.TransitCycle{first, second} {
		for _, item := range []struct {
			age    time.Duration
			meters float64
		}{{-10 * time.Second, 6.00}, {0, 6.01}} {
			if _, err := controller.Observe(cycle.ChamberID, sample(cycle, item.meters, now.Add(item.age))); err != nil {
				t.Fatalf("observe %s: %v", cycle.ChamberID, err)
			}
		}
	}
	if status := controller.WindowStatus("chamber-1", first.ID, first.Generation); !status.Verdict.Stable {
		t.Fatalf("chamber-1 policy allows two samples: %+v", status.Verdict)
	}
	status := controller.WindowStatus("chamber-2", second.ID, second.Generation)
	if status.Verdict.Stable || !strings.Contains(strings.Join(status.Verdict.Reasons, "; "), "insufficient samples: 2 of 4") {
		t.Fatalf("chamber-2 policy requires four samples: %+v", status.Verdict)
	}
}
