package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteRoundTrip(t *testing.T) {
	in := &Config{
		Package: Package{Name: "github.com/spf13/cobra", Ecosystem: "go"},
		Dependents: []Dependent{
			{
				Name:    "github.com/cli/cli",
				Repo:    "https://github.com/cli/cli",
				Ref:     "v2.40.0",
				Test:    "go test ./pkg/...",
				Source:  "discover",
				Comment: "discover: 5000 deps, 40000 stars",
			},
			{
				Name:         "github.com/gohugoio/hugo",
				Repo:         "https://github.com/gohugoio/hugo",
				Subdir:       ".",
				SkipBaseline: true,
				Source:       "manual",
			},
		},
	}

	path := filepath.Join(t.TempDir(), "downstream.toml")
	if err := Write(path, in); err != nil {
		t.Fatalf("Write: %v", err)
	}

	out, err := Load(path)
	if err != nil {
		t.Fatalf("Load round-trip: %v", err)
	}
	if out.Package != in.Package {
		t.Errorf("package = %+v, want %+v", out.Package, in.Package)
	}
	if len(out.Dependents) != 2 {
		t.Fatalf("dependents = %d, want 2", len(out.Dependents))
	}
	for i := range out.Dependents {
		want := in.Dependents[i]
		want.Comment = "" // not round-tripped
		if out.Dependents[i] != want {
			t.Errorf("dependents[%d] = %+v, want %+v", i, out.Dependents[i], want)
		}
	}
}

func TestWriteEmitsComment(t *testing.T) {
	cfg := &Config{
		Package: Package{Name: "x"},
		Dependents: []Dependent{
			{Name: "a", Repo: "https://a", Comment: "line one\nline two"},
		},
	}
	var b strings.Builder
	if _, err := WriteTo(&b, cfg); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	want := "# line one\n# line two\n[[dependents]]"
	if !strings.Contains(got, want) {
		t.Errorf("output missing comment block:\n%s", got)
	}
}
