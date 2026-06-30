package run

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
)

// narrowGoTest returns the dependent's packages whose own code or
// tests reach upstreamModule, so the test command can be limited to
// those instead of ./... . Computed before the replace step so a
// build break in the patched upstream doesn't stop go list.
//
// Returns (nil, nil) when nothing matches; caller falls back to ./...
func narrowGoTest(ctx context.Context, dir, upstreamModule string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "go", "list", "-test", "-json", "./...")
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	depModule, err := readModulePath(dir)
	if err != nil {
		return nil, err
	}

	matched := map[string]bool{}
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p goListPackage
		if err := dec.Decode(&p); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("decoding go list output: %w", err)
		}
		base := basePackagePath(p)
		if !inModule(base, depModule) {
			continue
		}
		if !depsReach(p.Deps, upstreamModule) {
			continue
		}
		matched[base] = true
	}

	if len(matched) == 0 {
		return nil, nil
	}
	pkgs := make([]string, 0, len(matched))
	for p := range matched {
		pkgs = append(pkgs, p)
	}
	sort.Strings(pkgs)
	return pkgs, nil
}

type goListPackage struct {
	ImportPath string   `json:"ImportPath"`
	ForTest    string   `json:"ForTest"`
	Deps       []string `json:"Deps"`
}

// basePackagePath maps the various -test output forms back to the
// package path that "go test <path>" accepts:
//
//	pkg/foo                       -> pkg/foo
//	pkg/foo.test                  -> pkg/foo
//	pkg/foo [pkg/foo.test]        -> pkg/foo  (ForTest = pkg/foo)
//	pkg/foo_test [pkg/foo.test]   -> pkg/foo  (ForTest = pkg/foo)
func basePackagePath(p goListPackage) string {
	if p.ForTest != "" {
		return p.ForTest
	}
	return strings.TrimSuffix(p.ImportPath, ".test")
}

func inModule(pkg, module string) bool {
	return pkg == module || strings.HasPrefix(pkg, module+"/")
}

func depsReach(deps []string, upstream string) bool {
	for _, d := range deps {
		if d == upstream || strings.HasPrefix(d, upstream+"/") {
			return true
		}
	}
	return false
}

func readModulePath(dir string) (string, error) {
	cmd := exec.Command("go", "list", "-m")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("go list -m: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
