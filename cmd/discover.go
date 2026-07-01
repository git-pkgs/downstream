package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/git-pkgs/downstream/internal/config"
	"github.com/git-pkgs/downstream/internal/discover"
	"github.com/git-pkgs/manifests"
	"github.com/spf13/cobra"
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
reference the package. The ranked top N is reconciled with any existing
downstream.toml: manual entries and per-dependent overrides are kept,
discovered entries are rescored, and new candidates are appended.

If --package is omitted, the package name and ecosystem are read from
the manifest in the current directory (go.mod, Cargo.toml, package.json,
etc.).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			out, errw := cmd.OutOrStdout(), cmd.ErrOrStderr()
			ctx := cmd.Context()

			if pkg == "" {
				p, eco, err := readLocalPackage(".")
				if err != nil {
					return fmt.Errorf("--package is required (%w)", err)
				}
				pkg = p
				if ecosystem == "" {
					ecosystem = eco
				}
			}
			if ecosystem == "" {
				ecosystem = "go"
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

	c.Flags().StringVar(&pkg, "package", "", "Package to discover dependents for (default: read from local manifest)")
	c.Flags().StringVarP(&ecosystem, "ecosystem", "e", "", "Ecosystem (go, npm, rubygems, pypi, cargo, packagist; default: from local manifest)")
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

// readLocalPackage finds the first manifest in dir that declares a
// package name and returns that name and its ecosystem. Used to
// default --package/--ecosystem when running from a project root.
func readLocalPackage(dir string) (name, ecosystem string, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", "", err
	}
	var found []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if _, kind, ok := manifests.Identify(e.Name()); !ok || kind != manifests.Manifest {
			continue
		}
		found = append(found, e.Name())
		content, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		r, err := manifests.Parse(e.Name(), content)
		if err != nil || r.Name == "" {
			continue
		}
		return r.Name, r.Ecosystem, nil
	}
	if len(found) == 0 {
		return "", "", fmt.Errorf("no manifest found in %s", dir)
	}
	return "", "", fmt.Errorf("no package name in %v", found)
}
