// Package config defines the on-disk configuration for aven and its
// load/save/validate lifecycle. The config file is the single source of
// truth; the daemon is stateless and rebuilt from it.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Kind values for Domain.
const (
	KindProxy  = "proxy"
	KindStatic = "static"
)

// Domain is one managed local domain.
type Domain struct {
	// Name is the bare label; the FQDN is Name + "." + Suffix.
	Name string `yaml:"name"`
	// Kind is "proxy" or "static".
	Kind string `yaml:"kind"`
	// Target is the proxy upstream, e.g. "localhost:3000" or "http://localhost:3000".
	Target string `yaml:"target,omitempty"`
	// Root is the static file server root directory.
	Root string `yaml:"root,omitempty"`
	// Paused removes the domain's routes without deleting it.
	Paused bool `yaml:"paused,omitempty"`
}

// Config is the full ~/\.aven/config.yaml document.
type Config struct {
	Suffix    string   `yaml:"suffix"`
	HTTPPort  int      `yaml:"http_port"`
	HTTPSPort int      `yaml:"https_port"`
	AdminPort int      `yaml:"admin_port"`
	DNSPort   int      `yaml:"dns_port"`
	Domains   []Domain `yaml:"domains"`
}

// Default returns the built-in configuration used when no file exists.
func Default() *Config {
	return &Config{
		Suffix:    "aven",
		HTTPPort:  80,
		HTTPSPort: 443,
		AdminPort: 2019,
		// macOS returns EPERM for unprivileged binds of port 53, so the
		// responder defaults to 5354; /etc/resolver/<suffix> includes a
		// `port` directive pointing at it.
		DNSPort: 5354,
		Domains: []Domain{},
	}
}

var nameRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// BaseDir returns ~/.aven.
func BaseDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "~"
	}
	return filepath.Join(home, ".aven")
}

// Path returns the config file path ~/.aven/config.yaml.
func Path() string { return filepath.Join(BaseDir(), "config.yaml") }

// Load reads the config file, creating it with defaults when missing.
// A malformed file is an error; partial defaults are never silently mixed in.
func Load() (*Config, error) {
	p := Path()
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		cfg := Default()
		if err := cfg.Validate(); err != nil {
			return nil, fmt.Errorf("default config invalid: %w", err)
		}
		if err := cfg.Save(); err != nil {
			return nil, err
		}
		return cfg, nil
	}
	if err != nil {
		return nil, err
	}
	cfg := Default()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Save writes the config atomically (temp file + rename in the same dir).
func (c *Config) Save() error {
	if err := c.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(BaseDir(), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(BaseDir(), ".config-*.yaml")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, Path())
}

// Validate enforces all invariants: names, uniqueness, kind-specific fields.
func (c *Config) Validate() error {
	if c.Suffix == "" {
		return fmt.Errorf("suffix must not be empty")
	}
	if !nameRE.MatchString(c.Suffix) {
		return fmt.Errorf("suffix %q is not a valid DNS label", c.Suffix)
	}
	seen := map[string]bool{}
	for i, d := range c.Domains {
		label := fmt.Sprintf("domains[%d]", i)
		if !nameRE.MatchString(d.Name) {
			return fmt.Errorf("%s: name %q must match %s", label, d.Name, nameRE)
		}
		if d.Name == "localhost" {
			return fmt.Errorf("%s: name %q is reserved", label, d.Name)
		}
		if seen[d.Name] {
			return fmt.Errorf("%s: duplicate name %q", label, d.Name)
		}
		seen[d.Name] = true
		switch d.Kind {
		case KindProxy:
			if strings.TrimSpace(d.Target) == "" {
				return fmt.Errorf("%s (%s): target is required", label, d.Name)
			}
			if d.Root != "" {
				return fmt.Errorf("%s (%s): proxy domains must not set root", label, d.Name)
			}
		case KindStatic:
			if strings.TrimSpace(d.Root) == "" {
				return fmt.Errorf("%s (%s): root is required", label, d.Name)
			}
			if d.Target != "" {
				return fmt.Errorf("%s (%s): static domains must not set target", label, d.Name)
			}
		default:
			return fmt.Errorf("%s (%s): kind must be %q or %q, got %q", label, d.Name, KindProxy, KindStatic, d.Kind)
		}
	}
	return nil
}

// FQDN returns the fully qualified domain name for a domain name label.
func (c *Config) FQDN(name string) string { return name + "." + c.Suffix }

// Find returns the domain with the given name, or nil.
func (c *Config) Find(name string) *Domain {
	for i := range c.Domains {
		if c.Domains[i].Name == name {
			return &c.Domains[i]
		}
	}
	return nil
}

// ExpandTilde expands a leading "~" or "~/" to the user's home directory.
func ExpandTilde(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}
