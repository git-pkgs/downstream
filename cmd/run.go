package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/git-pkgs/downstream/internal/config"
	"github.com/git-pkgs/downstream/internal/discover"
	"github.com/spf13/cobra"
)

func addRunCmd(parent *cobra.Command) {
	var (
		f          testFlags
		ecosystem  string
		limit      int
		pool       int
		noDiscover bool
	)

	c := &cobra.Command{
		Use:   "run",
		Short: "Discover dependents (or read downstream.toml) and test each against the patched upstream",
		Long: `On-demand mode: if downstream.toml exists it's loaded, otherwise dependents
are discovered via ecosyste.ms; either way each is tested against the
patched upstream and an aggregate report is printed.

This is "downstream discover" piped into "downstream test" without the
intermediate file.

Examples:
  downstream run --upstream-path .
  downstream run --upstream github.com/spf13/cobra@my-branch --limit 3`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			out, errw := cmd.OutOrStdout(), cmd.ErrOrStderr()

			module, repo, ref, deps, err := loadOrDiscover(ctx, errw, f, ecosystem, limit, pool, noDiscover)
			if err != nil {
				return err
			}
			if f.upstreamPath == "" && ref == "" {
				return errors.New("provide --upstream-path or a ref in --upstream name@ref")
			}

			if len(f.only) > 0 {
				cfg := &config.Config{Dependents: deps}
				deps, err = cfg.Filter(f.only)
				if err != nil {
					return err
				}
			}

			return runMulti(ctx, out, errw, module, repo, ref, deps, f)
		},
	}

	c.Flags().StringVar(&f.upstream, "upstream", "", "Upstream package name, optionally name@ref (default: [package].name from config or ./go.mod)")
	c.Flags().StringVar(&f.upstreamRepo, "upstream-repo", "", "Upstream git URL for cloning by ref (default: [package].repo)")
	c.Flags().StringVar(&f.upstreamPath, "upstream-path", "", "Local path to the patched upstream (overrides @ref)")
	c.Flags().StringVarP(&f.configPath, "config", "c", config.DefaultPath, "Path to downstream.toml; used if it exists")
	c.Flags().StringSliceVar(&f.only, "only", nil, "Filter dependents by name, slug, glob, or substring")
	c.Flags().StringVar(&f.workdir, "workdir", "", "Directory for clones (default: temp dir)")
	c.Flags().StringVar(&f.testCmd, "test", "", "Override test command (default: detected via brief)")
	c.Flags().DurationVar(&f.timeout, "timeout", defaultTestTimeout, "Timeout for each test run")
	c.Flags().BoolVar(&f.keep, "keep", false, "Keep workdir after run")

	c.Flags().StringVarP(&ecosystem, "ecosystem", "e", "go", "Ecosystem for discovery")
	c.Flags().IntVarP(&limit, "limit", "n", defaultDiscoverLimit, "Number of dependents to discover when no config exists")
	c.Flags().IntVar(&pool, "pool", 0, "Candidates to fetch before filtering (default limit*6)")
	c.Flags().BoolVar(&noDiscover, "no-discover", false, "Fail if config doesn't exist instead of querying ecosyste.ms")

	parent.AddCommand(c)
}

// loadOrDiscover returns the upstream module, repo URL, ref, and the
// set of dependents to test. If the config file exists it wins;
// otherwise discovery runs against ecosyste.ms.
func loadOrDiscover(ctx context.Context, errw io.Writer, f testFlags, ecosystem string, limit, pool int, noDiscover bool) (string, string, string, []config.Dependent, error) {
	if cfg, err := config.Load(f.configPath); err == nil {
		module, ref, mErr := resolveModule(f.upstream, cfg.Package.Name)
		if mErr != nil {
			return "", "", "", nil, mErr
		}
		repo := firstNonEmpty(f.upstreamRepo, cfg.Package.Repo)
		_, _ = fmt.Fprintf(errw, "downstream: using %s (%d dependents)\n", f.configPath, len(cfg.Dependents))
		return module, repo, ref, cfg.Dependents, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", "", "", nil, err
	}

	if noDiscover {
		return "", "", "", nil, fmt.Errorf("%s not found and --no-discover is set", f.configPath)
	}

	module, ref, err := resolveModule(f.upstream, "")
	if err != nil {
		m, mErr := readGoModule()
		if mErr != nil {
			return "", "", "", nil, errors.New("--upstream is required (no config, no ./go.mod)")
		}
		module = m
	}

	cands, err := discover.Discover(ctx, discover.Options{
		Ecosystem: ecosystem,
		Package:   module,
		Limit:     limit,
		Pool:      pool,
		Client:    newDiscoverClient(),
		Stderr:    errw,
	})
	if err != nil {
		return "", "", "", nil, err
	}
	deps := make([]config.Dependent, len(cands))
	for i, c := range cands {
		deps[i] = c.Dependent()
	}
	return module, f.upstreamRepo, ref, deps, nil
}
