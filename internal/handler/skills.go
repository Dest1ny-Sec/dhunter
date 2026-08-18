package handler

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/dhunter/dhunter/internal/store"
)

// SkillHandler handles CRUD for the Library → Skills tab.
//
// A "skill" is a markdown prompt the agent reads into context to do
// a focused job (e.g. "Web 攻击面排查" orchestrates subfinder → httpx
// → nuclei). Built-in skills are seeded by SeedBuiltinSkills; user-
// created ones come from the UI.
type SkillHandler struct {
	Stores *store.Stores
}

func NewSkillHandler(s *store.Stores) *SkillHandler {
	return &SkillHandler{Stores: s}
}

var skillNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_.\-]{0,63}$`)

// createSkillReq is the body for POST /api/skills. Source defaults to
// "custom" when omitted; "builtin" rows are write-protected (the UI
// hides Edit on them).
type createSkillReq struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Content     string `json:"content"`
	Category    string `json:"category"`
	Tags        string `json:"tags"`
	Enabled     *bool  `json:"enabled"`
	Source      string `json:"source"`
}

type updateSkillReq = createSkillReq

// List handles GET /api/skills[?source=builtin|custom|community].
func (h *SkillHandler) List(c *gin.Context) {
	source := c.Query("source")
	rows, err := h.Stores.Skills.List(c.Request.Context(), source)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"skills": rows})
}

// Get handles GET /api/skills/:id.
func (h *SkillHandler) Get(c *gin.Context) {
	id := c.Param("id")
	sk, err := h.Stores.Skills.Get(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "skill not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, sk)
}

// Create handles POST /api/skills. UI users create `source='custom'`
// rows; the seed path writes `source='builtin'` directly via the store.
func (h *SkillHandler) Create(c *gin.Context) {
	var body createSkillReq
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json body"})
		return
	}
	body.Name = strings.ToLower(strings.TrimSpace(body.Name))
	if !skillNameRE.MatchString(body.Name) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name must match ^[a-z0-9][a-z0-9_.-]{0,63}$"})
		return
	}
	if body.Source == "builtin" {
		// UI cannot mint builtin rows; reject before it can collide
		// with a seed.
		c.JSON(http.StatusBadRequest, gin.H{"error": "source 'builtin' is reserved; the server seeds these"})
		return
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	source := body.Source
	if source == "" {
		source = "custom"
	}
	sk := &store.Skill{
		Name:        body.Name,
		Title:       strings.TrimSpace(body.Title),
		Description: strings.TrimSpace(body.Description),
		Content:     body.Content,
		Category:    strings.TrimSpace(body.Category),
		Tags:        strings.TrimSpace(body.Tags),
		Enabled:     enabled,
		Source:      source,
		CreatedAt:   time.Now().UTC(),
	}
	if err := h.Stores.Skills.Create(c.Request.Context(), sk); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			c.JSON(http.StatusConflict, gin.H{"error": "name already exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, sk)
}

// Update handles PUT /api/skills/:id. The UI only sends this for
// source='custom' rows; built-ins are toggle-only.
func (h *SkillHandler) Update(c *gin.Context) {
	id := c.Param("id")
	existing, err := h.Stores.Skills.Get(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "skill not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	var body updateSkillReq
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json body"})
		return
	}
	if existing.Source == "builtin" {
		// Allow toggling enabled, but no content edits — builtins are
		// versioned by the server release.
		enabled := existing.Enabled
		if body.Enabled != nil {
			enabled = *body.Enabled
		}
		if err := h.Stores.Skills.Toggle(c.Request.Context(), id, enabled); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	} else {
		body.Name = strings.ToLower(strings.TrimSpace(body.Name))
		if !skillNameRE.MatchString(body.Name) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "name must match ^[a-z0-9][a-z0-9_.-]{0,63}$"})
			return
		}
		enabled := true
		if body.Enabled != nil {
			enabled = *body.Enabled
		}
		updated := *existing
		updated.Name = body.Name
		updated.Title = strings.TrimSpace(body.Title)
		updated.Description = strings.TrimSpace(body.Description)
		updated.Content = body.Content
		updated.Category = strings.TrimSpace(body.Category)
		updated.Tags = strings.TrimSpace(body.Tags)
		updated.Enabled = enabled
		if body.Source != "" {
			updated.Source = body.Source
		}
		if err := h.Stores.Skills.Update(c.Request.Context(), &updated); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "skill not found"})
				return
			}
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				c.JSON(http.StatusConflict, gin.H{"error": "name already exists"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	final, err := h.Stores.Skills.Get(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, final)
}

// Delete handles DELETE /api/skills/:id. Builtins are protected — the
// UI never offers a Delete button on them, and we reject here as a
// belt-and-braces guard.
func (h *SkillHandler) Delete(c *gin.Context) {
	id := c.Param("id")
	existing, err := h.Stores.Skills.Get(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "skill not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if existing.Source == "builtin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "builtin skills cannot be deleted (toggle off instead)"})
		return
	}
	if err := h.Stores.Skills.Delete(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": id})
}

// Toggle handles POST /api/skills/:id/toggle. Body: {enabled: bool}.
// Cheap path for the UI's per-row switch — avoids the full Update.
func (h *SkillHandler) Toggle(c *gin.Context) {
	id := c.Param("id")
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json body"})
		return
	}
	if body.Enabled == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "enabled is required"})
		return
	}
	if err := h.Stores.Skills.Toggle(c.Request.Context(), id, *body.Enabled); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "skill not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": id, "enabled": *body.Enabled})
}

// ----- Built-in seed ----------------------------------------------------

// BuiltinSkills is the curated starter set the server seeds on first
// run. Each entry is a workflow the agent can lean on — they're not
// toys, they're things like "Web 攻击面排查" that orchestrate several
// of the built-in tools in a specific order.
//
// To add a new built-in, append a row here. The seed is idempotent
// (GetByName → skip if found) so old DBs keep their settings.
var BuiltinSkills = []store.Skill{
	{
		Name:        "web-attack-surface",
		Title:       "Web 攻击面排查",
		Description: "子域名 → 存活探测 → 指纹 → 风险面打点的标准工作流",
		Category:    "web",
		Tags:        "recon,subdomain,fingerprint,httpx",
		Enabled:     true,
		Source:      "builtin",
		Content: `# Web 攻击面排查

