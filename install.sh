#!/usr/bin/env sh
set -eu

REPO="Pratham-Mishra04/trail"
BIN_DIR="${BIN_DIR:-${HOME}/.local/bin}"
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

mkdir -p "$BIN_DIR" || {
  echo "trail: cannot create ${BIN_DIR}. Set BIN_DIR=<writable-path> and re-run." >&2
  exit 1
}
mv "$tmp/trail" "$BIN_DIR/trail" || {
  echo "trail: cannot write to ${BIN_DIR}. Set BIN_DIR=<writable-path> and re-run." >&2
  exit 1
}

echo "trail: installed to ${BIN_DIR}/trail"

case ":${PATH:-}:" in
  *":${BIN_DIR}:"*)
    "$BIN_DIR/trail" version || true
    ;;
  *)
    echo
    echo "trail: ${BIN_DIR} is not on your PATH. Add it with one of:"
    echo "  export PATH=\"${BIN_DIR}:\$PATH\"                          # current shell only"
    echo "  echo 'export PATH=\"${BIN_DIR}:\$PATH\"' >> ~/.zshrc        # zsh (macOS default)"
    echo "  echo 'export PATH=\"${BIN_DIR}:\$PATH\"' >> ~/.bashrc       # bash"
    echo "Then run: trail version"
    ;;
esac
