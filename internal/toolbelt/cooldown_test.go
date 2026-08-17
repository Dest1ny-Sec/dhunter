package toolbelt

import (
	"context"
	"errors"
	"net"
	"net/url"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestIsConnectionError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"dns error", &net.DNSError{Err: "no such host", IsNotFound: true}, true},
		{"connect refused", &url.Error{Op: "Get", URL: "http://x", Err: &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}}, true},
		{"deadline", &url.Error{Op: "Get", URL: "http://x", Err: os.ErrDeadlineExceeded}, true},
		{"timeout", &url.Error{Op: "Get", URL: "http://x", Err: &net.OpError{Op: "dial", Err: &net.DNSError{Err: "i/o timeout"}}}, true},
		{"plain http status-ish error", errors.New("some other error"), false},
	}
	for _, c := range cases {
		if got := isConnectionError(c.err); got != c.want {
			t.Errorf("%s: isConnectionError = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestUnreachableCooldownBlocksAndExpires(t *testing.T) {
	// Fresh host: not cooling.
	if _, bad := hostUnreachable("dead.example.com"); bad {
		t.Fatal("fresh host should not be on cooldown")
	}

	noteUnreachableHost("dead.example.com", "dial tcp: connection refused")
	why, bad := hostUnreachable("dead.example.com")
	if !bad {
		t.Fatal("host should be on cooldown after connection failure")
	}
	if why == "" {
		t.Fatal("cooldown should carry the failure reason")
	}

	// A different host is unaffected.
	if _, bad := hostUnreachable("alive.example.com"); bad {
		t.Fatal("unrelated host must not be cooling")
	}
}

func TestCooldownExpires(t *testing.T) {
	// Simulate expiry by overwriting the window directly.
	unreachableMu.Lock()
	unreachableUntil["expire.example.com"] = time.Now().Add(-time.Second)
	unreachableWhy["expire.example.com"] = "old"
	unreachableMu.Unlock()

	if _, bad := hostUnreachable("expire.example.com"); bad {
		t.Fatal("expired cooldown should no longer block")
	}
}

func TestHttpRequestSkipsCoolingHostImmediately(t *testing.T) {
	noteUnreachableHost("cool.example.com", "dial tcp: connection refused")
	res := httpRequest(context.Background(), map[string]interface{}{"url": "https://cool.example.com/"})
	if !res.IsError {
		t.Fatal("cooldown hit should return an error result")
	}
	if text := res.Content[0].Text; !contains(text, "已被标记不可达") {
		t.Fatalf("expected unreachable message, got: %s", text)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
