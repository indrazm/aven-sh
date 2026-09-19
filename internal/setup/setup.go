// Package setup performs the one-time machine configuration: aven's CA
// provisioned and trusted, and a scoped DNS resolver file for the
// configured suffix. All privileged steps are composed into a single
// elevation dialog.
package setup

import (
	"fmt"
	"os"
	"strings"

	"aven/internal/config"
	"aven/internal/daemon"
	"aven/internal/elevate"
	"aven/internal/trust"
)

// ResolverPath returns the macOS scoped-resolver file for the suffix
// (/etc/resolver/<suffix>). Its presence makes every *.suffix domain route
// to the local DNS responder, removing the need for per-domain hosts
// entries.
func ResolverPath(cfg *config.Config) string {
	return "/etc/resolver/" + cfg.Suffix
}

// ResolverFile returns the desired resolver file content.
func ResolverFile(cfg *config.Config) string {
	return fmt.Sprintf("nameserver 127.0.0.1\nport %d\n", cfg.DNSPort)
}

// ResolverInstalled reports whether the scoped resolver file matches the
// desired content for the current config.
func ResolverInstalled(cfg *config.Config) bool {
	data, err := os.ReadFile(ResolverPath(cfg))
	return err == nil && string(data) == ResolverFile(cfg)
}

// Run brings the machine to fully-configured state. Idempotent: every step
// short-circuits when already done, so a second run is dialog-free.
func Run(cfg *config.Config) error {
	root := trust.RootCertPath()
	if _, err := os.Stat(root); err != nil {
		if daemon.NewClient(cfg.AdminPort).Alive() {
			return fmt.Errorf("daemon is running but CA is missing at %s; restart the daemon", root)
		}
		if err := trust.ProvisionCA(); err != nil {
			return err
		}
		fmt.Printf("provisioned CA at %s\n", root)
	}

	var parts []string
	if !ResolverInstalled(cfg) {
		parts = append(parts,
			fmt.Sprintf("mkdir -p /etc/resolver && printf %s > %s",
				elevate.ShellQuote(ResolverFile(cfg)), elevate.ShellQuote(ResolverPath(cfg))))
	}
	if !trust.Trusted() {
		parts = append(parts, trust.TrustCommand(root))
	}
	if len(parts) == 0 {
		fmt.Println("already set up: CA trusted, resolver installed")
		return nil
	}
	if err := elevate.Run(strings.Join(parts, " && "),
		"aven needs to set up local domain resolution"); err != nil {
		return err
	}
	for _, line := range []string{
		"resolver installed: " + ResolverPath(cfg) + " (every *." + cfg.Suffix + " domain resolves to localhost)",
		"root CA trusted: " + root,
	} {
		fmt.Println(line)
	}
	return nil
}
