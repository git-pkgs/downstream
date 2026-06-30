// Package run implements the clone/baseline/replace/retest loop for a
// single (upstream, dependent) pair.
package run

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/git-pkgs/managers"
	"github.com/git-pkgs/managers/definitions"
)

const managerGo = "gomod"

type Options struct {
	Module       string        // upstream module path, e.g. github.com/spf13/cobra
	UpstreamRef  string        // git ref to clone the upstream at; ignored if UpstreamPath is set
	UpstreamPath string        // local path to the patched upstream
	Dependent    string        // repo URL or local path
	DependentRef string        // optional ref to check out in the dependent
	Name         string        // display name for the dependent in reports; defaults to Dependent
	Workdir      string        // base directory for clones; temp dir if empty
	TestCmd      string        // override test command; defaults per ecosystem
	Timeout      time.Duration // per test run
	Keep         bool          // keep workdir on exit
	Stderr       io.Writer     // progress output
}

// Test runs the full loop for one dependent and returns a Result. The
// returned error is non-nil only for setup failures (clone, replace,
// detection); test failures are reported in the Result and don't cause
// an error here.
func Test(ctx context.Context, opts Options) (*Result, error) {
	if opts.Stderr == nil {
		opts.Stderr = io.Discard
	}

	workdir, cleanup, err := setupWorkdir(opts)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	upstreamPath, err := resolveUpstream(ctx, workdir, opts)
	if err != nil {
		return nil, fmt.Errorf("resolving upstream: %w", err)
	}

	dependentPath, err := fetchDependent(ctx, workdir, opts)
	if err != nil {
		return nil, fmt.Errorf("fetching dependent: %w", err)
	}

	mgr, err := detectManager(dependentPath)
	if err != nil {
		return nil, fmt.Errorf("detecting package manager in %s: %w", dependentPath, err)
	}
	if !mgr.Supports(managers.CapReplacePath) {
		return nil, fmt.Errorf("manager %s does not support path replacement", mgr.Name())
	}

	name := opts.Name
	if name == "" {
		name = opts.Dependent
	}
	result := &Result{
		Module:        opts.Module,
		Dependent:     name,
		DependentPath: dependentPath,
		UpstreamPath:  upstreamPath,
		Manager:       mgr.Name(),
	}

	// Install dependencies before the baseline run. Go would auto-fetch
	// on `go test` but other ecosystems need this explicit, and for Go
	// it pre-warms the module cache before go list in autoNarrow.
	// Failures are logged and the run continues so the baseline test
	// surfaces the actual breakage as broken-baseline rather than a
	// setup error.
	install(ctx, mgr, opts, "after clone")

	opts.TestCmd, result.Narrowed = resolveTestCommand(ctx, dependentPath, mgr.Name(), opts)
	logf(opts, "test command: %s", opts.TestCmd)

	logf(opts, "running baseline tests in %s", dependentPath)
	result.Baseline = runTests(ctx, dependentPath, opts)

	logf(opts, "replacing %s -> %s", opts.Module, upstreamPath)
	if _, err := mgr.Replace(ctx, opts.Module, managers.ReplaceOptions{Path: upstreamPath}); err != nil {
		return nil, fmt.Errorf("replace: %w", err)
	}
	if mgr.Name() == managerGo {
		if err := tidy(ctx, dependentPath); err != nil {
			return nil, fmt.Errorf("go mod tidy after replace: %w", err)
		}
	}
	// Re-resolve after the override. For Go this is cheap (everything
	// cached); for cargo/uv/bundler it's required since Replace only
	// edits the manifest; for npm-family Replace already ran install
	// so this is a no-op resolve.
	install(ctx, mgr, opts, "after replace")

	logf(opts, "running tests with replacement")
	result.Patched = runTests(ctx, dependentPath, opts)

	return result, nil
}

func setupWorkdir(opts Options) (string, func(), error) {
	if opts.Workdir != "" {
		if err := os.MkdirAll(opts.Workdir, dirPerm); err != nil {
			return "", nil, err
		}
		return opts.Workdir, func() {}, nil
	}
	dir, err := os.MkdirTemp("", "downstream-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() {
		if !opts.Keep {
			_ = os.RemoveAll(dir)
		}
	}
	return dir, cleanup, nil
}

func resolveUpstream(ctx context.Context, workdir string, opts Options) (string, error) {
	return ResolveUpstream(ctx, workdir, opts.Module, opts.UpstreamRef, opts.UpstreamPath, opts.Stderr)
}

