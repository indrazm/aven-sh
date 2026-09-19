// Package domain is the shared mutation path used by the CLI, the TUI and
// the MCP server: validate → save config → hot-reload the daemon. Domain
// resolution is handled by the scoped DNS resolver (see `setup`), so
// mutations never need elevation.
package domain

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"aven/admin"
	"aven/caddyconf"
	"aven/config"
)

// Status is one row of `list`: config data plus live observations.
type Status struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	FQDN        string `json:"fqdn"`
	Paused      bool   `json:"paused"`
	Spec        string `json:"spec"` // proxy target or static root
	BackendUp   bool   `json:"backend_up"`
	BackendNote string `json:"backend_note,omitempty"` // reason when BackendUp is false
}

// Add appends a domain, persists the config and hot-reloads the daemon
// when it is running.
func Add(name, kind, target, root string) (warnings []string, err error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	d := config.Domain{Name: name, Kind: kind, Target: target, Root: root}
	if kind == config.KindStatic {
		d.Root = config.ExpandTilde(d.Root)
		if abs, aerr := filepath.Abs(d.Root); aerr == nil {
			d.Root = abs
		}
	}
	if existing := cfg.Find(name); existing != nil {
		return nil, fmt.Errorf("domain %q already exists", name)
	}
	// Resolve the proxy target up front so a bad target never reaches disk.
	if kind == config.KindProxy {
		if _, _, perr := caddyconf.ParseTarget(d.Target); perr != nil {
			return nil, perr
		}
	}
	cfg.Domains = append(cfg.Domains, d)
	if err := cfg.Save(); err != nil {
		return nil, err
	}
	if rerr := ReloadIfRunning(cfg); rerr != nil {
		return warnings, fmt.Errorf("config saved, but daemon reload failed: %w", rerr)
	}
	return warnings, nil
}

// Remove deletes a domain by name and follows the same save/reload path
// as Add. Missing names are an error.
func Remove(name string) (warnings []string, err error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	idx := -1
	for i := range cfg.Domains {
		if cfg.Domains[i].Name == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, fmt.Errorf("domain %q not found", name)
	}
	cfg.Domains = append(cfg.Domains[:idx], cfg.Domains[idx+1:]...)
	if err := cfg.Save(); err != nil {
		return nil, err
	}
	if rerr := ReloadIfRunning(cfg); rerr != nil {
		return warnings, fmt.Errorf("config saved, but daemon reload failed: %w", rerr)
	}
	return warnings, nil
}

// SetPaused marks a domain paused/unpaused and hot-reloads. A paused
// domain keeps its config entry but serves no routes.
func SetPaused(name string, paused bool) (warnings []string, err error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	d := cfg.Find(name)
	if d == nil {
		return nil, fmt.Errorf("domain %q not found", name)
	}
	if d.Paused == paused {
		return nil, nil
	}
	d.Paused = paused
	if err := cfg.Save(); err != nil {
		return nil, err
	}
	if rerr := ReloadIfRunning(cfg); rerr != nil {
		return warnings, fmt.Errorf("config saved, but daemon reload failed: %w", rerr)
	}
	return warnings, nil
}

// List returns config domains with live status; daemonUp reports whether
// the embedded Caddy daemon is answering on the admin port.
func List() (daemonUp bool, rows []Status, err error) {
	cfg, err := config.Load()
	if err != nil {
		return false, nil, err
	}
	daemonUp = admin.NewClient(cfg.AdminPort).Alive()
	for _, d := range cfg.Domains {
		row := Status{
			Name:   d.Name,
			Kind:   d.Kind,
			FQDN:   cfg.FQDN(d.Name),
			Paused: d.Paused,
			Spec:   d.Spec(),
		}
		switch {
		case d.Paused:
			row.BackendNote = "paused"
		case d.Kind == config.KindProxy:
			dial, _, perr := caddyconf.ParseTarget(d.Target)
			if perr != nil {
				row.BackendNote = perr.Error()
				break
			}
			conn, derr := net.DialTimeout("tcp", dial, 750*time.Millisecond)
			if derr != nil {
				row.BackendNote = fmt.Sprintf("%s unreachable", dial)
				break
			}
			conn.Close()
			row.BackendUp = true
		case d.Kind == config.KindStatic:
			if _, serr := os.Stat(d.Root); serr != nil {
				row.BackendNote = "root missing"
				break
			}
			row.BackendUp = true
		}
		rows = append(rows, row)
	}
	return daemonUp, rows, nil
}

// ReloadIfRunning pushes the generated Caddy JSON to a running daemon via
// POST /load. A stopped daemon is a no-op: the config is picked up at the
// next start.
func ReloadIfRunning(cfg *config.Config) error {
	c := admin.NewClient(cfg.AdminPort)
	if !c.Alive() {
		return nil
	}
	b, err := caddyconf.Build(cfg)
	if err != nil {
		return err
	}
	return c.Reload(b)
}
