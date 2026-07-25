#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${TUNLEASE_BASE_URL:-https://github.com/iml885203/tunlease/releases/latest/download}"
TMP=""
SUMS=""
trap 'rm -f "${TMP:-}" "${SUMS:-}"' EXIT

platform() {
  local os arch
  case "$(uname -s)" in Darwin) os=darwin;; Linux) os=linux;; MINGW*|MSYS*|CYGWIN*) os=windows;; *) echo "unsupported OS" >&2; exit 1;; esac
  case "$(uname -m)" in x86_64|amd64) arch=amd64;; arm64|aarch64) arch=arm64;; *) echo "unsupported architecture" >&2; exit 1;; esac
  echo "$os-$arch"
}

verify() {
  local file=$1 sums=$2 expected actual
  expected=$(awk '{print $1}' "$sums")
  if command -v sha256sum >/dev/null 2>&1; then actual=$(sha256sum "$file" | awk '{print $1}'); else actual=$(shasum -a 256 "$file" | awk '{print $1}'); fi
  [ "$actual" = "$expected" ] || { echo "checksum mismatch" >&2; exit 1; }
}

main() {
  local p name dir target tmp sums
  p=$(platform); name="tunle-$p"; [[ "$p" == windows-* ]] && name="$name.exe"
  dir="${TUNLEASE_INSTALL_DIR:-$HOME/.local/bin}"; mkdir -p "$dir"; target="$dir/tunle"; [[ "$p" == windows-* ]] && target="$target.exe"
  tmp=$(mktemp); sums=$(mktemp); TMP=$tmp; SUMS=$sums
  curl -fsSL "$BASE_URL/$name" -o "$tmp"; curl -fsSL "$BASE_URL/$name.sha256" -o "$sums"; verify "$tmp" "$sums"; chmod +x "$tmp"
  [ ! -f "$target" ] || cp -p "$target" "$target.prev"
  mv "$tmp" "$target"; echo "Installed: $target"; "$target" --version
  case ":$PATH:" in *":$dir:"*) ;; *) echo "Add $dir to PATH.";; esac
}
main "$@"
