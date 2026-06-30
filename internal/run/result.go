package run

import (
	"fmt"
	"strings"
	"time"
)

type Status string

const (
	StatusPassed         Status = "passed"
	StatusFailed         Status = "failed"
	StatusBrokenBaseline Status = "broken-baseline"
	StatusError          Status = "error"
)

type Result struct {
	Module        string
	Dependent     string
	DependentPath string
	UpstreamPath  string
	Manager       string
	Narrowed      int // packages the test command was auto-narrowed to; 0 = ./...
	Baseline      TestRun
	Patched       TestRun
}

// Status classifies the run. Only "failed" should turn a CI check
// red: a dependent that was already broken is reported but not held
// against the upstream change.
func (r *Result) Status() Status {
	switch {
	case r.Baseline.Err != nil || r.Patched.Err != nil:
		return StatusError
	case !r.Baseline.Passed():
		return StatusBrokenBaseline
	case !r.Patched.Passed():
		return StatusFailed
	default:
		return StatusPassed
	}
}

func (r *Result) Failed() bool {
	return r.Status() == StatusFailed
}

func (r *Result) Markdown() string {
	var b strings.Builder

	fmt.Fprintf(&b, "## %s\n\n", r.Dependent)
	fmt.Fprintf(&b, "| | baseline | patched |\n")
	fmt.Fprintf(&b, "|---|---|---|\n")
	fmt.Fprintf(&b, "| result | %s | %s |\n", passLabel(r.Baseline), passLabel(r.Patched))
	fmt.Fprintf(&b, "| duration | %s | %s |\n",
		r.Baseline.Duration.Round(durationRound),
		r.Patched.Duration.Round(durationRound))
	if r.Narrowed > 0 {
		fmt.Fprintf(&b, "| packages | %d | %d |\n", r.Narrowed, r.Narrowed)
	}
	fmt.Fprintf(&b, "\n**status:** %s\n", r.Status())

	if r.Status() == StatusBrokenBaseline {
		fmt.Fprintf(&b, "\nBaseline failed before any change was applied; skipping comparison. Pin a green ref with `ref` in `downstream.toml` or narrow the test command.\n")
	}

	if r.Failed() {
		fmt.Fprintf(&b, "\nReplacing `%s` with `%s` introduced new failures.\n", r.Module, r.UpstreamPath)
		fmt.Fprintf(&b, "\n<details><summary>patched test output</summary>\n\n```\n%s```\n</details>\n",
			tail(r.Patched.Output, outputTailLines))
	}

	return b.String()
}

const (
	durationRound   = 100 * time.Millisecond
	outputTailLines = 200
)

func passLabel(r TestRun) string {
	if r.Err != nil {
		return "error"
	}
	if r.Passed() {
		return "pass"
	}
	return "fail"
}

func tail(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}

// Results aggregates one Result per dependent. SetupErr is set when
// Test returned an error before producing a Result (clone failed,
// replace failed, manager not detected); those count as errors in
// the summary but don't fail the overall run.
type Results []ResultEntry

type ResultEntry struct {
	Name     string
	Result   *Result
	SetupErr error
}

func (rs Results) AnyFailed() bool {
	for _, e := range rs {
		if e.Result != nil && e.Result.Failed() {
			return true
		}
	}
	return false
}

func (rs Results) Count(s Status) int {
	n := 0
	for _, e := range rs {
		if e.Status() == s {
			n++
		}
	}
	return n
}

func (e ResultEntry) Status() Status {
	if e.SetupErr != nil || e.Result == nil {
		return StatusError
	}
	return e.Result.Status()
}

// Markdown returns a summary table followed by per-dependent detail
// for anything that didn't pass.
func (rs Results) Markdown() string {
	var b strings.Builder

	fmt.Fprintf(&b, "# downstream\n\n")
	fmt.Fprintf(&b, "| dependent | baseline | patched | status |\n")
	fmt.Fprintf(&b, "|---|---|---|---|\n")
	for _, e := range rs {
		if e.SetupErr != nil {
			fmt.Fprintf(&b, "| %s | - | - | error: %s |\n", e.Name, e.SetupErr)
			continue
		}
		fmt.Fprintf(&b, "| %s | %s %s | %s %s | %s |\n",
			e.Name,
			passLabel(e.Result.Baseline), e.Result.Baseline.Duration.Round(durationRound),
			passLabel(e.Result.Patched), e.Result.Patched.Duration.Round(durationRound),
			e.Status())
	}
	fmt.Fprintf(&b, "\n%d passed, %d failed, %d broken-baseline, %d error\n",
		rs.Count(StatusPassed), rs.Count(StatusFailed), rs.Count(StatusBrokenBaseline), rs.Count(StatusError))

	for _, e := range rs {
		if e.Status() == StatusPassed {
			continue
		}
		b.WriteString("\n")
		if e.SetupErr != nil {
			fmt.Fprintf(&b, "## %s\n\n```\n%s\n```\n", e.Name, e.SetupErr)
			continue
		}
		b.WriteString(e.Result.Markdown())
	}

	return b.String()
}
