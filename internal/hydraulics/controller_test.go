package hydraulics

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wyw14/cry-96/internal/model"
)

func newTestController(now func() time.Time) *Controller {
	if now == nil {
		now = func() time.Time { return time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC) }
	}
	return NewController(nil, nil, nil, now)
}

func TestConfigureValidatesPerChamber(t *testing.T) {
	controller := newTestController(nil)
	if got := controller.Config("chamber-1").MinimumSamples; got != 4 {
		t.Fatalf("default minimum samples = %d, want 4", got)
	}
	custom := model.LevelConfig{MinimumSamples: 3, MinimumSpanMS: 1000, SwingToleranceM: 0.02, MaxObservationAgeMS: 5000}
	if err := controller.Configure("chamber-1", custom); err != nil {
		t.Fatalf("configure: %v", err)
	}
	if got := controller.Config("chamber-1"); got.SwingToleranceM != 0.02 {
		t.Fatalf("chamber-1 config not applied: %+v", got)
	}
	if got := controller.Config("chamber-2"); got.SwingToleranceM != 0.05 {
		t.Fatalf("chamber-2 should keep default, got %+v", got)
	}
	bad := model.LevelConfig{MinimumSamples: 1, MinimumSpanMS: 0, SwingToleranceM: 0.05, MaxObservationAgeMS: 10000}
	if err := controller.Configure("chamber-1", bad); err == nil {
		t.Fatal("accepted invalid configuration")
	}
}

func TestControllerAssessmentAndGenerationReset(t *testing.T) {
	clock := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	controller := newTestController(now)

	first := model.TransitCycle{ID: uuid.New(), ChamberID: "chamber-1", Stage: model.StageLeveling, Generation: 1}
	if err := controller.BeginCycle(first); err != nil {
		t.Fatalf("begin cycle: %v", err)
	}
	for index := 0; index < 4; index++ {
		s := sample(first.ID, 1, 5.0+float64(index)*0.001, model.QualityOK, clock.Add(time.Duration(index)*time.Second))
		if _, err := controller.Observe("chamber-1", s); err != nil {
			t.Fatalf("observe: %v", err)
		}
	}
	clock = clock.Add(3500 * time.Millisecond)
	assessment := controller.Assessment("chamber-1", model.StageLeveling)
	if assessment.Decision != model.LevelRelease {
		t.Fatalf("expected release, got hold: %s", assessment.Reason)
	}

	// A new lockage starts: previous samples must not participate.
	second := model.TransitCycle{ID: uuid.New(), ChamberID: "chamber-1", Stage: model.StagePlanned, Generation: 2}
	if err := controller.BeginCycle(second); err != nil {
		t.Fatalf("begin second cycle: %v", err)
	}
	if got := controller.WindowSamples("chamber-1"); len(got) != 0 {
		t.Fatalf("new cycle carried %d old samples", len(got))
	}
	stale := sample(first.ID, 1, 5.0, model.QualityOK, clock)
	if _, err := controller.Observe("chamber-1", stale); err == nil {
		t.Fatal("accepted sample from previous lockage")
	}

	// Entering sealed equalization clears any readings collected beforehand.
	second.Stage = model.StageAdmitting
	controller.Observe("chamber-1", sample(second.ID, 2, 5.0, model.QualityOK, clock))
	second.Stage = model.StageSealed
	if err := controller.BeginObservation(second); err != nil {
		t.Fatalf("begin observation: %v", err)
	}
	assessment = controller.Assessment("chamber-1", model.StageSealed)
	if assessment.Decision != model.LevelHold || !strings.HasPrefix(assessment.Reason, reasonInsufficient) {
		t.Fatalf("sealed observation should reset evidence, got decision=%s reason=%q", assessment.Decision, assessment.Reason)
	}
}

