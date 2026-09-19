//go:build linux

package trust

import (
	"fmt"
	"os"
	"os/exec"

	"aven/elevate"
)

const (
	debAnchor  = "/usr/local/share/ca-certificates/aven-root.crt"
	rhelAnchor = "/etc/pki/ca-trust/source/anchors/aven-root.crt"
)

// trusted reports whether the aven anchor is installed in a known system
// store. "Trusted" on Linux means the anchor file is in place and the
// store was rebuilt; probing every consumer is not feasible.
func trusted() bool {
	for _, p := range []string{debAnchor, rhelAnchor} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// installCmd returns the elevated command installing the root into the
// system store, detecting the certificate-update tooling of the distro.
func InstallCmd(root string) (string, error) {
	quoted := elevate.ShellQuote(root)
	switch {
	case commandExists("update-ca-certificates"): // Debian/Ubuntu
		return fmt.Sprintf("cp %s %s && update-ca-certificates", quoted, elevate.ShellQuote(debAnchor)), nil
	case commandExists("update-ca-trust"): // Fedora/RHEL/openSUSE
		return fmt.Sprintf("cp %s %s && update-ca-trust extract", quoted, elevate.ShellQuote(rhelAnchor)), nil
	default:
		return "", fmt.Errorf(
			"no system CA updater found (update-ca-certificates / update-ca-trust); "+
				"install %s into your trust store manually", root)
	}
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
