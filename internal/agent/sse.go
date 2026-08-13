package agent

import (
	"bufio"
	"bytes"
	"errors"
	"io"
)

// sseReader is a tiny SSE parser. We honour two fields:
//
//	event: <name>     — the SSE event name (e.g. "response_delta")
//	data:  <json>     — the JSON payload
//
// Both can repeat; we accumulate across continuation lines and yield
// a `SSEEvent{Name, Data}` per blank-line terminator. Heartbeats
// (lines starting with `:`) and empty events are skipped.
//
// Why this shape: the Python sidecar emits a flat data payload per
// event type (response_delta → {delta, accumulated}, tool_call →
// {name, arguments}, ...). The event name lives in the SSE `event:`
// line, not the JSON body. So we hand back the raw bytes plus the
// name and let the bridge map it to the flat Event struct.
type SSEEvent struct {
	Name string
	Data []byte
}

type sseReader struct {
	br *bufio.Reader
}

func newSSEReader(r io.Reader) *sseReader {
	return &sseReader{br: bufio.NewReaderSize(r, 16*1024)}
}

// ReadEvent returns the next SSEEvent or io.EOF.
func (s *sseReader) ReadEvent() (SSEEvent, error) {
	var ev SSEEvent
	for {
		line, err := s.br.ReadBytes('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				if ev.Name != "" || len(ev.Data) > 0 {
					out := ev
					return out, io.EOF
				}
				return SSEEvent{}, io.EOF
			}
			return SSEEvent{}, err
		}
		line = bytes.TrimRight(line, "\r\n")
		// Blank line terminates the current event.
		if len(line) == 0 {
			if ev.Name != "" || len(ev.Data) > 0 {
				out := ev
				ev = SSEEvent{}
				return out, nil
			}
			continue
		}
		// Comment / heartbeat line.
		if line[0] == ':' {
			continue
		}
		switch {
		case bytes.HasPrefix(line, []byte("event:")):
			ev.Name = string(bytes.TrimSpace(bytes.TrimPrefix(line, []byte("event:"))))
		case bytes.HasPrefix(line, []byte("data:")):
			chunk := bytes.TrimLeft(bytes.TrimPrefix(line, []byte("data:")), " ")
			if len(ev.Data) > 0 {
				ev.Data = append(ev.Data, '\n')
			}
			ev.Data = append(ev.Data, chunk...)
		}
	}
}