func TestAssessmentHoldsOutsideObservationAndWhenAbnormal(t *testing.T) {
	clock := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	controller := newTestController(now)
	cycle := model.TransitCycle{ID: uuid.New(), ChamberID: "chamber-9", Stage: model.StageAdmitting, Generation: 1}
	if err := controller.BeginCycle(cycle); err != nil {
		t.Fatalf("begin cycle: %v", err)
	}
	assessment := controller.Assessment("chamber-9", model.StageAdmitting)
	if assessment.Decision != model.LevelHold || assessment.Reason != reasonNotObserving {
		t.Fatalf("admitting stage must hold, got %s/%q", assessment.Decision, assessment.Reason)
	}

	cycle.Stage = model.StageSealed
	if err := controller.BeginObservation(cycle); err != nil {
		t.Fatalf("begin observation: %v", err)
	}
	for index := 0; index < 3; index++ {
		controller.Observe("chamber-9", sample(cycle.ID, 1, 5.0, model.QualityOK, clock.Add(time.Duration(index)*time.Second)))
	}
	if _, err := controller.Observe("chamber-9", sample(cycle.ID, 1, 99, model.QualityAbnormal, clock.Add(3*time.Second))); err != nil {
		t.Fatalf("observe abnormal: %v", err)
	}
	clock = clock.Add(3500 * time.Millisecond)
	assessment = controller.Assessment("chamber-9", model.StageSealed)
	if assessment.Decision != model.LevelHold || !strings.HasPrefix(assessment.Reason, reasonAbnormal) {
		t.Fatalf("abnormal sample must hold, got %s/%q", assessment.Decision, assessment.Reason)
	}
	if assessment.LatestQuality != model.QualityAbnormal {
		t.Fatalf("latest quality not surfaced: %q", assessment.LatestQuality)
	}
}

func TestControllerCustomConfigAndEvidence(t *testing.T) {
	clock := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	controller := newTestController(now)
	custom := model.LevelConfig{MinimumSamples: 3, MinimumSpanMS: 1500, SwingToleranceM: 0.02, MaxObservationAgeMS: 4000}
	if err := controller.Configure("chamber-7", custom); err != nil {
		t.Fatalf("configure: %v", err)
	}
	cycle := model.TransitCycle{ID: uuid.New(), ChamberID: "chamber-7", Stage: model.StageSealed, Generation: 1}
	if err := controller.BeginCycle(cycle); err != nil {
		t.Fatalf("begin cycle: %v", err)
	}
	if err := controller.BeginObservation(cycle); err != nil {
		t.Fatalf("begin observation: %v", err)
	}
	values := []float64{5.000, 5.005, 5.010}
	for index, meters := range values {
		controller.Observe("chamber-7", sample(cycle.ID, 1, meters, model.QualityOK, clock.Add(time.Duration(index)*time.Second)))
	}
	clock = clock.Add(2500 * time.Millisecond)
	assessment := controller.Assessment("chamber-7", model.StageSealed)
	if assessment.Decision != model.LevelRelease {
		t.Fatalf("custom 3-sample config should release, got hold: %s", assessment.Reason)
	}
	if len(assessment.RecentSamples) != 3 {
		t.Fatalf("recent samples = %d, want 3", len(assessment.RecentSamples))
	}
	if assessment.SpanMS != 2000 || assessment.AgeMS != 500 {
		t.Fatalf("evidence timing wrong: span=%d age=%d", assessment.SpanMS, assessment.AgeMS)
	}

	// Freshness limit is 4s; let the newest reading age past it.
	clock = clock.Add(4 * time.Second)
	assessment = controller.Assessment("chamber-7", model.StageSealed)
	if assessment.Decision != model.LevelHold || !strings.HasPrefix(assessment.Reason, reasonStale) {
		t.Fatalf("stale reading must hold, got %s/%q", assessment.Decision, assessment.Reason)
	}
}

func TestObserveRejectsForeignChamberBinding(t *testing.T) {
	controller := newTestController(nil)
	cycle := model.TransitCycle{ID: uuid.New(), ChamberID: "chamber-1", Stage: model.StageSealed, Generation: 1}
	if err := controller.BeginCycle(cycle); err != nil {
		t.Fatalf("begin cycle: %v", err)
	}
	other := model.TransitCycle{ID: uuid.New(), ChamberID: "chamber-2", Stage: model.StageSealed, Generation: 1}
	if _, err := controller.Observe("chamber-2", sample(other.ID, 1, 5, model.QualityOK, controller.now())); err == nil {
		t.Fatal("accepted sample for chamber with no bound window")
	}
	if _, err := controller.Observe("chamber-1", sample(other.ID, 1, 5, model.QualityOK, controller.now())); err == nil {
		t.Fatal("accepted sample from foreign cycle")
	}
}
