package toolbelt

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSafeExecMissingBinaryReturnsFriendlyMessage(t *testing.T) {
	out, err := safeExec(context.Background(), 5*time.Second, "dhunter_definitely_not_installed_bin_xyz", "-h")
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
	if !strings.Contains(err.Error(), "未安装") || !strings.Contains(err.Error(), "替代") {
		t.Fatalf("expected graceful-degradation message, got: %v", err)
	}
	_ = out
}

// searchEngine regression: the title capture group used to be non-capturing
// (`(?:...)`), so any matching link made m[2] panic with "index out of
// range". Parse a realistic result page and assert titles come back intact.
func TestSearchEngineParsesTitlesWithoutPanic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<html><body>
			<a class="b_algo" href="https://target.example.com/a"><h2>Target A 标题</h2></a>
			<a href="https://target.example.com/b">Target B</a>
			<a href="https://www.bing.com/search?q=skip">skip me</a>
		</body></html>`)
	}))
	defer srv.Close()

	results, err := searchEngine(context.Background(), srv.URL+"/search?q=", "test", "test-ua", 10)
	if err != nil {
		t.Fatalf("searchEngine: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d (%v), want 2 (bing/microsoft links filtered)", len(results), results)
	}
	byURL := map[string]string{}
	for _, r := range results {
		byURL[r["url"]] = r["title"]
	}
	if byURL["https://target.example.com/a"] != "Target A 标题" {
		t.Fatalf("title A = %q, want 'Target A 标题'", byURL["https://target.example.com/a"])
	}
	if byURL["https://target.example.com/b"] != "Target B" {
		t.Fatalf("title B = %q, want 'Target B'", byURL["https://target.example.com/b"])
	}
}

// safeExec must refuse non-bare command names (LLM-driven tools must never
// point it at an arbitrary executable path).
func TestSafeExecRejectsPathCommandNames(t *testing.T) {
	for _, name := range []string{"/bin/rm", "./evil", "a/b", `C:\evil.exe`} {
		if _, err := safeExec(context.Background(), time.Second, name, "-rf", "/"); err == nil {
			t.Fatalf("safeExec(%q) should be rejected", name)
		} else if !strings.Contains(err.Error(), "non-bare") {
			t.Fatalf("safeExec(%q) error = %v, want non-bare message", name, err)
		}
	}
}
