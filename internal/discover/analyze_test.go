package discover

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestScan(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"a.go":                 `package a; import "example.test/upstream"`,
		"a_test.go":            `package a; import "testing"`,
		"sub/b.go":             `package sub; import "example.test/upstream/sub"`,
		"sub/b_test.go":        `package sub; import _ "example.test/upstream"`,
		"unrelated/c.go":       `package unrelated; import "fmt"`,
		"vendor/x/x.go":        `package x; import "example.test/upstream"`,
		"vendor/x/x_test.go":   `package x`,
		"testdata/y_test.go":   `package y`,
		".git/hooks/z_test.go": `package z`,
		"node_modules/w.go":    `package w; import "example.test/upstream"`,
		"prefix.go":            `package a; import "example.test/upstream-fork"`,
		"broken.go":            `not valid go`,
	})

	tests, imports := scan(dir, "example.test/upstream")
	if tests != 2 {
		t.Errorf("test files = %d, want 2 (a_test.go, sub/b_test.go; vendor/testdata/.git skipped)", tests)
	}
	if imports != 3 {
		t.Errorf("import files = %d, want 3 (a.go, sub/b.go, sub/b_test.go; vendor/node_modules/prefix-mismatch/broken excluded)", imports)
	}
}

func TestAnalyzeRanksAndFilters(t *testing.T) {
	work := t.TempDir()

	// "high" has many imports and tests; "low" has few; "notest" has
	// imports but no tests; "noimport" has tests but doesn't import
	// upstream. Repos are pre-seeded as local dirs so shallowClone
	// reuses them instead of cloning.
	writeTree(t, filepath.Join(work, "high-high"), map[string]string{
		"a.go":      `package a; import "example.test/up"`,
		"b.go":      `package a; import "example.test/up/x"`,
		"c.go":      `package a; import "example.test/up/y"`,
		"a_test.go": `package a`,
		"b_test.go": `package a`,
	})
	writeTree(t, filepath.Join(work, "low-low"), map[string]string{
		"a.go":      `package a; import "example.test/up"`,
		"a_test.go": `package a`,
	})
	writeTree(t, filepath.Join(work, "notest-notest"), map[string]string{
		"a.go": `package a; import "example.test/up"`,
	})
	writeTree(t, filepath.Join(work, "noimport-noimport"), map[string]string{
		"a.go":      `package a; import "fmt"`,
		"a_test.go": `package a`,
	})

	cands := []Candidate{
		{Name: "low/low", Repo: "https://x/low", DependentRepos: 100000},
		{Name: "high/high", Repo: "https://x/high", DependentRepos: 1},
		{Name: "notest/notest", Repo: "https://x/notest", DependentRepos: 50},
		{Name: "noimport/noimport", Repo: "https://x/noimport", DependentRepos: 50},
	}

	got, err := Analyze(context.Background(), cands, AnalyzeOptions{
		Upstream: "example.test/up",
		Workdir:  work,
		Limit:    3,
	}, nil)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("got %d, want 2 (notest and noimport dropped)", len(got))
	}
	if got[0].Name != "high/high" {
		t.Errorf("rank[0] = %s, want high/high (3 import files beats popularity)", got[0].Name)
	}
	if got[0].ImportFiles != 3 || got[0].TestFiles != 2 || !got[0].Analyzed {
		t.Errorf("high = %+v", got[0])
	}
	if got[1].Name != "low/low" {
		t.Errorf("rank[1] = %s, want low/low", got[1].Name)
	}
}

func TestAnalyzeCloneFailureKeepsCandidate(t *testing.T) {
	cands := []Candidate{
		{Name: "x/x", Repo: "https://invalid.test/does/not/exist", DependentRepos: 10},
	}
	got, err := Analyze(context.Background(), cands, AnalyzeOptions{
		Upstream: "example.test/up",
		Workdir:  t.TempDir(),
	}, nil)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(got) != 1 || got[0].Analyzed {
		t.Fatalf("clone failure should keep candidate with Analyzed=false: %+v", got)
	}
}

func TestCommentIncludesAnalyzeFields(t *testing.T) {
	c := Candidate{
		Name: "x", Repo: "https://x",
		Analyzed: true, ImportFiles: 12, TestFiles: 34,
		DependentRepos: 100, Stars: 5,
	}
	cm := c.Comment()
	for _, want := range []string{"12 files import upstream", "34 test files", "100 dependent repos"} {
		if !strings.Contains(cm, want) {
			t.Errorf("comment missing %q: %s", want, cm)
		}
	}
}

func TestCommentNewMarker(t *testing.T) {
	c := Candidate{Name: "x", Repo: "https://x", DependentRepos: 1, New: true}
	if !strings.HasPrefix(c.Comment(), "discover (new): ") {
		t.Errorf("comment = %q, want (new) prefix", c.Comment())
	}
}

func TestScoreImportSurfaceBeatsPopularity(t *testing.T) {
	popular := Candidate{Analyzed: true, ImportFiles: 1, DependentRepos: 1_000_000, Stars: 100_000}
	exercised := Candidate{Analyzed: true, ImportFiles: 5, DependentRepos: 10}
	if exercised.Score() <= popular.Score() {
		t.Errorf("import surface should outrank popularity once analyzed: %d vs %d",
			exercised.Score(), popular.Score())
	}
}
