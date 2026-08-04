package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/r3dpan/project-descendence/internal/store"
)

const defaultRunTimeoutSeconds = 3600

// --- Request/response objects ---

type runCreateRequest struct {
	ImageRef       string   `json:"imageRef"`
	Argv           []string `json:"argv"`
	TimeoutSeconds *int32   `json:"timeoutSeconds"`
}

type runResponse struct {
	ID             int64      `json:"id"`
	State          string     `json:"state"`
	ImageRef       string     `json:"imageRef"`
	Argv           []string   `json:"argv"`
	TimeoutSeconds int32      `json:"timeoutSeconds"`
	ContainerID    *string    `json:"containerId"`
	ExitCode       *int32     `json:"exitCode"`
	FailureReason  *string    `json:"failureReason"`
	QueuedAt       time.Time  `json:"queuedAt"`
	StartedAt      *time.Time `json:"startedAt"`
	FinishedAt     *time.Time `json:"finishedAt"`
}

// toRunResponse converts a sqlc-generated Run (pgtype-heavy) into the
// wire shape described by the Run schema in api/openapi.yaml.
func toRunResponse(run store.Run) runResponse {
	resp := runResponse{
		ID:             run.ID,
		State:          run.State,
		ImageRef:       run.ImageRef,
		Argv:           run.Argv,
		TimeoutSeconds: run.TimeoutSeconds,
		QueuedAt:       run.QueuedAt.Time,
	}

	if run.ContainerID.Valid {
		resp.ContainerID = &run.ContainerID.String
	}
	if run.ExitCode.Valid {
		resp.ExitCode = &run.ExitCode.Int32
	}
	if run.FailureReason.Valid {
		resp.FailureReason = &run.FailureReason.String
	}
	if run.StartedAt.Valid {
		resp.StartedAt = &run.StartedAt.Time
	}
	if run.FinishedAt.Valid {
		resp.FinishedAt = &run.FinishedAt.Time
	}

	return resp
}

// --- Handlers ---

// Handles run creation. Validates the body, inserts a queued row stamped
// with the caller's principal, and returns 202 + Location - never blocks on
// the container actually running (that's the supervisor's job, Phase 1c).
func (s *APIServer) CreateRunHandler(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalFromContext(r.Context())
	if !ok {
		writeProblem(w, http.StatusInternalServerError, "no principal in request context")
		return
	}

	var req runCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeProblem(w, http.StatusBadRequest, "malformed JSON body")
		return
	}

	if strings.TrimSpace(req.ImageRef) == "" {
		writeProblem(w, http.StatusBadRequest, "imageRef is required")
		return
	}
	if len(req.Argv) == 0 {
		writeProblem(w, http.StatusBadRequest, "argv must have at least one element")
		return
	}

	timeoutSeconds := int32(defaultRunTimeoutSeconds)
	if req.TimeoutSeconds != nil {
		if *req.TimeoutSeconds <= 0 {
			writeProblem(w, http.StatusBadRequest, "timeoutSeconds must be positive")
			return
		}
		timeoutSeconds = *req.TimeoutSeconds
	}

	run, err := s.queries.CreateRun(r.Context(), store.CreateRunParams{
		PrincipalID:    principal.ID,
		ImageRef:       req.ImageRef,
		Argv:           req.Argv,
		TimeoutSeconds: timeoutSeconds,
	})
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "failed creating run")
		return
	}

	w.Header().Set("Location", fmt.Sprintf("/api/v1/runs/%d", run.ID))
	writeJSON(w, http.StatusAccepted, toRunResponse(run))
}
