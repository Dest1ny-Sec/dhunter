package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/dhunter/dhunter/internal/store"
)

// RunsHandler handles GET /api/runs, /api/runs/:id, and the per-run
// message / vulnerability sub-routes. (SSE lives in sse.go.)
type RunsHandler struct {
	Stores *store.Stores
}

// NewRunsHandler constructs a RunsHandler.
func NewRunsHandler(s *store.Stores) *RunsHandler {
	return &RunsHandler{Stores: s}
}

// List handles GET /api/runs.
func (h *RunsHandler) List(c *gin.Context) {
	rs, err := h.Stores.Runs.List(c.Request.Context(), 100)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"runs": rs})
}

// Get handles GET /api/runs/:id.
func (h *RunsHandler) Get(c *gin.Context) {
	id := c.Param("id")
	r, err := h.Stores.Runs.Get(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "run not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, r)
}

// Messages handles GET /api/runs/:id/messages.
func (h *RunsHandler) Messages(c *gin.Context) {
	id := c.Param("id")
	if _, err := h.Stores.Runs.Get(c.Request.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "run not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ms, err := h.Stores.Messages.ListByRun(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"messages": ms})
}

// Vulnerabilities handles GET /api/runs/:id/vulnerabilities.
func (h *RunsHandler) Vulnerabilities(c *gin.Context) {
	id := c.Param("id")
	if _, err := h.Stores.Runs.Get(c.Request.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "run not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	vs, err := h.Stores.Vulns.ListByRun(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"vulnerabilities": vs})
}

// ToolCalls handles GET /api/runs/:id/tool_calls — every tool invocation
// the agent made during this run (with arguments, results, errors, duration).
func (h *RunsHandler) ToolCalls(c *gin.Context) {
	id := c.Param("id")
	if _, err := h.Stores.Runs.Get(c.Request.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "run not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	tcs, err := h.Stores.ToolCalls.ListByRun(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"tool_calls": tcs})
}

// ProjectRuns handles GET /api/targets/:id/runs — the conversation
// history (all runs) against a given target. This is the "project
// session" view: the operator can scroll back, re-run, diff results.
func (h *RunsHandler) ProjectRuns(c *gin.Context) {
	tid := c.Param("id")
	if _, err := h.Stores.Targets.Get(c.Request.Context(), tid); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "target not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	rs, err := h.Stores.Runs.ListByTarget(c.Request.Context(), tid, 200)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"runs": rs})
}
