package config

import (
	"os"
	"testing"
)

// Config contains domain names and filesystem paths; it must not be
// world-readable (regression: it was written 0644).
func TestSaveFileModeIsOwnerOnly(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(Path())
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("config file mode = %o, want 600", perm)
	}
}
