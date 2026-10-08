#!/bin/sh

set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/lzpody-install-test.XXXXXX")
trap 'rm -rf "$tmp_dir"' EXIT INT TERM

case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  armv7l|armv7) arch=armv7 ;;
  *) echo "unsupported test architecture: $(uname -m)" >&2; exit 1 ;;
esac

release_dir="$tmp_dir/release"
bin_dir="$tmp_dir/bin"
mkdir -p "$release_dir"
asset="$release_dir/lzpody-linux-$arch"

write_binary() {
  version=$1
  cat > "$asset" <<EOF
#!/bin/sh
if [ "\${1:-}" = "--version" ]; then
  printf '%s\n' 'lzpody $version'
  exit 0
fi
exit 0
EOF
  chmod 0755 "$asset"
}

write_binary v1.2.3
(cd "$release_dir" && sha256sum "lzpody-linux-$arch" > SHA256SUMS)
LZPODY_VERSION=v1.2.3 LZPODY_RELEASE_BASE_URL="file://$release_dir" BIN_DIR="$bin_dir" sh "$repo_dir/install.sh" >/dev/null
[ "$("$bin_dir/lzpody" --version)" = "lzpody v1.2.3" ]

write_binary v1.2.4
(cd "$release_dir" && sha256sum "lzpody-linux-$arch" > SHA256SUMS)
LZPODY_VERSION=v1.2.4 LZPODY_RELEASE_BASE_URL="file://$release_dir" BIN_DIR="$bin_dir" sh "$repo_dir/install.sh" >/dev/null
[ "$("$bin_dir/lzpody" --version)" = "lzpody v1.2.4" ]

write_binary v1.2.5
if LZPODY_VERSION=v1.2.6 LZPODY_RELEASE_BASE_URL="file://$release_dir" BIN_DIR="$bin_dir" sh "$repo_dir/install.sh" >/dev/null 2>&1; then
  echo "installer accepted a binary with an invalid checksum" >&2
  exit 1
fi
[ "$("$bin_dir/lzpody" --version)" = "lzpody v1.2.4" ]

write_binary v1.2.6
(cd "$release_dir" && sha256sum "lzpody-linux-$arch" > SHA256SUMS)
if LZPODY_VERSION=v1.2.7 LZPODY_RELEASE_BASE_URL="file://$release_dir" BIN_DIR="$bin_dir" sh "$repo_dir/install.sh" >/dev/null 2>&1; then
  echo "installer accepted a binary with the wrong pinned version" >&2
  exit 1
fi
[ "$("$bin_dir/lzpody" --version)" = "lzpody v1.2.4" ]

asset="$release_dir/custom-build"
write_binary v1.2.8
(cd "$release_dir" && sha256sum custom-build > SHA256SUMS)
LZPODY_BINARY_URL="file://$asset" LZPODY_CHECKSUM_URL="file://$release_dir/SHA256SUMS" BIN_DIR="$bin_dir" sh "$repo_dir/install.sh" >/dev/null
[ "$("$bin_dir/lzpody" --version)" = "lzpody v1.2.8" ]

echo "installer install/update/checksum tests passed"
