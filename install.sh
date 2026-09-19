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
if [ -n "${LZPODY_BINARY_URL:-}" ]; then
  BINARY_URL=$LZPODY_BINARY_URL
elif [ -n "${LZPODY_BASE_URL:-${PODMAN_TUI_BASE_URL:-}}" ]; then
  BASE_URL=${LZPODY_BASE_URL:-${PODMAN_TUI_BASE_URL:-}}
  BINARY_URL="${BASE_URL%/}/lzpody-$OS-$ARCH"
else
  BINARY_URL="$RELEASE_BASE_URL/lzpody-$OS-$ARCH"
fi

command -v curl >/dev/null 2>&1 || {
  echo "lzpody installer: curl is required" >&2
  exit 1
}
tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/lzpody.XXXXXX")
trap 'rm -rf "$tmp_dir"' EXIT INT TERM

if ! curl --fail --silent --show-error --location "$BINARY_URL" -o "$tmp_dir/lzpody"; then
  echo "lzpody installer: could not download $BINARY_URL" >&2
  echo "Set LZPODY_BINARY_URL to a matching Go build, or publish a release asset." >&2
  exit 1
fi

mkdir -p "$BIN_DIR"
install -m 0755 "$tmp_dir/lzpody" "$BIN_DIR/lzpody"

echo "Installed lzpody to $BIN_DIR/lzpody"
case ":${PATH:-}:" in
  *:"$BIN_DIR":*) ;;
  *) echo "Add $BIN_DIR to PATH if 'lzpody' is not found." ;;
esac
