// Package discover finds and ranks dependents of a package via the
// ecosyste.ms API. Phase one is API-only: fetch popularity-sorted
// dependents, drop forks/archived/stale repos using the inline
// repo_metadata, dedupe by repository, and rank.
package discover

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/git-pkgs/downstream/internal/config"
)

type Options struct {
	Ecosystem string // go, npm, rubygems, ...
	Package   string // module path or package name
	Limit     int    // final number of dependents to keep
	Pool      int    // candidates to fetch before filtering; defaults to Limit*poolMultiplier
	MaxAge    time.Duration
	Client    *Client
	Stderr    io.Writer
}

const (
	poolMultiplier = 6
	defaultMaxAge  = 2 * 365 * 24 * time.Hour
	defaultLimit   = 5
)

// Candidate is a dependent that survived phase-one filtering, with
// phase-two fields filled by Analyze.
type Candidate struct {
	Name              string
	Repo              string
	Stars             int
	DependentPackages int
	DependentRepos    int
	Downloads         int64
	PushedAt          time.Time
	Language          string

	// Phase two (Analyze)
	TestFiles   int  // *_test.go count
	ImportFiles int  // .go files importing the upstream module
	Analyzed    bool // distinguishes "not analyzed" from "analyzed, zero"
	New         bool // appended by reconcile, not in the existing file

	// Not yet implemented; placeholder so Comment() shape is stable.
	TransitiveReach int // modules in go.sum that also depend on upstream
	CIGreen         bool

	// dropReason is set on candidates filtered out so the progress
	// log can explain why.
	dropReason string
}

// Score ranks candidates. After Analyze, ImportFiles dominates
// (a candidate that actually exercises the upstream beats a
// popular one that barely touches it); before Analyze, falls back
// to popularity.
func (c Candidate) Score() int64 {
	base := c.Downloads
	if base == 0 {
		base = int64(c.DependentRepos)*scoreRepoWeight + int64(c.Stars)
	}
	if !c.Analyzed {
		return base
	}
	return int64(c.ImportFiles)*scoreImportWeight +
		int64(c.TransitiveReach)*scoreReachWeight +
		base/scorePopDamp
}

const (
	scoreRepoWeight   = 10
	scoreImportWeight = 1_000_000
	scoreReachWeight  = 100_000
	scorePopDamp      = 100
)

func (c Candidate) Comment() string {
	parts := []string{}
	if c.Analyzed {
		parts = append(parts, fmt.Sprintf("%d files import upstream", c.ImportFiles))
		parts = append(parts, fmt.Sprintf("%d test files", c.TestFiles))
		if c.TransitiveReach > 0 {
			parts = append(parts, fmt.Sprintf("%d transitive consumers", c.TransitiveReach))
		}
	}
	if c.DependentRepos > 0 {
		parts = append(parts, fmt.Sprintf("%d dependent repos", c.DependentRepos))
	}
	if c.DependentPackages > 0 {
		parts = append(parts, fmt.Sprintf("%d dependent packages", c.DependentPackages))
	}
	if c.Downloads > 0 {
		parts = append(parts, fmt.Sprintf("%d downloads", c.Downloads))
	}
	if c.Stars > 0 {
		parts = append(parts, fmt.Sprintf("%d stars", c.Stars))
	}
	if !c.PushedAt.IsZero() {
		parts = append(parts, "pushed "+c.PushedAt.Format("2006-01-02"))
	}
	if len(parts) == 0 {
		return "discover: no repo metadata"
	}
	prefix := "discover: "
	if c.New {
		prefix = "discover (new): "
	}
	return prefix + strings.Join(parts, ", ")
}

func (c Candidate) Dependent() config.Dependent {
	return config.Dependent{
		Name:    c.Name,
		Repo:    c.Repo,
		Source:  "discover",
		Comment: c.Comment(),
	}
}

