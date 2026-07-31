#!/usr/bin/env bash
# Install (or remove) the launchd job that keeps SuperAI's scheduled prompts
# running while the app is closed.
#
# The schedules themselves live in the shared database; this only decides whether
# something is up to fire them. The daemon takes an advisory lock, so having both
# it and the app running does not double-execute anything — whoever starts first
# owns the timers.
#
#   ./scripts/install-daemon.sh            install and start
#   ./scripts/install-daemon.sh --uninstall
#   ./scripts/install-daemon.sh --status
set -euo pipefail
cd "$(dirname "$0")/.."

LABEL="com.superleo.superai.scheduler"
PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"
BIN_DIR="$HOME/.superai-desktop/bin"
BIN="$BIN_DIR/superai-daemon"
LOG="$HOME/.superai-desktop/daemon.log"

case "${1:-install}" in
--uninstall)
  launchctl bootout "gui/$(id -u)/$LABEL" 2>/dev/null || launchctl unload "$PLIST" 2>/dev/null || true
  rm -f "$PLIST"
  echo "removed $LABEL (the binary and your schedules are left in place)"
  exit 0
  ;;
--status)
  if launchctl print "gui/$(id -u)/$LABEL" >/dev/null 2>&1; then
    echo "loaded:"
    launchctl print "gui/$(id -u)/$LABEL" | grep -E "^\s+(state|pid|last exit status) =" || true
  else
    echo "not loaded"
  fi
  echo "--- schedules ---"
  "$BIN" --list 2>/dev/null || echo "(daemon not installed yet)"
  exit 0
  ;;
esac

echo "==> building"
# The daemon is installed to its own directory rather than run from build/bin,
# because a `make clean` or a rebuild would otherwise pull the binary out from
# under a running launchd job.
GOWORK=off go build -o "$BIN" ./cmd/superai-daemon
mkdir -p "$BIN_DIR"

echo "==> writing $PLIST"
mkdir -p "$(dirname "$PLIST")"
cat >"$PLIST" <<PLIST_EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>$LABEL</string>
	<key>ProgramArguments</key>
	<array>
		<string>$BIN</string>
		<string>--log</string>
		<string>$LOG</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<!-- Restart if it dies, but not if it exited cleanly because the app
		     already owns the timers. -->
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>ProcessType</key>
	<string>Background</string>
	<key>StandardErrorPath</key>
	<string>$LOG</string>
	<key>WorkingDirectory</key>
	<string>$HOME</string>
	<key>EnvironmentVariables</key>
	<dict>
		<!-- Nothing here browses, and launching Chrome costs a second per start. -->
		<key>SUPERAI_NO_BROWSER</key>
		<string>1</string>
		<!-- launchd gives a job a minimal PATH; npx/uvx-based MCP servers and
		     git-based skill installs need the usual places. -->
		<key>PATH</key>
		<string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
	</dict>
</dict>
</plist>
PLIST_EOF

echo "==> loading"
launchctl bootout "gui/$(id -u)/$LABEL" 2>/dev/null || true
launchctl bootstrap "gui/$(id -u)" "$PLIST"

sleep 1
echo "==> status"
launchctl print "gui/$(id -u)/$LABEL" | grep -E "^\s+(state|pid|last exit status) =" || true
echo
echo "logs:      tail -f $LOG"
echo "schedules: $BIN --list"
echo "test one:  $BIN --once"
