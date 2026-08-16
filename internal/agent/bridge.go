// Package agent talks to the Python FastAPI sidecar that runs the LLM loop.
//
// The bridge has two responsibilities:
//   1. POST /v1/runs to spin up a new agent run.
//   2. Subscribe to /v1/runs/{id}/events, persist every event into the
//      SQLite store, and fan it out to the SSE hub so browsers see the
//      stream in real time.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/dhunter/dhunter/internal/store"
	"github.com/dhunter/dhunter/internal/stream"
)

// Event types we expect from the Python agent. The string values are
// stable contract — changing them is a wire-protocol break.
const (
	EventReasoningDelta = "reasoning_delta"
	EventToolCall       = "tool_call"
	EventToolResult     = "tool_result"
	EventResponseDelta  = "response_delta"
	EventMessageDone    = "message_done"
	EventRunDone        = "run_done"
	EventVulnerability  = "vulnerability"
	EventTokenUsage     = "token_usage"
)

// CreateRunRequest is the body we send to POST /v1/runs.
type CreateRunRequest struct {
	RunID     string `json:"run_id"`
	Target    string `json:"target"`
	Objective string `json:"objective"`
}

// Event is the canonical event shape the Python agent emits. The
// Payload field is opaque to the bridge — we forward it verbatim to
// subscribers and to the message store.
type Event struct {
	EventType string          `json:"event_type"`
	// Type mirrors EventType under the key `type` — browser clients parse
	// `type` (the Go-side wire name is `event_type`), so both are emitted
	// to keep the SSE contract unambiguous.
	Type string `json:"type"`
	RunID     string          `json:"run_id"`
	Role      string          `json:"role,omitempty"`
	Content   string          `json:"content,omitempty"`
	Name      string          `json:"name,omitempty"`
	Args      json.RawMessage `json:"arguments,omitempty"`
	Result    string          `json:"result,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
	Duration  int64           `json:"duration_ms,omitempty"`
	// CallID is the LLM's tool_use block id, carried on both tool_call and
	// its tool_result so the frontend can pair them into one invocation.
	CallID string `json:"call_id,omitempty"`
	// Vulnerability fields, populated when EventType == "vulnerability".
	VulnTitle          string `json:"title,omitempty"`
	VulnSeverity       string `json:"severity,omitempty"`
	VulnStatus         string `json:"status,omitempty"`
	VulnTarget         string `json:"vuln_target,omitempty"`
	VulnEvidence       string `json:"evidence,omitempty"`
	VulnImpact         string `json:"impact,omitempty"`
	VulnRecommendation string `json:"recommendation,omitempty"`
	// RunStatus carries the terminal status from run_done (completed /
	// failed / cancelled) so the store isn't force-set to completed.
	RunStatus string `json:"run_status,omitempty"`
	// Token usage (cumulative across the run).
	InputTokens              int `json:"input_tokens,omitempty"`
	OutputTokens             int `json:"output_tokens,omitempty"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
	ReasoningTokens          int `json:"reasoning_tokens,omitempty"`
	Payload                  json.RawMessage `json:"payload,omitempty"`
}

// Bridge proxies to the Python sidecar.
type Bridge struct {
	baseURL string
	// token is the bearer credential the platform sends to the Python
	// agent sidecar (cfg.Agent.Token / DHUNTER_AGENT_TOKEN). Empty = the
	// sidecar runs without auth (local dev only).
	token  string
	hc     *http.Client
	stores *store.Stores
	hub    *stream.Hub
}

// New builds a Bridge. There is no client-level timeout: the subscribe
// stream stays open for the whole run (which can exceed 30 minutes on
// real targets), and every request carries a caller-supplied context that
// bounds it. The run handler passes a 70-minute context; the Python
// agent's per-turn and overall timeouts are the real safety nets.
func New(baseURL, token string, stores *store.Stores, hub *stream.Hub) *Bridge {
	return &Bridge{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		// Timeout 0 = no client deadline; the per-call context governs.
		hc:     &http.Client{Timeout: 0},
		stores: stores,
		hub:    hub,
	}
}

// withAuth attaches the sidecar bearer token (when configured).
func (b *Bridge) withAuth(req *http.Request) {
	if b.token == "" {
		return
	}
	req.Header.Set("Authorization", "Bearer "+b.token)
}

