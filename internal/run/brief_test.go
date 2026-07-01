package run

import (
	"context"
	"io"
	"path/filepath"
	"testing"

	"github.com/git-pkgs/brief"
)

func TestExtractBriefTest(t *testing.T) {
	kbCmd := func(run string, src brief.Source) *brief.Command {
		return &brief.Command{Run: run, Source: src}
	}

	tests := []struct {
		name        string
		report      *brief.Report
		wantCmd     string
		wantProject bool
	}{
		{
			name: "project script",
			report: &brief.Report{Tools: map[string][]brief.Detection{
				"test": {{Command: kbCmd("make test", brief.SourceProjectScript)}},
			}},
			wantCmd:     "make test",
			wantProject: true,
		},
		{
			name: "knowledge base default",
			report: &brief.Report{Tools: map[string][]brief.Detection{
				"test": {{Command: kbCmd("go test ./...", brief.SourceKnowledgeBase)}},
			}},
			wantCmd:     "go test ./...",
			wantProject: false,
		},
		{
			name: "scripts fallback",
			report: &brief.Report{
				Tools:   map[string][]brief.Detection{},
				Scripts: []brief.Script{{Name: "build", Run: "make"}, {Name: "test", Run: "npm test"}},
			},
			wantCmd:     "npm test",
			wantProject: true,
		},
		{
			name:   "nothing",
			report: &brief.Report{Tools: map[string][]brief.Detection{}},
		},
		{
			name: "first non-empty test entry wins",
			report: &brief.Report{Tools: map[string][]brief.Detection{
				"test": {
					{Command: nil},
					{Command: kbCmd("", brief.SourceKnowledgeBase)},
					{Command: kbCmd("cargo test", brief.SourceKnowledgeBase)},
				},
			}},
			wantCmd:     "cargo test",
			wantProject: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, project := extractBriefTest(tt.report)
			if cmd != tt.wantCmd || project != tt.wantProject {
				t.Errorf("got (%q, %v), want (%q, %v)", cmd, project, tt.wantCmd, tt.wantProject)
			}
		})
	}
}

// TestBriefDetectOnFixture exercises the full library path against
// the local Go fixture. It should always find something now that the
// knowledge base is embedded.
func TestBriefDetectOnFixture(t *testing.T) {
	cmd, project := briefDetect(fixturePath(t, "dependent"))
	if cmd == "" {
		t.Fatalf("brief returned no test command for a Go module")
	}
	if project {
		t.Errorf("fixture has no project script; got fromProject=true (cmd=%q)", cmd)
	}
}

func TestBriefDetectCargoFixture(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "Cargo.toml"), "[package]\nname = \"x\"\nversion = \"0.1.0\"\n")
	mustMkdir(t, filepath.Join(dir, "src"))
	mustWrite(t, filepath.Join(dir, "src", "lib.rs"), "pub fn f() {}\n")

	cmd, project := briefDetect(dir)
	if cmd != "cargo test" {
		t.Errorf("cmd = %q, want cargo test", cmd)
	}
	if project {
		t.Errorf("no project script present; got fromProject=true")
	}
}

func TestResolveTestCommandPrecedence(t *testing.T) {
	// The fixture imports example.test/upstream, so auto-narrow can
	// find a package list; brief on it returns a knowledge_base
	// default which should yield to narrowing.
	workdir := t.TempDir()
	if err := copyDir(fixturePath(t, "upstream"), filepath.Join(workdir, "upstream")); err != nil {
		t.Fatal(err)
	}
	dep := filepath.Join(workdir, "dependent")
	if err := copyDir(fixturePath(t, "dependent"), dep); err != nil {
		t.Fatal(err)
	}

	base := Options{Module: "example.test/upstream", Stderr: io.Discard}

	// User override always wins.
	cmd, n := resolveTestCommand(context.Background(), dep, "gomod", with(base, "make ci"))
	if cmd != "make ci" || n != 0 {
		t.Errorf("override: got (%q, %d)", cmd, n)
	}

	// No override, gomod: should auto-narrow to the one package.
	cmd, n = resolveTestCommand(context.Background(), dep, "gomod", base)
	if cmd != "go test example.test/dependent" || n != 1 {
		t.Errorf("narrow: got (%q, %d)", cmd, n)
	}

	// Project-script via brief should bypass narrowing. Add a
	// Makefile so brief reports source=project_script.
	mustWrite(t, filepath.Join(dep, "Makefile"), "test:\n\tgo test -race ./...\n")
	cmd, n = resolveTestCommand(context.Background(), dep, "gomod", base)
	if cmd != "make test" || n != 0 {
		t.Errorf("project-script: got (%q, %d), want (make test, 0)", cmd, n)
	}
}

func with(o Options, testCmd string) Options {
	o.TestCmd = testCmd
	return o
}
