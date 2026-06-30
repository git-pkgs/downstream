package run

import (
	"errors"
	"strings"
	"testing"
)

func TestResultStatus(t *testing.T) {
	pass := TestRun{ExitCode: 0}
	fail := TestRun{ExitCode: 1}
	errRun := TestRun{Err: errors.New("exec: not found"), ExitCode: -1}

	tests := []struct {
		name     string
		baseline TestRun
		patched  TestRun
		want     Status
	}{
		{"both pass", pass, pass, StatusPassed},
		{"patched fails", pass, fail, StatusFailed},
		{"baseline fails", fail, fail, StatusBrokenBaseline},
		{"baseline fails patched passes", fail, pass, StatusBrokenBaseline},
		{"baseline error", errRun, pass, StatusError},
		{"patched error", pass, errRun, StatusError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Result{Baseline: tt.baseline, Patched: tt.patched}
			if got := r.Status(); got != tt.want {
				t.Errorf("Status() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestResultMarkdownPassed(t *testing.T) {
	r := &Result{
		Module:    "example.test/lib",
		Dependent: "https://github.com/example/dep",
		Baseline:  TestRun{ExitCode: 0},
		Patched:   TestRun{ExitCode: 0},
	}
	md := r.Markdown()
	if !strings.Contains(md, "**status:** passed") {
		t.Errorf("missing passed status:\n%s", md)
	}
	if strings.Contains(md, "introduced new failures") {
		t.Errorf("should not mention failures when passed:\n%s", md)
	}
}

func TestResultMarkdownBrokenBaseline(t *testing.T) {
	r := &Result{
		Dependent: "dep",
		Baseline:  TestRun{ExitCode: 1},
		Patched:   TestRun{ExitCode: 1},
	}
	md := r.Markdown()
	if !strings.Contains(md, "**status:** broken-baseline") {
		t.Errorf("missing broken-baseline status:\n%s", md)
	}
	if !strings.Contains(md, "Baseline failed before any change") {
		t.Errorf("missing baseline note:\n%s", md)
	}
	if r.Failed() {
		t.Errorf("Failed() should be false for broken baseline")
	}
}

func TestTail(t *testing.T) {
	in := "a\nb\nc\nd\ne"
	if got := tail(in, 3); got != "c\nd\ne" {
		t.Errorf("tail = %q", got)
	}
	if got := tail(in, 10); got != in {
		t.Errorf("tail with n>len should return input, got %q", got)
	}
}

func TestResultsAggregate(t *testing.T) {
	rs := Results{
		{Name: "a", Result: &Result{Dependent: "a", Baseline: TestRun{ExitCode: 0}, Patched: TestRun{ExitCode: 0}}},
		{Name: "b", Result: &Result{Dependent: "b", Baseline: TestRun{ExitCode: 0}, Patched: TestRun{ExitCode: 1, Output: "FAIL\n"}}},
		{Name: "c", Result: &Result{Dependent: "c", Baseline: TestRun{ExitCode: 1}, Patched: TestRun{ExitCode: 1}}},
		{Name: "d", SetupErr: errors.New("clone failed")},
	}

	if !rs.AnyFailed() {
		t.Error("AnyFailed should be true")
	}
	if rs.Count(StatusPassed) != 1 {
		t.Errorf("passed = %d, want 1", rs.Count(StatusPassed))
	}
	if rs.Count(StatusFailed) != 1 {
		t.Errorf("failed = %d, want 1", rs.Count(StatusFailed))
	}
	if rs.Count(StatusBrokenBaseline) != 1 {
		t.Errorf("broken-baseline = %d, want 1", rs.Count(StatusBrokenBaseline))
	}
	if rs.Count(StatusError) != 1 {
		t.Errorf("error = %d, want 1", rs.Count(StatusError))
	}

	md := rs.Markdown()
	for _, want := range []string{
		"| a |", "| b |", "| c |", "| d |",
		"1 passed, 1 failed, 1 broken-baseline, 1 error",
		"## b", "## c", "## d", "clone failed",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "## a") {
		t.Errorf("markdown should not detail passed dependent a:\n%s", md)
	}
}

func TestResultsNoFailures(t *testing.T) {
	rs := Results{
		{Name: "a", Result: &Result{Baseline: TestRun{ExitCode: 0}, Patched: TestRun{ExitCode: 0}}},
		{Name: "b", SetupErr: errors.New("clone failed")},
	}
	if rs.AnyFailed() {
		t.Error("setup errors should not count as failed")
	}
}
