package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/dhunter/dhunter/internal/store"
)

// ReportHandler handles GET /api/runs/:id/report.
//
// The report is generated on demand from the current store contents.
// This means a partial run still produces a useful report — the UI can
// poll the endpoint as the run progresses.
type ReportHandler struct {
	Stores *store.Stores
}

// NewReportHandler constructs a ReportHandler.
func NewReportHandler(s *store.Stores) *ReportHandler {
	return &ReportHandler{Stores: s}
}

// Markdown handles GET /api/runs/:id/report.
//
// The response Content-Type is `text/markdown; charset=utf-8`. We use
// Gin's `c.Render` with a tiny custom renderer to avoid leaking the
// markdown into the JSON escape path.
func (h *ReportHandler) Markdown(c *gin.Context) {
	id := c.Param("id")
	md, err := h.buildMarkdown(c, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "run not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Header("Content-Type", "text/markdown; charset=utf-8")
	c.String(http.StatusOK, md)
}

// buildMarkdown assembles the Markdown report for a run.
func (h *ReportHandler) buildMarkdown(c *gin.Context, runID string) (string, error) {
	ctx := c.Request.Context()

	run, err := h.Stores.Runs.Get(ctx, runID)
	if err != nil {
		return "", err
	}
	target, err := h.Stores.Targets.Get(ctx, run.TargetID)
	if err != nil {
		return "", err
	}
	vulns, err := h.Stores.Vulns.ListByRun(ctx, runID)
	if err != nil {
		return "", err
	}
	tools, err := h.Stores.ToolCalls.ListByRun(ctx, runID)
	if err != nil {
		return "", err
	}
	msgs, err := h.Stores.Messages.ListByRun(ctx, runID)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# Dhunter 渗透测试报告\n\n")
	fmt.Fprintf(&b, "- **运行 ID**: `%s`\n", run.ID)
	fmt.Fprintf(&b, "- **目标**: %s (`%s`)\n", target.Value, target.Type)
	fmt.Fprintf(&b, "- **状态**: %s\n", run.Status)
	fmt.Fprintf(&b, "- **开始时间**: %s\n", run.StartedAt.Format(time.RFC3339))
	if run.EndedAt != nil {
		fmt.Fprintf(&b, "- **结束时间**: %s\n", run.EndedAt.Format(time.RFC3339))
	}
	if run.Objective != "" {
		fmt.Fprintf(&b, "- **目标说明**: %s\n", run.Objective)
	}
	if run.Summary != "" {
		fmt.Fprintf(&b, "\n## 摘要\n\n%s\n", run.Summary)
	}

	// --- Vulnerabilities ---------------------------------------------
	fmt.Fprintf(&b, "\n## 漏洞成果 (%d)\n\n", len(vulns))
	if len(vulns) == 0 {
		fmt.Fprintf(&b, "_暂无确认漏洞。_\n")
	} else {
		// Group by severity so the most urgent findings sit on top.
		order := []string{"critical", "high", "medium", "low", "info"}
		bySev := map[string][]*store.Vulnerability{}
		for _, v := range vulns {
			bySev[strings.ToLower(v.Severity)] = append(bySev[strings.ToLower(v.Severity)], v)
		}
		fmt.Fprintf(&b, "| 严重等级 | 漏洞 | 状态 |\n|---|---|---|\n")
		for _, sev := range order {
			for _, v := range bySev[sev] {
				fmt.Fprintf(&b, "| %s | %s | %s |\n", v.Severity, v.Title, v.Status)
			}
		}
		fmt.Fprintf(&b, "\n### 详情\n\n")
		for i, v := range vulns {
			fmt.Fprintf(&b, "#### %d. [%s] %s\n\n", i+1, strings.ToUpper(v.Severity), v.Title)
			if v.Target != "" {
				fmt.Fprintf(&b, "- **影响目标**: %s\n", v.Target)
			}
			if v.Evidence != "" {
				fmt.Fprintf(&b, "- **证据**:\n\n```\n%s\n```\n", v.Evidence)
			}
			if v.Reproduction != "" {
				fmt.Fprintf(&b, "- **复现步骤**:\n\n```\n%s\n```\n", v.Reproduction)
			}
			if v.Impact != "" {
				fmt.Fprintf(&b, "- **影响**: %s\n", v.Impact)
			}
			if v.Recommendation != "" {
				fmt.Fprintf(&b, "- **修复建议**: %s\n", v.Recommendation)
			}
			fmt.Fprintf(&b, "\n")
		}
	}

	// --- Tool calls --------------------------------------------------
	fmt.Fprintf(&b, "\n## 工具调用记录 (%d 次)\n\n", len(tools))
	if len(tools) == 0 {
		fmt.Fprintf(&b, "_暂无工具调用记录。_\n")
	} else {
		// Tools are append-only; we render in order. The result column
		// is truncated so the report stays readable.
		fmt.Fprintf(&b, "| # | 工具 | 耗时 | 结果 |\n|---|---|---|---|\n")
		for i, t := range tools {
			result := oneLine(t.Result)
			fmt.Fprintf(&b, "| %d | `%s` | %dms | %s |\n", i+1, t.Name, t.DurationMs, result)
		}
	}

	// --- Timeline (assistant / tool messages) ------------------------
	fmt.Fprintf(&b, "\n## 时间线 (%d 条事件)\n\n", len(msgs))
	if len(msgs) == 0 {
		fmt.Fprintf(&b, "_暂无事件记录。_\n")
	} else {
		fmt.Fprintf(&b, "| 时间 | 角色 | 事件 | 内容 |\n|---|---|---|---|\n")
		for _, m := range msgs {
			ts := m.CreatedAt.Format("15:04:05")
			content := oneLine(m.Content)
			fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", ts, m.Role, m.EventType, content)
		}
	}

	fmt.Fprintf(&b, "\n---\n_由 Dhunter 生成于 %s_\n", time.Now().UTC().Format(time.RFC3339))
	return b.String(), nil
}

// oneLine collapses a block of text into a single line for table cells.
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return strings.TrimSpace(s)
}
