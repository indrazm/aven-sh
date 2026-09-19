// Package setup performs the one-time machine configuration: aven's CA
// provisioned and trusted, and platform-scoped DNS routing for the
// configured suffix (a resolver file on macOS, systemd-resolved routing on
// Linux). All privileged steps are composed into a single elevation
// command.
package setup

import (
	"fmt"
	"os"
	"strings"

	"aven/admin"
	"aven/config"
	"aven/elevate"
	"aven/trust"
)

// Run brings the machine to fully-configured state. Idempotent: every step
// short-circuits when already done, so a second run is dialog-free.
func Run(cfg *config.Config) error {
	root := trust.RootCertPath()
	if _, err := os.Stat(root); err != nil {
		if admin.NewClient(cfg.AdminPort).Alive() {
			return fmt.Errorf("daemon is running but CA is missing at %s; restart the daemon", root)
		}
		if err := trust.ProvisionCA(); err != nil {
			return err
		}
		fmt.Printf("provisioned CA at %s\n", root)
	}

	var parts []string
	parts = append(parts, resolverParts(cfg)...)
	if !trust.Trusted() {
		cmd, err := trust.InstallCmd(root)
		if err != nil {
			return err
		}
		parts = append(parts, cmd)
	}
	if len(parts) == 0 {
		fmt.Println("already set up: CA trusted, DNS routing installed")
		return nil
	}
	if err := elevate.Run(strings.Join(parts, " && "),
		"aven needs to set up local domain resolution"); err != nil {
		return err
	}
	for _, line := range resolverSummary(cfg) {
		fmt.Println(line)
	}
	fmt.Printf("root CA trusted: %s\n", root)
	return nil
}
