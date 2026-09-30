#!/usr/bin/env bash
# Builds the Edge TTS image, loads it on every orange node (there is no
# registry) and applies its Deployment and Service.
set -euo pipefail
NODES="${SUPERAI_NODES:-orange1 orange2 orange3}"
KUBE="${SUPERAI_KUBE:-orange1}"
cd "$(dirname "$0")"
docker build --platform linux/amd64 -t edge-tts:hive .
tar="$(mktemp -t edge-tts.XXXXXX)"
trap 'rm -f "$tar"' EXIT
docker save edge-tts:hive -o "$tar"
for n in $NODES; do ssh "$n" 'sudo k3s ctr images import -' < "$tar" >/dev/null & done
wait
scp -q edge-tts.yaml "$KUBE:/tmp/edge-tts.yaml"
ssh "$KUBE" "sudo k3s kubectl apply -f /tmp/edge-tts.yaml && sudo k3s kubectl -n superai rollout restart deploy/edge-tts >/dev/null && sudo k3s kubectl -n superai rollout status deploy/edge-tts --timeout=180s"
