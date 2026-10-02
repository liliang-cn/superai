#!/bin/sh
# Installs this machine as a SuperAI agent node: SuperAI runs headless as a
# login service and another SuperAI links it with a pairing code to drive the
# Claude Code / Codex installed here.
#
#   curl -fsSL https://raw.githubusercontent.com/liliang-cn/superai/main/scripts/install-node.sh | sh
#
# Options pass through to `node install`, e.g. `| sh -s -- -roots ~/code`.
set -eu

repo=liliang-cn/superai
os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) echo "unsupported CPU: $(uname -m)" >&2; exit 1 ;;
esac
case "$os" in
  darwin | linux) ;;
  *) echo "unsupported system: $os" >&2; exit 1 ;;
esac

asset="superai-$os-$arch"
url="https://github.com/$repo/releases/latest/download"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "downloading $asset"
curl -fsSL -o "$tmp/$asset" "$url/$asset"
curl -fsSL -o "$tmp/SHA256SUMS" "$url/SHA256SUMS"
want=$(grep " $asset\$" "$tmp/SHA256SUMS" | cut -d' ' -f1)
if command -v sha256sum >/dev/null 2>&1; then
  got=$(sha256sum "$tmp/$asset" | cut -d' ' -f1)
else
  got=$(shasum -a 256 "$tmp/$asset" | cut -d' ' -f1)
fi
if [ -z "$want" ] || [ "$want" != "$got" ]; then
  echo "checksum mismatch for $asset" >&2
  exit 1
fi
chmod +x "$tmp/$asset"
# A download is quarantined on macOS; the copy node install makes is what runs.
[ "$os" = darwin ] && xattr -d com.apple.quarantine "$tmp/$asset" 2>/dev/null || true

"$tmp/$asset" node install "$@"
