//go:build darwin

package trust

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"aven/elevate"
)

// trusted shells out to `security verify-cert`; exit 0 means the root
// chains to a trust-store anchor.
func trusted() bool {
	return exec.Command("security", "verify-cert", "-c", RootCertPath()).Run() == nil
}

// installCmd builds the elevated shell command that installs root into the
// system trust store. When invoked from a background session (SSH, daemon),
// SecTrustSettingsSetTrustSettings cannot reach SecurityAgent, so the
// command is attached to the console user's GUI session first.
func InstallCmd(root string) (string, error) {
	cmd := fmt.Sprintf("security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain %s",
		elevate.ShellQuote(root))
	if uid := consoleUID(); uid > 0 {
		cmd = fmt.Sprintf("launchctl asuser %d %s", uid, cmd)
	}
	return cmd, nil
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
