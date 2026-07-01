package run

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

const tidyTimeout = 5 * time.Minute

// TestRun captures the outcome of one `go test ./...` invocation.
// Step 5 in the plan upgrades this to per-test diffing via -json; for
// now it's exit-code plus combined output.
type TestRun struct {
	Command  []string
	Output   string
	ExitCode int
	Duration time.Duration
	Err      error // non-nil only when the process failed to start
}

func (r TestRun) Passed() bool {
	return r.Err == nil && r.ExitCode == 0
}

func runTests(ctx context.Context, dir string, opts Options) TestRun {
	return runIn(ctx, dir, opts.Timeout, strings.Fields(opts.TestCmd)...)
}

func tidy(ctx context.Context, dir string) error {
	r := runIn(ctx, dir, tidyTimeout, "go", "mod", "tidy")
	if r.Err != nil {
		return r.Err
	}
	if r.ExitCode != 0 {
		return &exec.ExitError{Stderr: []byte(r.Output)}
	}
	return nil
}

func runIn(ctx context.Context, dir string, timeout time.Duration, argv ...string) TestRun {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	start := time.Now()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()

	r := TestRun{
		Command:  argv,
		Output:   string(out),
		Duration: time.Since(start),
	}
	if cmd.ProcessState != nil {
		r.ExitCode = cmd.ProcessState.ExitCode()
	} else {
		r.ExitCode = -1
		r.Err = err
	}
	return r
}
