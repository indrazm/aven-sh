//go:build darwin

package setup

import (
	"fmt"
	"os"

	"aven/config"
	"aven/elevate"
)

// ResolverPath returns the macOS scoped-resolver file for the suffix
// (/etc/resolver/<suffix>). Its presence makes every *.suffix domain route
// to the local DNS responder, removing the need for per-domain hosts
// entries.
func ResolverPath(cfg *config.Config) string {
	return "/etc/resolver/" + cfg.Suffix
}

func ResolverFile(cfg *config.Config) string {
	return fmt.Sprintf("nameserver 127.0.0.1\nport %d\n", cfg.DNSPort)
}

func ResolverDetail(cfg *config.Config) string {
	return ResolverPath(cfg) + " → 127.0.0.1"
}

// ResolverInstalled reports whether the scoped resolver file matches the
// desired content for the current config.
func ResolverInstalled(cfg *config.Config) bool {
	data, err := os.ReadFile(ResolverPath(cfg))
	return err == nil && string(data) == ResolverFile(cfg)
}

// resolverParts returns the privileged shell commands installing scoped
// DNS routing for the suffix.
func resolverParts(cfg *config.Config) []string {
	if ResolverInstalled(cfg) {
		return nil
	}
	return []string{
		fmt.Sprintf("mkdir -p /etc/resolver && printf %s > %s",
			elevate.ShellQuote(ResolverFile(cfg)), elevate.ShellQuote(ResolverPath(cfg))),
	}
}

// ApplyResolver is a no-op on macOS: the resolver file in /etc/resolver is
// permanent; there is nothing to re-apply.
func ApplyResolver(cfg *config.Config) error {
	return fmt.Errorf("resolver apply is only needed on Linux; on macOS the resolver file in /etc/resolver is permanent")
}

func resolverSummary(cfg *config.Config) []string {
	return []string{
		"resolver installed: " + ResolverPath(cfg) + " (every *." + cfg.Suffix + " domain resolves to localhost)",
	}
}
