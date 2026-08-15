package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// writeConfig writes a config file into a temp dir and returns its path.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "dhunter.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestRelativeSQLitePathAnchorsToConfigDir(t *testing.T) {
	path := writeConfig(t, "storage:\n  sqlite_path: \"../data/dhunter.db\"\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := filepath.Join(filepath.Dir(filepath.Dir(path)), "data", "dhunter.db")
	if cfg.Storage.SQLitePath != want {
		t.Fatalf("sqlite path = %q, want %q", cfg.Storage.SQLitePath, want)
	}
	if !filepath.IsAbs(cfg.Storage.SQLitePath) {
		t.Fatalf("sqlite path should be absolute, got %q", cfg.Storage.SQLitePath)
	}
}

func TestAbsoluteSQLitePathLeftUntouched(t *testing.T) {
	path := writeConfig(t, "storage:\n  sqlite_path: \"/var/lib/dhunter/data.db\"\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Storage.SQLitePath; got != "/var/lib/dhunter/data.db" {
		t.Fatalf("absolute path was rewritten to %q", got)
	}
}

func TestDefaultSQLitePathWhenNoConfigFile(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// No config file read -> no anchor -> cwd-relative (legacy), but absolute.
	if !filepath.IsAbs(cfg.Storage.SQLitePath) {
		t.Fatalf("expected absolute default path, got %q", cfg.Storage.SQLitePath)
	}
	if !strings.Contains(filepath.ToSlash(cfg.Storage.SQLitePath), "data/dhunter.db") {
		t.Fatalf("unexpected default path %q", cfg.Storage.SQLitePath)
	}
}

func TestEnvOverrideWinsOverYAML(t *testing.T) {
	t.Setenv("DHUNTER_SQLITE_PATH", "/opt/override.db")
	path := writeConfig(t, "storage:\n  sqlite_path: \"../data/dhunter.db\"\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Storage.SQLitePath != "/opt/override.db" {
		t.Fatalf("env override ignored, got %q", cfg.Storage.SQLitePath)
	}
}

// repoRoot walks up from this package to the repository root (contains go.mod).
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test file")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repo root not found")
		}
		dir = parent
	}
}

func TestShippedConfigResolvesDBIntoRepoData(t *testing.T) {
	root := repoRoot(t)
	cfg, err := Load(filepath.Join(root, "configs", "dhunter.yaml"))
	if err != nil {
		t.Fatalf("Load shipped config: %v", err)
	}
	// sqlite_path is "../data/dhunter.db" relative to configs/ -> repo/data.
	want := filepath.Join(root, "data", "dhunter.db")
	if cfg.Storage.SQLitePath != want {
		t.Fatalf("shipped config resolves to %q, want %q", cfg.Storage.SQLitePath, want)
	}
	// The shipped YAML itself must never carry a machine-specific absolute
	// path (the old macOS hardcode), so a fresh checkout on any OS boots.
	raw, err := os.ReadFile(filepath.Join(root, "configs", "dhunter.yaml"))
	if err != nil {
		t.Fatalf("read shipped config: %v", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "sqlite_path") {
			if strings.HasPrefix(trimmed, "sqlite_path:") {
				val := strings.Trim(strings.TrimPrefix(trimmed, "sqlite_path:"), ` "'`)
				if filepath.IsAbs(val) || strings.HasPrefix(val, "/") {
					t.Fatalf("shipped config sqlite_path is absolute (%q); must be relative", val)
				}
			}
		}
	}
}
