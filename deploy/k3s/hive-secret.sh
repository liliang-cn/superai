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
# Model and CortexDB credentials are copied from the same place.
# SUPERAI_LLM_MODEL='gemini-3.8-flash' runs the hive on another of the gateway's
# models — for when the usual one has no account behind it. To run it on
# another provider altogether, SUPERAI_LLM_BASE_URL='https://api.deepseek.com'
# with SUPERAI_LLM_KEY_ENV=DEEPSEEK_API_KEY (the name of a variable in this
# shell holding the key; the key goes through a pipe, never an argument) and
# SUPERAI_LLM_MODEL='deepseek-flash'. How much the model holds is optional
# and stated, never looked up by name: SUPERAI_LLM_CONTEXT_TOKENS=1000000 and
# SUPERAI_LLM_MAX_OUTPUT_TOKENS=384000 (the provider's own page says); unset,
# the hive keeps whatever the source settings say, and with neither agent-go
# compacts at a fixed 60k. Roles come from the settings: the queen accepts
# joins and the workers announce themselves.
set -euo pipefail
export SUPERAI_PASSWORD_HASH="${SUPERAI_PASSWORD_HASH:-}"
export SUPERAI_NEW_TOKEN="${SUPERAI_NEW_TOKEN:-}"
export SUPERAI_LLM_MODEL="${SUPERAI_LLM_MODEL:-}"
export SUPERAI_LLM_BASE_URL="${SUPERAI_LLM_BASE_URL:-}"
export SUPERAI_LLM_KEY_ENV="${SUPERAI_LLM_KEY_ENV:-}"
export SUPERAI_LLM_CONTEXT_TOKENS="${SUPERAI_LLM_CONTEXT_TOKENS:-}"
export SUPERAI_LLM_MAX_OUTPUT_TOKENS="${SUPERAI_LLM_MAX_OUTPUT_TOKENS:-}"
if [ -n "$SUPERAI_LLM_BASE_URL" ] && { [ -z "$SUPERAI_LLM_KEY_ENV" ] || [ -z "${!SUPERAI_LLM_KEY_ENV:-}" ]; }; then
  echo "SUPERAI_LLM_BASE_URL needs SUPERAI_LLM_KEY_ENV naming a set variable" >&2; exit 1
fi
SRC="${SUPERAI_SRC:-ops@192.168.123.65}"
KUBE="${SUPERAI_KUBE:-orange1}"
NS=superai

ssh "$SRC" "PW='$SUPERAI_PASSWORD_HASH' NEW='$SUPERAI_NEW_TOKEN' MODEL='$SUPERAI_LLM_MODEL' sudo -E python3 - <<'PY'
import json, os, secrets
s = json.load(open('/opt/superai/data/settings.json'))
a = json.load(open('/opt/superai/data/auth.json'))
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
base.update(memory_backend='shared', shared_memory_endpoint=s['shared_memory_endpoint'],
            shared_memory_namespace='hive', max_rounds=-1, headless=True, disable_browser=True,
            workspace_dir='/data/workspace',
            # Off: the default starts an embedded proxy with no accounts in it and
            # routes the model through that, which answers "unknown provider".
            cliproxy_enabled=False)
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
PY" | python3 -c '
import json, os, sys
d = json.load(sys.stdin)
env = os.environ.get
url, window, out = env("SUPERAI_LLM_BASE_URL"), env("SUPERAI_LLM_CONTEXT_TOKENS"), env("SUPERAI_LLM_MAX_OUTPUT_TOKENS")
for k in ("settings-queen.json", "settings-worker.json"):
    s = json.loads(d[k])
    if url:
        # Another provider: its address and key replace the gateway ones, and
        # what the source said its model holds no longer applies.
        s.update(llm_base_url=url, llm_key=env(env("SUPERAI_LLM_KEY_ENV")))
        s.pop("llm_context_tokens", None)
        s.pop("llm_max_output_tokens", None)
    if window:
        s["llm_context_tokens"] = int(window)
    if out:
        s["llm_max_output_tokens"] = int(out)
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
    yaml = subprocess.run([\"sudo\", \"k3s\", \"kubectl\", \"-n\", \"$NS\", \"create\", \"secret\", \"generic\", \"superai-hive\", \"--dry-run=client\", \"-o\", \"yaml\"] + args, check=True, capture_output=True).stdout
    subprocess.run([\"sudo\", \"k3s\", \"kubectl\", \"apply\", \"-f\", \"-\"], input=yaml, check=True)
'"
