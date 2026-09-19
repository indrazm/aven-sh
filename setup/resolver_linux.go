//go:build linux

package setup

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"aven/config"
	"aven/elevate"
)

// ResolverDetail returns a human-readable description of the routing state.
func ResolverDetail(cfg *config.Config) string {
	return fmt.Sprintf("systemd-resolved routes ~%s to 127.0.0.1:%d", cfg.Suffix, cfg.DNSPort)
}

// ResolverInstalled reports whether systemd-resolved already routes the
func ResolverInstalled(cfg *config.Config) bool {
	link := defaultLink()
	if link == "" {
		return false
	}
	out, err := exec.Command("resolvectl", "domain", link).CombinedOutput()
	return err == nil && strings.Contains(string(out), "~"+cfg.Suffix)
}

// resolverParts returns the privileged shell commands routing *.suffix to
// the local responder via systemd-resolved, plus a oneshot unit that
// re-applies the routing at boot (resolved drops per-link settings when
// the link goes down).
func resolverParts(cfg *config.Config) []string {
	parts := []string{applyResolverCmd(cfg)}
	if _, err := os.Stat("/etc/systemd/system"); err == nil {
		parts = append(parts,
			fmt.Sprintf("printf %s > /etc/systemd/system/aven-resolver.service",
				elevate.ShellQuote(unitText(cfg))),
			"systemctl daemon-reload",
			"systemctl enable aven-resolver.service")
	}
	return parts
}

// ApplyResolver re-applies scoped DNS routing. Used by the boot unit (as
// root) and `aven resolver apply`.
func ApplyResolver(cfg *config.Config) error {
	link := defaultLink()
	if link == "" {
		return fmt.Errorf("no default route link found; cannot configure resolved")
	}
	return elevate.Run(applyResolverCmd(cfg), "")
}

func applyResolverCmd(cfg *config.Config) string {
	link := defaultLink()
	return fmt.Sprintf("resolvectl dns %s 127.0.0.1:%d && resolvectl domain %s ~%s",
		elevate.ShellQuote(link), cfg.DNSPort, elevate.ShellQuote(link), cfg.Suffix)
}

func unitText(cfg *config.Config) string {
	self, _ := os.Executable()
	return fmt.Sprintf(`[Unit]
Description=aven scoped DNS routing (~%s)
Wants=network-online.target
After=network-online.target

[Service]
Type=oneshot
ExecStart=%s resolver apply

[Install]
WantedBy=multi-user.target
`, cfg.Suffix, self)
}

// resolverSummary returns human-readable confirmation lines.
func resolverSummary(cfg *config.Config) []string {
	link := defaultLink()
	name := link
	if name == "" {
		name = "<default link>"
	}
	return []string{
		fmt.Sprintf("resolver routing: %s queries on %s now go to 127.0.0.1:%d", cfg.Suffix, name, cfg.DNSPort),
		"boot persistence: aven-resolver.service enabled (re-applies routing at boot)",
	}
}

// defaultLink parses the interface of the default route from
// `ip route show default` (e.g. "default via ... dev eth0" -> "eth0").
func defaultLink() string {
	out, err := exec.Command("ip", "route", "show", "default").Output()
	if err != nil {
		return ""
	}
	fields := strings.Fields(string(out))
	for i, f := range fields {
		if f == "dev" && i+1 < len(fields) {
			return fields[i+1]
		}
	}
	return ""
}
