package discover

import (
	"github.com/git-pkgs/downstream/internal/config"
)

// Reconcile merges discovered candidates with an existing config.
// Manual entries are kept verbatim. Discover entries that are still
// in the candidate set get their comment refreshed but keep any
// ref/subdir/test/skip_baseline overrides the user added. Discover
// entries that fell out are dropped. New candidates not in the
// existing file are appended at the end with the New flag set.
//
// If existing is nil, all candidates are returned as a fresh config.
func Reconcile(existing *config.Config, pkg config.Package, cands []Candidate) *config.Config {
	out := &config.Config{Package: pkg}
	if existing != nil {
		out.Package = existing.Package
	}

	byRepo := make(map[string]*Candidate, len(cands))
	for i := range cands {
		byRepo[cands[i].Repo] = &cands[i]
	}
	used := make(map[string]bool, len(cands))

	if existing != nil {
		for _, d := range existing.Dependents {
			if d.Source == "manual" || d.Source == "" {
				used[d.Repo] = true
				out.Dependents = append(out.Dependents, d)
				continue
			}
			c, ok := byRepo[d.Repo]
			if !ok {
				continue // discover entry that fell out of the candidate set
			}
			used[d.Repo] = true
			d.Comment = c.Comment()
			out.Dependents = append(out.Dependents, d)
		}
	}

	for _, c := range cands {
		if used[c.Repo] {
			continue
		}
		c.New = existing != nil
		out.Dependents = append(out.Dependents, c.Dependent())
	}

	return out
}
