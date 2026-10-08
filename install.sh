#!/bin/sh

set -eu

PREFIX=${PREFIX:-"$HOME/.local"}
BIN_DIR=${BIN_DIR:-"$PREFIX/bin"}
VERSION=${LZPODY_VERSION:-${PODMAN_TUI_VERSION:-latest}}

case "$(uname -s)" in
  Linux) OS=linux ;;
  *) echo "lzpody installer: only Linux is currently supported" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  armv7l|armv7) ARCH=armv7 ;;
  *) echo "lzpody installer: unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

if [ "$VERSION" = "latest" ]; then
  DEFAULT_RELEASE_BASE_URL="https://github.com/roubilibo/lzpody/releases/latest/download"
else
  DEFAULT_RELEASE_BASE_URL="https://github.com/roubilibo/lzpody/releases/download/$VERSION"
fi
RELEASE_BASE_URL=${LZPODY_RELEASE_BASE_URL:-$DEFAULT_RELEASE_BASE_URL}
RELEASE_BASE_URL=${RELEASE_BASE_URL%/}
CUSTOM_BINARY=0
if [ -n "${LZPODY_BINARY_URL:-}" ]; then
  BINARY_URL=$LZPODY_BINARY_URL
  CUSTOM_BINARY=1
elif [ -n "${LZPODY_BASE_URL:-${PODMAN_TUI_BASE_URL:-}}" ]; then
  BASE_URL=${LZPODY_BASE_URL:-${PODMAN_TUI_BASE_URL:-}}
  RELEASE_BASE_URL=${BASE_URL%/}
  BINARY_URL="$RELEASE_BASE_URL/lzpody-$OS-$ARCH"
else
  BINARY_URL="$RELEASE_BASE_URL/lzpody-$OS-$ARCH"
fi
CHECKSUM_URL=${LZPODY_CHECKSUM_URL:-"$RELEASE_BASE_URL/SHA256SUMS"}

command -v curl >/dev/null 2>&1 || {
  echo "lzpody installer: curl is required" >&2
  exit 1
}
tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/lzpody.XXXXXX")
stage_file=
cleanup() {
  rm -rf "$tmp_dir"
  if [ -n "$stage_file" ]; then
    rm -f "$stage_file"
  fi
}
trap cleanup EXIT INT TERM

if ! curl --fail --silent --show-error --location "$BINARY_URL" -o "$tmp_dir/lzpody"; then
  echo "lzpody installer: could not download $BINARY_URL" >&2
  echo "Set LZPODY_BINARY_URL to a matching Go build, or publish a release asset." >&2
  exit 1
fi
chmod 0755 "$tmp_dir/lzpody"

if [ -n "${LZPODY_CHECKSUM_URL:-}" ] || [ "$CUSTOM_BINARY" -eq 0 ]; then
  if ! curl --fail --silent --show-error --location "$CHECKSUM_URL" -o "$tmp_dir/SHA256SUMS"; then
    echo "lzpody installer: could not download checksum manifest $CHECKSUM_URL" >&2
    exit 1
  fi
  binary_name=${BINARY_URL%%\?*}
  binary_name=${binary_name##*/}
  if [ -z "$binary_name" ]; then
    binary_name="lzpody-$OS-$ARCH"
  fi
  expected_checksum=$(awk -v name="$binary_name" '$2 == name || substr($2, 2) == name { print $1; exit }' "$tmp_dir/SHA256SUMS")
  if [ -z "$expected_checksum" ]; then
    echo "lzpody installer: checksum for $binary_name not found in manifest" >&2
    exit 1
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    actual_checksum=$(sha256sum "$tmp_dir/lzpody" | awk '{print $1}')
  elif command -v shasum >/dev/null 2>&1; then
    actual_checksum=$(shasum -a 256 "$tmp_dir/lzpody" | awk '{print $1}')
  else
    echo "lzpody installer: sha256sum or shasum is required to verify the download" >&2
    exit 1
  fi
  if [ "$actual_checksum" != "$expected_checksum" ]; then
    echo "lzpody installer: checksum verification failed" >&2
    exit 1
  fi
elif [ "$CUSTOM_BINARY" -eq 1 ]; then
  echo "lzpody installer: custom binary URL has no checksum; set LZPODY_CHECKSUM_URL to verify it" >&2
fi

version_output=$("$tmp_dir/lzpody" --version 2>/dev/null) || {
  echo "lzpody installer: downloaded file is not a working lzpody binary" >&2
  exit 1
}
case "$version_output" in
  "lzpody "*) ;;
  *) echo "lzpody installer: unexpected binary version output: $version_output" >&2; exit 1 ;;
esac
if [ "$VERSION" != "latest" ] && [ "$CUSTOM_BINARY" -eq 0 ] && [ "$version_output" != "lzpody $VERSION" ]; then
  echo "lzpody installer: expected version $VERSION, received $version_output" >&2
  exit 1
fi

mkdir -p "$BIN_DIR"
stage_file="$BIN_DIR/.lzpody.$$"
install -m 0755 "$tmp_dir/lzpody" "$stage_file"
mv -f "$stage_file" "$BIN_DIR/lzpody"
stage_file=

if [ -x "$BIN_DIR/lzpody" ]; then
  echo "Installed/updated $version_output at $BIN_DIR/lzpody"
fi
case ":${PATH:-}:" in
  *:"$BIN_DIR":*) ;;
  *) echo "Add $BIN_DIR to PATH if 'lzpody' is not found." ;;
esac
