package handler

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/dhunter/dhunter/internal/agent"
	"github.com/dhunter/dhunter/internal/store"
	"github.com/dhunter/dhunter/internal/stream"
)

// RunHandler handles POST /api/runs.
type RunHandler struct {
	Stores *store.Stores
	Bridge *agent.Bridge
	Hub    *stream.Hub
}

// NewRunHandler constructs a RunHandler.
func NewRunHandler(s *store.Stores, b *agent.Bridge, h *stream.Hub) *RunHandler {
	return &RunHandler{Stores: s, Bridge: b, Hub: h}
}

// createRunReq is the JSON body for POST /api/runs.
type createRunReq struct {
	TargetID  string `json:"target_id"`
	Objective string `json:"objective"`
}

// Create kicks off a new agent run.
//
// We persist the run first (so /api/runs/{id} is queryable even if the
// sidecar is briefly down), then asynchronously ask the Python agent
// to start work and to stream events back. The HTTP response is
// returned as soon as the row is durable.
func (h *RunHandler) Create(c *gin.Context) {
	var req createRunReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json body"})
		return
	}
	if req.TargetID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "target_id is required"})
		return
	}
	target, err := h.Stores.Targets.Get(c.Request.Context(), req.TargetID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "target not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	run := &store.Run{
		TargetID:  target.ID,
		Objective: req.Objective,
		Status:    "running",
		StartedAt: time.Now().UTC(),
	}
	if err := h.Stores.Runs.Create(c.Request.Context(), run); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "create run: " + err.Error()})
		return
	}

	// Fire-and-forget: spawn the sidecar call in a goroutine. We use a
	// detached context so the request finishing doesn't cancel the
	// agent's streaming subscription.
	go h.startAgent(run, target.Value)

	c.JSON(http.StatusAccepted, gin.H{
		"id":     run.ID,
		"run_id": run.ID, // kept for backward compatibility with older clients
		"status": run.Status,
	})
}

// Cancel handles POST /api/runs/:id/cancel. It asks the Python sidecar to
// cancel the run and, as a fallback if the sidecar is unreachable, marks
// the run cancelled locally so the UI never hangs in "running".
func (h *RunHandler) Cancel(c *gin.Context) {
	runID := c.Param("id")
	run, err := h.Stores.Runs.Get(c.Request.Context(), runID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "run not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if run.Status != "running" && run.Status != "pending" {
		c.JSON(http.StatusConflict, gin.H{"error": "run is not running"})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := h.Bridge.CancelRun(ctx, runID); err != nil {
		// Sidecar unreachable — mark cancelled locally so the run isn't
		// stuck as running forever.
		log.Printf("dhunter: cancel run %s: sidecar error: %v", runID, err)
		ended := time.Now().UTC()
		_ = h.Stores.Runs.Update(context.Background(), &store.Run{
			ID: runID, Status: "cancelled", EndedAt: &ended,
		})
	}
	c.JSON(http.StatusAccepted, gin.H{"ok": true, "run_id": runID})
}

// Pause handles POST /api/runs/:id/pause. It asks the Python sidecar to stop
// the agent loop WITHOUT a terminal status (board kept), and marks the run
// "paused" in the store so the UI shows the right state. Resume is just
// POST /api/runs/:id/continue.
func (h *RunHandler) Pause(c *gin.Context) {
	runID := c.Param("id")
	run, err := h.Stores.Runs.Get(c.Request.Context(), runID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "run not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if run.Status != "running" {
		c.JSON(http.StatusConflict, gin.H{"error": "run is not running"})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := h.Bridge.PauseRun(ctx, runID); err != nil {
		// Sidecar unreachable — mark paused locally so the UI isn't stuck.
		log.Printf("dhunter: pause run %s: sidecar error: %v", runID, err)
	}
	_ = h.Stores.Runs.Update(context.Background(), &store.Run{ID: runID, Status: "paused"})
	c.JSON(http.StatusAccepted, gin.H{"ok": true, "run_id": runID, "status": "paused"})
}

// Continue handles POST /api/runs/:id/continue — resumes a finished run
// from its durable board. The Python agent starts a FRESH agent loop that
// re-reads the existing facts/intents (the board IS the session memory), so
// the user can "go deeper" without the LLM context growing unboundedly.
func (h *RunHandler) Continue(c *gin.Context) {
	runID := c.Param("id")
	run, err := h.Stores.Runs.Get(c.Request.Context(), runID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "run not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if run.Status == "running" || run.Status == "pending" {
		c.JSON(http.StatusConflict, gin.H{"error": "run is already running"})
		return
	}
	target, err := h.Stores.Targets.Get(c.Request.Context(), run.TargetID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// reset to running; the agent loop appends to the same board.
	started := time.Now().UTC()
	_ = h.Stores.Runs.Update(c.Request.Context(), &store.Run{
		ID: run.ID, Status: "running", StartedAt: started, EndedAt: nil,
	})
	go h.startAgent(run, target.Value)
	c.JSON(http.StatusAccepted, gin.H{"ok": true, "run_id": run.ID, "status": "running"})
}

// startAgent asks the Python sidecar to start the run and then
// subscribes to its SSE stream. If either call fails we mark the run as
// failed in the store so the UI can surface a useful error.
func (h *RunHandler) startAgent(run *store.Run, targetValue string) {
	// Slightly longer than the Python agent's overall timeout (default 1h)
	// so the bridge never yanks the stream out from under a still-running
	// agent. Both are configurable via env.
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Minute)
	defer cancel()

	if err := h.Bridge.CreateRun(ctx, agent.CreateRunRequest{
		RunID:     run.ID,
		Target:    targetValue,
		Objective: run.Objective,
	}); err != nil {
		h.markFailed(run.ID, err)
		return
	}
	if err := h.Bridge.Subscribe(ctx, run.ID); err != nil {
		h.markFailed(run.ID, err)
		return
	}
	// Successful end-of-stream: ensure the run is marked completed even
	// if the sidecar forgot to send run_done — but only if the sidecar
	// hasn't already set a terminal status (completed/failed/cancelled)
	// via the run_done event. Overwriting here would clobber "cancelled".
	ended := time.Now().UTC()
	if cur, err := h.Stores.Runs.Get(ctx, run.ID); err == nil &&
		(cur.Status == "running" || cur.Status == "pending") {
		_ = h.Stores.Runs.Update(ctx, &store.Run{
			ID:      run.ID,
			Status:  "completed",
			EndedAt: &ended,
		})
	}
}

func (h *RunHandler) markFailed(runID string, cause error) {
	ended := time.Now().UTC()
	_ = h.Stores.Runs.Update(context.Background(), &store.Run{
		ID:      runID,
		Status:  "failed",
		Summary: "agent error: " + cause.Error(),
		EndedAt: &ended,
	})
}
