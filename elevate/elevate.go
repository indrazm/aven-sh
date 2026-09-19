// Package elevate runs shell commands with administrator privileges via a
// macOS osascript password dialog. It is the single elevation path used by
// hostsfile sync and CA trust installation.
package elevate

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// ErrDenied is returned when the user cancels the privilege dialog.
var ErrDenied = errors.New("user denied elevation")

// Run executes command (a full shell command line) as root, prompting for
// administrator credentials with the given prompt.
func Run(command, prompt string) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("elevation unsupported on %s; run `sudo` for: %s", runtime.GOOS, command)
	}
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
