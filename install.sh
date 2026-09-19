#!/bin/sh

set -eu

PREFIX=${PREFIX:-"$HOME/.local"}
BIN_DIR=${BIN_DIR:-"$PREFIX/bin"}
VERSION=${LZPODY_VERSION:-${PODMAN_TUI_VERSION:-main}}
BASE_URL=${LZPODY_BASE_URL:-${PODMAN_TUI_BASE_URL:-"https://raw.githubusercontent.com/roubilibo/lzpody/$VERSION"}}
BASE_URL=${BASE_URL%/}

command -v curl >/dev/null 2>&1 || {
  echo "lzpody installer: curl is required" >&2
  exit 1
}
command -v python3 >/dev/null 2>&1 || {
  echo "lzpody installer: python3 is required" >&2
  exit 1
}
python3 - <<'PY'
import sys
if sys.version_info < (3, 11):
    print("lzpody installer: Python 3.11 or newer is required", file=sys.stderr)
    raise SystemExit(1)
PY

tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/lzpody.XXXXXX")
trap 'rm -rf "$tmp_dir"' EXIT INT TERM

curl --fail --silent --show-error --location "$BASE_URL/lzpody.py" -o "$tmp_dir/lzpody.py"
curl --fail --silent --show-error --location "$BASE_URL/lzpody" -o "$tmp_dir/lzpody"

mkdir -p "$BIN_DIR"
install -m 0755 "$tmp_dir/lzpody.py" "$BIN_DIR/lzpody.py"
install -m 0755 "$tmp_dir/lzpody" "$BIN_DIR/lzpody"

echo "Installed lzpody to $BIN_DIR/lzpody"
case ":${PATH:-}:" in
  *:"$BIN_DIR":*) ;;
  *) echo "Add $BIN_DIR to PATH if 'lzpody' is not found." ;;
esac
