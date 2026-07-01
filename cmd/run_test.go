package cmd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/git-pkgs/downstream/internal/discover"
)

func withMockEcosystems(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(handler)
	prev := newDiscoverClient
	newDiscoverClient = func() *discover.Client {
		c := discover.NewClient()
		c.BaseURL = srv.URL
		c.HTTPClient = srv.Client()
		c.Backoff = time.Millisecond
		return c
	}
	t.Cleanup(func() {
		newDiscoverClient = prev
		srv.Close()
	})
}

func TestLoadOrDiscoverPrefersConfig(t *testing.T) {
	withMockEcosystems(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("discover should not be called when config exists")
		http.Error(w, "should not be called", http.StatusInternalServerError)
	})

	module, repo, ref, deps, err := loadOrDiscover(context.Background(), io.Discard, testFlags{
		configPath:   "testdata/downstream.toml",
		upstream:     "github.com/spf13/cobra@v1.0.0",
		upstreamRepo: "https://github.com/spf13/cobra",
	}, "go", 5, 0, false)
	if err != nil {
		t.Fatalf("loadOrDiscover: %v", err)
	}
	if module != "github.com/spf13/cobra" || ref != "v1.0.0" {
		t.Errorf("module/ref = %q/%q", module, ref)
	}
	if repo != "https://github.com/spf13/cobra" {
		t.Errorf("repo = %q", repo)
	}
	if len(deps) != 2 {
		t.Errorf("deps = %d, want 2 from config", len(deps))
	}
}

func TestLoadOrDiscoverFallsBackToAPI(t *testing.T) {
	withMockEcosystems(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawPath, "github.com%2Facme%2Flib") {
			t.Errorf("unexpected path: raw=%s decoded=%s", r.URL.RawPath, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode([]discover.Package{
			{
				Name:                   "github.com/cli/cli",
				RepositoryURL:          "https://github.com/cli/cli",
				DependentPackagesCount: 5000,
				RepoMetadata:           discover.RepoMetadata{HTMLURL: "https://github.com/cli/cli", PushedAt: time.Now()},
			},
			{
				Name:                   "github.com/gohugoio/hugo",
				RepositoryURL:          "https://github.com/gohugoio/hugo",
				DependentPackagesCount: 1200,
				RepoMetadata:           discover.RepoMetadata{HTMLURL: "https://github.com/gohugoio/hugo", PushedAt: time.Now()},
			},
		})
	})

	module, _, _, deps, err := loadOrDiscover(context.Background(), io.Discard, testFlags{
		configPath: filepath.Join(t.TempDir(), "missing.toml"),
		upstream:   "github.com/acme/lib",
	}, "go", 5, 10, false)
	if err != nil {
		t.Fatalf("loadOrDiscover: %v", err)
	}
	if module != "github.com/acme/lib" {
		t.Errorf("module = %q", module)
	}
	if len(deps) != 2 || deps[0].Name != "github.com/cli/cli" || deps[0].Source != "discover" {
		t.Errorf("deps = %+v", deps)
	}
}

func TestLoadOrDiscoverNoDiscover(t *testing.T) {
	_, _, _, _, err := loadOrDiscover(context.Background(), io.Discard, testFlags{
		configPath: filepath.Join(t.TempDir(), "missing.toml"),
		upstream:   "github.com/acme/lib",
	}, "go", 5, 0, true)
	if err == nil || !strings.Contains(err.Error(), "--no-discover") {
		t.Fatalf("error = %v, want no-discover", err)
	}
}

func TestLoadOrDiscoverConfigParseError(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "downstream.toml")
	mustWriteFile(t, p, "not = valid = toml = at = all\n[[[")

	_, _, _, _, err := loadOrDiscover(context.Background(), io.Discard, testFlags{
		configPath: p,
		upstream:   "github.com/acme/lib",
	}, "go", 5, 0, false)
	if err == nil {
		t.Fatal("want error for malformed config (should not fall through to discover)")
	}
}

// TestRunFromConfig drives the full run command end to end against
// the local fixtures, exercising the config-exists branch and the
// shared runMulti loop.
func TestRunFromConfig(t *testing.T) {
	fixture, err := filepath.Abs(filepath.Join("..", "internal", "run", "testdata"))
	if err != nil {
		t.Fatal(err)
	}

	workdir := t.TempDir()
	cfgPath := filepath.Join(workdir, "downstream.toml")
	mustWriteFile(t, cfgPath, `
[package]
name = "example.test/upstream"
ecosystem = "go"

[[dependents]]
name = "example.test/dependent"
repo = "`+filepath.Join(fixture, "dependent")+`"
`)

	slug := "example.test-dependent"
	if err := copyTree(filepath.Join(fixture, "upstream"), filepath.Join(workdir, slug, "upstream")); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCmd(t, "run",
		"-c", cfgPath,
		"--upstream-path", filepath.Join(fixture, "upstream"),
		"--workdir", workdir,
	)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "1 passed, 0 failed") {
		t.Errorf("missing summary:\n%s", out)
	}
	if !strings.Contains(out, "using "+cfgPath) {
		t.Errorf("should report config path:\n%s", out)
	}
}

// TestRunDiscoversWhenNoConfig drives the discover branch end to end:
// no config file, mock API serves the local fixture as the only
// dependent, run tests it and reports.
func TestRunDiscoversWhenNoConfig(t *testing.T) {
	fixture, err := filepath.Abs(filepath.Join("..", "internal", "run", "testdata"))
	if err != nil {
		t.Fatal(err)
	}

	withMockEcosystems(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]discover.Package{{
			Name:                   "example.test/dependent",
			RepositoryURL:          filepath.Join(fixture, "dependent"),
			DependentPackagesCount: 1,
			RepoMetadata:           discover.RepoMetadata{PushedAt: time.Now()},
		}})
	})

	workdir := t.TempDir()
	slug := "example.test-dependent"
	if err := copyTree(filepath.Join(fixture, "upstream"), filepath.Join(workdir, slug, "upstream")); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Run from a temp cwd so the default config path (./downstream.toml)
	// doesn't exist and discover kicks in.
	cwd := t.TempDir()
	t.Chdir(cwd)

	out, err := runCmd(t, "run",
		"--upstream", "example.test/upstream",
		"--upstream-path", filepath.Join(fixture, "upstream-broken"),
		"--workdir", workdir,
		"--limit", "1",
	)
	if err == nil {
		t.Fatalf("expected non-zero exit for broken upstream\n%s", out)
	}
	if !strings.Contains(out, "querying ecosyste.ms") {
		t.Errorf("should hit discover path:\n%s", out)
	}
	if !strings.Contains(out, "0 passed, 1 failed") {
		t.Errorf("missing summary:\n%s", out)
	}
}

func TestRunRequiresUpstreamSource(t *testing.T) {
	_, err := runCmd(t, "run", "-c", "testdata/downstream.toml")
	if err == nil || !strings.Contains(err.Error(), "provide --upstream-path or a ref") {
		t.Fatalf("error = %v, want upstream-path-required", err)
	}
}

func TestRunOnlyFilter(t *testing.T) {
	withMockEcosystems(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("should not discover when config exists")
		_, _ = w.Write([]byte("[]"))
	})

	out, err := runCmd(t, "run",
		"-c", "testdata/downstream.toml",
		"--upstream-path", os.DevNull,
		"--only", "nonesuch",
	)
	if err == nil || !strings.Contains(err.Error(), "matches no dependent") {
		t.Fatalf("error = %v, want no-match\n%s", err, out)
	}
}
