// Package discover adapts downstream's package-specific ecosyste.ms lookup
// and configuration format to github.com/git-pkgs/dependents.
package discover

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/git-pkgs/dependents"
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

// Candidate contains the downstream-specific presentation fields for a
// repository candidate. Selection, ranking, and analysis are delegated to the
// dependents package.
type Candidate struct {
	Name              string
	Repo              string
	Stars             int
	DependentPackages int
	DependentRepos    int
	Downloads         int64
	PushedAt          time.Time
	Language          string

	TestFiles   int
	ImportFiles int
	Analyzed    bool
	New         bool
}

func (c Candidate) shared() dependents.Candidate {
	return dependents.Candidate{
		Repository: c.Repo,
		Packages: []dependents.Package{{
			Name:           c.Name,
			Downloads:      c.Downloads,
			DependentRepos: c.DependentRepos,
		}},
		RepositoryMetadata: dependents.RepositoryMetadata{
			PushedAt:        c.PushedAt,
			StargazersCount: c.Stars,
			Language:        c.Language,
		},
		Downloads:      c.Downloads,
		DependentRepos: c.DependentRepos,
		Analysis: dependents.Analysis{
			TestFiles:   c.TestFiles,
			ImportFiles: c.ImportFiles,
		},
		Analyzed: c.Analyzed,
	}
}

func updateFromShared(candidate *Candidate, shared dependents.Candidate) {
	candidate.Repo = shared.Repository
	candidate.Stars = shared.RepositoryMetadata.StargazersCount
	candidate.DependentRepos = shared.DependentRepos
	candidate.Downloads = shared.Downloads
	candidate.PushedAt = shared.RepositoryMetadata.PushedAt
	candidate.Language = shared.RepositoryMetadata.Language
	candidate.TestFiles = shared.Analysis.TestFiles
	candidate.ImportFiles = shared.Analysis.ImportFiles
	candidate.Analyzed = shared.Analyzed
	if candidate.Name == "" && len(shared.Packages) > 0 {
		candidate.Name = shared.Packages[0].Name
	}
}

func (c Candidate) Comment() string {
	parts := []string{}
	if c.Analyzed {
		parts = append(parts, fmt.Sprintf("%d files reference upstream", c.ImportFiles))
		parts = append(parts, fmt.Sprintf("%d test files", c.TestFiles))
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

// Discover fetches package dependents, then uses the dependents package to
// combine repositories, apply downstream's repository policy, and rank the
// result.
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
	packages, err := opts.Client.DependentPackages(ctx, opts.Ecosystem, opts.Package, opts.Pool)
	if err != nil {
		return nil, err
	}
	logf(opts, "fetched %d candidates", len(packages))
	if len(packages) == 0 {
		return nil, fmt.Errorf("no dependents found for %s (%s); the package may not be indexed yet", opts.Package, opts.Ecosystem)
	}

	group, details, dropped := buildGroup(packages, opts)
	shared := dependents.Build([]dependents.Group{group})

	if upstream := opts.upstreamRepo(); upstream != "" {
		withoutUpstream := make([]dependents.Candidate, 0, len(shared))
		for _, candidate := range shared {
			if len(dependents.ExcludeRepositories([]dependents.Candidate{candidate}, upstream)) == 0 {
				logf(opts, "drop %s: same repository as upstream", candidateName(candidate))
				dropped++
				continue
			}
			withoutUpstream = append(withoutUpstream, candidate)
		}
		shared = withoutUpstream
	}

	shared, rejected := dependents.Filter(shared, dependents.FilterOptions{
		ExcludeForks:    true,
		ExcludeArchived: true,
		ExcludeMirrors:  true,
		MaxAge:          opts.MaxAge,
	})
	for _, rejection := range rejected {
		logf(opts, "drop %s: %s", candidateName(rejection.Candidate), rejectionMessage(rejection))
	}
	dropped += len(rejected)

	shared = dependents.Rank(shared, opts.Limit, nil)
	candidates := make([]Candidate, 0, len(shared))
	for _, candidate := range shared {
		detail := details[candidate.Repository]
		converted := Candidate{
			Name:              detail.name,
			DependentPackages: detail.dependentPackages,
		}
		updateFromShared(&converted, candidate)
		candidates = append(candidates, converted)
	}

	logf(opts, "kept %d, dropped %d", len(candidates), dropped)
	return candidates, nil
}

type candidateDetail struct {
	name              string
	dependentPackages int
}

func buildGroup(packages []Package, opts Options) (dependents.Group, map[string]candidateDetail, int) {
	group := dependents.Group{
		Upstream: dependents.PackageRef{Name: opts.Package, Ecosystem: opts.Ecosystem},
	}
	details := make(map[string]candidateDetail, len(packages))
	dropped := 0
	for _, pkg := range packages {
		repository := firstNonEmpty(pkg.RepoMetadata.HTMLURL, pkg.RepositoryURL)
		if repository == "" {
			logf(opts, "drop %s: no repository_url", pkg.Name)
			dropped++
			continue
		}
		if pkg.Status == "removed" || pkg.Status == "deprecated" {
			logf(opts, "drop %s: %s", pkg.Name, pkg.Status)
			dropped++
			continue
		}

		detail := details[repository]
		if detail.name == "" {
			detail.name = pkg.Name
		}
		detail.dependentPackages = max(detail.dependentPackages, pkg.DependentPackagesCount)
		details[repository] = detail

		group.Dependents = append(group.Dependents, dependents.Dependent{
			Package: dependents.Package{
				Name:           pkg.Name,
				Ecosystem:      pkg.Ecosystem,
				LatestVersion:  pkg.LatestRelease,
				Downloads:      pkg.Downloads,
				DependentRepos: pkg.DependentReposCount,
			},
			Repository: repository,
			RepositoryMetadata: dependents.RepositoryMetadata{
				Fork:            pkg.RepoMetadata.Fork,
				Archived:        pkg.RepoMetadata.Archived,
				MirrorURL:       pkg.RepoMetadata.MirrorURL,
				SourceName:      pkg.RepoMetadata.SourceName,
				PushedAt:        pkg.RepoMetadata.PushedAt,
				StargazersCount: pkg.RepoMetadata.StargazersCount,
				Language:        pkg.RepoMetadata.Language,
			},
		})
	}
	return group, details, dropped
}

func candidateName(candidate dependents.Candidate) string {
	if len(candidate.Packages) > 0 && candidate.Packages[0].Name != "" {
		return candidate.Packages[0].Name
	}
	return candidate.Repository
}

func rejectionMessage(rejection dependents.Rejection) string {
	if rejection.Reason == dependents.ReasonStale && !rejection.Candidate.RepositoryMetadata.PushedAt.IsZero() {
		return fmt.Sprintf("stale (last push %s)", rejection.Candidate.RepositoryMetadata.PushedAt.Format("2006-01-02"))
	}
	if rejection.Reason == dependents.ReasonMirror && rejection.Candidate.RepositoryMetadata.SourceName != "" {
		return "mirror of " + rejection.Candidate.RepositoryMetadata.SourceName
	}
	return rejection.Reason
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
