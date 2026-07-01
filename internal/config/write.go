package config

import (
	"fmt"
	"io"
	"os"
	"strings"
)

const writePerm os.FileMode = 0o644

// Write emits the config as downstream.toml. Each dependent's
// Comment field is written as a "# ..." line above its
// [[dependents]] header. Encoding is done by hand rather than via
// toml.Encoder so the comment lines survive and the field order is
// stable.
func Write(path string, cfg *Config) error {
	var b strings.Builder
	if _, err := WriteTo(&b, cfg); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(b.String()), writePerm)
}

func WriteTo(w io.Writer, cfg *Config) (int64, error) {
	var b strings.Builder

	b.WriteString("[package]\n")
	writeKV(&b, "name", cfg.Package.Name)
	if cfg.Package.Ecosystem != "" {
		writeKV(&b, "ecosystem", cfg.Package.Ecosystem)
	}
	if cfg.Package.Repo != "" {
		writeKV(&b, "repo", cfg.Package.Repo)
	}
	if cfg.Package.Build != "" {
		writeKV(&b, "build", cfg.Package.Build)
	}

	for _, d := range cfg.Dependents {
		b.WriteString("\n")
		if d.Comment != "" {
			for line := range strings.SplitSeq(d.Comment, "\n") {
				fmt.Fprintf(&b, "# %s\n", line)
			}
		}
		b.WriteString("[[dependents]]\n")
		writeKV(&b, "name", d.Name)
		writeKV(&b, "repo", d.Repo)
		if d.Ref != "" {
			writeKV(&b, "ref", d.Ref)
		}
		if d.Subdir != "" {
			writeKV(&b, "subdir", d.Subdir)
		}
		if d.Test != "" {
			writeKV(&b, "test", d.Test)
		}
		if d.SkipBaseline {
			b.WriteString("skip_baseline = true\n")
		}
		if d.Source != "" {
			writeKV(&b, "source", d.Source)
		}
	}

	n, err := io.WriteString(w, b.String())
	return int64(n), err
}

func writeKV(b *strings.Builder, key, val string) {
	fmt.Fprintf(b, "%s = %q\n", key, val)
}