// ResolveUpstream returns an absolute path to the patched upstream
// module. If path is set it's used as-is; otherwise the module is
// cloned at ref into workdir/upstream. Exported so callers running
// many dependents can resolve once and pass the path to each Test.
func ResolveUpstream(ctx context.Context, workdir, module, ref, path string, stderr io.Writer) (string, error) {
	if path != "" {
		abs, err := filepath.Abs(path)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(filepath.Join(abs, "go.mod")); err != nil {
			return "", fmt.Errorf("no go.mod in %s", abs)
		}
		return abs, nil
	}
	dest := filepath.Join(workdir, "upstream")
	if stderr != nil {
		_, _ = fmt.Fprintf(stderr, "downstream: cloning upstream %s@%s -> %s\n", module, ref, dest)
	}
	if err := clone(ctx, "https://"+module, ref, dest); err != nil {
		return "", err
	}
	return dest, nil
}

// fetchDependent clones the dependent repo into workdir. A local path
// is copied rather than cloned so the original isn't modified by the
// replace step. Any existing dest is removed first so reused workdirs
// don't fail on the second run.
func fetchDependent(ctx context.Context, workdir string, opts Options) (string, error) {
	dest := filepath.Join(workdir, "dependent")
	if err := os.RemoveAll(dest); err != nil {
		return "", err
	}
	if isLocalPath(opts.Dependent) {
		logf(opts, "copying dependent %s -> %s", opts.Dependent, dest)
		return dest, copyDir(opts.Dependent, dest)
	}
	logf(opts, "cloning dependent %s -> %s", opts.Dependent, dest)
	return dest, clone(ctx, opts.Dependent, opts.DependentRef, dest)
}

func detectManager(dir string) (managers.Manager, error) { //nolint:ireturn
	defs, err := definitions.LoadEmbedded()
	if err != nil {
		return nil, err
	}
	tr := managers.NewTranslator()
	det := managers.NewDetector(tr, managers.NewExecRunner())
	for _, def := range defs {
		det.Register(def)
	}
	return det.Detect(dir, managers.DetectOptions{})
}

func install(ctx context.Context, mgr managers.Manager, opts Options, stage string) {
	logf(opts, "installing dependencies (%s)", stage)
	r, err := mgr.Install(ctx, managers.InstallOptions{})
	if err != nil {
		logf(opts, "install %s failed: %v; continuing", stage, err)
		return
	}
	if r != nil && r.ExitCode != 0 {
		logf(opts, "install %s exited %d; continuing\n%s", stage, r.ExitCode, strings.TrimSpace(r.Stderr))
	}
}

// resolveTestCommand decides what to run for both baseline and
// patched. Precedence:
//
//  1. User-supplied TestCmd (flag or downstream.toml) wins.
//  2. brief CLI in the dependent: if it reports a project-defined
//     test script (Makefile target, package.json script) use that
//     as-is since it may carry flags or setup auto-narrowing would
//     bypass.
//  3. For Go, auto-narrow to packages whose imports reach the
//     upstream module.
//  4. brief's generic command if any; otherwise the per-ecosystem
//     fallback.
//
// Returns the command and the auto-narrow package count (0 if not
// narrowed).
func resolveTestCommand(ctx context.Context, dir, manager string, opts Options) (string, int) {
	if opts.TestCmd != "" {
		return opts.TestCmd, 0
	}

	detected, fromProject := briefDetect(ctx, dir)
	if detected != "" {
		logf(opts, "brief detected test command %q (project-script=%v)", detected, fromProject)
	}
	if fromProject {
		return detected, 0
	}

	if manager == managerGo {
		if pkgs, err := narrowGoTest(ctx, dir, opts.Module); err == nil && len(pkgs) > 0 {
			logf(opts, "narrowed to %d package(s) importing %s", len(pkgs), opts.Module)
			return "go test " + strings.Join(pkgs, " "), len(pkgs)
		}
	}

	if detected != "" {
		return detected, 0
	}
	return fallbackTestCommand(manager), 0
}

func fallbackTestCommand(manager string) string {
	switch manager {
	case managerGo:
		return "go test ./..."
	case "cargo":
		return "cargo test"
	case "npm":
		return "npm test"
	case "pnpm":
		return "pnpm test"
	case "yarn":
		return "yarn test"
	case "bun":
		return "bun test"
	case "bundler":
		return "bundle exec rake test"
	case "uv":
		return "uv run pytest"
	case "composer":
		return "composer test"
	default:
		return ""
	}
}

func logf(opts Options, format string, args ...any) {
	_, _ = fmt.Fprintf(opts.Stderr, "downstream: "+format+"\n", args...)
}
