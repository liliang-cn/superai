#!/usr/bin/env bash
# Build SuperAI.exe for 64-bit Windows and its installer, SuperAI-Setup.exe,
# on a Mac or Linux machine. Needs makensis (brew install makensis).
# Output: build/bin/SuperAI.exe and build/bin/SuperAI-Setup.exe.
#
# The installer puts SuperAI in Program Files with a Start menu entry, and
# installs Microsoft's WebView2 runtime first on the rare Windows without it.
# Neither file is code-signed: Windows SmartScreen warns before the first run.
set -euo pipefail
cd "$(dirname "$0")/.."
. scripts/version.sh

echo "==> wails build (windows/amd64) + installer"
wails build -clean -platform windows/amd64 -nsis

mv -f build/bin/SuperAI-amd64-installer.exe build/bin/SuperAI-Setup.exe
echo "==> done"
ls -l build/bin/SuperAI.exe build/bin/SuperAI-Setup.exe
