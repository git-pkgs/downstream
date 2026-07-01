// Package config parses downstream.toml.
package config

import (
	"errors"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/BurntSushi/toml"
)

const DefaultPath = "downstream.toml"

type Config struct {
	Package    Package     `toml:"package"`
	Dependents []Dependent `toml:"dependents"`

	path string
}

type Package struct {
	Name      string `toml:"name"`
	Ecosystem string `toml:"ecosystem"`
	Repo      string `toml:"repo,omitempty"`
	Build     string `toml:"build,omitempty"`
}

type Dependent struct {
	Name         string `toml:"name"`
	Repo         string `toml:"repo"`
	Ref          string `toml:"ref,omitempty"`
	Subdir       string `toml:"subdir,omitempty"`
	Test         string `toml:"test,omitempty"`
	SkipBaseline bool   `toml:"skip_baseline,omitempty"`
	Source       string `toml:"source,omitempty"` // "discover" or "manual"

	// Comment is written as a "# ..." line above the [[dependents]]
	// header. Used by discover to record why a candidate was picked.
	// Not round-tripped; comments are lost on Load.
	Comment string `toml:"-"`
}

// Slug returns a filesystem-safe short name for the dependent,
// derived from the last two path segments of Name (or Repo).
func (d Dependent) Slug() string {
	s := d.Name
	if s == "" {
		s = d.Repo
	}
	s = strings.TrimSuffix(s, ".git")
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == '/' || r == ':' || r == '@'
	})
	const keep = 2 // owner-repo
	if len(parts) > keep {
		parts = parts[len(parts)-keep:]
	}
	slug := strings.Join(parts, "-")
	if slug == "" {
		return "dependent"
	}
	return slug
}

func Load(file string) (*Config, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var cfg Config
	md, err := toml.Decode(string(data), &cfg)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", file, err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, k := range undecoded {
			keys[i] = k.String()
		}
		return nil, fmt.Errorf("%s: unknown keys: %s", file, strings.Join(keys, ", "))
	}
	cfg.path = file
	return &cfg, cfg.validate()
}

func (c *Config) validate() error {
	if c.Package.Name == "" {
		return errors.New("[package] name is required")
	}
	if len(c.Dependents) == 0 {
		return errors.New("at least one [[dependents]] entry is required")
	}
	seen := make(map[string]int, len(c.Dependents))
	for i := range c.Dependents {
		d := &c.Dependents[i]
		if d.Repo == "" {
			return fmt.Errorf("dependents[%d]: repo is required", i)
		}
		if d.Name == "" {
			d.Name = strings.TrimSuffix(path.Base(d.Repo), ".git")
		}
		if j, dup := seen[d.Name]; dup {
			return fmt.Errorf("dependents[%d]: duplicate name %q (also at dependents[%d])", i, d.Name, j)
		}
		seen[d.Name] = i
	}
	return nil
}

// Filter returns the dependents whose name or slug matches one of the
// given patterns. An empty pattern list returns all dependents. A
// pattern that matches nothing is an error so typos surface.
func (c *Config) Filter(only []string) ([]Dependent, error) {
	if len(only) == 0 {
		return c.Dependents, nil
	}
	var out []Dependent
	for _, pat := range only {
		matched := false
		for _, d := range c.Dependents {
			if matchDependent(d, pat) {
				out = append(out, d)
				matched = true
			}
		}
		if !matched {
			return nil, fmt.Errorf("--only %q matches no dependent in %s", pat, c.path)
		}
	}
	return dedupe(out), nil
}

func matchDependent(d Dependent, pat string) bool {
	if d.Name == pat || d.Slug() == pat {
		return true
	}
	if ok, _ := path.Match(pat, d.Name); ok {
		return true
	}
	return strings.Contains(d.Name, pat)
}

func dedupe(deps []Dependent) []Dependent {
	seen := make(map[string]bool, len(deps))
	out := deps[:0]
	for _, d := range deps {
		if seen[d.Name] {
			continue
		}
		seen[d.Name] = true
		out = append(out, d)
	}
	return out
}
