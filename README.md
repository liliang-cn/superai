# SuperAI

A personal AI assistant you run yourself. It remembers things, keeps your schedule, and can hand work to Claude Code, Codex and other coding tools on your machines. Its brain is [agent-go](https://github.com/liliang-cn/agent-go).

One program, three ways to run it:

| Mode | Command | What it does |
|---|---|---|
| core | `superai serve` | The assistant itself, with a web UI and an API. Phones and the desktop app talk to it. |
| agent | `superai agent` | Links another machine to the core, so the core can use the coding tools installed there. |
| desktop | `SuperAI.app` | The core in a window (macOS / Windows). |

Several cores can run together as a **hive** on k3s: one queen takes the work and splits it, and workers do the parts in parallel.

Apps: the built-in web UI, the desktop app, and the iOS app ([superai-ios](https://github.com/liliang-cn/superai-ios)).

## Install

Download a binary from [Releases](https://github.com/liliang-cn/superai/releases) (macOS, Linux, Windows; `SHA256SUMS` included):

```sh
curl -fsSLO https://github.com/liliang-cn/superai/releases/latest/download/superai-linux-amd64
sudo install -m 755 superai-linux-amd64 /usr/local/bin/superai
```

On macOS, clear the download flag first: `xattr -d com.apple.quarantine superai-darwin-arm64`.

## Run

```sh
superai serve                          # http://127.0.0.1:43117
superai serve -bind 0.0.0.0            # open to your network (put HTTPS in front if it faces the internet)
```

The first start creates `~/.superai/auth.json` and prints the login once:

- **Browser:** user `superai`, the printed password.
- **Programs and agents:** `Authorization: Bearer <token>`.

Lost it? Stop the server, delete `auth.json`, and start again. Every client then needs the new token.

**Pick a model** in **Settings › Model**: sign in with a Claude Code, Codex or Gemini CLI account (no API key needed), or enter any OpenAI-compatible URL, key and model.

### Docker

```sh
docker build -t superai .
docker run -d -p 43117:43117 -v superai-data:/data superai
docker logs superai | grep -A2 'no credentials found'   # first login
```

### Desktop app

Needs Go 1.26, Node 22 and the [Wails v2 CLI](https://wails.io/docs/gettingstarted/installation).

```sh
make deps && make package   # macOS: build/bin/SuperAI.app and SuperAI.dmg
wails build                 # Windows: build/bin/SuperAI.exe
```

## Link another machine

Run `superai agent` on a machine and the core can send work to its coding tools: write `@claude.<machine>` in a chat, or let the assistant decide. The agent dials out, so the machine needs no open ports.

1. Start the core with an agent port: `superai serve -bind 0.0.0.0 -agent-port 43120`.
2. On the other machine, turn on its coding tools and connect:

```sh
mkdir -p ~/.superai && echo '{"external_agents":{"enabled":true}}' > ~/.superai/settings.json
SUPERAI_CORE=core.lan:43120 SUPERAI_CORE_TOKEN=<token> superai agent
```

| Variable | Meaning |
|---|---|
| `SUPERAI_CORE` | The core's agent port, `host:port`. Comma-separate several. |
| `SUPERAI_CORE_TOKEN` / `SUPERAI_CORE_TOKEN_FILE` | The core's token, or a file holding it. |
| `SUPERAI_AGENT_NAME` | The name the core shows. Defaults to the hostname. |
| `SUPERAI_HOST_CONTROL=off` | Let the core look at this machine but not run commands or restart services. |

The coding tools must already be installed and signed in on that machine.

## Pair a phone

In the web UI open **Settings › Runtime** and make a pairing code. In the iOS app, enter the core's address and the code.

## Hive on k3s

Everything is in `deploy/k3s/`: the manifest (`superai-hive.yaml`), a script that builds the secret (`hive-secret.sh`), and one that builds the image and restarts the pods (`rollout.sh`). The web UI is on NodePort `31743` and agents connect to `31744`.

```sh
SUPERAI_NODES="node1 node2 node3" SUPERAI_KUBE=node1 ./deploy/k3s/rollout.sh
```

## Use it from Claude Code or Codex

The core serves MCP at `/mcp` (same token as the API):

```sh
claude mcp add --scope user --transport http superai https://ai.example.com/mcp --header "Authorization: Bearer <token>"
```

The main tool is `superai_ask`: ask SuperAI anything, as if you typed it in the app. There are also tools for conversations, schedules, the hive and machines.

To give SuperAI its own MCP servers, list them in `~/.superai/mcpServers.json`. Write secrets as `${VAR}`; they are read from the environment.

## Send it notifications

Other programs can push a notice to every paired phone:

```
POST /api/rpc/PostNotification
Authorization: Bearer <integration token>

[{"title":"Time to leave","body":"Bus at **17:10**","kind":"reminder"}]
```

Make a token that can only do this with `superai integration-token create <name>`.

## Ports and data

| Port | Use |
|---|---|
| `43117` | Web UI and API |
| `43120` | Agent port (set with `-agent-port`) |
| `31743` / `31744` | Hive NodePorts: web / agents |

Everything lives in `~/.superai` (change it with `SUPERAI_HOME`). To back up, stop the server and copy the folder.

## Develop

Go commands need `GOWORK=off`; the Makefile sets it.

```sh
make dev      # hot reload
make test     # Go tests
make check    # everything CI runs
```

Push a `v*` tag to publish binaries to Releases.
