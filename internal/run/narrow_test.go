package run

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestNarrowGoTest(t *testing.T) {
	// The dependent fixture has two packages: the root (imports
	// upstream) and unrelated/ (does not). Only the root should be
	// returned. We need ../upstream resolvable, so seed a workdir
	// like the integration tests do.
	workdir := t.TempDir()
	if err := copyDir(fixturePath(t, "upstream"), filepath.Join(workdir, "upstream")); err != nil {
		t.Fatalf("seed upstream: %v", err)
	}
	depDir := filepath.Join(workdir, "dependent")
	if err := copyDir(fixturePath(t, "dependent"), depDir); err != nil {
		t.Fatalf("copy dependent: %v", err)
	}

	got, err := narrowGoTest(context.Background(), depDir, "example.test/upstream")
	if err != nil {
		t.Fatalf("narrowGoTest: %v", err)
	}
	want := []string{"example.test/dependent"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestNarrowGoTestNoMatch(t *testing.T) {
	workdir := t.TempDir()
	if err := copyDir(fixturePath(t, "upstream"), filepath.Join(workdir, "upstream")); err != nil {
		t.Fatal(err)
	}
	depDir := filepath.Join(workdir, "dependent")
	if err := copyDir(fixturePath(t, "dependent"), depDir); err != nil {
		t.Fatal(err)
	}

	got, err := narrowGoTest(context.Background(), depDir, "example.test/something-else")
	if err != nil {
		t.Fatalf("narrowGoTest: %v", err)
	}
	if got != nil {
		t.Fatalf("got %v, want nil for no match", got)
	}
}

func TestBasePackagePath(t *testing.T) {
	tests := []struct {
		in   goListPackage
		want string
	}{
		{goListPackage{ImportPath: "pkg/foo"}, "pkg/foo"},
		{goListPackage{ImportPath: "pkg/foo.test"}, "pkg/foo"},
		{goListPackage{ImportPath: "pkg/foo [pkg/foo.test]", ForTest: "pkg/foo"}, "pkg/foo"},
		{goListPackage{ImportPath: "pkg/foo_test [pkg/foo.test]", ForTest: "pkg/foo"}, "pkg/foo"},
	}
	for _, tt := range tests {
		if got := basePackagePath(tt.in); got != tt.want {
			t.Errorf("basePackagePath(%+v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestDepsReach(t *testing.T) {
	deps := []string{"fmt", "github.com/spf13/cobra", "github.com/spf13/cobra/doc"}
	if !depsReach(deps, "github.com/spf13/cobra") {
		t.Error("should match exact")
	}
	if !depsReach([]string{"github.com/spf13/cobra/doc"}, "github.com/spf13/cobra") {
		t.Error("should match prefix")
	}
	if depsReach(deps, "github.com/spf13/cobra-extra") {
		t.Error("should not match cobra-extra against cobra")
	}
	if depsReach(deps, "github.com/other/lib") {
		t.Error("should not match unrelated")
	}
}

// TestRunNarrows verifies the integration: Test() should compute
// the narrowed command and record it on the result.
func TestRunNarrows(t *testing.T) {
	result := runFixture(t, "upstream")

	if result.Narrowed != 1 {
		t.Errorf("Narrowed = %d, want 1 (only the root package imports upstream)", result.Narrowed)
	}
	if !reflect.DeepEqual(result.Baseline.Command, []string{"go", "test", "example.test/dependent"}) {
		t.Errorf("baseline command = %v, want narrowed", result.Baseline.Command)
	}
	if strings.Contains(strings.Join(result.Baseline.Command, " "), "unrelated") {
		t.Errorf("unrelated package should be excluded: %v", result.Baseline.Command)
	}
}
