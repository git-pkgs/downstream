package discover

import (
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// AnalyzeOptions controls phase-two scoring.
type AnalyzeOptions struct {
	Upstream string // module path the candidates depend on
	Workdir  string // clones go under here; temp dir if empty
	Limit    int    // final number to keep after re-ranking
	Keep     bool   // leave clones for a follow-up downstream test
}

// Analyze shallow-clones each candidate, counts *_test.go files and
// .go files that import the upstream module, drops candidates with
// no tests, re-ranks, and returns the top Limit. Clone failures and
// parse errors demote the candidate (Analyzed stays false) rather
// than aborting the run.
func Analyze(ctx context.Context, cands []Candidate, opts AnalyzeOptions, log func(string, ...any)) ([]Candidate, error) {
	if log == nil {
		log = func(string, ...any) {}
	}
	if opts.Limit <= 0 {
		opts.Limit = len(cands)
	}

	workdir := opts.Workdir
	if workdir == "" {
		dir, err := os.MkdirTemp("", "downstream-analyze-")
		if err != nil {
			return nil, err
		}
		workdir = dir
		if !opts.Keep {
			defer func() { _ = os.RemoveAll(dir) }()
		}
	} else if err := os.MkdirAll(workdir, analyzeDirPerm); err != nil {
		return nil, err
	}

	out := make([]Candidate, 0, len(cands))
	for _, c := range cands {
		dest := filepath.Join(workdir, slug(c))
		if err := shallowClone(ctx, c.Repo, dest); err != nil {
			log("analyze %s: clone failed (%v); keeping with phase-one score", c.Name, err)
			out = append(out, c)
			continue
		}
		c.TestFiles, c.ImportFiles = scan(dest, opts.Upstream)
		c.Analyzed = true
		log("analyze %s: %d test files, %d import upstream", c.Name, c.TestFiles, c.ImportFiles)
		if c.TestFiles == 0 {
			log("drop %s: no tests", c.Name)
			continue
		}
		if c.ImportFiles == 0 {
			log("drop %s: no files import %s (wrong subpackage or module path?)", c.Name, opts.Upstream)
			continue
		}
		out = append(out, c)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Score() > out[j].Score() })
	if len(out) > opts.Limit {
		out = out[:opts.Limit]
	}
	return out, nil
}

const analyzeDirPerm os.FileMode = 0o755

func shallowClone(ctx context.Context, url, dest string) error {
	if fi, err := os.Stat(dest); err == nil && fi.IsDir() {
		return nil // reuse a prior clone
	}
	cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", "--", url, dest)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git clone %s: %w: %s", url, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// scan walks dir counting test files and files that import any
// package under the upstream module path. Vendored, testdata, and
// hidden directories are skipped. Imports are read with
// go/parser ImportsOnly so no module resolution is needed.
func scan(dir, upstream string) (testFiles, importFiles int) {
	fset := token.NewFileSet()
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // best effort
		}
		if d.IsDir() {
			name := d.Name()
			if name == "vendor" || name == "testdata" || name == "node_modules" ||
				(strings.HasPrefix(name, ".") && name != ".") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			testFiles++
		}
		if fileImports(fset, path, upstream) {
			importFiles++
		}
		return nil
	})
	return testFiles, importFiles
}

func fileImports(fset *token.FileSet, path, upstream string) bool {
	f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
	if err != nil {
		return false
	}
	for _, imp := range f.Imports {
		p := strings.Trim(imp.Path.Value, `"`)
		if p == upstream || strings.HasPrefix(p, upstream+"/") {
			return true
		}
	}
	return false
}

func slug(c Candidate) string {
	d := c.Dependent()
	return d.Slug()
}
