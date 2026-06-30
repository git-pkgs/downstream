package cmd

import (
	"strings"
	"testing"
)

func TestParseUpstream(t *testing.T) {
	tests := []struct {
		in      string
		module  string
		ref     string
		wantErr bool
	}{
		{"github.com/spf13/cobra", "github.com/spf13/cobra", "", false},
		{"github.com/spf13/cobra@v1.8.0", "github.com/spf13/cobra", "v1.8.0", false},
		{"github.com/spf13/cobra@my-branch", "github.com/spf13/cobra", "my-branch", false},
		{"  github.com/spf13/cobra  ", "github.com/spf13/cobra", "", false},
		{"", "", "", true},
		{"   ", "", "", true},
	}
	for _, tt := range tests {
		module, ref, err := parseUpstream(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("parseUpstream(%q) = (%q, %q, nil), want error", tt.in, module, ref)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseUpstream(%q) error: %v", tt.in, err)
			continue
		}
		if module != tt.module || ref != tt.ref {
			t.Errorf("parseUpstream(%q) = (%q, %q), want (%q, %q)", tt.in, module, ref, tt.module, tt.ref)
		}
	}
}

func TestResolveModule(t *testing.T) {
	m, r, err := resolveModule("github.com/a/b@v1", "github.com/c/d")
	if err != nil || m != "github.com/a/b" || r != "v1" {
		t.Errorf("flag should win: got (%q, %q, %v)", m, r, err)
	}
	m, r, err = resolveModule("", "github.com/c/d")
	if err != nil || m != "github.com/c/d" || r != "" {
		t.Errorf("config fallback: got (%q, %q, %v)", m, r, err)
	}
	_, _, err = resolveModule("", "")
	if err == nil {
		t.Error("want error when neither flag nor config")
	}
}

func TestTestNoDependentNoConfig(t *testing.T) {
	_, err := runCmd(t, "test", "--upstream", "github.com/a/b", "--upstream-path", ".", "-c", "testdata/nonexistent.toml")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error = %v, want config-not-found", err)
	}
}

func TestTestRequiresUpstreamSource(t *testing.T) {
	_, err := runCmd(t, "test", "-c", "testdata/downstream.toml")
	if err == nil || !strings.Contains(err.Error(), "provide --upstream-path or a ref") {
		t.Fatalf("error = %v, want upstream-path-required", err)
	}
}
