package handler

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/dhunter/dhunter/internal/store"
	"github.com/dhunter/dhunter/internal/stream"
)

// SSEHandler handles GET /api/runs/:id/events.
//
// The handler subscribes to the in-process hub for the given run and
// relays every event payload to the browser using the SSE wire format.
// A heartbeat comment is sent every `keepalive` to defeat idle proxies.
type SSEHandler struct {
	Hub       *stream.Hub
	Stores    *store.Stores
	KeepAlive time.Duration
	AdminToken string
}

// NewSSEHandler constructs an SSEHandler.
func NewSSEHandler(hub *stream.Hub, stores *store.Stores, keepAlive time.Duration, adminToken string) *SSEHandler {
	return &SSEHandler{Hub: hub, Stores: stores, KeepAlive: keepAlive, AdminToken: adminToken}
}

// Events streams events for a single run.
//
// We deliberately do NOT add this route to the auth middleware list —
// instead the route is registered separately, after the auth middleware,
// and we re-check the token here so SSE clients can use either a header
// or a `?token=` query string.
func (h *SSEHandler) Events(c *gin.Context) {
	// Auth: the route is registered outside the Bearer middleware group so
	// SSE clients can pass the token via `?token=...` (EventSource cannot set
	// headers). The header form works too. We verify here — this is the
	// only gate, so it must not be skipped.
	if !sseTokenMatches(c, h.AdminToken) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized", "hint": "send `Authorization: Bearer <admin_token>` or `?token=<admin_token>`"})
		return
	}

	runID := c.Param("id")
	if _, err := h.Stores.Runs.Get(c.Request.Context(), runID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "run not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// SSE response headers. nginx / cloudflare will close the conn if
	// we don't set these.
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	// A long-lived SSE stream must not be cut by the server's WriteTimeout
	// (60s) — reset the per-connection write deadline to none.
	if rc := http.NewResponseController(c.Writer); rc != nil {
		_ = rc.SetWriteDeadline(time.Time{})
	}
	c.Writer.WriteHeader(http.StatusOK)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming unsupported"})
		return
	}

	ch, cancel := h.Hub.Subscribe(runID, 32)
	defer cancel()

	// Send an initial "open" event so the client knows the stream is
	// live even if no agent events have arrived yet.
	_, _ = fmt.Fprintf(c.Writer, "event: open\ndata: {\"run_id\":%q}\n\n", runID)
	flusher.Flush()

	heartbeat := time.NewTicker(h.KeepAlive)
	defer heartbeat.Stop()

	c.Stream(func(w io.Writer) bool {
		select {
		case <-c.Request.Context().Done():
			return false
		case <-heartbeat.C:
			// SSE comment line — clients ignore it, intermediaries count it.
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return false
			}
			return true
		case payload, alive := <-ch:
			if !alive {
				return false
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
				return false
			}
			return true
		}
	})
}

// sseTokenMatches verifies the caller against the admin token using the
// Authorization header or the `?token=` query parameter.
func sseTokenMatches(c *gin.Context, adminToken string) bool {
	if adminToken == "" {
		return false
	}
	token := ""
	if h := c.GetHeader("Authorization"); h != "" {
		const prefix = "Bearer "
		if strings.HasPrefix(h, prefix) {
			token = strings.TrimSpace(strings.TrimPrefix(h, prefix))
		}
	}
	if token == "" {
		token = strings.TrimSpace(c.Query("token"))
	}
	return token != "" && token == adminToken
}
