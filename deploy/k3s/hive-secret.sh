#!/usr/bin/env bash
# Builds the hive's Secret from an existing SuperAI install and applies it to
# the cluster, without the values ever reaching a terminal or a file here.
#
#   ./deploy/k3s/hive-secret.sh
#
# The login comes from the SuperAI on the apps VM: its user, password hash and
# bearer token. The queen is what https://ai.superleo.cn serves, so the same
# password and the same token keep working after the switch — MCP clients
# included. SUPERAI_PASSWORD_HASH='$2a$…' overrides the password; with
# SUPERAI_NEW_TOKEN=1 the token is generated fresh instead, and then the pods
# need a restart and every client a new token.
#
# Model and CortexDB credentials are copied from the same place. Roles come from the
# settings: the queen accepts joins and the workers announce themselves.
set -euo pipefail
export SUPERAI_PASSWORD_HASH="${SUPERAI_PASSWORD_HASH:-}"
export SUPERAI_NEW_TOKEN="${SUPERAI_NEW_TOKEN:-}"
SRC="${SUPERAI_SRC:-ops@192.168.123.65}"
KUBE="${SUPERAI_KUBE:-orange1}"
NS=superai

ssh "$SRC" "PW='$SUPERAI_PASSWORD_HASH' NEW='$SUPERAI_NEW_TOKEN' sudo -E python3 - <<'PY'
import json, os, secrets
s = json.load(open('/opt/superai/data/settings.json'))
a = json.load(open('/opt/superai/data/auth.json'))
token = secrets.token_hex(24) if os.environ.get('NEW') else a['token']
# The voice too: the console reads answers aloud through /api/tts, and a
# queen without these answers 501 and the console falls back to the browser's
# own voice.
base = {k: s[k] for k in ('llm_base_url','llm_key','llm_model','embed_base_url','embed_key','embed_model',
                          'searxng_url','tts_base_url','tts_key','tts_model','tts_voice') if k in s}
# The hive speaks with Microsoft Edge's Xiaoxiao, through the converter in
# deploy/k3s/edge-tts: quicker and more natural than the gateway's model. The
# standalone install keeps its own voice; only the hive's is overridden here.
base.update(tts_base_url='http://edge-tts.$NS.svc.cluster.local:43540/v1', tts_model='edge',
            tts_voice='zh-CN-XiaoxiaoNeural', tts_key='')
base.update(memory_backend='shared', shared_memory_endpoint=s['shared_memory_endpoint'],
            shared_memory_namespace='hive', max_rounds=-1, headless=True, disable_browser=True,
            workspace_dir='/data/workspace',
            # Off: the default starts an embedded proxy with no accounts in it and
            # routes the model through that, which answers "unknown provider".
            cliproxy_enabled=False)
# What the model costs, so a turn's cost is a number and not "unpriced": agent-go
# has no Gemini rates of its own. Gemini 3.8 Flash's introductory prices, per
# 1K tokens, valid through 2026-12-31 (ai.google.dev/gemini-api/docs/pricing);
# from 2027-01-01 they double to 0.0015 / 0.00015 / 0.0075.
if base.get('llm_model', '').startswith('gemini-3.8-flash'):
    base.update(llm_price_input_per_1k=0.00075, llm_price_cached_per_1k=0.000075, llm_price_output_per_1k=0.00375)
    # And what it holds (the model page, ai.google.dev/gemini-api/docs/models):
    # without it the conversation compacts at 60k tokens of a 1M window.
    base.update(llm_context_tokens=1048576, llm_max_output_tokens=65536)
# Roles, not a list. The queen accepts joins; a worker announces itself to it
# (superai-hive/1, see internal/backend/hive.go), so the roster is whoever is
# actually there and adding a worker is raising the replica count.
# The queen may resize the workers' StatefulSet — its own ServiceAccount says how
# far (superai-operator) and max_workers says how many.
# The queen runs unattended too: orders come from the console, from schedules
# and from other programs as often as from a person at the web page, and a
# shell call she made waited two minutes for an approval nobody was there to
# give. Like the workers, she is bounded by her pod's Role instead.
queen = dict(base, disable_tool_approval=True, hive={'role': 'queen', 'name': 'queen',
                         'spawner': {'kind': 'kubectl', 'namespace': '$NS', 'statefulset': 'superai-worker', 'max_workers': 20}})
# Workers act with nobody at a keyboard to approve a tool call, so the gate would
# only make every command hang. The pod's Role is what bounds them instead.
worker = dict(base, disable_tool_approval=True,
            hive={'role': 'worker', 'join_url': 'http://superai-queen.$NS.svc.cluster.local:43117'})
out = {'token': token, 'user': a.get('user') or 'superai', 'password_hash': a.get('password_hash', ''), 'cortexdb_token': s['shared_memory_token'],
                  'settings-queen.json': json.dumps(queen), 'settings-worker.json': json.dumps(worker)}
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
