package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/wyw14/cry-96/internal/model"
)

// levelSampleInput is a field telemetry reading. Quality defaults to "ok";
// sensors or upstream validators may mark readings "abnormal" and missed
// reporting slots "missing". Neither bad quality can ever release a gate.
type levelSampleInput struct {
	Meters     float64             `json:"meters"`
	Quality    model.SampleQuality `json:"quality"`
	ObservedAt *time.Time          `json:"observed_at"`
}

type levelSampleResult struct {
	Assessment model.LevelAssessment `json:"assessment"`
	Accepted   bool                  `json:"accepted"`
}

func submitLevelSampleHandler(runtime *Runtime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		chamberID := chi.URLParam(r, "chamberID")
		var input levelSampleInput
		if err := decodeJSON(r, &input); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if input.Quality == "" {
			input.Quality = model.QualityOK
		}
		if input.Quality != model.QualityOK && input.Quality != model.QualityAbnormal && input.Quality != model.QualityMissing {
			writeError(w, http.StatusBadRequest, errors.New("quality must be one of ok, abnormal, missing"))
			return
		}
		cycleValue, ok := runtime.Cycles.Current(chamberID)
		if !ok {
			writeError(w, http.StatusConflict, errors.New("active transit not found"))
			return
		}
		if cycleValue.Stage.Terminal() {
			writeError(w, http.StatusConflict, errors.New("transit is already complete; level samples are no longer accepted"))
			return
		}
		observedAt := runtime.Now().UTC()
		if input.ObservedAt != nil {
			observedAt = input.ObservedAt.UTC()
		}
		sample := model.LevelSample{
			CycleID: cycleValue.ID, Generation: cycleValue.Generation,
			Meters: input.Meters, Quality: input.Quality, ObservedAt: observedAt,
		}
		assessment, err := runtime.Hydraulics.Observe(chamberID, sample)
		if err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, http.StatusAccepted, levelSampleResult{Assessment: assessment, Accepted: true})
	}
}

func levelAssessmentHandler(runtime *Runtime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		chamberID := chi.URLParam(r, "chamberID")
		stage := model.TransitStage("")
		if cycleValue, ok := runtime.Cycles.Current(chamberID); ok {
			stage = cycleValue.Stage
		}
		writeJSON(w, http.StatusOK, runtime.Hydraulics.Assessment(chamberID, stage))
	}
}

func getLevelConfigHandler(runtime *Runtime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, runtime.Hydraulics.Config(chi.URLParam(r, "chamberID")))
	}
}

func putLevelConfigHandler(runtime *Runtime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		chamberID := chi.URLParam(r, "chamberID")
		var config model.LevelConfig
		if err := decodeJSON(r, &config); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := runtime.Hydraulics.Configure(chamberID, config); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, config)
	}
}
