package discover

import (
	"strings"
	"testing"

	"github.com/git-pkgs/downstream/internal/config"
)

func TestReconcileFresh(t *testing.T) {
	cands := []Candidate{
		{Name: "a", Repo: "https://a", DependentRepos: 10},
		{Name: "b", Repo: "https://b", DependentRepos: 5},
	}
	got := Reconcile(nil, config.Package{Name: "up", Ecosystem: "go"}, cands)

	if got.Package.Name != "up" {
		t.Errorf("package = %+v", got.Package)
	}
	if len(got.Dependents) != 2 {
		t.Fatalf("dependents = %d, want 2", len(got.Dependents))
	}
	if got.Dependents[0].Source != "discover" {
		t.Errorf("source = %q, want discover", got.Dependents[0].Source)
	}
	if strings.Contains(got.Dependents[0].Comment, "(new)") {
		t.Errorf("fresh config should not mark entries as new: %q", got.Dependents[0].Comment)
	}
}

func TestReconcileMerge(t *testing.T) {
	existing := &config.Config{
		Package: config.Package{Name: "up", Ecosystem: "go", Build: "make"},
		Dependents: []config.Dependent{
			{Name: "manual1", Repo: "https://m1", Source: "manual", Test: "go test ./x"},
			{Name: "kept", Repo: "https://kept", Source: "discover", Ref: "v1.0", Test: "custom"},
			{Name: "dropped", Repo: "https://dropped", Source: "discover"},
			{Name: "untagged", Repo: "https://untagged"},
		},
	}
	cands := []Candidate{
		{Name: "kept", Repo: "https://kept", Analyzed: true, ImportFiles: 7, TestFiles: 3},
		{Name: "newone", Repo: "https://newone", Analyzed: true, ImportFiles: 2, TestFiles: 1},
	}

	got := Reconcile(existing, config.Package{Name: "up", Ecosystem: "go"}, cands)

	if got.Package.Build != "make" {
		t.Errorf("existing package fields should be preserved: %+v", got.Package)
	}

	names := make([]string, len(got.Dependents))
	for i, d := range got.Dependents {
		names[i] = d.Name
	}
	want := []string{"manual1", "kept", "untagged", "newone"}
	if !equalSlices(names, want) {
		t.Fatalf("dependents = %v, want %v", names, want)
	}

	for _, d := range got.Dependents {
		switch d.Name {
		case "manual1":
			if d.Test != "go test ./x" || d.Source != "manual" {
				t.Errorf("manual entry should be untouched: %+v", d)
			}
		case "kept":
			if d.Ref != "v1.0" || d.Test != "custom" || d.Source != "discover" {
				t.Errorf("kept entry should preserve overrides: %+v", d)
			}
			if !strings.Contains(d.Comment, "7 files reference upstream") {
				t.Errorf("kept entry should be rescored: %q", d.Comment)
			}
		case "untagged":
			if d.Source != "" {
				t.Errorf("untagged entry should be treated as manual: %+v", d)
			}
		case "newone":
			if d.Source != "discover" {
				t.Errorf("new entry source = %q", d.Source)
			}
			if !strings.Contains(d.Comment, "(new)") {
				t.Errorf("new entry should be marked: %q", d.Comment)
			}
		case "dropped":
			t.Errorf("dropped entry should not appear: %+v", d)
		}
	}
}

func TestReconcileNoCollisionWithManual(t *testing.T) {
	existing := &config.Config{
		Package: config.Package{Name: "up"},
		Dependents: []config.Dependent{
			{Name: "x", Repo: "https://x", Source: "manual"},
		},
	}
	cands := []Candidate{
		{Name: "x", Repo: "https://x", DependentRepos: 10},
	}

	got := Reconcile(existing, config.Package{Name: "up"}, cands)
	if len(got.Dependents) != 1 || got.Dependents[0].Source != "manual" {
		t.Fatalf("manual entry should win and not be duplicated: %+v", got.Dependents)
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
