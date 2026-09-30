package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/wyw14/cry-96/internal/hydraulics"
	"github.com/wyw14/cry-96/internal/model"
)

type levelSampleInput struct {
	CycleID    string    `json:"cycle_id"`
	Generation uint64    `json:"generation"`
	Meters     float64   `json:"meters"`
	ObservedAt time.Time `json:"observed_at,omitempty"`
}

func submitLevelSampleHandler(runtime *Runtime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input levelSampleInput
		if err := decodeJSON(r, &input); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		cycleID, err := parseUUID(input.CycleID)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		chamberID := chi.URLParam(r, "chamberID")
		cycleValue, ok := runtime.Cycles.Current(chamberID)
		if !ok || !cycleValue.IdentityMatches(cycleID, input.Generation) {
			writeError(w, http.StatusConflict, errors.New("sample does not match the active transit for this chamber"))
			return
		}
		observedAt := input.ObservedAt
		if observedAt.IsZero() {
			observedAt = runtime.Now()
		}
		sample := model.LevelSample{
			CycleID: cycleID, Generation: input.Generation,
			Meters: input.Meters, ObservedAt: observedAt.UTC(),
		}
		verdict, err := runtime.Hydraulics.Observe(chamberID, sample)
		if errors.Is(err, hydraulics.ErrNoLevelWindow) {
			writeError(w, http.StatusConflict, err)
			return
		}
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, err)
			return
		}
		writeJSON(w, http.StatusOK, verdict)
	}
}

func levelStatusHandler(runtime *Runtime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		chamberID := chi.URLParam(r, "chamberID")
		var cycleID uuid.UUID
		var generation uint64
		if cycleValue, ok := runtime.Cycles.Current(chamberID); ok {
			cycleID, generation = cycleValue.ID, cycleValue.Generation
		}
		writeJSON(w, http.StatusOK, runtime.Hydraulics.WindowStatus(chamberID, cycleID, generation))
	}
}
