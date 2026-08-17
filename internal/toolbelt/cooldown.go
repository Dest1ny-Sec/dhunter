package toolbelt

import (
	"errors"
	"net"
	"net/url"
	"os"
	"sync"
	"syscall"
	"time"
)

// Unreachable-host cooldown: a host that fails at the CONNECTION layer (DNS
// resolution, connect refused/timeout) almost never recovers within a run,
// and every retry costs the LLM a long wait plus a useless tool round trip.
// After one such failure the host is skipped for UNREACHABLE_COOLDOWN — the
// agent gets an instant "marked unreachable" answer instead of burning time
// and tokens re-probing a dead host (e.g. oa.lsnu.edu.cn was retried 5-6
// times with 15s timeouts before this existed).
const UNREACHABLE_COOLDOWN = 5 * time.Minute

var (
	unreachableMu    sync.Mutex
	unreachableUntil = map[string]time.Time{}
	unreachableWhy   = map[string]string{}
)

// hostUnreachable returns (reason, true) when the host is inside its
// cooldown window.
func hostUnreachable(host string) (string, bool) {
	unreachableMu.Lock()
	defer unreachableMu.Unlock()
	until, ok := unreachableUntil[host]
	if !ok || time.Now().After(until) {
		return "", false
	}
	return unreachableWhy[host], true
}

// noteUnreachableHost records a connection-layer failure for a host.
func noteUnreachableHost(host, reason string) {
	if host == "" {
		return
	}
	unreachableMu.Lock()
	defer unreachableMu.Unlock()
	unreachableUntil[host] = time.Now().Add(UNREACHABLE_COOLDOWN)
	unreachableWhy[host] = reason
}

// clipStr truncates a string to n runes with an ellipsis (used in error
// messages fed back to the LLM, so a giant DNS error can't blow the tool
// result).
func clipStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// isConnectionError classifies an error from http.Client.Do: connection
// layer (DNS / refused / reset / unreachable / timeout) → cooldown
// candidate. HTTP status errors arrive as normal responses, not here.
func isConnectionError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	for _, target := range []error{
		syscall.ECONNREFUSED, syscall.ECONNRESET, syscall.EHOSTUNREACH, syscall.ENETUNREACH, syscall.ETIMEDOUT,
	} {
		if errors.Is(err, target) {
			return true
		}
	}
	var nerr net.Error
	if errors.As(err, &nerr) && nerr.Timeout() {
		return true
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}
	var uerr *url.Error
	if errors.As(err, &uerr) {
		return isConnectionError(uerr.Err)
	}
	return false
}
