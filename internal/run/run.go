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
	if mgr.Name() != "gomod" {
		return nil, fmt.Errorf("only Go modules are supported (found %s)", mgr.Name())
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

	if opts.TestCmd == "" {
		opts.TestCmd, result.Narrowed = autoNarrow(ctx, dependentPath, opts)
	}

	logf(opts, "running baseline tests in %s", dependentPath)
	result.Baseline = runTests(ctx, dependentPath, opts)

	logf(opts, "replacing %s -> %s", opts.Module, upstreamPath)
	if _, err := mgr.Replace(ctx, opts.Module, managers.ReplaceOptions{Path: upstreamPath}); err != nil {
		return nil, fmt.Errorf("replace: %w", err)
	}
	if err := tidy(ctx, dependentPath); err != nil {
		return nil, fmt.Errorf("go mod tidy after replace: %w", err)
	}

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

// autoNarrow computes a narrowed test command for the dependent if
// one isn't already set. Returns the command string and the count of
// packages it was narrowed to; on any error, falls back to ./... and
// returns 0 so the run isn't blocked by a flaky go list.
func autoNarrow(ctx context.Context, dir string, opts Options) (string, int) {
	pkgs, err := narrowGoTest(ctx, dir, opts.Module)
	if err != nil {
		logf(opts, "auto-narrow failed (%v); using ./...", err)
		return "", 0
	}
	if len(pkgs) == 0 {
		logf(opts, "no packages in dependent import %s; using ./...", opts.Module)
		return "", 0
	}
	logf(opts, "narrowed to %d package(s) importing %s", len(pkgs), opts.Module)
	return "go test " + strings.Join(pkgs, " "), len(pkgs)
}

func logf(opts Options, format string, args ...any) {
	_, _ = fmt.Fprintf(opts.Stderr, "downstream: "+format+"\n", args...)
}
