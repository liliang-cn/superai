#!/usr/bin/env bash
# Build the two headless binaries and put them on the apps VM.
#
# This existed only as a sequence of commands typed by hand, which is how the
# box ended up running a build two commits behind the repo for most of a day.
#
#   ./scripts/deploy-apps.sh            build, upload, restart, verify
#   ./scripts/deploy-apps.sh --check    report what is deployed vs. what is here
#   ./scripts/deploy-apps.sh --rollback restore the previous binaries
#
# Both entry points cross-compile with CGO off: the serve path never touches
# Wails' GUI dependencies, so no webkit2gtk is needed on the target.
set -euo pipefail
cd "$(dirname "$0")/.."

HOST="${SUPERAI_DEPLOY_HOST:-ops@192.168.123.65}"
REMOTE_BIN="${SUPERAI_REMOTE_BIN:-/opt/superai/bin}"
UNITS="superai superai-daemon"
# The reverse proxy in front of the service on the VM itself.
HEALTH_URL="${SUPERAI_HEALTH_URL:-http://192.168.123.65:43118/}"

export GOWORK=off

say() { printf '\n=== %s ===\n' "$1"; }

deployed_version() {
  ssh "$HOST" "stat -c '%y' $REMOTE_BIN/superai-desktop 2>/dev/null | cut -d. -f1" || true
}

case "${1:-deploy}" in
--check)
  say "local"
  git log -1 --format='%h %ad %s' --date=iso-local
  [ -n "$(git status --porcelain)" ] && echo "(working tree has uncommitted changes)"
  say "deployed"
  echo "binary built: $(deployed_version)"
  ssh "$HOST" "systemctl is-active $UNITS" || true
  exit 0
  ;;
--rollback)
  say "rolling back"
  ssh "$HOST" "
    set -e
    cd $REMOTE_BIN
    prev=\$(ls -t superai-desktop.prev-* 2>/dev/null | head -1)
    [ -n \"\$prev\" ] || { echo 'no previous build to roll back to'; exit 1; }
    cp \"\$prev\" superai-desktop.rollback && mv superai-desktop.rollback superai-desktop
    prevd=\$(ls -t superai-daemon.prev-* 2>/dev/null | head -1)
    [ -n \"\$prevd\" ] && { cp \"\$prevd\" superai-daemon.rollback && mv superai-daemon.rollback superai-daemon; }
    sudo systemctl restart $UNITS
    echo \"rolled back to \$prev\"
  "
  exit 0
  ;;
esac

say "frontend"
# The SPA is embedded in the serve binary, so it has to be built first or the
# upload ships the previous UI with the new backend. It also has to exist
# before `go test`: the root package embeds frontend/dist, and on a fresh
# checkout — a release worktree, a CI runner — there is no dist yet, so the
# tests failed at setup before a single test ran.
(cd frontend && npm run build)

say "tests"
go test ./...

say "cross-compile linux/amd64"
mkdir -p build/bin
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o build/bin/superai-desktop-linux .
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o build/bin/superai-daemon-linux ./cmd/superai-daemon
ls -la build/bin/superai-*-linux

say "upload"
scp -q build/bin/superai-desktop-linux build/bin/superai-daemon-linux "$HOST:/tmp/"

say "install and restart"
# Replacement is a rename, never a copy over the running file: writing into a
# busy binary is ETXTBSY. Restart, never stop-then-start — on a cluster node a
# stop is what a failover watchdog is watching for.
ssh "$HOST" "
  set -e
  ts=\$(date +%Y%m%d%H%M%S)
  cp $REMOTE_BIN/superai-desktop $REMOTE_BIN/superai-desktop.prev-\$ts
  cp $REMOTE_BIN/superai-daemon  $REMOTE_BIN/superai-daemon.prev-\$ts
  chmod +x /tmp/superai-desktop-linux /tmp/superai-daemon-linux
  mv /tmp/superai-desktop-linux $REMOTE_BIN/superai-desktop
  mv /tmp/superai-daemon-linux  $REMOTE_BIN/superai-daemon
  sudo systemctl restart $UNITS
  # Keep the three most recent rollback targets; the binaries are ~115MB each.
  ls -t $REMOTE_BIN/superai-desktop.prev-* 2>/dev/null | tail -n +4 | xargs -r rm -f
  ls -t $REMOTE_BIN/superai-daemon.prev-*  2>/dev/null | tail -n +4 | xargs -r rm -f
"

say "verify"
ssh "$HOST" "systemctl is-active $UNITS"

# Wait for the port, do not guess how long the service takes. This was
# `sleep 3` and one curl, and the service needs about four seconds to get from
# "Started" to "serving on 127.0.0.1:43117" — it opens its store, its scheduler
# and its MCP children first. So a deployment that worked reported "service did
# not come back healthy" and exited 1, which is the kind of false alarm that
# teaches people to stop reading the check.
deadline=$(( $(date +%s) + 60 ))
code=000
while [ "$(date +%s)" -lt "$deadline" ]; do
  code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 "$HEALTH_URL" || echo 000)
  [ "$code" = "200" ] && break
  sleep 2
done
echo "GET $HEALTH_URL -> $code"
[ "$code" = "200" ] || { echo "service did not come back healthy within 60s"; exit 1; }

# A start that logs a configuration problem is not a successful deploy, even
# though the port answers.
if ssh "$HOST" "journalctl -u superai --since '-1 min' --no-pager" | grep -qi "No embedding provider configured"; then
  echo "WARNING: embeddings are not configured — vector memory and RAG are off"
fi

echo
echo "deployed $(git log -1 --format='%h %s')"
