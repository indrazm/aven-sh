// Package upgrade updates the aven binary in place from the latest GitHub
// release: download, checksum-verify, extract, atomically replace the
// running executable and restart the daemon if it was running.
package upgrade

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"aven/admin"
	"aven/config"
	"aven/daemon"
	"aven/elevate"
)

const repo = "indrazm/aven-sh"

// Run checks GitHub for the latest release and, unless checkOnly, replaces
// the current executable with it. current is the running version (may be
// "dev" for local builds). If a daemon is running it is stopped and
// restarted on the new binary.
func Run(current string, checkOnly bool) error {
	latest, err := latestTag()
	if err != nil {
		return err
	}
	if normalize(current) == normalize(latest) {
		fmt.Printf("already up to date (%s)\n", current)
		return nil
	}
	if checkOnly {
		fmt.Printf("update available: %s -> %s\n", current, latest)
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return err
	}

	daemonWasUp := admin.NewClient(loadAdminPort()).Alive()

	staged, err := fetchBinary(latest)
	if err != nil {
		return err
	}

	fmt.Printf("upgrading %s -> %s\n", current, latest)
	if err := replace(staged, exe); err != nil {
		return err
	}

	fmt.Printf("upgraded to %s\n", latest)

	if daemonWasUp {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if err := admin.NewClient(cfg.AdminPort).Stop(); err != nil {
			fmt.Println("note: old daemon still running; stop it with `aven serve`'s terminal or the console")
			return nil
		}
		if err := daemon.StartInBackground(cfg); err != nil {
			return fmt.Errorf("daemon restart failed: %w (start it with `aven serve`)", err)
		}
		fmt.Println("daemon restarted on the new version")
	}
	return nil
}

func loadAdminPort() int {
	cfg, err := config.Load()
	if err != nil {
		return 2019
	}
	return cfg.AdminPort
}

// latestTag returns the tag name of the newest GitHub release.
func latestTag() (string, error) {
	req, err := http.NewRequest(http.MethodGet,
		"https://api.github.com/repos/"+repo+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API: HTTP %d", resp.StatusCode)
	}
	var out struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.TagName == "" {
		return "", fmt.Errorf("no releases found")
	}
	return out.TagName, nil
}

// fetchBinary downloads the release tarball for this platform, verifies it
// against checksums.txt and returns the path of the extracted binary.
func fetchBinary(tag string) (string, error) {
	asset := fmt.Sprintf("aven-%s-%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	base := fmt.Sprintf("https://github.com/%s/releases/download/%s", repo, tag)

	tmp, err := os.MkdirTemp("", "aven-upgrade-*")
	if err != nil {
		return "", err
	}
	tarball := filepath.Join(tmp, asset)
	if err := download(base+"/"+asset, tarball); err != nil {
		return "", err
	}
	sums := filepath.Join(tmp, "checksums.txt")
	if err := download(base+"/checksums.txt", sums); err != nil {
		return "", err
	}
	data, err := os.ReadFile(sums)
	if err != nil {
		return "", err
	}
	expected := checksumFor(string(data), asset)
	if expected == "" {
		return "", fmt.Errorf("checksum for %s not found in checksums.txt", asset)
	}
	sum, err := fileSHA256(tarball)
	if err != nil {
		return "", err
	}
	if subtle.ConstantTimeCompare([]byte(sum), []byte(expected)) != 1 {
		return "", fmt.Errorf("checksum mismatch: want %s, got %s", expected, sum)
	}

	bin, err := extractAven(tarball, tmp)
	if err != nil {
		return "", err
	}
	return bin, nil
}

func checksumFor(sums, asset string) string {
	for _, line := range strings.Split(sums, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && (fields[1] == asset || strings.HasSuffix(fields[1], "/"+asset)) {
			return fields[0]
		}
	}
	return ""
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// extractAven pulls the single "aven" entry out of the tarball.
func extractAven(tarball, dir string) (string, error) {
	f, err := os.Open(tarball)
	if err != nil {
		return "", err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	out := filepath.Join(dir, "aven")
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		name := strings.TrimPrefix(hdr.Name, "./")
		if name != "aven" {
			continue
		}
		outFile, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(outFile, tr); err != nil {
			outFile.Close()
			return "", err
		}
		outFile.Close()
		return out, nil
	}
	return "", fmt.Errorf("tarball contains no 'aven' binary")
}

// replace atomically moves the staged binary over the running executable.
// Unix allows renaming over a running binary; if the destination directory
// is not writable the move is elevated instead.
func replace(staged, exe string) error {
	if err := os.Rename(staged, exe); err == nil {
		return nil
	}
	return elevate.Run(fmt.Sprintf("mv %s %s", elevate.ShellQuote(staged), elevate.ShellQuote(exe)),
		"aven needs to replace its binary")
}

// download fetches url into dest.
func download(url, dest string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d downloading %s", resp.StatusCode, url)
	}
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func normalize(v string) string { return strings.TrimPrefix(v, "v") }
