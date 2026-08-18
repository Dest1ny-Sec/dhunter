package handler

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/dhunter/dhunter/internal/store"
)

// AssetHandler handles GET/POST /api/targets/:id/assets — the structured
// project asset inventory (subdomains, endpoints, services, ...) that the
// agent discovers and the UI shows as a list/tree.
type AssetHandler struct {
	Stores *store.Stores
}

func NewAssetHandler(s *store.Stores) *AssetHandler {
	return &AssetHandler{Stores: s}
}

var assetTypes = map[string]struct{}{
	"root-domain": {}, "subdomain": {}, "ip": {}, "service": {}, "app": {}, "endpoint": {},
}

// createAssetReq is the body for POST /api/targets/:id/assets.
type createAssetReq struct {
	Type     string `json:"type"`
	Value    string `json:"value"`
	Meta     string `json:"meta"`
	ParentID string `json:"parent_id"`
	// RunID is the discovering run (audit); optional, filled from query if absent.
	RunID string `json:"run_id"`
}

// List handles GET /api/targets/:id/assets.
func (h *AssetHandler) List(c *gin.Context) {
	id := c.Param("id")
	if _, err := h.Stores.Targets.Get(c.Request.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "target not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	assets, err := h.Stores.Assets.ListByTarget(c.Request.Context(), id, 1000)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"assets": assets})
}

// Create handles POST /api/targets/:id/assets — the agent's write_asset tool
// lands here. Same-type+value rows for a target are deduped (409 → existing).
func (h *AssetHandler) Create(c *gin.Context) {
	id := c.Param("id")
	if _, err := h.Stores.Targets.Get(c.Request.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "target not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	var body createAssetReq
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json body"})
		return
	}
	body.Value = strings.TrimSpace(body.Value)
	body.Type = strings.ToLower(strings.TrimSpace(body.Type))
	if body.Value == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "value required"})
		return
	}
	if body.Type == "" {
		body.Type = "endpoint"
	}
	if _, ok := assetTypes[body.Type]; !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "type must be one of root-domain|subdomain|ip|service|app|endpoint"})
		return
	}
	a := &store.Asset{
		TargetID: id,
		RunID:    body.RunID,
		Type:     body.Type,
		Value:    body.Value,
		Meta:     strings.TrimSpace(body.Meta),
		ParentID: strings.TrimSpace(body.ParentID),
		CreatedAt: time.Now().UTC(),
	}
	created, existingID, err := h.Stores.Assets.CreateIfAbsent(c.Request.Context(), a)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if !created {
		c.JSON(http.StatusConflict, gin.H{"error": "duplicate asset", "existing_id": existingID})
		return
	}
	c.JSON(http.StatusCreated, a)
}
