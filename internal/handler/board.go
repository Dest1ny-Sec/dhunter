// BoardHandler exposes the blackboard (facts / intents / hints) for a run.
//
// The board is the durable coordination point for the agent's workers:
// workers claim intents (CAS), explore, then conclude them into facts.
// Humans read the graph via GET /graph and can inject hints at any time.
package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/dhunter/dhunter/internal/store"
	"github.com/dhunter/dhunter/internal/stream"
)

// BoardHandler holds stores + hub for publishing board events to SSE.
type BoardHandler struct {
	Stores *store.Stores
	Hub    *stream.Hub
}

// NewBoardHandler constructs a BoardHandler.
func NewBoardHandler(s *store.Stores, h *stream.Hub) *BoardHandler {
	return &BoardHandler{Stores: s, Hub: h}
}

// ---- facts --------------------------------------------------------------

func (h *BoardHandler) ListFacts(c *gin.Context) {
	runID := c.Param("id")
	if !h.runExists(c, runID) {
		return
	}
	fs, err := h.Stores.Board.Facts.ListByRun(c.Request.Context(), runID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"facts": fs})
}

type createFactReq struct {
	Description string `json:"description"`
	Source      string `json:"source"`
}

func (h *BoardHandler) CreateFact(c *gin.Context) {
	runID := c.Param("id")
	if !h.runExists(c, runID) {
		return
	}
	var body createFactReq
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json body"})
		return
	}
	body.Description = strings.TrimSpace(body.Description)
	if body.Description == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "description required"})
		return
	}
	f := &store.Fact{
		RunID:       runID,
		Description: body.Description,
		Source:      body.Source,
		CreatedAt:   time.Now().UTC(),
	}
	if err := h.Stores.Board.Facts.Create(c.Request.Context(), f); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.publish(c, runID, "fact_created", "fact: "+f.Description)
	c.JSON(http.StatusCreated, f)
}

// ---- intents -------------------------------------------------------------

func (h *BoardHandler) ListIntents(c *gin.Context) {
	runID := c.Param("id")
	if !h.runExists(c, runID) {
		return
	}
	its, err := h.Stores.Board.Intents.ListByRun(c.Request.Context(), runID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"intents": its})
}

type createIntentReq struct {
	From        []string `json:"from"`
	Description string   `json:"description"`
	Creator     string   `json:"creator"`
}

func (h *BoardHandler) CreateIntent(c *gin.Context) {
	runID := c.Param("id")
	if !h.runExists(c, runID) {
		return
	}
	var body createIntentReq
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json body"})
		return
	}
	body.Description = strings.TrimSpace(body.Description)
	if body.Description == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "description required"})
		return
	}
	if body.From == nil {
		body.From = []string{}
	}
	it := &store.Intent{
		RunID:       runID,
		FromFacts:   body.From,
		Description: body.Description,
		Creator:     body.Creator,
		Status:      store.IntentOpen,
		CreatedAt:   time.Now().UTC(),
	}
	if err := h.Stores.Board.Intents.Create(c.Request.Context(), it); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.publish(c, runID, "intent_created", "intent: "+it.Description)
	c.JSON(http.StatusCreated, it)
}

type claimReq struct {
	Worker string `json:"worker"`
}

func (h *BoardHandler) ClaimIntent(c *gin.Context) {
	runID, intentID := c.Param("id"), c.Param("iid")
	if !h.runExists(c, runID) {
		return
	}
	var body claimReq
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json body"})
		return
	}
	if strings.TrimSpace(body.Worker) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "worker required"})
		return
	}
	if err := h.Stores.Board.Intents.Claim(c.Request.Context(), runID, intentID, body.Worker); err != nil {
		if errors.Is(err, store.ErrIntentClaimed) {
			c.JSON(http.StatusConflict, gin.H{"error": "intent already claimed"})
			return
		}
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "intent not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.publish(c, runID, "intent_claimed", "intent claimed by "+body.Worker)
	c.JSON(http.StatusOK, gin.H{"ok": true, "intent_id": intentID, "worker": body.Worker})
}

func (h *BoardHandler) ReleaseIntent(c *gin.Context) {
	runID, intentID := c.Param("id"), c.Param("iid")
	if !h.runExists(c, runID) {
		return
	}
	var body claimReq
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json body"})
		return
	}
	if err := h.Stores.Board.Intents.Release(c.Request.Context(), runID, intentID, body.Worker); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "intent not claimed by this worker"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.publish(c, runID, "intent_released", "intent released by "+body.Worker)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