// CreateRun tells the Python sidecar to start a new run. The sidecar is
// expected to return 202 Accepted with a JSON body of {run_id, status}.
func (b *Bridge) CreateRun(ctx context.Context, req CreateRunRequest) error {
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal create run: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		b.baseURL+"/v1/runs", bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	b.withAuth(httpReq)

	resp, err := b.hc.Do(httpReq)
	if err != nil {
		return fmt.Errorf("create run: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		// Surface the body so failures are debuggable from the Go log.
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("create run: status=%d body=%s", resp.StatusCode, string(snippet))
	}
	// Drain the body so the underlying conn can be reused.
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// CancelRun asks the Python sidecar to cancel a running run. The sidecar
// cancels the agent task, which emits a terminal run_done(status=cancelled)
// back through the normal SSE path so the store ends in a consistent state.
func (b *Bridge) CancelRun(ctx context.Context, runID string) error {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		b.baseURL+"/v1/runs/"+runID+"/cancel", nil)
	if err != nil {
		return err
	}
	b.withAuth(httpReq)
	resp, err := b.hc.Do(httpReq)
	if err != nil {
		return fmt.Errorf("cancel run: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("cancel run: status=%d", resp.StatusCode)
	}
	return nil
}

// PauseRun asks the Python sidecar to pause a running run: the agent loop
// stops dispatching without a terminal run_done, keeping the board so the run
// can be resumed via Continue.
func (b *Bridge) PauseRun(ctx context.Context, runID string) error {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		b.baseURL+"/v1/runs/"+runID+"/pause", nil)
	if err != nil {
		return err
	}
	b.withAuth(httpReq)
	resp, err := b.hc.Do(httpReq)
	if err != nil {
		return fmt.Errorf("pause run: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("pause run: status=%d", resp.StatusCode)
	}
	return nil
}

// Subscribe opens an SSE stream from /v1/runs/{run_id}/events and pumps
// every event through the store + hub. It blocks until the stream ends
// or the context is cancelled. SSE framing is `data: <json>\n\n`.
func (b *Bridge) Subscribe(ctx context.Context, runID string) error {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet,
		b.baseURL+"/v1/runs/"+runID+"/events", nil)
	if err != nil {
		return err
	}
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Cache-Control", "no-cache")
	b.withAuth(httpReq)

	resp, err := b.hc.Do(httpReq)
	if err != nil {
		return fmt.Errorf("subscribe events: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("subscribe events: status=%d body=%s", resp.StatusCode, string(snippet))
	}

	return b.consumeSSE(ctx, runID, resp.Body)
}

// consumeSSE parses the SSE wire format and dispatches each event. It
// tolerates CRLF, heartbeats (lines starting with `:`), and the
// `[DONE]` sentinel the Python sidecar uses to signal end-of-stream.
//
// Wire shape (Python sidecar):
//
//	event: response_delta
//	data:  {"delta": "I'll", "accumulated": "I'll"}
//
// The event *name* lives on the `event:` line, the event *payload* on
// the `data:` line. We parse the payload to a generic map and then
// map known keys into the flat Event struct that downstream code
// already understands.
func (b *Bridge) consumeSSE(ctx context.Context, runID string, r io.Reader) error {
	br := newSSEReader(r)
	// Streaming text (reasoning_delta / response_delta) is fanned out live to
	// browsers but COALESCED for storage: one message row per turn instead of
	// one per delta chunk, so long runs don't bloat the messages table.
	streamBuf := ""
	streamType := ""
	flush := func() {
		if streamBuf == "" {
			return
		}
		ev := &Event{EventType: streamType, Type: streamType, RunID: runID, Role: "assistant", Content: streamBuf}
		if streamType == EventReasoningDelta {
			ev.Role = "reasoning"
		}
		rawJSON, _ := json.Marshal(ev)
		b.handle(ctx, ev, rawJSON)
		streamBuf = ""
		streamType = ""
	}
	for {
		se, err := br.ReadEvent()
		if err != nil {
			if errors.Is(err, io.EOF) {
				flush()
				return nil
			}
			flush()
			return err
		}
		if se.Name == "" && len(se.Data) == 0 {
			continue
		}
		// [DONE] sentinel — sidecar uses it to close the stream.
		if bytes.Equal(se.Data, []byte("[DONE]")) {
			flush()
			return nil
		}
		var payload map[string]interface{}
		if err := json.Unmarshal(se.Data, &payload); err != nil {
			continue
		}
		ev := mapPayloadToEvent(se.Name, runID, payload)
		if ev.EventType == EventReasoningDelta || ev.EventType == EventResponseDelta {
			// Live fan-out now; buffer the text for coalesced storage.
			rawJSON, _ := json.Marshal(ev)
			b.hub.Publish(runID, rawJSON)
			if ev.EventType == EventReasoningDelta {
				// mapPayloadToEvent sets Content to accumulated || delta; the
				// agent sends accumulated="" + per-chunk delta, so append.
				streamBuf += ev.Content
			} else {
				// response_delta carries the full accumulated text; keep the
				// latest as the coalesced content.
				streamBuf = ev.Content
			}
			streamType = ev.EventType
			continue
		}
		flush()
		rawJSON, _ := json.Marshal(ev)
		b.handle(ctx, ev, rawJSON)
	}
}

// mapPayloadToEvent flattens the Python sidecar's nested data shape
// into the Go bridge's flat Event struct.
//
// Mapping table:
//
//	{event: "ready"}        → Role: "system",       Content: "ready"
//	{event: "ping"}         → dropped (heartbeat)
//	{event: "response_delta", delta, accumulated}
//	                        → Content: accumulated
//	{event: "message_done", role, content}
//	                        → Role: "assistant",    Content: content
//	{event: "tool_call", name, arguments}
//	                        → Name: name, Args: arguments
//	{event: "tool_result", name, content, is_error, duration_ms}
//	                        → Name: name, Result: content, IsError, Duration
//	{event: "run_done", status, summary, error}
//	                        → Content: summary, special handling in handle
//	{event: "vulnerability", title, severity, ...}
//	                        → vulns via persistVuln
//	unknown event            → Role: "system", Content: <raw>
func mapPayloadToEvent(name, runID string, p map[string]interface{}) *Event {
	ev := &Event{
		EventType: name,
		Type:      name,
		RunID:     runID,
	}
	getStr := func(k string) string {
		if v, ok := p[k].(string); ok {
			return v
		}
		return ""
	}
	getBool := func(k string) bool {
		if v, ok := p[k].(bool); ok {
			return v
		}
		return false
	}
	getNum := func(k string) float64 {
		if v, ok := p[k].(float64); ok {
			return v
		}
		return 0
	}
	switch name {
	case "ready":
		ev.Role = "system"
		ev.Content = "ready"
	case "ping":
		// heartbeat — bridge will keep streaming
		ev.Role = "system"
		ev.Content = "ping"
	case "response_delta", "reasoning_delta":
		// Prefer accumulated; fall back to delta.
		acc := getStr("accumulated")
		if acc == "" {
			acc = getStr("delta")
		}
		ev.Role = "assistant"
		ev.Content = acc
		if name == "reasoning_delta" {
			// Mark as reasoning for the UI; we use a role of
			// "reasoning" so the message list can fold it under
			// a separate stream.
			ev.Role = "reasoning"
		}
	case "message_done":
		ev.Role = "assistant"
		ev.Content = getStr("content")
		if ev.Role == "" {
			ev.Role = getStr("role")
		}
	case "tool_call":
		ev.Name = getStr("name")
		ev.CallID = getStr("call_id")
		if args, ok := p["arguments"]; ok {
			if ab, err := json.Marshal(args); err == nil {
				ev.Args = ab
			}
		}
	case "tool_result":
		ev.Name = getStr("name")
		ev.CallID = getStr("call_id")
		ev.Result = getStr("content")
		ev.IsError = getBool("is_error")
		ev.Duration = int64(getNum("duration_ms"))
	case "run_done":
		ev.Role = "system"
		ev.Content = getStr("summary")
		if ev.Content == "" {
			ev.Content = getStr("status")
		}
		ev.RunStatus = getStr("status")
	case "vulnerability":
		ev.VulnTitle = getStr("title")
		ev.VulnSeverity = getStr("severity")
		ev.VulnStatus = getStr("status")
		ev.VulnTarget = getStr("target")
		ev.VulnEvidence = getStr("evidence")
		ev.VulnImpact = getStr("impact")
		ev.VulnRecommendation = getStr("recommendation")
	case "token_usage":
		ev.Role = "system"
		ev.EventType = EventTokenUsage
		// Accumulate into the run-level counters via Update.
		ev.InputTokens = int(getNum("input_tokens"))
		ev.OutputTokens = int(getNum("output_tokens"))
		ev.CacheCreationInputTokens = int(getNum("cache_creation_input_tokens"))
		ev.CacheReadInputTokens = int(getNum("cache_read_input_tokens"))
		ev.ReasoningTokens = int(getNum("reasoning_tokens"))
	default:
		// Unknown event: keep the raw payload so the UI can still
		// render it; mark it as system so it doesn't pollute the
		// assistant stream.
		ev.Role = "system"
		if raw, err := json.Marshal(p); err == nil {
			ev.Content = string(raw)
		}
	}
	// Preserve any leftover fields as opaque payload.
	known := map[string]struct{}{
		"event": {}, "data": {},
		"delta": {}, "accumulated": {},
		"role": {}, "content": {},
		"name": {}, "arguments": {},
		"is_error": {}, "duration_ms": {},
		"status": {}, "summary": {}, "error": {},
		"title": {}, "severity": {}, "target": {},
		"evidence": {}, "impact": {}, "recommendation": {},
		"ts": {}, "run_id": {},
	}
	leftover := map[string]interface{}{}
	for k, v := range p {
		if _, ok := known[k]; ok {
			continue
		}
		leftover[k] = v
	}
	if len(leftover) > 0 {
		if lj, err := json.Marshal(leftover); err == nil {
			ev.Payload = lj
		}
	}
	return ev
}

// handle persists the event to SQLite and republishes it on the hub.
// The raw JSON bytes are reused for fan-out so subscribers see exactly
// what the sidecar sent.
func (b *Bridge) handle(ctx context.Context, ev *Event, rawJSON []byte) {
	switch ev.EventType {
	case EventToolCall:
		_ = b.stores.ToolCalls.Append(ctx, &store.ToolCall{
			RunID:     ev.RunID,
			Name:      ev.Name,
			Arguments: coalesceJSON(ev.Args, ev.Payload),
			CreatedAt: time.Now().UTC(),
		})
	case EventToolResult:
		// Merge the result into the latest open tool_call row for the same
		// run+name, so one logical invocation = one row (args + result).
		// Falls back to a standalone row if no matching open call exists.
		_ = b.stores.ToolCalls.AppendResult(ctx, ev.RunID, ev.Name,
			ev.Result, ev.IsError, ev.Duration)
	case EventVulnerability:
		_ = b.persistVuln(ctx, ev)
	case EventTokenUsage:
		// Append a small record so the run timeline shows the
		// cumulative spend; the run row is also incremented below.
		_ = b.stores.Messages.Append(ctx, &store.Message{
			RunID:     ev.RunID,
			Role:      "system",
			EventType: EventTokenUsage,
			Content:   fmt.Sprintf("tokens in=%d out=%d reasoning=%d cache_create=%d cache_read=%d", ev.InputTokens, ev.OutputTokens, ev.ReasoningTokens, ev.CacheCreationInputTokens, ev.CacheReadInputTokens),
			Payload:   coalesceJSON(nil, rawJSON),
			CreatedAt: time.Now().UTC(),
		})
		_ = b.bumpRunTokens(ctx, ev)
	case EventRunDone:
		ended := time.Now().UTC()
		status := ev.RunStatus
		if status == "" {
			status = "completed"
		}
		_ = b.stores.Runs.Update(ctx, &store.Run{
			ID:      ev.RunID,
			Status:  status,
			Summary: ev.Content,
			EndedAt: &ended,
		})
	default:
		// reasoning_delta / response_delta / message_done / anything
		// else lands in the messages table so the report timeline is
		// complete.
		role := ev.Role
		if role == "" {
			role = inferRole(ev.EventType)
		}
		payload := coalesceJSON(ev.Payload, ev.Args)
		_ = b.stores.Messages.Append(ctx, &store.Message{
			RunID:     ev.RunID,
			Role:      role,
			EventType: ev.EventType,
			Content:   ev.Content,
			Payload:   payload,
		})
	}

	// Fan out to every browser subscribed to this run.
	b.hub.Publish(ev.RunID, rawJSON)
}

func (b *Bridge) bumpRunTokens(ctx context.Context, ev *Event) error {
	// No-op if the event carries nothing (defensive).
	if ev.InputTokens == 0 && ev.OutputTokens == 0 && ev.CacheCreationInputTokens == 0 &&
		ev.CacheReadInputTokens == 0 && ev.ReasoningTokens == 0 {
		return nil
	}
	return b.stores.Runs.AddTokens(ctx, ev.RunID,
		ev.InputTokens, ev.OutputTokens,
		ev.CacheCreationInputTokens, ev.CacheReadInputTokens, ev.ReasoningTokens)
}

func (b *Bridge) persistVuln(ctx context.Context, ev *Event) error {
	// We need a target_id to satisfy the FK; the run row owns it.
	run, err := b.stores.Runs.Get(ctx, ev.RunID)
	if err != nil {
		return err
	}
	return b.stores.Vulns.Create(ctx, &store.Vulnerability{
		RunID:          ev.RunID,
		TargetID:       run.TargetID,
		Title:          ev.VulnTitle,
		Severity:       ev.VulnSeverity,
		Status:         ev.VulnStatus,
		Target:         ev.VulnTarget,
		Evidence:       ev.VulnEvidence,
		Impact:         ev.VulnImpact,
		Recommendation: ev.VulnRecommendation,
	})
}

func inferRole(eventType string) string {
	switch eventType {
	case EventToolCall, EventToolResult:
		return "tool"
	case EventReasoningDelta, EventResponseDelta, EventMessageDone, EventRunDone:
		return "assistant"
	}
	return "assistant"
}

// coalesceJSON picks the first non-empty of the two raw JSON fields so
// the bridge doesn't have to guess which one the sidecar used.
func coalesceJSON(a, b json.RawMessage) json.RawMessage {
	if len(a) > 0 {
		return a
	}
	if len(b) > 0 {
		return b
	}
	return json.RawMessage(`{}`)
}
