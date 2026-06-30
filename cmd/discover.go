package cmd

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/git-pkgs/downstream/internal/config"
	"github.com/git-pkgs/downstream/internal/discover"
	"github.com/spf13/cobra"
	"golang.org/x/mod/modfile"
)

func addDiscoverCmd(parent *cobra.Command) {
	var (
		pkg        string
		ecosystem  string
		limit      int
		pool       int
		maxAge     time.Duration
		output     string
		stdoutOnly bool
		noAnalyze  bool
		workdir    string
		keep       bool
	)

	c := &cobra.Command{
		Use:   "discover",
		Short: "Find and rank dependents via ecosyste.ms and write downstream.toml",
		Long: `Queries packages.ecosyste.ms for the most-used packages that depend on
the given module, drops forks/archived/stale repos, then shallow-clones
the survivors and scores them on test count and how many of their files
import the module. The ranked top N is reconciled with any existing
downstream.toml: manual entries and per-dependent overrides are kept,
discovered entries are rescored, and new candidates are appended.

If --package is omitted and a go.mod is present, the module path is
read from there.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			out, errw := cmd.OutOrStdout(), cmd.ErrOrStderr()
			ctx := cmd.Context()

			if pkg == "" {
				p, err := readGoModule()
				if err != nil {
					return errors.New("--package is required (no go.mod found in current directory)")
				}
				pkg = p
			}

			log := func(format string, a ...any) { _, _ = fmt.Fprintf(errw, "discover: "+format+"\n", a...) }

			if pool <= 0 {
				pool = limit * defaultPoolMultiplier
			}
			cands, err := discover.Discover(ctx, discover.Options{
				Ecosystem: ecosystem,
				Package:   pkg,
				Limit:     pool, // phase one keeps the whole pool; Analyze trims to limit
				Pool:      pool,
				MaxAge:    maxAge,
				Client:    newDiscoverClient(),
				Stderr:    errw,
			})
			if err != nil {
				return err
			}

			if !noAnalyze {
				cands, err = discover.Analyze(ctx, cands, discover.AnalyzeOptions{
					Upstream: pkg,
					Workdir:  workdir,
					Limit:    limit,
					Keep:     keep,
				}, log)
				if err != nil {
					return err
				}
			} else if len(cands) > limit {
				cands = cands[:limit]
			}

			var existing *config.Config
			if cfg, lerr := config.Load(output); lerr == nil {
				existing = cfg
			} else if !errors.Is(lerr, os.ErrNotExist) {
				return lerr
			}

			cfg := discover.Reconcile(existing, config.Package{Name: pkg, Ecosystem: ecosystem}, cands)

			if stdoutOnly {
				_, werr := config.WriteTo(out, cfg)
				return werr
			}
			if err := config.Write(output, cfg); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(errw, "wrote %s with %d dependents\n", output, len(cfg.Dependents))
			return nil
		},
	}

	c.Flags().StringVar(&pkg, "package", "", "Package/module to discover dependents for (default: module from ./go.mod)")
	c.Flags().StringVarP(&ecosystem, "ecosystem", "e", "go", "Ecosystem (go, npm, rubygems, pypi, cargo, packagist)")
	c.Flags().IntVarP(&limit, "limit", "n", defaultDiscoverLimit, "Number of dependents to keep")
	c.Flags().IntVar(&pool, "pool", 0, "Candidates to fetch before filtering (default limit*6)")
	c.Flags().DurationVar(&maxAge, "max-age", 0, "Drop repos with no push in this window (default 2y)")
	c.Flags().StringVarP(&output, "output", "o", config.DefaultPath, "Output path")
	c.Flags().BoolVar(&stdoutOnly, "stdout", false, "Write to stdout instead of a file")
	c.Flags().BoolVar(&noAnalyze, "no-analyze", false, "Skip phase two (cloning and scoring)")
	c.Flags().StringVar(&workdir, "workdir", "", "Directory for analysis clones (default: temp dir)")
	c.Flags().BoolVar(&keep, "keep", false, "Keep analysis clones for a follow-up downstream test")

	parent.AddCommand(c)
}

const (
	defaultDiscoverLimit  = 5
	defaultPoolMultiplier = 6
)

// newDiscoverClient is swapped in tests to point at an httptest
// server instead of the live ecosyste.ms API.
var newDiscoverClient = discover.NewClient

func readGoModule() (string, error) {
	data, err := os.ReadFile("go.mod")
	if err != nil {
		return "", err
	}
	mf, err := modfile.ParseLax("go.mod", data, nil)
	if err != nil {
		return "", err
	}
	if mf.Module == nil {
		return "", errors.New("go.mod has no module directive")
	}
	return mf.Module.Mod.Path, nil
}