针对一个根域名，按以下顺序生成攻击面：

1. **资产发现**：用 subfinder_enum + assetfinder_enum 收集子域名；用 fofa_search + baidu_search 补充搜索引擎可达的 URL；记录所有发现到 write_asset（type='subdomain' / 'endpoint'）。
2. **存活探测**：用 httpx_probe 探测每个子域名的 HTTP 状态 + 标题 + 指纹；用 waf_detect 标记 WAF（影响后续请求频率）。
3. **路径爆破**：对核心目标用 katana_crawl 抓取公开路径 + wayback_history / gau_history 找历史端点。
4. **JS 分析**：用 fetch_js + js_analyzer 拉取前端 JS，提取 API 路径、密钥、用户标识。
5. **风险打点**：基于指纹选择 poc_scaffold 生成针对性的 PoC 模板；用 write_finding 记录高置信度漏洞。
6. **报告**：所有发现汇总到 write_fact 板，verifier 会做机械复现。

## 红线

- 严格遵守目标授权范围 (authorization 字段)
- 限速 0.5s/host，避免触发 WAF
- 不要重复爆破已尝试过的路径
- 重大发现（critical / high）必须 verifier 复现后才写 finding`,
	},
	{
		Name:        "api-fuzz-workflow",
		Title:       "API Fuzz 工作流",
		Description: "认证绕过 + 越权 + 参数污染的系统化扫描",
		Category:    "api",
		Tags:        "fuzz,auth-bypass,idor,api",
		Enabled:     true,
		Source:      "builtin",
		Content: `# API Fuzz 工作流

针对一个 REST / GraphQL API：

1. **指纹与入口**：用 fetch_js + js_analyzer 找到所有 API 路径 + 鉴权方式（Bearer / Cookie / API Key / OAuth）；用 http_request 确认每个路径可达。
2. **认证与未授权测试**：去掉认证头请求受保护路径，记录 401 / 403 / 200 三种响应；对 200 的路径用 write_finding 报告 "未授权访问"。
3. **越权测试 (IDOR)**：用 session_set 保存两个不同账号的会话，遍历关键资源 ID 列表，切换账号交叉访问；用 switch_account 简化操作。
4. **参数污染**：用 api_fuzz 对每个参数测试：
   - 类型混淆 (string/number/array/object)
   - 边界值 (0/-1/MAX_INT/空字符串/超长字符串)
   - 注入 (SQL/XSS/XXE/SSRF payload)
5. **限速绕过**：如果有限速，记录 429 响应后退避 5s 继续。
6. **验证**：用 write_finding 报告时附带完整 curl 复现 + 响应片段；verifier 会做机械复现。

## 重点关注

- 任何返回 200 但本应 401/403 的端点
- 用户 A 能访问用户 B 资源的 IDOR
- 数值参数 SQL 注入、字符串参数 XSS
- 文件参数任意文件下载/SSRF
- GraphQL 字段级越权`,
	},
	{
		Name:        "vuln-report-writer",
		Title:       "漏洞报告写作",
		Description: "把单次发现组织成可交付的漏洞条目（标题/证据/复现/影响/建议）",
		Category:    "reporting",
		Tags:        "report,writeup,poc",
		Enabled:     true,
		Source:      "builtin",
		Content: `# 漏洞报告写作

调用 write_finding 时按以下结构组织：

- **title**：动词 + 目标 + 类型，例 "API /admin/users/{id} 存在水平越权"
- **severity**：critical / high / medium / low / info（影响用户数据/权限的 critical，能读到但不能改的 high）
- **target**：受影响的 URL 或端点
- **evidence**：原始请求/响应对（≤4KB），保留关键 header 和 body 片段
- **reproduction**：可粘贴运行的 curl 命令 + 预期结果；verifier 会机械复现
- **impact**：业务影响，不要技术黑话
- **recommendation**：具体修复建议（不要 "加强权限校验" 这种空话）

## 报告风格

- 一句话就能让开发理解问题
- 复现步骤要能让初级工程师 5 分钟内重现
- 证据不能是截图（verifier 复现失败就废了），必须是可解析的文本
- 严重程度评估要保守：宁可漏报不可误报`,
	},
}

// SeedBuiltinSkills inserts BuiltinSkills into the DB, skipping any
// whose name already exists. Safe to call on every boot.
func SeedBuiltinSkills(ctx context.Context, s *store.SkillStore) error {
	for i := range BuiltinSkills {
		want := BuiltinSkills[i]
		existing, err := s.GetByName(ctx, want.Name)
		if err == nil && existing != nil {
			// Already present — keep user toggles but refresh content
			// when the bundled version changes. v0.7.1 just skips.
			continue
		}
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		// Insert (CreatedAt will be set by the store if zero).
		if err := s.Create(ctx, &want); err != nil {
			return err
		}
	}
	return nil
}
