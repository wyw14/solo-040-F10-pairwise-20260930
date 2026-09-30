package hydraulics

import "time"

// LevelPolicy tunes water-level stability judgement for one chamber.
type LevelPolicy struct {
	MinSamples    int           // minimum samples required before stability can hold
	MinSpan       time.Duration // minimum observation span between oldest and newest sample
	FluctuationM  float64       // maximum allowed peak-to-peak fluctuation in meters
	MaxSampleAge  time.Duration // samples older than this at evaluation time are stale
	MaxFutureSkew time.Duration // tolerated clock skew for sample timestamps
	MinMeters     float64       // plausible level range; samples outside it are abnormal
	MaxMeters     float64
}

// DefaultLevelPolicy applies when a chamber has no explicit configuration.
func DefaultLevelPolicy() LevelPolicy {
	return LevelPolicy{
		MinSamples: 4, MinSpan: 30 * time.Second, FluctuationM: 0.05,
		MaxSampleAge: 60 * time.Second, MaxFutureSkew: 5 * time.Second,
		MinMeters: 0, MaxMeters: 20,
	}
}

func (p LevelPolicy) normalized() LevelPolicy {
	defaults := DefaultLevelPolicy()
	if p.MinSamples <= 0 {
		p.MinSamples = defaults.MinSamples
	}
	if p.MinSamples < 2 {
		p.MinSamples = 2
	}
	if p.MinSpan < 0 {
		p.MinSpan = defaults.MinSpan
	}
	if p.FluctuationM <= 0 {
		p.FluctuationM = defaults.FluctuationM
	}
	if p.MaxSampleAge <= 0 {
		p.MaxSampleAge = defaults.MaxSampleAge
	}
	if p.MaxFutureSkew <= 0 {
		p.MaxFutureSkew = defaults.MaxFutureSkew
	}
	if p.MaxMeters <= p.MinMeters {
		p.MinMeters = defaults.MinMeters
		p.MaxMeters = defaults.MaxMeters
	}
	return p
}

func (p LevelPolicy) windowCapacity() int {
	capacity := p.MinSamples * 2
	if capacity < 8 {
		capacity = 8
	}
	return capacity
}