type concludeReq struct {
	Worker      string `json:"worker"`
	Description string `json:"description"`
}

func (h *BoardHandler) ConcludeIntent(c *gin.Context) {
	runID, intentID := c.Param("id"), c.Param("iid")
	if !h.runExists(c, runID) {
		return
	}
	var body concludeReq
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json body"})
		return
	}
	body.Description = strings.TrimSpace(body.Description)
	if body.Description == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "description required"})
		return
	}
	f, err := h.Stores.Board.Intents.Conclude(c.Request.Context(), runID, intentID, body.Worker, body.Description)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "intent not claimed by this worker"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.publish(c, runID, "intent_concluded", "fact: "+f.Description)
	c.JSON(http.StatusOK, gin.H{"ok": true, "fact": f})
}

func (h *BoardHandler) FailIntent(c *gin.Context) {
	runID, intentID := c.Param("id"), c.Param("iid")
	if !h.runExists(c, runID) {
		return
	}
	var body claimReq
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json body"})
		return
	}
	if err := h.Stores.Board.Intents.Fail(c.Request.Context(), runID, intentID, body.Worker); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "intent not claimed by this worker"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.publish(c, runID, "intent_failed", "intent failed by "+body.Worker)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ---- hints ---------------------------------------------------------------

func (h *BoardHandler) ListHints(c *gin.Context) {
	runID := c.Param("id")
	if !h.runExists(c, runID) {
		return
	}
	hs, err := h.Stores.Board.Hints.ListByRun(c.Request.Context(), runID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"hints": hs})
}

type createHintReq struct {
	Content string `json:"content"`
	Creator string `json:"creator"`
}

func (h *BoardHandler) CreateHint(c *gin.Context) {
	runID := c.Param("id")
	if !h.runExists(c, runID) {
		return
	}
	var body createHintReq
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json body"})
		return
	}
	body.Content = strings.TrimSpace(body.Content)
	if body.Content == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "content required"})
		return
	}
	hint := &store.Hint{RunID: runID, Content: body.Content, Creator: body.Creator, CreatedAt: time.Now().UTC()}
	if err := h.Stores.Board.Hints.Create(c.Request.Context(), hint); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.publish(c, runID, "hint_created", "hint: "+hint.Content)
	c.JSON(http.StatusCreated, hint)
}

// ---- graph ---------------------------------------------------------------

// Graph returns everything the UI / workers need to render the board.
type Graph struct {
	Run     *store.Run        `json:"run"`
	Facts   []*store.Fact     `json:"facts"`
	Intents []*store.Intent   `json:"intents"`
	Hints   []*store.Hint     `json:"hints"`
}

func (h *BoardHandler) Graph(c *gin.Context) {
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
	ctx := c.Request.Context()
	fs, err := h.Stores.Board.Facts.ListByRun(ctx, runID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	its, err := h.Stores.Board.Intents.ListByRun(ctx, runID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	hs, err := h.Stores.Board.Hints.ListByRun(ctx, runID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, Graph{Run: run, Facts: fs, Intents: its, Hints: hs})
}

// ---- helpers -------------------------------------------------------------

func (h *BoardHandler) runExists(c *gin.Context, runID string) bool {
	if _, err := h.Stores.Runs.Get(c.Request.Context(), runID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "run not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return false
	}
	return true
}

// publish fans a board activity event out to SSE subscribers so the run
// page's thinking stream stays live even for board mutations that happen
// through the API (not through the Python agent's event stream).
func (h *BoardHandler) publish(c *gin.Context, runID, eventType, summary string) {
	if h.Hub == nil {
		return
	}
	payload, err := marshalBoardEvent(runID, eventType, summary)
	if err != nil {
		return
	}
	h.Hub.Publish(runID, payload)
}

// marshalBoardEvent builds the flat SSE payload the frontend already
// understands: {event, data, ts}.
func marshalBoardEvent(runID, eventType, summary string) ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"event": eventType,
		"data": map[string]interface{}{
			"run_id":  runID,
			"summary": summary,
		},
		"ts": time.Now().UnixMilli(),
	})
}
