package agent

import (
	"encoding/json"
	"testing"
)

// The SSE contract between bridge and browser: every event must be
// classifyable via `type` (the browser parses `type`, not the Go-side
// `event_type`), and tool_call/tool_result must share the LLM's call_id so
// the frontend can pair them into one invocation.
func TestMapPayloadToEventCarriesTypeField(t *testing.T) {
	ev := mapPayloadToEvent("tool_call", "run-1", map[string]interface{}{
		"name": "http_request", "call_id": "call_abc", "arguments": map[string]interface{}{"url": "https://x"},
	})
	if ev.Type != "tool_call" || ev.EventType != "tool_call" {
		t.Fatalf("type fields = %q/%q, want tool_call", ev.Type, ev.EventType)
	}
	if ev.CallID != "call_abc" {
		t.Fatalf("call_id = %q, want call_abc", ev.CallID)
	}
	// The marshalled JSON must include BOTH `type` and `event_type` so
	// older and newer frontend parsers both work.
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["type"] != "tool_call" {
		t.Fatalf("marshalled JSON missing `type`: %s", raw)
	}
	if m["event_type"] != "tool_call" {
		t.Fatalf("marshalled JSON missing `event_type`: %s", raw)
	}
	if m["call_id"] != "call_abc" {
		t.Fatalf("marshalled JSON missing `call_id`: %s", raw)
	}
}

func TestMapPayloadToEventToolResultPairsCallID(t *testing.T) {
	ev := mapPayloadToEvent("tool_result", "run-1", map[string]interface{}{
		"name": "http_request", "call_id": "call_abc", "content": "HTTP 200", "is_error": true, "duration_ms": float64(12),
	})
	if ev.Type != "tool_result" || ev.CallID != "call_abc" {
		t.Fatalf("tool_result type/call_id = %q/%q", ev.Type, ev.CallID)
	}
	if !ev.IsError || ev.Duration != 12 || ev.Result != "HTTP 200" {
		t.Fatalf("tool_result fields wrong: %+v", ev)
	}
}

func TestMapPayloadToEventRunDoneCarriesStatus(t *testing.T) {
	ev := mapPayloadToEvent("run_done", "run-1", map[string]interface{}{
		"status": "success", "summary": "converged",
	})
	if ev.Type != "run_done" || ev.RunStatus != "success" {
		t.Fatalf("run_done type/status = %q/%q", ev.Type, ev.RunStatus)
	}
	if ev.Content != "converged" {
		t.Fatalf("run_done summary = %q", ev.Content)
	}
}
