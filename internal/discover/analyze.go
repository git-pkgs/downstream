package discover

import (
	"context"

	"github.com/git-pkgs/dependents"
)

// AnalyzeOptions controls phase-two scoring.
type AnalyzeOptions struct {
	Upstream string // package name the candidates depend on
	Workdir  string // clones go under here; temp dir if empty
	Limit    int    // final number to keep after re-ranking
	Keep     bool   // leave clones for a follow-up downstream test
	Checkout dependents.Checkout
}

// Analyze delegates checkout and source analysis to dependents, then applies
// downstream's requirement that analyzed repositories contain tests and
// references to the upstream package. Checkout failures retain their
// popularity score instead of aborting discovery.
func Analyze(ctx context.Context, candidates []Candidate, opts AnalyzeOptions, log func(string, ...any)) ([]Candidate, error) {
	if log == nil {
		log = func(string, ...any) {}
	}
	if opts.Limit <= 0 {
		opts.Limit = len(candidates)
	}

	shared := make([]dependents.Candidate, len(candidates))
	for i, candidate := range candidates {
		shared[i] = candidate.shared()
	}
	result, err := dependents.Analyze(ctx, shared, dependents.AnalyzeOptions{
		Upstreams: []string{opts.Upstream},
		Workdir:   opts.Workdir,
		Checkout:  opts.Checkout,
		Keep:      opts.Keep,
	})
	if err != nil {
		return nil, err
	}

	failures := make(map[string]error, len(result.Failures))
	for _, failure := range result.Failures {
		failures[failure.Repository] = failure.Err
	}

	filtered := make([]Candidate, 0, len(candidates))
	for i, sharedCandidate := range result.Candidates {
		candidate := candidates[i]
		updateFromShared(&candidate, sharedCandidate)
		if failure := failures[candidate.Repo]; failure != nil {
			log("analyze %s: analysis failed (%v); keeping with phase-one score", candidate.Name, failure)
			filtered = append(filtered, candidate)
			continue
		}

		log("analyze %s: %d test files, %d reference upstream", candidate.Name, candidate.TestFiles, candidate.ImportFiles)
		_, rejected := dependents.Filter([]dependents.Candidate{sharedCandidate}, dependents.FilterOptions{
			RequireTests:   true,
			RequireImports: true,
		})
		if len(rejected) > 0 {
			logAnalysisRejection(log, candidate, rejected[0].Reason, opts.Upstream)
			continue
		}
		filtered = append(filtered, candidate)
	}

	return rankCandidates(filtered, opts.Limit), nil
}

func rankCandidates(candidates []Candidate, limit int) []Candidate {
	shared := make([]dependents.Candidate, len(candidates))
	byRepository := make(map[string]Candidate, len(candidates))
	for i, candidate := range candidates {
		shared[i] = candidate.shared()
		byRepository[candidate.Repo] = candidate
	}
	ranked := dependents.Rank(shared, limit, nil)
	out := make([]Candidate, 0, len(ranked))
	for _, candidate := range ranked {
		out = append(out, byRepository[candidate.Repository])
	}
	return out
}

func logAnalysisRejection(log func(string, ...any), candidate Candidate, reason, upstream string) {
	switch reason {
	case dependents.ReasonNoTests:
		log("drop %s: no tests", candidate.Name)
	case dependents.ReasonNoImports:
		log("drop %s: no files reference %s (stale listing or wrong subpackage?)", candidate.Name, upstream)
	default:
		log("drop %s: %s", candidate.Name, reason)
	}
}
