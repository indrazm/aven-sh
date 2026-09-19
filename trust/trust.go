// Package trust provisions the aven root CA (via the embedded Caddy pki
// app, without serving) and installs it into the system trust store.
// Platform specifics (trust-store location, verification command) live in
// the _darwin / _linux files.
package trust

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/caddyserver/caddy/v2"
	_ "github.com/caddyserver/caddy/v2/modules/standard"

	"aven/caddyconf"
	"aven/config"
	"aven/elevate"
)

// RootCertPath re-exports the canonical root certificate location.
func RootCertPath() string { return caddyconf.RootCertPath() }

// Ensure provisions the CA if missing and trusts it. Idempotent: an
// existing CA is reused (the pki app never regenerates), and an already
// trusted root short-circuits.
func Ensure(cfg *config.Config) error {
	root := RootCertPath()
	if _, err := os.Stat(root); err != nil {
		if err := ProvisionCA(); err != nil {
			return err
		}
		fmt.Printf("provisioned CA at %s\n", root)
	}
	if trusted() {
		fmt.Printf("CA already trusted: %s\n", root)
		return nil
	}
	cmd, err := InstallCmd(root)
	if err != nil {
		return err
	}
	if err := elevate.Run(cmd, "aven needs to trust its local root CA"); err != nil {
		return err
	}
	fmt.Printf("trusted root CA: %s\n", root)
	return nil
}

// Trusted reports whether the root certificate exists and is trusted by
// the system.
func Trusted() bool {
	if _, err := os.Stat(RootCertPath()); err != nil {
		return false
	}
	return trusted()
}

// ProvisionCA runs a minimal pki-only Caddy config so the CA materializes
// in storage without binding ports. Admin is disabled so a stale daemon on
// 2019 cannot block provisioning.
func ProvisionCA() error {
	doc := map[string]any{
		"admin":   map[string]any{"disabled": true},
		"storage": map[string]any{"module": "file_system", "root": caddyconf.StorageDir()},
		"apps": map[string]any{
			"pki": map[string]any{
				// install_trust=false keeps provisioning free of any
				// trust-store/sudo side effects; trust is installed only
				// by the explicit `aven setup`/`aven trust` elevation.
				"certificate_authorities": map[string]any{"local": map[string]any{
					"name": caddyconf.CAName, "install_trust": false,
				}},
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
