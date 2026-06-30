package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func runCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestListPlain(t *testing.T) {
	out, err := runCmd(t, "list", "-c", "testdata/downstream.toml")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2:\n%s", len(lines), out)
	}
	if !strings.HasPrefix(lines[0], "github.com/cli/cli\t") {
		t.Errorf("line 0 = %q", lines[0])
	}
}

func TestListJSON(t *testing.T) {
	out, err := runCmd(t, "list", "-c", "testdata/downstream.toml", "--json")
	if err != nil {
		t.Fatalf("list --json: %v", err)
	}
	var entries []listEntry
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	if entries[0].Name != "github.com/cli/cli" || entries[0].Slug != "cli-cli" || entries[0].Ref != "v2.40.0" {
		t.Errorf("entries[0] = %+v", entries[0])
	}
	if entries[1].Name != "github.com/gohugoio/hugo" || entries[1].Test != "" {
		t.Errorf("entries[1] = %+v", entries[1])
	}
}

func TestListGithubOutput(t *testing.T) {
	out, err := runCmd(t, "list", "-c", "testdata/downstream.toml", "--github-output")
	if err != nil {
		t.Fatalf("list --github-output: %v", err)
	}
	if !strings.HasPrefix(out, "dependents=[") {
		t.Fatalf("output should start with dependents=[, got %q", out)
	}
	payload := strings.TrimPrefix(strings.TrimSpace(out), "dependents=")
	var entries []listEntry
	if err := json.Unmarshal([]byte(payload), &entries); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, payload)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
}

func TestListOnly(t *testing.T) {
	out, err := runCmd(t, "list", "-c", "testdata/downstream.toml", "--json", "--only", "hugo")
	if err != nil {
		t.Fatalf("list --only: %v", err)
	}
	var entries []listEntry
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "github.com/gohugoio/hugo" {
		t.Fatalf("entries = %+v, want only hugo", entries)
	}
}

func TestListOnlyMiss(t *testing.T) {
	_, err := runCmd(t, "list", "-c", "testdata/downstream.toml", "--only", "nonesuch")
	if err == nil || !strings.Contains(err.Error(), "matches no dependent") {
		t.Fatalf("error = %v, want no-match error", err)
	}
}

func TestListMissingConfig(t *testing.T) {
	_, err := runCmd(t, "list", "-c", "testdata/nonexistent.toml")
	if err == nil {
		t.Fatal("want error for missing config")
	}
}
