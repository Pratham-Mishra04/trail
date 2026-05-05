#!/usr/bin/env sh
set -eu

REPO="Pratham-Mishra04/trail"
BIN_DIR="${BIN_DIR:-/usr/local/bin}"
VERSION="${VERSION:-latest}"

uname_s=$(uname -s)
uname_m=$(uname -m)

case "$uname_s" in
  Darwin) os=darwin ;;
  Linux)  os=linux  ;;
  *) echo "trail: unsupported OS: $uname_s" >&2; exit 1 ;;
esac

case "$uname_m" in
  x86_64|amd64)  arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "trail: unsupported arch: $uname_m" >&2; exit 1 ;;
esac

archive="trail_${os}_${arch}.tar.gz"

if [ "$VERSION" = "latest" ]; then
  url="https://github.com/${REPO}/releases/latest/download/${archive}"
else
  url="https://github.com/${REPO}/releases/download/${VERSION}/${archive}"
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

checksums_url="$(dirname "$url")/checksums.txt"

echo "trail: downloading ${url}"
if command -v curl >/dev/null 2>&1; then
  curl -fsSL "$url" -o "$tmp/$archive"
  curl -fsSL "$checksums_url" -o "$tmp/checksums.txt"
elif command -v wget >/dev/null 2>&1; then
  wget -qO "$tmp/$archive" "$url"
  wget -qO "$tmp/checksums.txt" "$checksums_url"
else
  echo "trail: need curl or wget on PATH" >&2; exit 1
fi

echo "trail: verifying checksum"
if command -v sha256sum >/dev/null 2>&1; then
  (cd "$tmp" && grep " $archive\$" checksums.txt | sha256sum -c -)
elif command -v shasum >/dev/null 2>&1; then
  (cd "$tmp" && grep " $archive\$" checksums.txt | shasum -a 256 -c -)
else
  echo "trail: warning: sha256sum/shasum not found, skipping checksum verification" >&2
fi

tar -xzf "$tmp/$archive" -C "$tmp"
chmod +x "$tmp/trail"

if [ -w "$BIN_DIR" ] || { [ ! -e "$BIN_DIR" ] && [ -w "$(dirname "$BIN_DIR")" ]; }; then
  mkdir -p "$BIN_DIR"
  mv "$tmp/trail" "$BIN_DIR/trail"
else
  echo "trail: installing to ${BIN_DIR} (sudo required)"
  command -v sudo >/dev/null 2>&1 || {
    echo "trail: sudo is required to install to ${BIN_DIR}" >&2
    exit 1
  }
  sudo mkdir -p "$BIN_DIR"
  sudo mv "$tmp/trail" "$BIN_DIR/trail"
fi

echo "trail: installed to ${BIN_DIR}/trail"
"$BIN_DIR/trail" version || true
