package run

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const dirPerm os.FileMode = 0o755

// clone shallow-clones url into dest. If ref is set, that branch or
// tag is checked out; otherwise the default branch is used.
func clone(ctx context.Context, url, ref, dest string) error {
	args := []string{"clone", "--depth", "1"}
	if ref != "" {
		args = append(args, "--branch", ref)
	}
	args = append(args, "--", url, dest)

	cmd := exec.CommandContext(ctx, "git", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return nil
}

func isLocalPath(s string) bool {
	if strings.Contains(s, "://") || strings.HasPrefix(s, "git@") {
		return false
	}
	if _, err := os.Stat(s); err == nil {
		return true
	}
	return strings.HasPrefix(s, ".") || strings.HasPrefix(s, "/")
}

// copyDir copies src to dest, skipping .git. Used for local-path
// dependents so replace doesn't modify the user's checkout.
func copyDir(src, dest string) error {
	src = filepath.Clean(src)
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return fs.SkipDir
		}
		target := filepath.Join(dest, rel)
		if d.IsDir() {
			return os.MkdirAll(target, dirPerm)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
}