// Discover runs phase one and returns up to opts.Limit candidates.
func Discover(ctx context.Context, opts Options) ([]Candidate, error) {
	if opts.Limit <= 0 {
		opts.Limit = defaultLimit
	}
	if opts.Pool <= 0 {
		opts.Pool = opts.Limit * poolMultiplier
	}
	if opts.MaxAge <= 0 {
		opts.MaxAge = defaultMaxAge
	}
	if opts.Client == nil {
		opts.Client = NewClient()
	}
	if opts.Stderr == nil {
		opts.Stderr = io.Discard
	}

	logf(opts, "querying ecosyste.ms for dependents of %s (%s), pool=%d", opts.Package, opts.Ecosystem, opts.Pool)
	pkgs, err := opts.Client.DependentPackages(ctx, opts.Ecosystem, opts.Package, opts.Pool)
	if err != nil {
		return nil, err
	}
	logf(opts, "fetched %d candidates", len(pkgs))
	if len(pkgs) == 0 {
		return nil, fmt.Errorf("no dependents found for %s (%s); the package may not be indexed yet", opts.Package, opts.Ecosystem)
	}

	cands := buildCandidates(pkgs, opts)
	kept, dropped := partition(cands, opts)
	for _, c := range dropped {
		logf(opts, "drop %s: %s", c.Name, c.dropReason)
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].Score() > kept[j].Score() })
	if len(kept) > opts.Limit {
		kept = kept[:opts.Limit]
	}
	logf(opts, "kept %d, dropped %d", len(kept), len(dropped))
	return kept, nil
}

func buildCandidates(pkgs []Package, opts Options) []Candidate {
	seen := make(map[string]bool, len(pkgs))
	out := make([]Candidate, 0, len(pkgs))
	for _, p := range pkgs {
		c := candidateFrom(p, opts)
		if c.Repo == "" {
			c.dropReason = "no repository_url"
		} else if seen[c.Repo] {
			continue // monorepo: same repo hosts several packages
		}
		seen[c.Repo] = true
		out = append(out, c)
	}
	return out
}

func candidateFrom(p Package, opts Options) Candidate {
	c := Candidate{
		Name:              p.Name,
		Repo:              firstNonEmpty(p.RepoMetadata.HTMLURL, p.RepositoryURL),
		Stars:             p.RepoMetadata.StargazersCount,
		DependentPackages: p.DependentPackagesCount,
		DependentRepos:    p.DependentReposCount,
		Downloads:         p.Downloads,
		PushedAt:          p.RepoMetadata.PushedAt,
		Language:          p.RepoMetadata.Language,
	}
	c.dropReason = dropReason(p, opts)
	if c.Repo == opts.upstreamRepo() {
		c.dropReason = "same repository as upstream"
	}
	return c
}

func dropReason(p Package, opts Options) string {
	switch {
	case p.RepoMetadata.Fork:
		return "fork"
	case p.RepoMetadata.Archived:
		return "archived"
	case p.RepoMetadata.SourceName != "":
		return "mirror of " + p.RepoMetadata.SourceName
	case p.Status == "removed" || p.Status == "deprecated":
		return p.Status
	case !p.RepoMetadata.PushedAt.IsZero() && time.Since(p.RepoMetadata.PushedAt) > opts.MaxAge:
		return fmt.Sprintf("stale (last push %s)", p.RepoMetadata.PushedAt.Format("2006-01-02"))
	}
	return ""
}

func partition(cands []Candidate, _ Options) (kept, dropped []Candidate) {
	for _, c := range cands {
		if c.dropReason == "" {
			kept = append(kept, c)
		} else {
			dropped = append(dropped, c)
		}
	}
	return kept, dropped
}

func (o Options) upstreamRepo() string {
	if strings.HasPrefix(o.Package, "github.com/") {
		return "https://" + o.Package
	}
	return ""
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func logf(opts Options, format string, args ...any) {
	_, _ = fmt.Fprintf(opts.Stderr, "discover: "+format+"\n", args...)
}
