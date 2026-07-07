// Package run implements the clone/baseline/replace/retest loop for a
// single (upstream, dependent) pair.
package run

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/git-pkgs/managers"
	"github.com/git-pkgs/managers/definitions"
)

const (
	managerGo   = "gomod"
	managerNPM  = "npm"
	managerPNPM = "pnpm"
	managerYarn = "yarn"
	managerBun  = "bun"
)

type Options struct {
	Module       string        // upstream package name as the dependent's manager knows it
	UpstreamRepo string        // git URL to clone the upstream from when UpstreamPath is empty
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

	upstreamPath, err := ResolveUpstream(ctx, workdir, opts)
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
	if opts.TestCmd == "" {
		return nil, fmt.Errorf("no test command for %s: none configured and brief detected nothing", dependentPath)
	}
	logf(opts, "test command: %s", opts.TestCmd)

	logf(opts, "running baseline tests in %s", dependentPath)
	result.Baseline = runTests(ctx, dependentPath, opts)

	logf(opts, "replacing %s -> %s", opts.Module, upstreamPath)
	r, err := mgr.Replace(ctx, opts.Module, managers.ReplaceOptions{Path: upstreamPath})
	if err != nil {
		return nil, fmt.Errorf("replace: %w", err)
	}
	if r != nil && r.ExitCode != 0 {
		return nil, fmt.Errorf("replace exited %d:\n%s", r.ExitCode, strings.TrimSpace(r.Stderr))
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

// ResolveUpstream returns an absolute path to the patched upstream
// source. If opts.UpstreamPath is set it's validated by manager
// detection and used as-is; otherwise opts.UpstreamRepo is cloned at
// opts.UpstreamRef into workdir/upstream. Exported so callers running
// many dependents can resolve once and pass the path to each Test.
func ResolveUpstream(ctx context.Context, workdir string, opts Options) (string, error) {
	if opts.Stderr == nil {
		opts.Stderr = io.Discard
	}

	if opts.UpstreamPath != "" {
		abs, err := filepath.Abs(opts.UpstreamPath)
		if err != nil {
			return "", err
		}
		if _, err := detectManager(abs); err != nil {
			return "", fmt.Errorf("upstream path %s: %w", abs, err)
		}
		return abs, nil
	}

	repo := opts.UpstreamRepo
	if repo == "" {
		// Go module paths are host/path and clone directly with an
		// https scheme; bare package names (crates, gems, npm) need
		// an explicit repo URL.
		if !strings.Contains(opts.Module, "/") {
			return "", fmt.Errorf("no upstream repo URL for %q; set [package].repo or use --upstream-path", opts.Module)
		}
		repo = "https://" + opts.Module
	}

	dest := filepath.Join(workdir, "upstream")
	logf(opts, "cloning upstream %s@%s -> %s", repo, opts.UpstreamRef, dest)
	if err := clone(ctx, repo, opts.UpstreamRef, dest); err != nil {
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
	if manager := npmFamilyManagerHint(dir); manager != "" {
		return det.Detect(dir, managers.DetectOptions{Manager: manager})
	}
	return det.Detect(dir, managers.DetectOptions{})
}

func npmFamilyManagerHint(dir string) string {
	if _, err := os.Stat(filepath.Join(dir, "package.json")); err != nil {
		return ""
	}

	if hasAnyFile(dir, "package-lock.json", "npm-shrinkwrap.json") {
		return managerNPM
	}
	if hasAnyFile(dir, "pnpm-lock.yaml") {
		return managerPNPM
	}
	if hasAnyFile(dir, "yarn.lock") {
		return managerYarn
	}
	if hasAnyFile(dir, "bun.lock", "bun.lockb") {
		return managerBun
	}
	if manager := packageManagerField(dir); manager != "" {
		return manager
	}

	// managers' generic detector ranks bun above npm for a bare
	// package.json because bun should win when its lockfile is present.
	// Without a lockfile or packageManager field, npm is the least
	// surprising default for npm-ecosystem repos.
	return managerNPM
}

func hasAnyFile(dir string, names ...string) bool {
	for _, name := range names {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return false
}

func packageManagerField(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return ""
	}
	var pkg struct {
		PackageManager string `json:"packageManager"`
	}
	if err := json.Unmarshal(b, &pkg); err != nil {
		return ""
	}
	name, _, _ := strings.Cut(pkg.PackageManager, "@")
	switch name {
	case managerNPM, managerPNPM, managerYarn, managerBun:
		return name
	default:
		return ""
	}
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
//  2. brief detection in the dependent: if it reports a
//     project-defined test script (Makefile target, package.json
//     script) use that as-is since it may carry flags or setup that
//     auto-narrowing would bypass.
//  3. For Go, auto-narrow to packages whose imports reach the
//     upstream module.
//  4. brief's generic per-ecosystem command from its knowledge base.
//
// Returns the command and the auto-narrow package count (0 if not
// narrowed). An empty command means brief recognised nothing.
func resolveTestCommand(ctx context.Context, dir, manager string, opts Options) (string, int) {
	if opts.TestCmd != "" {
		return opts.TestCmd, 0
	}

	detected, fromProject := briefDetect(dir)
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

	return detected, 0
}

func logf(opts Options, format string, args ...any) {
	_, _ = fmt.Fprintf(opts.Stderr, "downstream: "+format+"\n", args...)
}
