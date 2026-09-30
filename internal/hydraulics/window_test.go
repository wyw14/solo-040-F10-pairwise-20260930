package hydraulics

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wyw14/cry-96/internal/model"
)

func sample(id uuid.UUID, generation uint64, meters float64, quality model.SampleQuality, at time.Time) model.LevelSample {
	return model.LevelSample{
		CycleID: id, Generation: generation, Meters: meters,
		Quality: quality, ObservedAt: at,
	}
}

func okSeries(id uuid.UUID, generation uint64, base time.Time, values []float64, gap time.Duration) []model.LevelSample {
	samples := make([]model.LevelSample, len(values))
	for index, meters := range values {
		samples[index] = sample(id, generation, meters, model.QualityOK, base.Add(time.Duration(index)*gap))
	}
	return samples
}

func TestWindowReleasesForFreshQuietSpanningWindow(t *testing.T) {
	window := NewLevelWindow()
	id := uuid.New()
	base := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	window.Begin(id, 1)
	for _, each := range okSeries(id, 1, base, []float64{5.000, 5.004, 5.008, 5.010}, time.Second) {
		if err := window.Add(each); err != nil {
			t.Fatalf("add: %v", err)
		}
	}
	verdict := window.evaluate(model.DefaultLevelConfig(), base.Add(3500*time.Millisecond))
	if verdict.decision != model.LevelRelease {
		t.Fatalf("expected release, got hold: %s", verdict.reason)
	}
	if verdict.usable != 4 || verdict.swing > 0.011 || verdict.span < 3*time.Second {
		t.Fatalf("unexpected evidence: %+v", verdict)
	}
}

func TestWindowHoldReasons(t *testing.T) {
	config := model.DefaultLevelConfig()
	id := uuid.New()
	base := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)

	cases := []struct {
		name       string
		samples    []model.LevelSample
		evaluateAt time.Time
		wantReason string
	}{
		{
			name:       "insufficient samples",
			samples:    okSeries(id, 1, base, []float64{5, 5, 5}, time.Second),
			evaluateAt: base.Add(2500 * time.Millisecond),
			wantReason: reasonInsufficient,
		},
		{
			name:       "excessive swing",
			samples:    okSeries(id, 1, base, []float64{5, 5, 5, 5.1}, time.Second),
			evaluateAt: base.Add(3500 * time.Millisecond),
			wantReason: reasonSwing,
		},
		{
			name:       "span too short",
			samples:    okSeries(id, 1, base, []float64{5, 5.001, 5.002, 5.003}, 500*time.Millisecond),
			evaluateAt: base.Add(2000 * time.Millisecond),
			wantReason: reasonSpan,
		},
		{
			name:       "stale latest reading",
			samples:    okSeries(id, 1, base, []float64{5, 5.001, 5.002, 5.003}, time.Second),
			evaluateAt: base.Add(14 * time.Second),
			wantReason: reasonStale,
		},
		{
			name:       "abnormal sample",
			samples:    []model.LevelSample{sample(id, 1, 5, model.QualityOK, base), sample(id, 1, 99, model.QualityAbnormal, base.Add(time.Second)), sample(id, 1, 5, model.QualityOK, base.Add(2*time.Second)), sample(id, 1, 5, model.QualityOK, base.Add(3*time.Second))},
			evaluateAt: base.Add(3500 * time.Millisecond),
			wantReason: reasonAbnormal,
		},
		{
			name:       "missing sample",
			samples:    []model.LevelSample{sample(id, 1, 5, model.QualityOK, base), sample(id, 1, 0, model.QualityMissing, base.Add(time.Second)), sample(id, 1, 5, model.QualityOK, base.Add(2*time.Second)), sample(id, 1, 5, model.QualityOK, base.Add(3*time.Second))},
			evaluateAt: base.Add(3500 * time.Millisecond),
			wantReason: reasonMissing,
		},
		{
			name:       "future timestamp",
			samples:    okSeries(id, 1, base, []float64{5, 5.001, 5.002, 5.003}, time.Second),
			evaluateAt: base.Add(2 * time.Second),
			wantReason: reasonFuture,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			window := NewLevelWindow()
			window.Begin(id, 1)
			for _, each := range tc.samples {
				if err := window.Add(each); err != nil {
					t.Fatalf("add: %v", err)
				}
			}
			verdict := window.evaluate(config, tc.evaluateAt)
			if verdict.decision != model.LevelHold {
				t.Fatalf("expected hold, got release")
			}
			if !strings.HasPrefix(verdict.reason, tc.wantReason) {
				t.Fatalf("reason = %q, want prefix %q", verdict.reason, tc.wantReason)
			}
		})
	}
}

func TestWindowIsolatesTransitGenerations(t *testing.T) {
	window := NewLevelWindow()
	first := uuid.New()
	second := uuid.New()
	base := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)

	window.Begin(first, 1)
	for _, each := range okSeries(first, 1, base, []float64{5, 5.001, 5.002, 5.003}, time.Second) {
		if err := window.Add(each); err != nil {
			t.Fatalf("add first generation: %v", err)
		}
	}
	// A new lockage starts: its window must be empty.
	window.Begin(second, 2)
	if got := window.Samples(); len(got) != 0 {
		t.Fatalf("new generation inherited %d old samples", len(got))
	}
	if err := window.Add(sample(first, 1, 5, model.QualityOK, base.Add(4*time.Second))); err == nil {
		t.Fatal("accepted sample from previous generation")
	}
	verdict := window.evaluate(model.DefaultLevelConfig(), base.Add(5*time.Second))
	if verdict.decision != model.LevelHold || !strings.HasPrefix(verdict.reason, reasonInsufficient) {
		t.Fatalf("new generation should start insufficient, got %q", verdict.reason)
	}
}

func TestWindowRejectsUnboundAndOutOfOrderSamples(t *testing.T) {
	window := NewLevelWindow()
	id := uuid.New()
	base := time.Now().UTC()
	if err := window.Add(sample(id, 1, 5, model.QualityOK, base)); err == nil {
		t.Fatal("accepted sample before Begin")
	}
	window.Begin(id, 1)
	if err := window.Add(sample(id, 1, 5, model.QualityOK, base)); err != nil {
		t.Fatalf("first add: %v", err)
	}
	if err := window.Add(sample(id, 1, 5, model.QualityOK, base.Add(-time.Second))); err == nil {
		t.Fatal("accepted out-of-order sample")
	}
}
