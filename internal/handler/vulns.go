package handler

import (
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
		body.Status = "open"
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
		CreatedAt:      time.Now().UTC(),
	}
	if err := h.Stores.Vulns.Create(c.Request.Context(), v); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, v)
}
