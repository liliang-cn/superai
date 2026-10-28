#!/usr/bin/env bash
# Builds the hive's Secret from an existing SuperLeo install and applies it to
# the cluster, without the values ever reaching a terminal or a file here.
#
#   ./deploy/k3s/hive-secret.sh
#
# The login comes from the SuperLeo on the apps VM: its user, password hash and
# bearer token. The queen is what https://ai.superleo.cn serves, so the same
# password and the same token keep working after the switch — MCP clients
# included. SUPERLEO_PASSWORD_HASH='$2a$…' overrides the password; with
# SUPERLEO_NEW_TOKEN=1 the token is generated fresh instead, and then the pods
# need a restart and every client a new token.
#
# Model and CortexDB credentials are copied from the same place.
# SUPERLEO_LLM_MODEL='gemini-3.8-flash' runs the hive on another of the gateway's
# models — for when the usual one has no account behind it. To run it on
# another provider altogether, SUPERLEO_LLM_BASE_URL='https://api.deepseek.com'
# with SUPERLEO_LLM_KEY_ENV=DEEPSEEK_API_KEY (the name of a variable in this
# shell holding the key; the key goes through a pipe, never an argument) and
# SUPERLEO_LLM_MODEL='deepseek-flash'. How much the model holds is optional
# and stated, never looked up by name: SUPERLEO_LLM_CONTEXT_TOKENS=1000000 and
# SUPERLEO_LLM_MAX_OUTPUT_TOKENS=384000 (the provider's own page says); unset,
# the hive keeps whatever the source settings say, and with neither agent-go
# compacts at a fixed 60k. SUPERLEO_LLM_REASONING_EFFORT=low sends that
# reasoning_effort on every model call: on the gateway's thinking models it
# is most of a turn's wait. Roles come from the settings: the queen accepts
# joins and the workers announce themselves.
set -euo pipefail
export SUPERLEO_PASSWORD_HASH="${SUPERLEO_PASSWORD_HASH:-}"
export SUPERLEO_NEW_TOKEN="${SUPERLEO_NEW_TOKEN:-}"
export SUPERLEO_LLM_MODEL="${SUPERLEO_LLM_MODEL:-}"
export SUPERLEO_LLM_BASE_URL="${SUPERLEO_LLM_BASE_URL:-}"
export SUPERLEO_LLM_KEY_ENV="${SUPERLEO_LLM_KEY_ENV:-}"
export SUPERLEO_LLM_CONTEXT_TOKENS="${SUPERLEO_LLM_CONTEXT_TOKENS:-}"
export SUPERLEO_LLM_MAX_OUTPUT_TOKENS="${SUPERLEO_LLM_MAX_OUTPUT_TOKENS:-}"
export SUPERLEO_LLM_REASONING_EFFORT="${SUPERLEO_LLM_REASONING_EFFORT:-}"
if [ -n "$SUPERLEO_LLM_BASE_URL" ] && { [ -z "$SUPERLEO_LLM_KEY_ENV" ] || [ -z "${!SUPERLEO_LLM_KEY_ENV:-}" ]; }; then
  echo "SUPERLEO_LLM_BASE_URL needs SUPERLEO_LLM_KEY_ENV naming a set variable" >&2; exit 1
fi
SRC="${SUPERLEO_SRC:-ops@192.168.123.65}"
KUBE="${SUPERLEO_KUBE:-orange1}"
NS=superleo

ssh "$SRC" "PW='$SUPERLEO_PASSWORD_HASH' NEW='$SUPERLEO_NEW_TOKEN' MODEL='$SUPERLEO_LLM_MODEL' sudo -E python3 - <<'PY'
import json, os, secrets
s = json.load(open('/opt/superleo/data/settings.json'))
a = json.load(open('/opt/superleo/data/auth.json'))
token = secrets.token_hex(24) if os.environ.get('NEW') else a['token']
# The voice too: the console reads answers aloud through /api/tts, and a
# queen without these answers 501 and the console falls back to the browser's
# own voice.
base = {k: s[k] for k in ('llm_base_url','llm_key','llm_model','llm_context_tokens','llm_max_output_tokens',
                          'embed_base_url','embed_key','embed_model',
                          'searxng_url','tts_base_url','tts_key','tts_model','tts_voice') if k in s}
# The hive speaks with Microsoft Edge's Xiaoxiao, through the converter in
# deploy/k3s/edge-tts: quicker and more natural than the gateway's model. The
# standalone install keeps its own voice; only the hive's is overridden here.
if os.environ.get('MODEL'): base['llm_model'] = os.environ['MODEL']
base.update(tts_base_url='http://edge-tts.$NS.svc.cluster.local:43540/v1', tts_model='edge',
            tts_voice='zh-CN-XiaoxiaoNeural', tts_key='')
