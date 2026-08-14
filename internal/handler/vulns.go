package handler

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/dhunter/dhunter/internal/store"
)

// VulnsHandler handles GET /api/vulnerabilities (global list with filters)
// and POST /api/vulnerabilities (create one — used by write_finding fallback
// or by external automation that already has a run_id).
type VulnsHandler struct {
	Stores *store.Stores
}

// NewVulnsHandler constructs a VulnsHandler.
func NewVulnsHandler(s *store.Stores) *VulnsHandler {
	return &VulnsHandler{Stores: s}
}

// List handles GET /api/vulnerabilities with optional ?run_id, ?target_id, ?severity.
func (h *VulnsHandler) List(c *gin.Context) {
	vs, err := h.Stores.Vulns.ListAll(
		c.Request.Context(),
		c.Query("run_id"),
		c.Query("target_id"),
		c.Query("severity"),
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"vulnerabilities": vs})
}

// Create handles POST /api/vulnerabilities.
//
// Accepts either a nested object (full schema) or the flat shape that
// the Python agent's write_finding fallback uses:
//
//	{
//	  "run_id":     "...",      // required
//	  "target_id":  "...",      // optional
//	  "title":      "...",      // required
//	  "severity":   "high",     // required
//	  "target":     "https://...",
//	  "evidence":   "..."
//	}
func (h *VulnsHandler) Create(c *gin.Context) {
	var body struct {
		RunID          string `json:"run_id"`
		TargetID       string `json:"target_id"`
		Title          string `json:"title"`
		Severity       string `json:"severity"`
		Status         string `json:"status"`
		Target         string `json:"target"`
		Evidence       string `json:"evidence"`
		Impact         string `json:"impact"`
		Recommendation string `json:"recommendation"`
		Reproduction   string `json:"reproduction"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json: " + err.Error()})
		return
	}
	body.Title = strings.TrimSpace(body.Title)
	body.Severity = strings.ToLower(strings.TrimSpace(body.Severity))
	if body.Title == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "title required"})
		return
	}
	if body.Severity == "" {
		body.Severity = "medium"
	}
	if body.Status == "" {
		body.Status = "pending" // new findings wait for the verifier
	}
	if body.RunID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "run_id required"})
		return
	}
	// Validate run exists (cheap FK sanity)
	if _, err := h.Stores.Runs.Get(c.Request.Context(), body.RunID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "run not found: " + body.RunID})
		return
	}
	// Dedup within the run: same title + target is a duplicate. The check
	// and insert are atomic (single transaction) so concurrent workers
	// submitting the same finding can't both insert it.
	v := &store.Vulnerability{
		RunID:          body.RunID,
		TargetID:       body.TargetID,
		Title:          body.Title,
		Severity:       body.Severity,
		Status:         body.Status,
		Target:         body.Target,
		Evidence:       body.Evidence,
		Impact:         body.Impact,
		Recommendation: body.Recommendation,
		Reproduction:   body.Reproduction,
		CreatedAt:      time.Now().UTC(),
	}
	created, existingID, err := h.Stores.Vulns.CreateIfAbsent(c.Request.Context(), v)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if !created {
		c.JSON(http.StatusConflict, gin.H{"error": "duplicate vulnerability", "existing_id": existingID})
		return
	}
	c.JSON(http.StatusCreated, v)
}

// validVulnStatuses are the lifecycle states a finding can be in.
var validVulnStatuses = map[string]struct{}{
	"pending":   {}, // submitted, awaiting verification
	"confirmed": {}, // verifier reproduced it
	"dismissed": {}, // verifier could not reproduce
	"open":      {}, // legacy / manually created
}

// validVulnSeverities accepted by the severity cap.
var validVulnSeverities = map[string]struct{}{
	"critical": {}, "high": {}, "medium": {}, "low": {}, "info": {},
}

// Patch handles PATCH /api/vulnerabilities/:id — the verifier flips the
// lifecycle status (pending -> confirmed/dismissed) and may correct the
// severity (SRC calibration).
func (h *VulnsHandler) Patch(c *gin.Context) {
	var body struct {
		Status   string `json:"status"`
		Severity string `json:"severity"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json body"})
		return
	}
	body.Status = strings.ToLower(strings.TrimSpace(body.Status))
	body.Severity = strings.ToLower(strings.TrimSpace(body.Severity))

	if body.Status != "" {
		if _, ok := validVulnStatuses[body.Status]; !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "status must be one of pending|confirmed|dismissed|open"})
			return
		}
		if err := h.Stores.Vulns.UpdateStatus(c.Request.Context(), c.Param("id"), body.Status); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "vulnerability not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	if body.Severity != "" {
		if _, ok := validVulnSeverities[body.Severity]; !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "severity must be one of critical|high|medium|low|info"})
			return
		}
		if err := h.Stores.Vulns.UpdateSeverity(c.Request.Context(), c.Param("id"), body.Severity); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "vulnerability not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "id": c.Param("id"), "status": body.Status, "severity": body.Severity})
}
