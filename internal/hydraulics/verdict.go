package hydraulics

import (
	"fmt"
	"time"

	"github.com/wyw14/cry-96/internal/model"
)

// LevelVerdict explains whether the window currently proves a stable level.
// Reasons lists every unmet condition, so operators see why release is delayed.
type LevelVerdict struct {
	Stable       bool      `json:"stable"`
	Reasons      []string  `json:"reasons"`
	SampleCount  int       `json:"sample_count"`
	SpanSeconds  float64   `json:"span_seconds"`
	FluctuationM float64   `json:"fluctuation_m"`
	MinMeters    float64   `json:"min_meters"`
	MaxMeters    float64   `json:"max_meters"`
	LatestAt     time.Time `json:"latest_at,omitempty"`
	EvaluatedAt  time.Time `json:"evaluated_at"`
}

func evaluateSamples(samples []model.LevelSample, policy LevelPolicy, now time.Time) LevelVerdict {
	verdict := LevelVerdict{Reasons: []string{}, EvaluatedAt: now.UTC()}
	if len(samples) == 0 {
		verdict.Reasons = append(verdict.Reasons, "no level samples recorded for the current transit")
		return verdict
	}
	oldest, newest := samples[0].ObservedAt, samples[0].ObservedAt
	minimum, maximum := samples[0].Meters, samples[0].Meters
	stale := 0
	for _, sample := range samples {
		if sample.ObservedAt.Before(oldest) {
			oldest = sample.ObservedAt
		}
		if sample.ObservedAt.After(newest) {
			newest = sample.ObservedAt
		}
		if sample.Meters < minimum {
			minimum = sample.Meters
		}
		if sample.Meters > maximum {
			maximum = sample.Meters
		}
		if now.Sub(sample.ObservedAt) > policy.MaxSampleAge {
			stale++
		}
	}
	verdict.SampleCount = len(samples)
	verdict.SpanSeconds = newest.Sub(oldest).Seconds()
	verdict.FluctuationM = maximum - minimum
	verdict.MinMeters = minimum
	verdict.MaxMeters = maximum
	verdict.LatestAt = newest.UTC()
	if len(samples) < policy.MinSamples {
		verdict.Reasons = append(verdict.Reasons,
			fmt.Sprintf("insufficient samples: %d of %d required", len(samples), policy.MinSamples))
	}
	if verdict.SpanSeconds < policy.MinSpan.Seconds() {
		verdict.Reasons = append(verdict.Reasons,
			fmt.Sprintf("observation span %.0fs below required %.0fs", verdict.SpanSeconds, policy.MinSpan.Seconds()))
	}
	if verdict.FluctuationM > policy.FluctuationM {
		verdict.Reasons = append(verdict.Reasons,
			fmt.Sprintf("level fluctuation %.3fm exceeds %.3fm", verdict.FluctuationM, policy.FluctuationM))
	}
	if stale > 0 {
		verdict.Reasons = append(verdict.Reasons,
			fmt.Sprintf("%d sample(s) older than %.0fs", stale, policy.MaxSampleAge.Seconds()))
	}
	verdict.Stable = len(verdict.Reasons) == 0
	return verdict
}
