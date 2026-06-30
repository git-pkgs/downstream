package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/git-pkgs/downstream/internal/config"
	"github.com/spf13/cobra"
)

type listEntry struct {
	Name   string `json:"name"`
	Repo   string `json:"repo"`
	Ref    string `json:"ref,omitempty"`
	Subdir string `json:"subdir,omitempty"`
	Test   string `json:"test,omitempty"`
	Slug   string `json:"slug"`
}

func addListCmd(parent *cobra.Command) {
	var (
		configPath string
		only       []string
		asJSON     bool
		ghOutput   bool
	)

	c := &cobra.Command{
		Use:   "list",
		Short: "List dependents from downstream.toml",
		Long: `Reads downstream.toml and prints the configured dependents.

With --json, prints a JSON array suitable for a GitHub Actions matrix.
With --github-output, wraps it as "dependents=<json>" for $GITHUB_OUTPUT.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			deps, err := cfg.Filter(only)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()

			if !asJSON && !ghOutput {
				for _, d := range deps {
					_, _ = fmt.Fprintf(out, "%s\t%s\n", d.Name, d.Repo)
				}
				return nil
			}

			entries := make([]listEntry, len(deps))
			for i, d := range deps {
				entries[i] = listEntry{
					Name:   d.Name,
					Repo:   d.Repo,
					Ref:    d.Ref,
					Subdir: d.Subdir,
					Test:   d.Test,
					Slug:   d.Slug(),
				}
			}
			b, err := json.Marshal(entries)
			if err != nil {
				return err
			}
			if ghOutput {
				_, _ = fmt.Fprintf(out, "dependents=%s\n", b)
			} else {
				_, _ = fmt.Fprintf(out, "%s\n", b)
			}
			return nil
		},
	}

	c.Flags().StringVarP(&configPath, "config", "c", config.DefaultPath, "Path to downstream.toml")
	c.Flags().StringSliceVar(&only, "only", nil, "Filter dependents by name, slug, glob, or substring")
	c.Flags().BoolVar(&asJSON, "json", false, "Output JSON array")
	c.Flags().BoolVar(&ghOutput, "github-output", false, "Output as dependents=<json> for $GITHUB_OUTPUT")

	parent.AddCommand(c)
}
