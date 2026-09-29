#!/usr/bin/env bash
# Builds the hive's Secret from an existing SuperAI install and applies it to
# the cluster, without the values ever reaching a terminal or a file here.
#
#   SUPERAI_PASSWORD_HASH='$2a$…' ./deploy/k3s/hive-secret.sh [rings]      default 2
#
# The password hash (bcrypt) is optional; without it only the bearer token
# gets in. Rings change the token every run, so restart the pods afterwards.
#
# Model and CortexDB credentials are copied from the SuperAI on the apps VM;
# the bearer token shared by the hive is generated fresh. The supreme ring's
# settings name every ring; the rings' name none.
set -euo pipefail
RINGS="${1:-2}"
export SUPERAI_PASSWORD_HASH="${SUPERAI_PASSWORD_HASH:-}"
SRC="${SUPERAI_SRC:-ops@192.168.123.65}"
KUBE="${SUPERAI_KUBE:-orange1}"
NS=superai

ssh "$SRC" "RINGS=$RINGS PW='$SUPERAI_PASSWORD_HASH' python3 - <<'PY'
import json, os, secrets
s = json.load(open('/opt/superai/data/settings.json'))
token = secrets.token_hex(24)
base = {k: s[k] for k in ('llm_base_url','llm_key','llm_model','embed_base_url','embed_key','embed_model',
                          'searxng_url') if k in s}
base.update(memory_backend='shared', shared_memory_endpoint=s['shared_memory_endpoint'],
            shared_memory_namespace='hive', max_rounds=-1, headless=True, disable_browser=True,
            workspace_dir='/data/workspace',
            # Off: the default starts an embedded proxy with no accounts in it and
            # routes the model through that, which answers "unknown provider".
            cliproxy_enabled=False)
n = int(os.environ['RINGS'])
rings = {f'ring-{i}': {'about': f'ring-{i} — a worker SuperAI in the hive, with its own tools and the shared memory.',
                       'url': f'http://superai-ring-{i}.superai-ring.$NS.svc.cluster.local:43117',
                       'token': token} for i in range(n)}
supreme = dict(base, remote_agents={'enabled': True, 'agents': rings})
# Rings act with nobody at a keyboard to approve a tool call, so the gate would
# only make every command hang. The pod's Role is what bounds them instead.
ring = dict(base, disable_tool_approval=True)
out = {'token': token, 'cortexdb_token': s['shared_memory_token'],
                  'settings-supreme.json': json.dumps(supreme), 'settings-ring.json': json.dumps(ring)}
if os.environ.get('PW'): out['password_hash'] = os.environ['PW']
print(json.dumps(out))
PY" | ssh "$KUBE" "python3 -c '
import json, subprocess, sys, tempfile, os
d = json.load(sys.stdin)
with tempfile.TemporaryDirectory() as t:
    args = []
    for k, v in d.items():
        p = os.path.join(t, k); open(p, \"w\").write(v); args += [\"--from-file=\" + k + \"=\" + p]
    yaml = subprocess.run([\"sudo\", \"k3s\", \"kubectl\", \"-n\", \"$NS\", \"create\", \"secret\", \"generic\", \"superai-hive\", \"--dry-run=client\", \"-o\", \"yaml\"] + args, check=True, capture_output=True).stdout
    subprocess.run([\"sudo\", \"k3s\", \"kubectl\", \"apply\", \"-f\", \"-\"], input=yaml, check=True)
'"
