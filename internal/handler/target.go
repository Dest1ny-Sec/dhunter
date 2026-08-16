// Package handler wires Gin routes to the store / agent / stream layers.
//
// Handlers stay thin: they validate input, call the store, and return
// JSON. Anything that smells like business logic lives in the
// underlying package.
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/dhunter/dhunter/internal/store"
)

// TargetHandler handles /api/targets.
type TargetHandler struct {
	Stores *store.Stores
}

// NewTargetHandler constructs a TargetHandler.
func NewTargetHandler(s *store.Stores) *TargetHandler {
	return &TargetHandler{Stores: s}
}

// targetTypes the API accepts. `auto` triggers the heuristic detector.
var targetTypes = map[string]struct{}{
	"auto":    {},
	"company": {},
	"domain":  {},
	"url":     {},
	"ip":      {},
}

// createTargetReq is the JSON body for POST /api/targets.
type createTargetReq struct {
	Input      string `json:"input"`
	Type       string `json:"type"`
	Name       string `json:"name"`
	RedLines   string `json:"red_lines"`
	MaxWorkers int    `json:"max_workers"`
}

// Create handles POST /api/targets.
//
// Flow:
//  1. validate `type` (default "auto")
//  2. detect type when `auto`
//  3. compute the `normalized` representation
//  4. persist and return
func (h *TargetHandler) Create(c *gin.Context) {
	var req createTargetReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json body"})
		return
	}
	req.Input = strings.TrimSpace(req.Input)
	if req.Input == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "input is required"})
		return
	}
	if req.Type == "" {
		req.Type = "auto"
	}
	if _, ok := targetTypes[req.Type]; !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "type must be one of auto|company|domain|url|ip"})
		return
	}

	detected, normalized, attrs, err := classify(req.Input, req.Type)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Per-project worker concurrency cap (0 = platform default).
	if req.MaxWorkers > 0 {
		if req.MaxWorkers > 16 {
			req.MaxWorkers = 16
		}
		attrs["max_workers"] = req.MaxWorkers
	}

	attrJSON, err := json.Marshal(attrs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "encode attributes: " + err.Error()})
		return
	}

	t := &store.Target{
		Type:       detected,
		Value:      req.Input,
		Normalized: normalized,
		Attributes: attrJSON,
		RedLines:   strings.TrimSpace(req.RedLines),
		Name:       strings.TrimSpace(req.Name),
		CreatedAt:  time.Now().UTC(),
	}
	if err := h.Stores.Targets.Create(c.Request.Context(), t); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "create target: " + err.Error()})
		return
	}
	c.JSON(http.StatusCreated, t)
}

// Get handles GET /api/targets/:id.
func (h *TargetHandler) Get(c *gin.Context) {
	id := c.Param("id")
	t, err := h.Stores.Targets.Get(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "target not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, t)
}

// List handles GET /api/targets.
func (h *TargetHandler) List(c *gin.Context) {
	ts, err := h.Stores.Targets.List(c.Request.Context(), 100)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"targets": ts})
}

// accountReq is one logged-in account for A/B IDOR testing. The agent can
// hold two sessions (account a / account b) and switch between them to
// check cross-account access (user A's session reads user B's resource).
type accountReq struct {
	Username string            `json:"username"`
	Password string            `json:"password"`
	LoginURL string            `json:"login_url"`
	Cookies  string            `json:"cookies"`
	Headers  map[string]string `json:"headers,omitempty"`
	Note     string            `json:"note,omitempty"`
}

// authReq is the body for PATCH /api/targets/:id/auth.
type authReq struct {
	Cookies string            `json:"cookies"`
	Headers map[string]string `json:"headers"`
	Note    string            `json:"note"`
	// AccountA / AccountB carry the two sessions for A/B IDOR testing.
	// They are persisted verbatim (JSON) so the Python agent can pick up
	// username/password for auto-login and cookies/headers for injection.
	AccountA *accountReq `json:"account_a,omitempty"`
	AccountB *accountReq `json:"account_b,omitempty"`
}

