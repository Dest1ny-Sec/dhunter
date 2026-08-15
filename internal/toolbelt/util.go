// Package toolbelt is the original, self-developed web-pentest toolset.
//
// It exposes a streamable-HTTP MCP server (JSON-RPC 2.0) whose tools are
// built to be driven by an LLM agent doing manual-style testing: recon,
// fingerprinting, active probing, and evidence-based finding submission.
//
// All code here is original to Dhunter. Nothing is vendored from third
// parties, and every tool carries its own timeout, output limit, and
// error handling so a runaway agent can't hang or exhaust the process.
package toolbelt

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// toolResult is the MCP CallToolResult shape the agent consumes.
type toolResult struct {
	Content []contentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func textResult(s string) toolResult {
	return toolResult{Content: []contentBlock{{Type: "text", Text: s}}}
}

func errResult(s string) toolResult {
	return toolResult{Content: []contentBlock{{Type: "text", Text: s}}, IsError: true}
}

// --- argument accessors ------------------------------------------------

func argString(args map[string]interface{}, key, def string) string {
	if v, ok := args[key].(string); ok && strings.TrimSpace(v) != "" {
		return v
	}
	return def
}

func argFloat(args map[string]interface{}, key string, def float64) float64 {
	if v, ok := args[key].(float64); ok {
		return v
	}
	return def
}

func argInt(args map[string]interface{}, key string, def int) int {
	if v, ok := args[key].(float64); ok {
		return int(v)
	}
	return def
}

func argBool(args map[string]interface{}, key string, def bool) bool {
	if v, ok := args[key].(bool); ok {
		return v
	}
	return def
}

func argStringSlice(args map[string]interface{}, key string) []string {
	raw, ok := args[key].([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		out = append(out, fmt.Sprintf("%v", v))
	}
	return out
}

func argStringMap(args map[string]interface{}, key string) map[string]string {
	raw, ok := args[key].(map[string]interface{})
	if !ok {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		out[k] = fmt.Sprintf("%v", v)
	}
	return out
}

// dedupNonEmpty trims, drops empties, and removes duplicates.
func dedupNonEmpty(ss []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// --- subprocess helper -------------------------------------------------

// shellForOS / shellFlagForOS return the command interpreter used to run
// custom-tool command templates. POSIX shells don't exist on Windows, so we
// fall back to cmd /C there; everything else uses sh -c. Custom commands are
// simple "{placeholder} -flags value" templates, so cmd /C handles them fine.
func shellForOS() string {
	if runtime.GOOS == "windows" {
		return "cmd"
	}
	return "sh"
}

func shellFlagForOS() string {
	if runtime.GOOS == "windows" {
		return "/C"
	}
	return "-c"
}

// safeExec runs an external binary with a hard timeout and capped output.
// It's the only way tools talk to on-disk scanners, so it must never leak
// a goroutine or unbounded memory.
func safeExec(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, name, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}

	var outBuf, errBuf limitedBuffer
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(&outBuf, stdout)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(&errBuf, stderr)
		done <- struct{}{}
	}()
	<-done
	<-done

	werr := cmd.Wait()
	outStr, errStr := outBuf.String(), errBuf.String()
	if werr != nil {
		if cctx.Err() == context.DeadlineExceeded {
			return outStr + "\n[stderr]: " + errStr, fmt.Errorf("%s: timed out after %s", name, timeout)
		}
		return outStr + "\n[stderr]: " + errStr, werr
	}
	return outStr, nil
}

// limitedBuffer is a byte buffer that stops accepting data past a cap so a
// noisy scanner can't OOM the process.
type limitedBuffer struct {
	buf []byte
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.max == 0 {
		b.max = 20 << 20 // 20 MiB default
	}
	room := b.max - len(b.buf)
	if room <= 0 {
		return len(p), nil // silently drop overflow
	}
	if len(p) > room {
		p = p[:room]
	}
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	return string(b.buf)
}

// httpClient builds a shared HTTP client. TLS verification is ON by
// default; callers opt out for self-signed targets.
func httpClient(insecure bool, timeout time.Duration, maxRedirects int) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure},
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if maxRedirects > 0 && len(via) >= maxRedirects {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
}
