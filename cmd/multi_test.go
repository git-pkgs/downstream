package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMultiFromConfig drives the toml path end to end against the
// run package's local fixtures. The dependent's go.mod has a relative
// replace to ../upstream for its baseline, so we seed
// workdir/<slug>/upstream with the good library before invoking.
func TestMultiFromConfig(t *testing.T) {
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

	// Seed baseline upstream where the dependent's relative replace
	// will look for it: workdir/<slug>/upstream.
	slug := "example.test-dependent"
	if err := copyTree(filepath.Join(fixture, "upstream"), filepath.Join(workdir, slug, "upstream")); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCmd(t, "test",
		"-c", cfgPath,
		"--upstream-path", filepath.Join(fixture, "upstream-broken"),
		"--workdir", workdir,
	)
	if err == nil {
		t.Fatalf("expected non-zero exit for failed dependent\n%s", out)
	}
	if !strings.Contains(out, "| example.test/dependent |") {
		t.Errorf("missing summary row:\n%s", out)
	}
	if !strings.Contains(out, "0 passed, 1 failed") {
		t.Errorf("missing counts:\n%s", out)
	}
	if !strings.Contains(out, "**status:** failed") {
		t.Errorf("missing per-dependent detail:\n%s", out)
	}

	// Per-dependent workdir layout: workdir/<slug>/dependent/go.mod
	// should now carry the absolute replace to the broken upstream.
	gomod, rerr := os.ReadFile(filepath.Join(workdir, slug, "dependent", "go.mod"))
	if rerr != nil {
		t.Fatalf("read dependent go.mod: %v", rerr)
	}
	if !strings.Contains(string(gomod), filepath.Join(fixture, "upstream-broken")) {
		t.Errorf("go.mod missing absolute replace:\n%s", gomod)
	}
}

func mustWriteFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, info.Mode().Perm())
	})
}