// SetAuth stores (or clears) the authenticated session the agent should
// auto-inject when testing this target. Stored as JSON on the target.
func (h *TargetHandler) SetAuth(c *gin.Context) {
	id := c.Param("id")
	var body authReq
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json body"})
		return
	}
	authJSON, err := json.Marshal(body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := h.Stores.Targets.SetAuth(c.Request.Context(), id, string(authJSON)); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "target not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "id": id})
}

// SetRedLines handles PATCH /api/targets/:id/redlines.
func (h *TargetHandler) SetRedLines(c *gin.Context) {
	id := c.Param("id")
	var body struct {
		RedLines string `json:"red_lines"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json body"})
		return
	}
	if err := h.Stores.Targets.SetRedLines(c.Request.Context(), id, body.RedLines); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "target not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "id": id})
}

// Delete handles DELETE /api/targets/:id — removes the target and all of
// its runs, findings, and board state.
func (h *TargetHandler) Delete(c *gin.Context) {
	id := c.Param("id")
	if err := h.Stores.Targets.Delete(c.Request.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "target not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "id": id})
}

// ipV4 matches a literal IPv4 address. We intentionally don't bother with
// IPv6 here — it's a stretch goal for v0.2.
var ipV4 = regexp.MustCompile(`^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$`)

// domainSuffixed is a fast check for "looks like a domain" before we
// fall through to the more expensive URL parser. We require at least one
// dot, no scheme, no whitespace, and only letters/digits/hyphens/dots.
var domainSuffixed = regexp.MustCompile(`^[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)+$`)

// classify inspects the input and returns the resolved type, the
// normalized representation, and any extra attributes we want to keep.
//
// The `requested` argument is the user-supplied type (or "auto"); we
// trust it when it's not "auto", and otherwise apply the heuristic
// detector.
func classify(input, requested string) (string, string, map[string]any, error) {
	attrs := map[string]any{}

	switch requested {
	case "company":
		return "company", normalizeCompany(input), attrs, nil
	case "domain":
		if !domainSuffixed.MatchString(input) {
			return "", "", nil, errors.New("invalid domain")
		}
		return "domain", extractDomain(input), attrs, nil
	case "url":
		u, err := url.Parse(input)
		if err != nil || u.Host == "" {
			return "", "", nil, errors.New("invalid url")
		}
		attrs["scheme"] = u.Scheme
		attrs["path"] = u.Path
		return "url", strings.ToLower(u.Host), attrs, nil
	case "ip":
		if net.ParseIP(input) == nil {
			return "", "", nil, errors.New("invalid ip")
		}
		return "ip", input, attrs, nil
	}

	// requested == "auto" — heuristic detection.
	if strings.HasPrefix(input, "http://") || strings.HasPrefix(input, "https://") {
		u, err := url.Parse(input)
		if err != nil || u.Host == "" {
			return "", "", nil, errors.New("invalid url")
		}
		attrs["scheme"] = u.Scheme
		attrs["path"] = u.Path
		return "url", strings.ToLower(u.Host), attrs, nil
	}
	if ipV4.MatchString(input) {
		if net.ParseIP(input) == nil {
			return "", "", nil, errors.New("invalid ip")
		}
		return "ip", input, attrs, nil
	}
	if domainSuffixed.MatchString(input) {
		return "domain", extractDomain(input), attrs, nil
	}
	return "company", normalizeCompany(input), attrs, nil
}

// normalizeCompany lower-cases and collapses internal whitespace.
func normalizeCompany(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	// Collapse runs of whitespace into single spaces.
	parts := strings.Fields(s)
	return strings.Join(parts, " ")
}

// extractDomain returns the registrable portion. For MVP we just
// lower-case and return the last two labels; a real implementation
// would consult the public suffix list. Documented as a follow-up.
func extractDomain(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return s
}

// quietImportCtx is here so the file can stay self-contained even
// if a future change needs to plumb a context beyond the gin request.
var _ = context.Background
