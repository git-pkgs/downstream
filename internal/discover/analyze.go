package discover

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/git-pkgs/brief"
	"github.com/git-pkgs/brief/kb"
)

// AnalyzeOptions controls phase-two scoring.
type AnalyzeOptions struct {
	Upstream string // package name the candidates depend on
	Workdir  string // clones go under here; temp dir if empty
	Limit    int    // final number to keep after re-ranking
	Keep     bool   // leave clones for a follow-up downstream test
}

// Analyze shallow-clones each candidate, counts test files and source
// files that reference the upstream package, drops candidates with no
// tests or no references, re-ranks, and returns the top Limit. Clone
// failures demote the candidate (Analyzed stays false) rather than
// aborting the run.
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
		log("analyze %s: %d test files, %d reference upstream", c.Name, c.TestFiles, c.ImportFiles)
		if c.TestFiles == 0 {
			log("drop %s: no tests", c.Name)
			continue
		}
		if c.ImportFiles == 0 {
			log("drop %s: no files reference %s (stale listing or wrong subpackage?)", c.Name, opts.Upstream)
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

const (
	analyzeDirPerm os.FileMode = 0o755
	maxScanSize                = 256 << 10 // skip files larger than this for content matching
)

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

// testDirs returns the set of directory names that conventionally
// hold tests, sourced from brief's knowledge base.
var testDirs = sync.OnceValue(func() map[string]bool {
	dirs := map[string]bool{}
	if k, err := kb.Load(brief.KnowledgeFS); err == nil {
		for _, d := range k.Layouts.Layout.TestDirs {
			dirs[d] = true
		}
	}
	return dirs
})

// isTestFile reports whether a file's basename follows a test-naming
// convention. These are test-framework conventions rather than
// per-language rules; the list should move to brief's _layout.toml
// alongside test_dirs.
func isTestFile(base string) bool {
	stem, ext, ok := strings.Cut(base, ".")
	if !ok {
		return false
	}
	if strings.HasSuffix(stem, "_test") || strings.HasSuffix(stem, "_spec") ||
		strings.HasPrefix(stem, "test_") {
		return true
	}
	// foo.test.js, foo.spec.ts: the first cut left "test.js" in ext.
	return strings.HasPrefix(ext, "test.") || strings.HasPrefix(ext, "spec.")
}

// scan walks dir counting test files and files whose content mentions
// the upstream package name. Both counts are ranking signals so
// precision matters less than consistency across candidates.
func scan(dir, upstream string) (testFiles, importFiles int) {
	testDirSet := testDirs()
	needle := []byte(upstream)

	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // best effort
		}
		name := d.Name()
		if d.IsDir() {
			if skipDir(name) {
				return fs.SkipDir
			}
			return nil
		}

		underTestDir := inTestDir(path, dir, testDirSet)
		if underTestDir || isTestFile(name) {
			testFiles++
		}
		if fileMentions(path, d, needle) {
			importFiles++
		}
		return nil
	})
	return testFiles, importFiles
}

func skipDir(name string) bool {
	switch name {
	case "vendor", "testdata", "node_modules", "target", "dist", "build":
		return true
	}
	return strings.HasPrefix(name, ".") && name != "."
}

func inTestDir(path, root string, testDirSet map[string]bool) bool {
	rel, err := filepath.Rel(root, filepath.Dir(path))
	if err != nil {
		return false
	}
	for seg := range strings.SplitSeq(rel, string(filepath.Separator)) {
		if testDirSet[seg] {
			return true
		}
	}
	return false
}

// nonSourceExt lists extensions whose contents aren't worth scanning
// for references to the upstream package: assets, archives, compiled
// artefacts, lockfiles. This is infrastructure filtering, not
// per-ecosystem knowledge.
var nonSourceExt = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".svg": true,
	".ico": true, ".webp": true, ".pdf": true, ".woff": true, ".woff2": true,
	".ttf": true, ".eot": true, ".otf": true, ".mp3": true, ".mp4": true,
	".webm": true, ".ogg": true, ".wav": true,
	".zip": true, ".tar": true, ".gz": true, ".bz2": true, ".xz": true,
	".7z": true, ".rar": true,
	".exe": true, ".dll": true, ".so": true, ".dylib": true, ".a": true,
	".o": true, ".class": true, ".jar": true, ".war": true, ".wasm": true,
	".pyc": true, ".pyo": true,
	".lock": true, ".sum": true,
	".min.js": true, ".min.css": true,
}

func fileMentions(path string, d fs.DirEntry, needle []byte) bool {
	ext := strings.ToLower(filepath.Ext(path))
	if nonSourceExt[ext] {
		return false
	}
	// Catch .min.js / .min.css which Ext() reports as .js / .css.
	lower := strings.ToLower(d.Name())
	if strings.HasSuffix(lower, ".min.js") || strings.HasSuffix(lower, ".min.css") {
		return false
	}
	if info, err := d.Info(); err != nil || info.Size() > maxScanSize {
		return false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return bytes.Contains(b, needle)
}

func slug(c Candidate) string {
	d := c.Dependent()
	return d.Slug()
}
