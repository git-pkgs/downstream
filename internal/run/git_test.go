package run

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsLocalPath(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"https://github.com/foo/bar", false},
		{"git@github.com:foo/bar.git", false},
		{"ssh://git@github.com/foo/bar", false},
		{"./relative", true},
		{"../relative", true},
		{"/absolute", true},
		{".", true},
	}
	for _, tt := range tests {
		if got := isLocalPath(tt.in); got != tt.want {
			t.Errorf("isLocalPath(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestCopyDir(t *testing.T) {
	src := t.TempDir()
	mustMkdir(t, filepath.Join(src, "sub"))
	mustMkdir(t, filepath.Join(src, ".git"))
	mustWrite(t, filepath.Join(src, "a.txt"), "a")
	mustWrite(t, filepath.Join(src, "sub", "b.txt"), "b")
	mustWrite(t, filepath.Join(src, ".git", "HEAD"), "ref: refs/heads/main")

	dest := filepath.Join(t.TempDir(), "copy")
	if err := copyDir(src, dest); err != nil {
		t.Fatalf("copyDir: %v", err)
	}

	if got := readWorkfile(t, dest, "a.txt"); got != "a" {
		t.Errorf("a.txt = %q", got)
	}
	if got := readWorkfile(t, filepath.Join(dest, "sub"), "b.txt"); got != "b" {
		t.Errorf("sub/b.txt = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dest, ".git")); !os.IsNotExist(err) {
		t.Errorf(".git should be skipped")
	}
}
