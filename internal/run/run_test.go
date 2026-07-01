package run

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRunPassed exercises the full clone/baseline/replace/retest loop
// against local fixtures. The dependent's go.mod has a relative
// replace to ../upstream so the baseline resolves; the test pre-seeds
// workdir/upstream with the good version, then Replace points at the
// same good version, so both runs pass.
func TestRunPassed(t *testing.T) {
	result := runFixture(t, "upstream")

	if result.Status() != StatusPassed {
		t.Fatalf("status = %s, want passed\nbaseline:\n%s\npatched:\n%s",
			result.Status(), result.Baseline.Output, result.Patched.Output)
	}
	if !result.Baseline.Passed() {
		t.Errorf("baseline should pass: %s", result.Baseline.Output)
	}
	if !result.Patched.Passed() {
		t.Errorf("patched should pass: %s", result.Patched.Output)
	}

	gomod := readWorkfile(t, result.DependentPath, "go.mod")
	if !strings.Contains(gomod, "replace example.test/upstream => "+result.UpstreamPath) {
		t.Errorf("go.mod missing absolute replace directive:\n%s", gomod)
	}
}

// TestRunFailed swaps in the broken upstream variant so the patched
// run fails while the baseline still passes.
func TestRunFailed(t *testing.T) {
	result := runFixture(t, "upstream-broken")

	if result.Status() != StatusFailed {
		t.Fatalf("status = %s, want failed\nbaseline:\n%s\npatched:\n%s",
			result.Status(), result.Baseline.Output, result.Patched.Output)
	}
	if !result.Baseline.Passed() {
		t.Errorf("baseline should pass: %s", result.Baseline.Output)
	}
	if result.Patched.Passed() {
		t.Errorf("patched should fail")
	}
	if !result.Failed() {
		t.Errorf("Failed() should be true")
	}

	md := result.Markdown()
	if !strings.Contains(md, "**status:** failed") {
		t.Errorf("markdown missing failed status:\n%s", md)
	}
	if !strings.Contains(md, "introduced new failures") {
		t.Errorf("markdown missing failure note:\n%s", md)
	}
}

func TestRunCargoFailed(t *testing.T) {
	requireCommand(t, "cargo")

	result := runFixtureWithBaseline(t, fixtureRun{
		module:           "downstream_fixture_upstream",
		baselineFixture:  "cargo-upstream",
		upstreamFixture:  "cargo-upstream-broken",
		dependentFixture: "cargo-dependent",
	})

	if result.Manager != "cargo" {
		t.Fatalf("manager = %q, want cargo", result.Manager)
	}
	if result.Status() != StatusFailed {
		t.Fatalf("status = %s, want failed\nbaseline:\n%s\npatched:\n%s",
			result.Status(), result.Baseline.Output, result.Patched.Output)
	}
	if !result.Baseline.Passed() {
		t.Errorf("baseline should pass: %s", result.Baseline.Output)
	}
	if result.Patched.Passed() {
		t.Errorf("patched should fail")
	}

	cargoToml := readWorkfile(t, result.DependentPath, "Cargo.toml")
	if !strings.Contains(cargoToml, result.UpstreamPath) {
		t.Errorf("Cargo.toml missing patched upstream path %q:\n%s", result.UpstreamPath, cargoToml)
	}
}

func TestRunNPMFailed(t *testing.T) {
	requireCommand(t, "npm")

	result := runFixtureWithBaseline(t, fixtureRun{
		module:           "downstream-fixture-upstream",
		baselineFixture:  "npm-upstream",
		upstreamFixture:  "npm-upstream-broken",
		dependentFixture: "npm-dependent",
	})

	if result.Manager != "npm" {
		t.Fatalf("manager = %q, want npm", result.Manager)
	}
	if result.Status() != StatusFailed {
		t.Fatalf("status = %s, want failed\nbaseline:\n%s\npatched:\n%s",
			result.Status(), result.Baseline.Output, result.Patched.Output)
	}
	if !result.Baseline.Passed() {
		t.Errorf("baseline should pass: %s", result.Baseline.Output)
	}
	if result.Patched.Passed() {
		t.Errorf("patched should fail")
	}
}

