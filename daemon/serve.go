// Package daemon runs the embedded Caddy engine (`serve`) and manages the
// daemon lifecycle from the manager side (admin client, background start).
package daemon

import (
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/caddyserver/caddy/v2"
	_ "github.com/caddyserver/caddy/v2/modules/standard" // register standard Caddy modules

	"aven/admin"
	"aven/caddyconf"
	"aven/config"
	"aven/consoleapi"
)

// Serve builds the Caddy config, validates it, runs the engine in-process
// and blocks until SIGINT/SIGTERM or until the engine is stopped through
// the admin API (POST /stop), which also terminates this process.
func Serve(cfg *config.Config) error {
	// Pre-check the serving ports on 127.0.0.1 — the addresses the
	// generated Caddy config actually binds — so a busy or unbindable
	// port produces a targeted error instead of Caddy's generic failure.
	for _, p := range []struct {
		name string
		port int
	}{
		{"http", cfg.HTTPPort},
		{"https", cfg.HTTPSPort},
	} {
		ln, err := net.Listen("tcp", caddyconf.ServerListen(p.port)[0])
		if err != nil {
			msg := fmt.Sprintf("bind port %d (%s): %v", p.port, p.name, err)
			if errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) {
				if runtime.GOOS == "linux" {
					self, _ := os.Executable()
					msg += fmt.Sprintf(
						"\n  privileged ports need a capability; run:\n"+
							"    sudo setcap 'cap_net_bind_service=+ep' %s\n"+
							"  or change http_port/https_port in %s", self, config.Path())
				}
			}
			return errors.New(msg)
		}
		ln.Close()
	}

	b, err := caddyconf.Build(cfg)
	if err != nil {
		return err
	}
	var cc caddy.Config
	if err := caddy.StrictUnmarshalJSON(b, &cc); err != nil {
		return fmt.Errorf("generated config rejected by Caddy: %w", err)
	}
	if err := caddy.Validate(&cc); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}
	// Validate mutates the Config it provisions (App() nils consumed
	// AppsRaw entries), so Run must decode its own copy from the bytes.
	var cr caddy.Config
	if err := caddy.StrictUnmarshalJSON(b, &cr); err != nil {
		return fmt.Errorf("generated config rejected by Caddy: %w", err)
	}
	if err := caddy.Run(&cr); err != nil {
		return fmt.Errorf("start: %w", err)
	}
	stopDNS, err := StartDNS(cfg)
	if err != nil {
		_ = caddy.Stop()
		return fmt.Errorf("dns responder: %w", err)
	}
	defer stopDNS()
	stopConsole, err := consoleapi.StartConsole(cfg, admin.NewClient(cfg.AdminPort).Alive)
	if err != nil {
		// The console is a convenience surface; a failure to start it
		// (rare: bind conflict) must not take serving down.
		log.Printf("aven: console API unavailable: %v", err)
	} else {
		defer stopConsole()
		log.Printf("aven: console API on https://daemon.%s:%d (127.0.0.1)", cfg.Suffix, cfg.ConsolePort)
	}
	log.Printf("aven: serving %d domain(s) (*.%s) on ports %d/%d, admin API on 127.0.0.1:%d, resolving *.%s via 127.0.0.1:%d",
		len(cfg.Domains), cfg.Suffix, cfg.HTTPPort, cfg.HTTPSPort, cfg.AdminPort, cfg.Suffix, cfg.DNSPort)

	adminC := admin.NewClient(cfg.AdminPort)
	ready := adminC.WaitForReady(10*time.Second) == nil
	if ready {
		log.Printf("aven: ready")
	} else {
		log.Printf("aven: warning: admin API not ready; only SIGINT/SIGTERM will stop the daemon")
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case sig := <-sigCh:
			log.Printf("aven: received %v, stopping", sig)
			return caddy.Stop()
		case <-ticker.C:
			// The engine can also be stopped externally via POST /stop
			// (MCP daemon control, self-upgrade); the admin listener going
			// away means we're done.
			if ready && !adminC.Alive() {
				log.Printf("aven: engine stopped")
				return nil
			}
		}
	}
}

// StartInBackground spawns `aven serve` detached from the terminal
// (own process group, output into ~/\.aven/serve.out) and waits until
// its admin API is ready.
func StartInBackground(cfg *config.Config) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(config.BaseDir(), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(filepath.Join(config.BaseDir(), "serve.out"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	cmd := exec.Command(self, "serve")
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := admin.NewClient(cfg.AdminPort).WaitForReady(10 * time.Second); err != nil {
		return fmt.Errorf("daemon did not become ready (see %s): %w", caddyconf.LogPath(), err)
	}
	return nil
}
