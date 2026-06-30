package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "downstream.toml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoad(t *testing.T) {
	p := writeConfig(t, `
[package]
name = "github.com/spf13/cobra"
ecosystem = "go"

[[dependents]]
name = "github.com/cli/cli"
repo = "https://github.com/cli/cli"
ref = "v2.40.0"
test = "go test ./pkg/..."
source = "discover"

[[dependents]]
repo = "https://github.com/kubernetes/kubectl.git"
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Package.Name != "github.com/spf13/cobra" {
		t.Errorf("package name = %q", cfg.Package.Name)
	}
	if len(cfg.Dependents) != 2 {
		t.Fatalf("dependents = %d, want 2", len(cfg.Dependents))
	}
	if cfg.Dependents[0].Ref != "v2.40.0" || cfg.Dependents[0].Test != "go test ./pkg/..." {
		t.Errorf("dependent[0] = %+v", cfg.Dependents[0])
	}
	if cfg.Dependents[1].Name != "kubectl" {
		t.Errorf("dependent[1] name should default from repo, got %q", cfg.Dependents[1].Name)
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name, body, wantErr string
	}{
		{"missing package name", `[[dependents]]
repo = "https://x"`, "[package] name is required"},
		{"no dependents", `[package]
name = "x"`, "at least one [[dependents]]"},
		{"missing repo", `[package]
name = "x"
[[dependents]]
name = "y"`, "repo is required"},
		{"duplicate name", `[package]
name = "x"
[[dependents]]
name = "y"
repo = "https://a"
[[dependents]]
name = "y"
repo = "https://b"`, "duplicate name"},
		{"unknown key", `[package]
name = "x"
[[dependents]]
repo = "https://a"
bogus = true`, "unknown keys: dependents.bogus"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tt.body))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestFilter(t *testing.T) {
	cfg := &Config{
		path: "downstream.toml",
		Dependents: []Dependent{
			{Name: "github.com/cli/cli", Repo: "https://github.com/cli/cli"},
			{Name: "github.com/kubernetes/kubectl", Repo: "https://github.com/kubernetes/kubectl"},
			{Name: "github.com/gohugoio/hugo", Repo: "https://github.com/gohugoio/hugo"},
		},
	}

	all, err := cfg.Filter(nil)
	if err != nil || len(all) != 3 {
		t.Fatalf("Filter(nil) = %d deps, err %v", len(all), err)
	}

	got, err := cfg.Filter([]string{"github.com/cli/cli"})
	if err != nil || len(got) != 1 || got[0].Name != "github.com/cli/cli" {
		t.Fatalf("Filter(exact) = %+v, err %v", got, err)
	}

	got, err = cfg.Filter([]string{"cli-cli"})
	if err != nil || len(got) != 1 || got[0].Name != "github.com/cli/cli" {
		t.Fatalf("Filter(slug) = %+v, err %v", got, err)
	}

	got, err = cfg.Filter([]string{"hugo"})
	if err != nil || len(got) != 1 || got[0].Name != "github.com/gohugoio/hugo" {
		t.Fatalf("Filter(substring) = %+v, err %v", got, err)
	}

	got, err = cfg.Filter([]string{"github.com/*/cli"})
	if err != nil || len(got) != 1 {
		t.Fatalf("Filter(glob) = %+v, err %v", got, err)
	}

	got, err = cfg.Filter([]string{"cli-cli", "github.com/cli/cli"})
	if err != nil || len(got) != 1 {
		t.Fatalf("Filter(dedupe) = %+v, err %v", got, err)
	}

	_, err = cfg.Filter([]string{"nonesuch"})
	if err == nil || !strings.Contains(err.Error(), "matches no dependent") {
		t.Fatalf("Filter(miss) error = %v, want no-match error", err)
	}
}

func TestSlug(t *testing.T) {
	tests := []struct {
		dep  Dependent
		want string
	}{
		{Dependent{Name: "github.com/cli/cli"}, "cli-cli"},
		{Dependent{Name: "github.com/kubernetes/kubectl"}, "kubernetes-kubectl"},
		{Dependent{Repo: "https://github.com/foo/bar.git"}, "foo-bar"},
		{Dependent{Repo: "git@github.com:foo/bar.git"}, "foo-bar"},
		{Dependent{Name: "single"}, "single"},
		{Dependent{}, "dependent"},
	}
	for _, tt := range tests {
		if got := tt.dep.Slug(); got != tt.want {
			t.Errorf("Slug(%+v) = %q, want %q", tt.dep, got, tt.want)
		}
	}
}