func TestResolveUpstreamLocalPath(t *testing.T) {
	ctx := context.Background()
	workdir := t.TempDir()

	// A directory with any manifest managers.Detector recognises should
	// pass; use Cargo.toml to prove the check isn't Go-specific.
	crate := filepath.Join(workdir, "crate")
	mustMkdir(t, crate)
	mustWrite(t, filepath.Join(crate, "Cargo.toml"), "[package]\nname = \"x\"\n")

	got, err := ResolveUpstream(ctx, workdir, Options{UpstreamPath: crate})
	if err != nil {
		t.Fatalf("cargo dir: %v", err)
	}
	if got != crate {
		t.Errorf("path = %q, want %q", got, crate)
	}

	empty := filepath.Join(workdir, "empty")
	mustMkdir(t, empty)
	if _, err := ResolveUpstream(ctx, workdir, Options{UpstreamPath: empty}); err == nil {
		t.Fatal("empty dir: want manager-detect error, got nil")
	} else if strings.Contains(err.Error(), "go.mod") {
		t.Errorf("error = %v; should not be Go-specific", err)
	}
}

func TestResolveUpstreamBareNameNeedsRepo(t *testing.T) {
	// A bare package name (crate/gem/npm) with no repo URL and no
	// local path should error with a hint rather than trying to clone
	// https://serde_json.
	_, err := ResolveUpstream(context.Background(), t.TempDir(), Options{
		Module:      "serde_json",
		UpstreamRef: "main",
	})
	if err == nil || !strings.Contains(err.Error(), "[package].repo") {
		t.Fatalf("error = %v, want [package].repo hint", err)
	}
}

func TestDetectManagerNPMFamilyHints(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{
			name: "package json only defaults to npm",
			files: map[string]string{
				"package.json": `{"name":"x","version":"1.0.0"}`,
			},
			want: "npm",
		},
		{
			name: "package lock selects npm",
			files: map[string]string{
				"package.json":      `{"name":"x","version":"1.0.0"}`,
				"package-lock.json": `{}`,
			},
			want: "npm",
		},
		{
			name: "package manager selects pnpm",
			files: map[string]string{
				"package.json": `{"name":"x","version":"1.0.0","packageManager":"pnpm@10.0.0"}`,
			},
			want: "pnpm",
		},
		{
			name: "bun lock selects bun",
			files: map[string]string{
				"package.json": `{"name":"x","version":"1.0.0"}`,
				"bun.lock":     "",
			},
			want: "bun",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, content := range tt.files {
				mustWrite(t, filepath.Join(dir, name), content)
			}
			mgr, err := detectManager(dir)
			if err != nil {
				t.Fatalf("detectManager: %v", err)
			}
			if mgr.Name() != tt.want {
				t.Fatalf("manager = %q, want %q", mgr.Name(), tt.want)
			}
		})
	}
}

func TestRunRejectsManagerWithoutReplacePath(t *testing.T) {
	workdir := t.TempDir()
	pipDep := filepath.Join(workdir, "src")
	mustMkdir(t, pipDep)
	mustWrite(t, filepath.Join(pipDep, "requirements.txt"), "requests==2.0\n")

	_, err := Test(context.Background(), Options{
		Module:       "example.test/upstream",
		UpstreamPath: fixturePath(t, "upstream"),
		Dependent:    pipDep,
		Workdir:      workdir,
		Stderr:       io.Discard,
	})
	if err == nil || !strings.Contains(err.Error(), "does not support path replacement") {
		t.Fatalf("error = %v, want replace-not-supported error", err)
	}
}

func runFixture(t *testing.T, upstreamFixture string) *Result {
	t.Helper()

	return runFixtureWithBaseline(t, fixtureRun{
		module:           "example.test/upstream",
		baselineFixture:  "upstream",
		upstreamFixture:  upstreamFixture,
		dependentFixture: "dependent",
	})
}

type fixtureRun struct {
	module           string
	baselineFixture  string
	upstreamFixture  string
	dependentFixture string
}

func runFixtureWithBaseline(t *testing.T, fr fixtureRun) *Result {
	t.Helper()

	workdir := t.TempDir()
	// Seed workdir/<baselineFixture> with the good library so relative
	// baseline replacements like ../upstream or file:../npm-upstream
	// resolve after the dependent is copied into workdir/dependent.
	if err := copyDir(fixturePath(t, fr.baselineFixture), filepath.Join(workdir, fr.baselineFixture)); err != nil {
		t.Fatalf("seed baseline upstream: %v", err)
	}

	result, err := Test(context.Background(), Options{
		Module:       fr.module,
		UpstreamPath: fixturePath(t, fr.upstreamFixture),
		Dependent:    fixturePath(t, fr.dependentFixture),
		Workdir:      workdir,
		Timeout:      2 * time.Minute,
		Stderr:       io.Discard,
	})
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	return result
}

func requireCommand(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s not on PATH", name)
	}
}

func fixturePath(t *testing.T, name string) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func readWorkfile(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
