#!/bin/sh
# aven installer — https://aven.sh
#
#   curl -fsSL https://raw.githubusercontent.com/indrazm/aven-sh/main/install.sh | sh
#
# Downloads the release binary for your platform from GitHub Releases,
# verifies its checksum and installs it. Set AVEN_VERSION to pin a version.
set -eu

REPO="indrazm/aven-sh"
NAME="aven"

say() { printf '%s\n' "$*"; }
err() { printf 'error: %s\n' "$*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || err "missing required command: $1"; }

need uname
need tar
need curl

# --- platform detection ---

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$arch" in
  x86_64 | amd64) arch="amd64" ;;
  aarch64 | arm64) arch="arm64" ;;
  *) err "unsupported architecture: $arch" ;;
esac
case "$os" in
  darwin | linux) ;;
  *) err "unsupported operating system: $os" ;;
esac
target="${os}-${arch}"

# --- resolve version ---

version="${AVEN_VERSION:-latest}"
if [ "$version" = "latest" ]; then
  version=$(curl -fsSLI -o /dev/null -w '%{url_effective}' \
    "https://github.com/${REPO}/releases/latest" | sed 's|.*/tag/||')
fi
[ -n "$version" ] || err "could not determine latest release"

asset="aven-${target}.tar.gz"
base_url="https://github.com/${REPO}/releases/download/${version}"

# --- download + verify ---

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

say "downloading ${NAME} ${version} (${target})…"
curl -fsSL "$base_url/$asset" -o "$tmp/$asset"
curl -fsSL "$base_url/checksums.txt" -o "$tmp/checksums.txt"

expected=$(grep " $asset\$" "$tmp/checksums.txt" | cut -d' ' -f1)
if [ -z "$expected" ]; then
  err "checksum for $asset not found in checksums.txt"
fi
if command -v shasum >/dev/null 2>&1; then
  actual=$(shasum -a 256 "$tmp/$asset" | cut -d' ' -f1)
elif command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$tmp/$asset" | cut -d' ' -f1)
else
  err "need shasum or sha256sum to verify the checksum"
fi
if [ "$actual" != "$expected" ]; then
  err "checksum mismatch: want $expected, got $actual"
fi

tar -xzf "$tmp/$asset" -C "$tmp"

# --- install ---

if [ -w /usr/local/bin ] || [ "$(id -u)" = "0" ]; then
  dest="/usr/local/bin"
else
  dest="$HOME/.local/bin"
  mkdir -p "$dest"
  case ":$PATH:" in
    *":$dest:"*) ;;
    *) say "note: add $dest to your PATH (export PATH=\"\$HOME/.local/bin:\$PATH\")" ;;
  esac
fi

mv "$tmp/aven" "$dest/$NAME"
chmod +x "$dest/$NAME"

version_out=$("$dest/$NAME" --version 2>/dev/null || echo "$NAME")
say "installed: $version_out"
say "next: $NAME setup   (one password dialog, then zero prompts)"
