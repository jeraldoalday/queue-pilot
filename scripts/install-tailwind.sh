#!/usr/bin/env bash
# Downloads the standalone Tailwind CSS CLI binary (no Node/npm required)
# into ./scripts/tailwindcss, matching the current OS/arch.
set -euo pipefail

VERSION="${TAILWIND_VERSION:-latest}"
DEST="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/tailwindcss"

os="$(uname -s)"
arch="$(uname -m)"

case "$os" in
  Darwin) plat="macos" ;;
  Linux) plat="linux" ;;
  *) echo "Unsupported OS: $os" >&2; exit 1 ;;
esac

case "$arch" in
  arm64|aarch64) a="arm64" ;;
  x86_64) a="x64" ;;
  *) echo "Unsupported arch: $arch" >&2; exit 1 ;;
esac

if [ "$VERSION" = "latest" ]; then
  url="https://github.com/tailwindlabs/tailwindcss/releases/latest/download/tailwindcss-${plat}-${a}"
else
  url="https://github.com/tailwindlabs/tailwindcss/releases/download/${VERSION}/tailwindcss-${plat}-${a}"
fi

echo "Downloading $url"
curl -fsSL "$url" -o "$DEST"
chmod +x "$DEST"
echo "Installed to $DEST"
"$DEST" --help >/dev/null && echo "OK: $("$DEST" --help | head -1)"
