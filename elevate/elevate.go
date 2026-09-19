// Package elevate runs privileged shell commands. It is the single
// elevation path used by setup and CA trust installation.
//
// macOS: an osascript administrator-privileges dialog (password prompt on
// the GUI console; the command is attached to the console user's session so
// trust-store changes work from SSH/background sessions).
// Linux: runs directly as root, otherwise attempts non-interactive sudo
// (succeeds when the user has a recent sudo timestamp) and otherwise asks
// the user to run the command with sudo.
package elevate

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// ErrDenied reports that the privileged operation was not performed —
// either the user declined the dialog or no passwordless escalation was
// available.
var ErrDenied = errors.New("elevation denied")

// Run executes command (a full shell command line) as root, prompting for
// privileges with the given prompt where the platform supports it.
func Run(command, prompt string) error {
	switch runtime.GOOS {
	case "darwin":
		return runDarwin(command, prompt)
	case "linux":
		return runLinux(command)
	default:
		return fmt.Errorf("elevation unsupported on %s; run as root: %s", runtime.GOOS, command)
	}
}

// Root reports whether the current process is already privileged.
func Root() bool { return os.Geteuid() == 0 }

func runDarwin(command, prompt string) error {
	script := fmt.Sprintf("do shell script %s with administrator privileges with prompt %s",
		appleScriptString(command), appleScriptString(prompt))
	out, err := exec.Command("osascript", "-e", script).CombinedOutput()
	if err == nil {
		return nil
	}
	if strings.Contains(strings.ToLower(string(out)), "user canceled") {
		return ErrDenied
	}
	return fmt.Errorf("elevated command failed: %w: %s", err, strings.TrimSpace(string(out)))
}

func runLinux(command string) error {
	if Root() {
		return sh(command)
	}
	// Non-interactive sudo succeeds when the user has a fresh sudo
	// timestamp in this terminal; otherwise tell them to run it by hand.
	if err := exec.Command("sudo", "-n", "sh", "-c", command).Run(); err == nil {
		return nil
	}
	return fmt.Errorf("%w (run: sudo %s)", ErrDenied, command)
}

func sh(command string) error {
	out, err := exec.Command("sh", "-c", command).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// appleScriptString quotes s as an AppleScript string literal (backslash and
// double-quote escapes; inputs are ASCII paths and commands).
func appleScriptString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// ShellQuote quotes s for POSIX shell single-quote safety.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
