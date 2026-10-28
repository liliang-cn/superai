#!/usr/bin/env bash
# Build SuperLeo.exe for 64-bit Windows and its installer, SuperLeo-Setup.exe,
# on a Mac or Linux machine. Needs makensis (brew install makensis).
# Output: build/bin/SuperLeo.exe and build/bin/SuperLeo-Setup.exe.
#
# The installer puts SuperLeo in Program Files with a Start menu entry, and
# installs Microsoft's WebView2 runtime first on the rare Windows without it.
# Neither file is code-signed: Windows SmartScreen warns before the first run.
set -euo pipefail
cd "$(dirname "$0")/.."
. scripts/version.sh

echo "==> wails build (windows/amd64) + installer"
wails build -clean -platform windows/amd64 -nsis

mv -f build/bin/SuperLeo-amd64-installer.exe build/bin/SuperLeo-Setup.exe
echo "==> done"
ls -l build/bin/SuperLeo.exe build/bin/SuperLeo-Setup.exe
