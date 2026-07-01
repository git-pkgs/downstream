package cmd

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestReadLocalPackage(t *testing.T) {
	tests := []struct {
		desc    string
		files   map[string]string
		name    string
		eco     string
		wantErr string
	}{
		{
			desc:  "go.mod",
			files: map[string]string{"go.mod": "module github.com/acme/lib\n\ngo 1.22\n"},
			name:  "github.com/acme/lib",
			eco:   "golang",
		},
		{
			desc:  "Cargo.toml",
			files: map[string]string{"Cargo.toml": "[package]\nname = \"acme\"\nversion = \"0.1.0\"\n"},
			name:  "acme",
			eco:   "cargo",
		},
		{
			desc:  "package.json",
			files: map[string]string{"package.json": `{"name":"@acme/lib","version":"1.0.0"}`},
			name:  "@acme/lib",
			eco:   "npm",
		},
		{
			desc: "Gemfile alone has no name",
			files: map[string]string{
				"Gemfile": "source 'https://rubygems.org'\ngem 'rake'\n",
			},
			wantErr: "no package name in [Gemfile]",
		},
		{
			desc: "gemspec beside Gemfile",
			files: map[string]string{
				"Gemfile":      "gemspec\n",
				"acme.gemspec": "Gem::Specification.new do |s|\n  s.name = 'acme'\nend\n",
			},
			name: "acme",
			eco:  "gem",
		},
		{
			desc:    "empty dir",
			files:   nil,
			wantErr: "no manifest found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			dir := t.TempDir()
			for f, body := range tt.files {
				mustWriteFile(t, filepath.Join(dir, f), body)
			}
			name, eco, err := readLocalPackage(dir)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("readLocalPackage: %v", err)
			}
			if name != tt.name {
				t.Errorf("name = %q, want %q", name, tt.name)
			}
			if eco != tt.eco {
				t.Errorf("ecosystem = %q, want %q", eco, tt.eco)
			}
		})
	}
}
