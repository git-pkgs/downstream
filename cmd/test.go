package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/git-pkgs/downstream/internal/config"
	"github.com/git-pkgs/downstream/internal/run"
	"github.com/spf13/cobra"
)

const (
	defaultTestTimeout             = 30 * time.Minute
	workdirPerm        os.FileMode = 0o755
)

type testFlags struct {
	upstream     string
	upstreamPath string
	dependent    string
	configPath   string
	only         []string
	workdir      string
	testCmd      string
	timeout      time.Duration
	keep         bool
}

func addTestCmd(parent *cobra.Command) {
	var f testFlags

	c := &cobra.Command{
		Use:   "test",
		Short: "Run dependents' tests against a patched upstream",
		Long: `Runs each dependent's tests for a baseline, replaces the upstream module
with the given path or ref, reruns the tests, and prints a markdown report
of the difference.

With --dependent, tests a single repo URL or local path. Otherwise reads
dependents from downstream.toml (or --config), filtered by --only.

Currently supports Go modules only.

Examples:
  downstream test --upstream github.com/spf13/cobra --upstream-path . \
      --dependent https://github.com/cli/cli

  downstream test --upstream-path . --only cli/cli`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTest(cmd, f)
		},
	}

	c.Flags().StringVar(&f.upstream, "upstream", "", "Upstream module path, optionally module@ref (default: [package].name from config)")
	c.Flags().StringVar(&f.upstreamPath, "upstream-path", "", "Local path to the patched upstream (overrides @ref)")
	c.Flags().StringVar(&f.dependent, "dependent", "", "Single dependent repo URL or local path (bypasses config)")
	c.Flags().StringVarP(&f.configPath, "config", "c", config.DefaultPath, "Path to downstream.toml")
	c.Flags().StringSliceVar(&f.only, "only", nil, "Filter dependents by name, slug, glob, or substring (repeatable)")
	c.Flags().StringVar(&f.workdir, "workdir", "", "Directory for clones (default: temp dir)")
	c.Flags().StringVar(&f.testCmd, "test", "", "Override test command (default: go test ./...)")
	c.Flags().DurationVar(&f.timeout, "timeout", defaultTestTimeout, "Timeout for each test run")
	c.Flags().BoolVar(&f.keep, "keep", false, "Keep workdir after run")

	parent.AddCommand(c)
}

func runTest(cmd *cobra.Command, f testFlags) error {
	ctx := cmd.Context()
	out, errw := cmd.OutOrStdout(), cmd.ErrOrStderr()

	if f.dependent != "" {
		return runSingle(ctx, out, errw, f)
	}

	cfg, err := config.Load(f.configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("no --dependent given and %s not found; run 'downstream discover' or pass --dependent", f.configPath)
		}
		return err
	}
	deps, err := cfg.Filter(f.only)
	if err != nil {
		return err
	}

	module, ref, err := resolveModule(f.upstream, cfg.Package.Name)
	if err != nil {
		return err
	}
	if f.upstreamPath == "" && ref == "" {
		return errors.New("provide --upstream-path or a ref in --upstream module@ref")
	}

	return runMulti(ctx, out, errw, module, ref, deps, f)
}

func runSingle(ctx context.Context, out, errw io.Writer, f testFlags) error {
	module, ref, err := resolveModule(f.upstream, "")
	if err != nil {
		return err
	}
	if f.upstreamPath == "" && ref == "" {
		return errors.New("provide --upstream-path or a ref in --upstream module@ref")
	}

	result, err := run.Test(ctx, run.Options{
		Module:       module,
		UpstreamRef:  ref,
		UpstreamPath: f.upstreamPath,
		Dependent:    f.dependent,
		Workdir:      f.workdir,
		TestCmd:      f.testCmd,
		Timeout:      f.timeout,
		Keep:         f.keep,
		Stderr:       errw,
	})
	if err != nil {
		return err
	}

	_, _ = fmt.Fprint(out, result.Markdown())
	if result.Failed() {
		return errors.New("new failures introduced by replacement")
	}
	return nil
}

func runMulti(ctx context.Context, out, errw io.Writer, module, ref string, deps []config.Dependent, f testFlags) error {
	workdir := f.workdir
	if workdir == "" {
		dir, err := os.MkdirTemp("", "downstream-")
		if err != nil {
			return err
		}
		workdir = dir
		if !f.keep {
			defer func() { _ = os.RemoveAll(dir) }()
		}
	} else if err := os.MkdirAll(workdir, workdirPerm); err != nil {
		return err
	}

	upstreamPath, err := run.ResolveUpstream(ctx, workdir, module, ref, f.upstreamPath, errw)
	if err != nil {
		return fmt.Errorf("resolving upstream: %w", err)
	}

	results := make(run.Results, 0, len(deps))
	for _, d := range deps {
		_, _ = fmt.Fprintf(errw, "downstream: === %s ===\n", d.Name)
		opts := run.Options{
			Module:       module,
			UpstreamPath: upstreamPath,
			Dependent:    d.Repo,
			DependentRef: d.Ref,
			Name:         d.Name,
			Workdir:      filepath.Join(workdir, d.Slug()),
			TestCmd:      firstNonEmpty(f.testCmd, d.Test),
			Timeout:      f.timeout,
			Keep:         true, // outer cleanup owns the workdir
			Stderr:       errw,
		}
		r, err := run.Test(ctx, opts)
		results = append(results, run.ResultEntry{Name: d.Name, Result: r, SetupErr: err})
	}

	_, _ = fmt.Fprint(out, results.Markdown())
	if results.AnyFailed() {
		return errors.New("new failures introduced by replacement")
	}
	return nil
}

func resolveModule(flag, fromConfig string) (module, ref string, err error) {
	if flag != "" {
		return parseUpstream(flag)
	}
	if fromConfig != "" {
		return fromConfig, "", nil
	}
	return "", "", errors.New("--upstream is required (or set [package].name in downstream.toml)")
}

func parseUpstream(s string) (module, ref string, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", errors.New("--upstream is required")
	}
	if i := strings.LastIndex(s, "@"); i > 0 {
		return s[:i], s[i+1:], nil
	}
	return s, "", nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
