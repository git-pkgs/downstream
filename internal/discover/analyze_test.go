package discover

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/git-pkgs/dependents"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAnalyzeRanksAndFilters(t *testing.T) {
	trees := map[string]map[string]string{
		"https://x/high": {
			"a.go":      `package a; import "example.test/up"`,
			"b.go":      `package a; import "example.test/up/x"`,
			"c.go":      `package a; import "example.test/up/y"`,
			"a_test.go": `package a`,
			"b_test.go": `package a`,
		},
		"https://x/low": {
			"a.go":      `package a; import "example.test/up"`,
			"a_test.go": `package a`,
		},
		"https://x/notest": {
			"a.go": `package a; import "example.test/up"`,
		},
		"https://x/noimport": {
			"a.go":      `package a; import "fmt"`,
			"a_test.go": `package a`,
		},
	}
	checkout := dependents.CheckoutFunc(func(_ context.Context, repository, destination string) (string, error) {
		writeTree(t, destination, trees[repository])
		return "abc123", nil
	})
	candidates := []Candidate{
		{Name: "low/low", Repo: "https://x/low", DependentRepos: 100000},
		{Name: "high/high", Repo: "https://x/high", DependentRepos: 1},
		{Name: "notest/notest", Repo: "https://x/notest", DependentRepos: 50},
		{Name: "noimport/noimport", Repo: "https://x/noimport", DependentRepos: 50},
	}

	got, err := Analyze(context.Background(), candidates, AnalyzeOptions{
		Upstream: "example.test/up",
		Workdir:  t.TempDir(),
		Limit:    3,
		Checkout: checkout,
	}, nil)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
	if got[0].Name != "high/high" || got[0].ImportFiles != 3 || got[0].TestFiles != 2 || !got[0].Analyzed {
		t.Errorf("rank[0] = %+v", got[0])
	}
	if got[1].Name != "low/low" {
		t.Errorf("rank[1] = %s, want low/low", got[1].Name)
	}
}

func TestAnalyzeFailureKeepsCandidate(t *testing.T) {
	wantErr := errors.New("checkout failed")
	checkout := dependents.CheckoutFunc(func(context.Context, string, string) (string, error) {
		return "", wantErr
	})
	var logs []string
	log := func(format string, args ...any) {
		logs = append(logs, format)
	}

	got, err := Analyze(context.Background(), []Candidate{{
		Name: "x/x", Repo: "https://invalid.test/x", DependentRepos: 10,
	}}, AnalyzeOptions{Workdir: t.TempDir(), Checkout: checkout}, log)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(got) != 1 || got[0].Analyzed {
		t.Fatalf("analysis failure should keep candidate: %+v", got)
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "analysis failed") {
		t.Fatalf("logs = %v", logs)
	}
}

func TestCommentIncludesAnalyzeFields(t *testing.T) {
	candidate := Candidate{
		Name: "x", Repo: "https://x",
		Analyzed: true, ImportFiles: 12, TestFiles: 34,
		DependentRepos: 100, Stars: 5,
	}
	comment := candidate.Comment()
	for _, want := range []string{"12 files reference upstream", "34 test files", "100 dependent repos"} {
		if !strings.Contains(comment, want) {
			t.Errorf("comment missing %q: %s", want, comment)
		}
	}
}

func TestCommentNewMarker(t *testing.T) {
	candidate := Candidate{Name: "x", Repo: "https://x", DependentRepos: 1, New: true}
	if !strings.HasPrefix(candidate.Comment(), "discover (new): ") {
		t.Errorf("comment = %q, want (new) prefix", candidate.Comment())
	}
}