# Recall embeds every question before it searches. The standalone install's
# embedder is t2m in Tokyo relaying to a laptop — the whole two-second recall
# budget, every turn. The hive uses the one CortexDB uses, on dell, 0.3s away.
base.update(embed_base_url='http://192.168.123.98:43746/v1', embed_key='', embed_model='embeddinggemma:latest')
base.update(memory_backend='shared', shared_memory_endpoint=s['shared_memory_endpoint'],
            shared_memory_namespace='hive', max_rounds=-1, headless=True, disable_browser=True,
            workspace_dir='/data/workspace',
            # Off: the default starts an embedded proxy with no accounts in it and
            # routes the model through that, which answers "unknown provider".
            cliproxy_enabled=False)
# Roles, not a list. The queen accepts joins; a worker announces itself to it
# (superleo-hive/1, see internal/backend/hive.go), so the roster is whoever is
# actually there and adding a worker is raising the replica count.
# The queen may resize the workers' StatefulSet — its own ServiceAccount says how
# far (superleo-operator) and max_workers says how many.
# The queen runs unattended too: orders come from the console, from schedules
# and from other programs as often as from a person at the web page, and a
# shell call she made waited two minutes for an approval nobody was there to
# give. Like the workers, she is bounded by her pod's Role instead.
queen = dict(base, disable_tool_approval=True, hive={'role': 'queen', 'name': 'queen',
                         'spawner': {'kind': 'kubectl', 'namespace': '$NS', 'statefulset': 'superleo-worker', 'max_workers': 20}})
# Workers act with nobody at a keyboard to approve a tool call, so the gate would
# only make every command hang. The pod's Role is what bounds them instead.
worker = dict(base, disable_tool_approval=True,
            hive={'role': 'worker', 'join_url': 'http://superleo-queen.$NS.svc.cluster.local:43117'})
out = {'token': token, 'user': a.get('user') or 'superleo', 'password_hash': a.get('password_hash', ''), 'cortexdb_token': s['shared_memory_token'],
                  'settings-queen.json': json.dumps(queen), 'settings-worker.json': json.dumps(worker)}
if os.environ.get('PW'): out['password_hash'] = os.environ['PW']
# The queen's noted token (minted with `noted token new <user> -label SuperLeo`).
if os.path.exists('/opt/superleo/data/noted_token'):
    out['noted_token'] = open('/opt/superleo/data/noted_token').read().strip()
# The APNs key the queen pushes alarm changes to the iOS app with: the .p8 from
# the developer account, and its key id. Neither is required.
if os.path.exists('/opt/superleo/data/apns_key.p8'):
    out['apns_key'] = open('/opt/superleo/data/apns_key.p8').read().strip() + '\n'
if os.path.exists('/opt/superleo/data/apns_key_id'):
    out['apns_key_id'] = open('/opt/superleo/data/apns_key_id').read().strip()
print(json.dumps(out))
PY" | python3 -c '
import json, os, sys
d = json.load(sys.stdin)
env = os.environ.get
url, window, out = env("SUPERLEO_LLM_BASE_URL"), env("SUPERLEO_LLM_CONTEXT_TOKENS"), env("SUPERLEO_LLM_MAX_OUTPUT_TOKENS")
for k in ("settings-queen.json", "settings-worker.json"):
    s = json.loads(d[k])
    if url:
        # Another provider: its address and key replace the gateway ones, and
        # what the source said its model holds no longer applies.
        s.update(llm_base_url=url, llm_key=env(env("SUPERLEO_LLM_KEY_ENV")))
        s.pop("llm_context_tokens", None)
        s.pop("llm_max_output_tokens", None)
    if window:
        s["llm_context_tokens"] = int(window)
    if out:
        s["llm_max_output_tokens"] = int(out)
    if env("SUPERLEO_LLM_REASONING_EFFORT"):
        s["llm_reasoning_effort"] = env("SUPERLEO_LLM_REASONING_EFFORT")
    d[k] = json.dumps(s)
# The CortexDB tools think with the same model the hive does.
q = json.loads(d["settings-queen.json"])
d.update(cortexdb_llm_base_url=q.get("llm_base_url", ""), cortexdb_llm_model=q.get("llm_model", ""), cortexdb_llm_key=q.get("llm_key", ""))
print(json.dumps(d))
' | ssh "$KUBE" "python3 -c '
import json, subprocess, sys, tempfile, os
d = json.load(sys.stdin)
with tempfile.TemporaryDirectory() as t:
    args = []
    for k, v in d.items():
        p = os.path.join(t, k); open(p, \"w\").write(v); args += [\"--from-file=\" + k + \"=\" + p]
    yaml = subprocess.run([\"sudo\", \"k3s\", \"kubectl\", \"-n\", \"$NS\", \"create\", \"secret\", \"generic\", \"superleo-hive\", \"--dry-run=client\", \"-o\", \"yaml\"] + args, check=True, capture_output=True).stdout
    subprocess.run([\"sudo\", \"k3s\", \"kubectl\", \"apply\", \"-f\", \"-\"], input=yaml, check=True)
'"
