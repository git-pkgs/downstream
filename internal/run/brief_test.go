package run

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestBriefOutputParse(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantCmd     string
		wantProject bool
	}{
		{
			name:        "project script",
			body:        `{"tools":{"test":[{"name":"go test","command":{"run":"make test","source":"project_script"}}]}}`,
			wantCmd:     "make test",
			wantProject: true,
		},
		{
			name:        "knowledge base default",
			body:        `{"tools":{"test":[{"name":"go test","command":{"run":"go test ./...","source":"knowledge_base"}}]}}`,
			wantCmd:     "go test ./...",
			wantProject: false,
		},
		{
			name:        "scripts fallback",
			body:        `{"tools":{},"scripts":[{"name":"build","run":"make"},{"name":"test","run":"npm test"}]}`,
			wantCmd:     "npm test",
			wantProject: true,
		},
		{
			name:    "nothing",
			body:    `{"tools":{},"scripts":[]}`,
			wantCmd: "",
		},
		{
			name:        "first non-empty test entry wins",
			body:        `{"tools":{"test":[{"command":{}},{"command":{"run":"cargo test","source":"knowledge_base"}}]}}`,
			wantCmd:     "cargo test",
			wantProject: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b briefOutput
			if err := json.Unmarshal([]byte(tt.body), &b); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			cmd, project := extractBriefTest(b)
			if cmd != tt.wantCmd || project != tt.wantProject {
				t.Errorf("got (%q, %v), want (%q, %v)", cmd, project, tt.wantCmd, tt.wantProject)
			}
		})
	}
}

func TestBriefDetectOnFixture(t *testing.T) {
	if _, err := exec.LookPath("brief"); err != nil {
		t.Skip("brief not on PATH")
	}
	// The dependent fixture is a plain Go module with no Makefile,
	// so brief should report a knowledge_base default, not a
	// project script.
	cmd, project := briefDetect(context.Background(), fixturePath(t, "dependent"))
	if cmd == "" {
		t.Fatalf("brief returned no test command")
	}
	if project {
		t.Errorf("fixture has no project script; got fromProject=true (cmd=%q)", cmd)
	}
}

func TestBriefDetectMissingBinary(t *testing.T) {
	t.Setenv("PATH", "")
	cmd, project := briefDetect(context.Background(), ".")
	if cmd != "" || project {
		t.Errorf("with no PATH, want empty result; got (%q, %v)", cmd, project)
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
	if _, err := exec.LookPath("brief"); err == nil {
		cmd, n = resolveTestCommand(context.Background(), dep, "gomod", base)
		if cmd != "make test" || n != 0 {
			t.Errorf("project-script: got (%q, %d), want (make test, 0)", cmd, n)
		}
	}
	_ = os.Remove(filepath.Join(dep, "Makefile"))

	// Non-Go manager, no brief result, falls back to per-manager default.
	t.Setenv("PATH", "")
	cmd, n = resolveTestCommand(context.Background(), dep, "cargo", base)
	if cmd != "cargo test" || n != 0 {
		t.Errorf("fallback: got (%q, %d)", cmd, n)
	}
}

func with(o Options, testCmd string) Options {
	o.TestCmd = testCmd
	return o
}
