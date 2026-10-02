#!/usr/bin/env bash
# Builds the image and puts it on every node of the orange cluster, then
# restarts the hive onto it.
#
#   ./deploy/k3s/rollout.sh
#
# The cluster has no registry: the image is built here, saved, and imported
# into each node's containerd. The tag stays superai:hive with
# imagePullPolicy IfNotPresent, so a restart is what picks the new one up.
set -euo pipefail
IMAGE="${SUPERAI_IMAGE:-superai:hive}"
NODES="${SUPERAI_NODES:-orange1 orange2 orange3}"
KUBE="${SUPERAI_KUBE:-orange1}"
NS=superai
cd "$(dirname "$0")/../.."

docker build --platform linux/amd64 -t "$IMAGE" .
tar="$(mktemp -t superai-image.XXXXXX)"
trap 'rm -f "$tar"' EXIT
docker save "$IMAGE" -o "$tar"
for n in $NODES; do
  echo "importing on $n"
  ssh "$n" 'sudo k3s ctr images import -' < "$tar" >/dev/null &
done
wait

# Workers first, so the queen comes back to a hive that is already on the new
# version; the queen's roster refills from their next heartbeat either way.
ssh "$KUBE" "sudo k3s kubectl -n $NS rollout restart sts/superai-worker sts/superai-queen >/dev/null &&
  sudo k3s kubectl -n $NS rollout status sts/superai-worker --timeout=600s &&
  sudo k3s kubectl -n $NS rollout status sts/superai-queen --timeout=300s"
