#!/usr/bin/env python3
"""Minimal Anthropic-compatible SSE mock LLM server for end-to-end testing.

Speaks just enough of the Messages API stream protocol for the agent's
anthropic_client to work. Branches on the request body:

  * reason turn            -> proposes 2 intents (JSON)
  * explore turn (1st)     -> tool_use for write_finding (so a real finding
                              is created) then a concluding text
  * explore turn (later)   -> plain concluding text (no more tool calls)
  * verifier turn          -> {"confirm": true}

Usage: python mock_llm_server.py [port]
Then run the agent with:
  DHUNTER_LLM_KEY=mock DHUNTER_LLM_BASE_URL=http://127.0.0.1:9997
"""

from __future__ import annotations

import hashlib
import json
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlparse

REASON_TEXT = json.dumps({
    "kind": "intents",
    "intents": [
        {"from": [], "description": "Probe /rest/user/login for SQL injection"},
        {"from": [], "description": "Check /api/ for unauthenticated admin endpoints"},
    ],
}, ensure_ascii=False)

VERIFY_TEXT = json.dumps({"confirm": True, "reason": "evidence shows a reproducible UNION-based auth bypass"})

EXPLORE_TEXT = (
    "Probed /rest/user/login with UNION payloads. "
    "Confirmed: the login endpoint reflects a UNION-based SQLi that bypasses auth."
)

FINDING_INPUT = {
    "title": "SQL Injection in login (UNION-based auth bypass)",
    "severity": "critical",
    "target": "http://example.com/rest/user/login",
    "evidence": "POST /rest/user/login with payload email=' OR 1=1-- returned an authenticated session.",
}

_explore_seen: dict[str, int] = {}
_seen_lock = threading.Lock()


def _sse(events):
    out = []
    for ev in events:
        out.append(f"event: {ev['event']}")
        out.append(f"data: {json.dumps(ev['data'], ensure_ascii=False)}")
        out.append("")
    return ("\n".join(out) + "\n").encode()


def _text_block(index, text):
    return [
        {"event": "content_block_start", "data": {"type": "content_block_start", "index": index, "content_block": {"type": "text", "text": ""}}},
        {"event": "content_block_delta", "data": {"type": "content_block_delta", "index": index, "delta": {"type": "text_delta", "text": text}}},
        {"event": "content_block_stop", "data": {"type": "content_block_stop", "index": index}},
    ]


def _tool_use_block(index, name, input_obj):
    input_json = json.dumps(input_obj, ensure_ascii=False)
    mid = max(1, len(input_json) // 2)
    events = [
        {"event": "content_block_start", "data": {"type": "content_block_start", "index": index, "content_block": {"id": f"toolu_{index}", "type": "tool_use", "name": name, "input": {}}}},
        {"event": "content_block_delta", "data": {"type": "content_block_delta", "index": index, "delta": {"type": "input_json_delta", "partial_json": input_json[:mid]}}},
        {"event": "content_block_delta", "data": {"type": "content_block_delta", "index": index, "delta": {"type": "input_json_delta", "partial_json": input_json[mid:]}}},
        {"event": "content_block_stop", "data": {"type": "content_block_stop", "index": index}},
    ]
    return events


def _tail(stop_reason, out_tokens):
    return [
        {"event": "message_delta", "data": {"type": "message_delta", "delta": {"stop_reason": stop_reason, "stop_sequence": None}, "usage": {"output_tokens": out_tokens}}},
        {"event": "message_stop", "data": {"type": "message_stop"}},
    ]


class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        path = urlparse(self.path).path
        if path != "/v1/messages":
            self.send_response(404)
            self.end_headers()
            return
        length = int(self.headers.get("Content-Length", 0))
        body = json.loads(self.rfile.read(length) or b"{}")
        msgs = body.get("messages") or []
        first = ""
        if msgs:
            c = msgs[0].get("content")
            first = c if isinstance(c, str) else ""

        events = []
        if "Current intent" in first:
            # explore turn
            key = hashlib.sha256(first.encode()).hexdigest()
            with _seen_lock:
                n = _explore_seen.get(key, 0)
                _explore_seen[key] = n + 1
            if n == 0:
                # first pass: run the finding tool, then conclude
                events = _tool_use_block(0, "write_finding", FINDING_INPUT)
                events += _text_block(1, EXPLORE_TEXT)
                events += _tail("tool_use", 40)
            else:
                events = _text_block(0, EXPLORE_TEXT + " No further findings in this direction.")
                events += _tail("end_turn", 20)
        elif "verification reviewer" in first:
            events = _text_block(0, VERIFY_TEXT)
            events += _tail("end_turn", 10)
        else:
            events = _text_block(0, REASON_TEXT)
            events += _tail("end_turn", 20)

        # prepend message_start with a small usage block
        start = {"event": "message_start", "data": {"type": "message_start", "message": {"id": "m1", "role": "assistant", "content": [], "model": "mock", "stop_reason": None}, "usage": {"input_tokens": 10, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 0}}}
        events = [start] + events

        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.send_header("Connection", "close")
        self.end_headers()
        self.wfile.write(_sse(events))

    def log_message(self, fmt, *args):
        sys.stderr.write("[mock-llm] %s\n" % (fmt % args))


if __name__ == "__main__":
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 9997
    sys.stderr.write(f"[mock-llm] listening on 127.0.0.1:{port}\n")
    ThreadingHTTPServer(("127.0.0.1", port), Handler).serve_forever()
