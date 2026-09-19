// Package trust provisions the aven root CA (via the embedded Caddy pki
// app, without serving) and installs it into the macOS system trust store.
package trust

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"syscall"

	"github.com/caddyserver/caddy/v2"
	_ "github.com/caddyserver/caddy/v2/modules/standard"

	"aven/internal/caddyconf"
	"aven/internal/config"
	"aven/internal/daemon"
	"aven/internal/elevate"
)

// RootCertPath re-exports the canonical root certificate location.
func RootCertPath() string { return caddyconf.RootCertPath() }

// Ensure provisions the CA if missing and trusts it. Idempotent: an
// existing CA is reused (the pki app never regenerates), and an already
// trusted root short-circuits.
func Ensure(cfg *config.Config) error {
	if runtime.GOOS != "darwin" {
		fmt.Printf("unsupported platform %s: install %s into the system trust store manually\n", runtime.GOOS, RootCertPath())
		return nil
	}
	root := RootCertPath()
	if _, err := os.Stat(root); err != nil {
		if daemon.NewClient(cfg.AdminPort).Alive() {
			return fmt.Errorf("daemon is running but CA is missing at %s; restart the daemon", root)
		}
		if err := ProvisionCA(); err != nil {
			return err
		}
		fmt.Printf("provisioned CA at %s\n", root)
	}
	if trusted() {
		fmt.Printf("CA already trusted: %s\n", root)
		return nil
	}
	if err := elevate.Run(TrustCommand(root), "aven needs to trust its local root CA"); err != nil {
		return err
	}
	fmt.Printf("trusted root CA: %s\n", root)
	return nil
}

// TrustCommand builds the elevated shell command that installs root into
// the system trust store. When invoked from a background session (SSH,
// daemon), SecTrustSettingsSetTrustSettings cannot reach SecurityAgent, so
// the command is attached to the console user's GUI session first.
func TrustCommand(root string) string {
	cmd := fmt.Sprintf("security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain %s",
		elevate.ShellQuote(root))
	if uid := consoleUID(); uid > 0 {
		cmd = fmt.Sprintf("launchctl asuser %d %s", uid, cmd)
	}
	return cmd
}

// consoleUID returns the uid logged in on the GUI console, or 0.
func consoleUID() int {
	st, err := os.Stat("/dev/console")
	if err != nil {
		return 0
	}
	if s, ok := st.Sys().(*syscall.Stat_t); ok && s.Uid > 0 {
		return int(s.Uid)
	}
	return 0
}

// Trusted reports whether the root certificate exists and verifies against
// the system trust store.
func Trusted() bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	if _, err := os.Stat(RootCertPath()); err != nil {
		return false
	}
	return trusted()
}

// ProvisionCA runs a minimal pki-only Caddy config so the CA materializes
// in storage without binding 80/443. Admin is disabled so a stale daemon on
// 2019 cannot block provisioning. Exposed for `setup`, which composes it
// into a single elevation command.
func ProvisionCA() error {
	doc := map[string]any{
		"admin":   map[string]any{"disabled": true},
		"storage": map[string]any{"module": "file_system", "root": caddyconf.StorageDir()},
		"apps": map[string]any{
			"pki": map[string]any{
				"certificate_authorities": map[string]any{"local": map[string]any{"name": caddyconf.CAName}},
			},
		},
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	var cc caddy.Config
	if err := caddy.StrictUnmarshalJSON(b, &cc); err != nil {
		return fmt.Errorf("provisioning config rejected: %w", err)
	}
	if err := caddy.Run(&cc); err != nil {
		return fmt.Errorf("CA provisioning failed: %w", err)
	}
	return caddy.Stop()
}

// trusted shells out to `security verify-cert`; exit 0 means the root
// chains to a trust-store anchor.
func trusted() bool {
	return exec.Command("security", "verify-cert", "-c", RootCertPath()).Run() == nil
}
