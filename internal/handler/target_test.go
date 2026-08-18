package handler

import "testing"

// classify is the pure target-type detector — the one handler function
// that is trivially unit-testable without a router/store. Route contracts
// are covered by cmd/dhunter-server/e2e_test.go (buildRouter + httptest).
func TestClassifyTargets(t *testing.T) {
	cases := []struct {
		input    string
		request  string
		wantType string
		wantNorm string
		wantErr  bool
	}{
		{"Acme Corp", "auto", "company", "acme corp", false},
		{"https://example.com/login", "auto", "url", "example.com", false},
		{"http://example.com", "auto", "url", "example.com", false},
		{"example.com", "auto", "domain", "example.com", false},
		{"10.0.0.1", "auto", "ip", "10.0.0.1", false},
		{"1.2.3.4", "ip", "ip", "1.2.3.4", false},
		{"https://example.com/x", "url", "url", "example.com", false},
		{"Acme 科技", "company", "company", "acme 科技", false},
		{"not a domain!!", "domain", "", "", true},
		{"999.1.1.1", "ip", "", "", true},
		{":::", "url", "", "", true},
	}
	for _, c := range cases {
		got, norm, _, err := classify(c.input, c.request)
		if c.wantErr {
			if err == nil {
				t.Errorf("classify(%q,%q) expected error, got %q", c.input, c.request, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("classify(%q,%q) unexpected error: %v", c.input, c.request, err)
			continue
		}
		if got != c.wantType {
			t.Errorf("classify(%q,%q) type = %q, want %q", c.input, c.request, got, c.wantType)
		}
		if norm != c.wantNorm {
			t.Errorf("classify(%q,%q) norm = %q, want %q", c.input, c.request, norm, c.wantNorm)
		}
	}
}
