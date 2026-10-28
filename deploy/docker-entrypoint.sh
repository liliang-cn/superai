#!/bin/sh
# Writes auth.json and settings.json from the environment on every start.
#
# In a cluster the Secret is the source of truth, so the environment wins: a
# rotated token that only applied to a fresh volume would leave every existing
# pod refusing the rest of the hive. What is kept is the session key, so a
# restart does not log browsers out. With no env set, files on the volume are
# used exactly as they are — the plain `docker run -v` case.
set -eu
home="${SUPERLEO_HOME:-/data}"
mkdir -p "$home"
umask 077

if [ -n "${SUPERLEO_TOKEN:-}" ]; then
  key=""
  if [ -f "$home/auth.json" ]; then
    key=$(sed -n 's/.*"session_key" *: *"\([^"]*\)".*/\1/p' "$home/auth.json" | head -1)
  fi
  [ -n "$key" ] || key=$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')
  cat > "$home/auth.json" <<JSON
{"user":"${SUPERLEO_USER:-superleo}","password_hash":"${SUPERLEO_PASSWORD_HASH:-}","token":"${SUPERLEO_TOKEN}","session_key":"$key"}
JSON
fi

# Settings a client changes at run time and that must outlive this rewrite are
# kept beside it in $home and laid over it on load: queen.json (her name),
# queenengine.json (her engine, fallback, public URL), agentlink.json (relays)
# and linked.json (other SuperLeos). Nothing here touches them.
if [ -n "${SUPERLEO_SETTINGS_JSON:-}" ]; then
  printf '%s' "$SUPERLEO_SETTINGS_JSON" > "$home/settings.json"
fi

# MCP servers the cluster provides. SuperLeo reads two files; this is the one
# under data/, so what the UI or the agent installs (the other) is left alone.
if [ -n "${SUPERLEO_MCP_JSON:-}" ]; then
  mkdir -p "$home/data"
  printf '%s' "$SUPERLEO_MCP_JSON" > "$home/data/mcpServers.json"
fi

chown -R superleo:superleo "$home"
exec su-exec superleo "$@"
