package consoleapi

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"aven/config"
)

// TokenPath returns the stored console pairing token (~/.aven/console-token).
// The daemon re-reads it on every request, so `aven console pair` and
// `aven console revoke` take effect without a restart.
func TokenPath() string {
	return filepath.Join(config.BaseDir(), "console-token")
}

// IsPaired reports whether a pairing token exists.
func IsPaired() bool {
	_, err := os.Stat(TokenPath())
	return err == nil
}

// NewToken generates a fresh pairing token, stores it (0600) and returns it.
func NewToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	if err := os.WriteFile(TokenPath(), []byte(token+"\n"), 0o600); err != nil {
		return "", err
	}
	return token, nil
}

// Revoke deletes the pairing token if present. Reports whether a token was
// removed.
func Revoke() (bool, error) {
	if _, err := os.Stat(TokenPath()); os.IsNotExist(err) {
		return false, nil
	}
	if err := os.Remove(TokenPath()); err != nil {
		return false, err
	}
	return true, nil
}

// loadToken returns the current pairing token, or "" when unpaired.
func loadToken() string {
	data, err := os.ReadFile(TokenPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// authorized checks a presented bearer token against the stored token.
func authorized(presented string) bool {
	stored := loadToken()
	if stored == "" || presented == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(presented), []byte(stored)) == 1
}

// PairURL builds the console deep link carrying the token in the URL
// fragment (fragments are never sent to any server).
func PairURL(token string) string {
	return fmt.Sprintf("https://console.aven.sh/pair#T=%s", token)
}
