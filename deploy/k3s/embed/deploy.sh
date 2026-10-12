#!/usr/bin/env bash
# Builds the embedding image with the Mac's own embeddinggemma files, loads it
# on every orange node (there is no registry) and applies the Deployment and
# Service. Then: OPENAI_BASE_URL=http://<orange node>:31746/v1 for CortexDB.
set -euo pipefail
NODES="${SUPERAI_NODES:-orange1 orange2 orange3}"
KUBE="${SUPERAI_KUBE:-orange1}"
cd "$(dirname "$0")"
src="$HOME/.ollama/models"
manifest="manifests/registry.ollama.ai/library/embeddinggemma/latest"
rm -rf models && mkdir -p "models/$(dirname "$manifest")" models/blobs
cp "$src/$manifest" "models/$manifest"
for d in $(python3 -c "import json,sys; m=json.load(open('$src/$manifest')); print(' '.join(l['digest'] for l in m['layers']+[m['config']]))"); do
  cp "$src/blobs/sha256-${d#sha256:}" models/blobs/
done
docker build --platform linux/amd64 -t embed:hive .
rm -rf models
tar="$(mktemp -t embed.XXXXXX)"
trap 'rm -f "$tar"' EXIT
docker save embed:hive -o "$tar"
for n in $NODES; do ssh "$n" 'sudo k3s ctr images import -' < "$tar" >/dev/null & done
wait
scp -q embed.yaml "$KUBE:/tmp/embed.yaml"
ssh "$KUBE" "sudo k3s kubectl apply -f /tmp/embed.yaml && sudo k3s kubectl -n superai rollout restart deploy/embed >/dev/null && sudo k3s kubectl -n superai rollout status deploy/embed --timeout=300s"
