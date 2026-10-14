# SuperAI

SuperAI 是一个以 [agent-go](https://github.com/liliang-cn/agent-go) 为大脑的个人 AI 助手。同一个 `superai` 程序有三种运行方式：

| 运行方式 | 命令 | 作用 |
|---|---|---|
| core（中枢） | `superai serve` | 跑大脑，提供网页界面和 API，手机和桌面都连它 |
| agent（干活的机器） | `superai agent` | 连到 core，把这台机器上的 Claude Code、Codex 等交给 core 调用 |
| 桌面版 | 双击 `SuperAI.app` | 带窗口的 core，macOS / Windows |

多台 core 还可以组成 **hive**：一个 queen 接活、拆活，若干 worker 并行去做，部署在 k3s 上。

客户端：浏览器（core 自带的网页）、桌面版、iOS App（[superai-ios](https://github.com/liliang-cn/superai-ios)）。

## 选一种装法

- **只在自己电脑上用**：[单机运行](#1-单机运行-core)，或者[桌面版](#3-桌面版macos--windows)。
- **想让手机、别的电脑也能用**：在一台常开的机器上[单机运行](#1-单机运行-core)或[用 Docker](#2-docker)，再[配对手机](#5-配对手机)。
- **想让 core 调用另一台机器上的 Claude Code / Codex**：在那台机器上[装 agent](#4-把别的机器接进来superai-agent)。
- **要多个 worker 并行干活**：[在 k3s 上部署 hive](#6-在-k3s-上部署-hive)。

---

## 1. 单机运行 core

### 下载

每个 `v*` 版本都会在 [Releases](https://github.com/liliang-cn/superai/releases) 发布不带窗口的静态二进制：`darwin-arm64`、`darwin-amd64`、`linux-amd64`、`linux-arm64`、`windows-amd64.exe`、`windows-arm64.exe`，附带 `SHA256SUMS`。

```sh
# 以 Linux x86_64 为例，换成你的平台
curl -fsSLO https://github.com/liliang-cn/superai/releases/latest/download/superai-linux-amd64
curl -fsSLO https://github.com/liliang-cn/superai/releases/latest/download/SHA256SUMS
grep ' superai-linux-amd64$' SHA256SUMS | sha256sum -c -
sudo install -m 755 superai-linux-amd64 /usr/local/bin/superai
```

macOS 下载的文件会被隔离，先去掉隔离标记：`xattr -d com.apple.quarantine superai-darwin-arm64`。

### 运行

```sh
superai serve                      # 只监听 127.0.0.1:43117
superai serve -bind 0.0.0.0 -port 43117   # 局域网可访问
```

**第一次启动**时会生成登录凭据，写入 `~/.superai/auth.json`，并**只在这一次**把它们打印到日志里：

```
no credentials found — generated a set and wrote ~/.superai/auth.json
  browser:  password <12 位随机密码>
  programs: Authorization: Bearer <token>
```

- 浏览器打开 `http://127.0.0.1:43117`，用户名 `superai`，密码就是上面打印的那个。
- 程序、MCP 客户端和 agent 调用时，带 `Authorization: Bearer <token>`。
- 忘了密码：停掉服务，删掉 `auth.json` 再启动，会重新生成一套。这会作废旧 token，所有已连上的 agent 和程序都要换成新 token。

`serve` 的参数：

| 参数 | 默认 | 说明 |
|---|---|---|
| `-port` | `43117` | 网页和 API 端口 |
| `-bind` | `127.0.0.1` | 监听地址。改成 `0.0.0.0` 就是对网络开放，所有请求都要先登录，但公网暴露请放在 HTTPS 反向代理后面 |
| `-agent-port` | `0`（关） | 接受 agent 连接的 gRPC 端口，见[第 4 节](#4-把别的机器接进来superai-agent)。也可以用环境变量 `SUPERAI_AGENT_PORT` 设置 |

### 配模型

打开网页，进入 **Settings › Model**，二选一：

- **用已有的订阅账号**（默认）：内置的 CLI 代理（只监听本机 `127.0.0.1:43517`）可以登录 Claude Code、Codex、Gemini CLI 的账号，不需要 API key。账号凭据存在 `~/.superai/cliproxy/`。
- **用 API**：填 OpenAI 兼容的 Base URL、Key 和模型名。第一次启动前也可以用环境变量预填：`LLM_BASE`、`LLM_KEY`、`LLM_MODEL`；向量模型对应 `EMBED_BASE`、`EMBED_KEY`、`EMBED_MODEL`。页面里对应项留空时，才使用这些变量。

记忆默认存在本地。如果有 CortexDB 服务器，可以在 **Settings › Memory** 里切换成共享记忆，几台 core 共用一个大脑。

### 做成开机服务（Linux）

```ini
# /etc/systemd/system/superai.service
[Unit]
Description=SuperAI core
After=network-online.target
Wants=network-online.target

[Service]
User=superai
Environment=SUPERAI_HOME=/var/lib/superai
Environment=SUPERAI_NO_BROWSER=1
ExecStart=/usr/local/bin/superai serve -bind 0.0.0.0 -port 43117
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

```sh
sudo useradd -r -m -d /var/lib/superai superai
sudo systemctl daemon-reload && sudo systemctl enable --now superai
sudo journalctl -u superai | grep -A2 'no credentials found'   # 第一次的密码和 token
```

`SUPERAI_NO_BROWSER=1` 表示不启动内置的无头 Chrome。服务器上一般没有 Chrome，不加这个变量每次启动都会白白等它。

---

## 2. Docker

仓库里的 `Dockerfile` 构建出的是不带窗口的镜像，里面有 `superai`、`superai-daemon`、`superai-hive-worker`、`cortexdb-mcp-stdio` 和 `kubectl`，以 UID 10001 运行。目前没有发布公共镜像，需要自己构建：

```sh
docker build -t superai .
docker run -d --name superai -p 43117:43117 -v superai-data:/data superai
docker logs superai | grep -A2 'no credentials found'   # 第一次的密码和 token
```

数据都在 `/data` 卷里，和单机版的 `~/.superai` 一样。容器启动时，入口脚本会读取以下环境变量，覆盖卷里的文件：

| 变量 | 作用 |
|---|---|
| `SUPERAI_TOKEN` | 写入 `auth.json` 的 token（同时可设 `SUPERAI_USER`、`SUPERAI_PASSWORD_HASH`，后者为 bcrypt）。会保留原来的会话密钥，重启不会让浏览器掉线 |
| `SUPERAI_SETTINGS_JSON` | 整个 `settings.json` 的内容 |
| `SUPERAI_MCP_JSON` | 写入 `data/mcpServers.json`，用来提供固定的 MCP 服务器 |

这些变量都不设时，直接使用卷里已有的文件，和普通的 `docker run -v` 一样。

---

## 3. 桌面版（macOS / Windows）

桌面版是带窗口的 core（Wails v2），Releases 里没有，需要自己构建。

需要的工具：Go 1.26、Node 22、[Wails v2 CLI](https://wails.io/docs/gettingstarted/installation)（`go install github.com/wailsapp/wails/v2/cmd/wails@latest`）。

```sh
make deps        # 安装前端依赖
make package     # macOS：通用二进制（arm64 + Intel）的 SuperAI.app 和 SuperAI.dmg，在 build/bin/
make run         # 构建后直接启动
wails build      # Windows 上用这个，得到 build/bin/SuperAI.exe
```

要签名并公证 dmg，见 `scripts/package-macos.sh` 开头的说明（`SIGN_ID`、`AC_APPLE_ID`、`AC_TEAM_ID`、`AC_PASSWORD`）。

桌面版关掉后，定时任务仍要按时执行的话，安装后台调度器（macOS，launchd）：

```sh
make install-daemon      # 安装并启动
make daemon-status
make uninstall-daemon    # 卸载，已有的定时任务会保留
```

---

## 4. 把别的机器接进来：`superai agent`

agent 主动连到 core 的 gRPC 端口，所以 agent 所在的机器不需要开放任何端口。连上之后，core 就能把活派给这台机器上的 CLI：在对话里写 `@claude.<机器名>`，或者由模型自己调用。

**第 1 步：core 打开 agent 端口。**单机版加上 `-agent-port`，例如 `superai serve -bind 0.0.0.0 -agent-port 43120`。k3s 上的 hive 已经开好了，地址是任一节点的 `31744`。

**第 2 步：准备 token。**用 core 的 token（`auth.json` 里的 `token`，或 hive 的 Secret 里的 `token`）。把它存到一个只有 root 可读的文件里，不要写进 unit 文件：

```sh
sudo install -d -m 700 /etc/superai-agent
sudo sh -c 'umask 077; printf %s "<core 的 token>" > /etc/superai-agent/core-token'
```

**第 3 步：安装二进制，打开 CLI 代理。**下载方法同[第 1 节](#下载)。然后在 agent 的数据目录里写入 `settings.json`。**不打开这一项，agent 能连上，但不会把本机的 CLI 报给 core**，core 那边就显示 nothing to run：

```sh
sudo install -d /var/lib/superai-agent
echo '{"external_agents":{"enabled":true}}' | sudo tee /var/lib/superai-agent/settings.json
```

`external_agents` 里还有几个可选项：`roots`（允许 CLI 工作的目录）、`unattended`（工具调用不再逐个询问，**不建议**打开）、`binaries`（CLI 不在 PATH 里时，指定它的路径）。

**第 4 步：做成服务。**

```ini
# /etc/systemd/system/superai-agent.service
[Unit]
Description=SuperAI agent (links this machine to core)
After=network-online.target
Wants=network-online.target

[Service]
Environment=SUPERAI_HOME=/var/lib/superai-agent
Environment=SUPERAI_CORE=core.example.lan:43120
Environment=SUPERAI_CORE_TOKEN_FILE=/etc/superai-agent/core-token
Environment=SUPERAI_AGENT_NAME=build-box
Environment=SUPERAI_NO_BROWSER=1
ExecStart=/usr/local/bin/superai agent
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

```sh
sudo systemctl daemon-reload && sudo systemctl enable --now superai-agent
journalctl -u superai-agent -n 5   # 看到 "connected to … as build-box, clis [claude codex …]" 就是连上了
```

| 环境变量 / 参数 | 说明 |
|---|---|
| `SUPERAI_CORE` / `-core` | core 的 agent 端口，`host:port`。可以用逗号写多个地址，例如 hive 的三个节点 `10.0.0.1:31744,10.0.0.2:31744,10.0.0.3:31744` |
| `SUPERAI_CORE_TOKEN_FILE` | 从文件读取 token（推荐）。也可以用 `SUPERAI_CORE_TOKEN` / `-core-token` 直接传 |
| `SUPERAI_AGENT_NAME` / `-name` | core 看到的机器名，不填就用主机名 |
| `SUPERAI_CORE_TLS=1` / `-core-tls` | 用 TLS 连接 core |

CLI 本身（`claude`、`codex`、`gemini` 等）要在这台机器上装好，并且已经登录。core 是以 `SUPERAI_HOME` 所属的用户身份去调用它们的。

一台已经在跑 `superai serve` 的机器，也可以加上同样的 `-core` 等参数，同时作为另一个 core 的 agent。

### 另一种接法：`superai node`（配对码）

这是早期的点对点接法：这台机器自己开一个端口（`43779`），由另一个 SuperAI 用 6 位配对码把它添加进去。

```sh
curl -fsSL https://raw.githubusercontent.com/liliang-cn/superai/main/scripts/install-node.sh | sh
# 选项会传给 node install，例如：| sh -s -- -roots ~/code
```

安装脚本会把它注册成登录服务（macOS 用 launchd，Linux 用 systemd 用户单元），并打印地址和配对码。然后在要调用它的 SuperAI 里，到 **Settings › Runtime › Other SuperAIs** 填入地址和配对码，并给它起个名字。管理命令有 `superai node pair`、`superai node status`、`superai node uninstall [-purge]`。

新部署建议用上面的 `superai agent`：agent 主动往外连，不需要开放端口，也不需要配对。

---

## 5. 配对手机

1. 在 core 的网页里打开 **Settings › Runtime**，生成配对码（几分钟内有效）。这一项只在 `serve` 模式（网页版、Docker、hive）里有。
2. iOS App 用 Xcode 从 [superai-ios](https://github.com/liliang-cn/superai-ios) 构建安装。第一次打开时，填入 core 的地址（例如 `https://ai.example.com`）和配对码。
3. 配对后手机拿到的是自己的设备 token，可以在同一页里单独撤销。

---

## 6. 在 k3s 上部署 hive

hive 由一个 queen 和若干 worker 组成：

- **queen**：对外提供网页、API 和 agent 端口；收到大任务会拆开，并行派给 worker 和已连接的 agent。
- **worker**：数量由 queen 自己按需调整（`hive_spawn` / `hive_retire`），上限在 Secret 的设置里，默认 20 个，初始为 1 个。
- **共享记忆**：所有成员通过同一个 CortexDB 共享记忆。

文件都在 `deploy/k3s/`：

| 文件 | 内容 |
|---|---|
| `superai-hive.yaml` | Namespace、RBAC、queen 和 worker 的 StatefulSet、Service（NodePort `31743` 网页、`31744` agent）、Ingress |
| `hive-secret.sh` | 生成 Secret `superai-hive` |
| `rollout.sh` | 构建镜像、导入到每个节点、滚动重启 |
| `edge-tts/` | 可选：中文语音朗读服务（Edge TTS） |

### 前提

- 一个 k3s 集群，并且能用 `ssh <节点>` 加 `sudo` 操作每个节点。集群里不需要镜像仓库：镜像在本机构建，再导入到每个节点。
- 一个给 queen 用的 StorageClass。manifest 里写的是 `haify-drbd-remote`（网络块存储，queen 换节点后数据仍然可以挂载）。没有的话，改成你集群里有的 StorageClass，比如 `local-path`，代价是 queen 会固定在一个节点上。
- 一个 CortexDB 服务器，作为共享记忆。地址写在 manifest 的 `SUPERAI_MCP_JSON` 和 Secret 的 `shared_memory_endpoint` 里，改成你自己的。

### 第一次部署

```sh
# 1. 构建镜像并导入每个节点（节点和管理节点可以用环境变量改）
SUPERAI_NODES="node1 node2 node3" SUPERAI_KUBE=node1 ./deploy/k3s/rollout.sh   # 首次运行时最后的 restart 会失败，可以忽略

# 2. 创建 Secret（见下）

# 3. 部署
scp deploy/k3s/superai-hive.yaml node1:/tmp/ && ssh node1 'sudo k3s kubectl apply -f /tmp/superai-hive.yaml'
ssh node1 'sudo k3s kubectl -n superai get pods -w'
```

部署完成后，用 `http://<任一节点>:31743` 访问网页；agent 连到 `<节点>:31744`。

### Secret `superai-hive`

模型 key、登录凭据和设置都放在 Secret 里，不写进 manifest。需要的键：

| 键 | 内容 |
|---|---|
| `token` | API / agent 用的 bearer token |
| `user`、`password_hash` | 网页登录的用户名和 bcrypt 密码哈希 |
| `settings-queen.json` | queen 的 `settings.json`，其中 `hive.role` 为 `queen`，`hive.spawner` 指向 worker 的 StatefulSet |
| `settings-worker.json` | worker 的 `settings.json`，其中 `hive.role` 为 `worker`，`hive.join_url` 为 `http://superai-queen.superai.svc.cluster.local:43117` |
| `cortexdb_token` | 访问 CortexDB 的 token |
| `cortexdb_llm_base_url`、`cortexdb_llm_model`、`cortexdb_llm_key` | 可选，CortexDB 工具使用的模型 |

`hive-secret.sh` 会从一台已经在运行的 SuperAI 上复制登录凭据和模型设置（`SUPERAI_SRC=user@host`，读取 `/opt/superai/data/` 下的文件），拼好上面这些键，再直接 apply 到集群。值不会出现在终端上，也不会落到本地文件里。没有这样一台机器的话，就用 `kubectl create secret generic superai-hive --from-file=…` 自己创建。

用 `hive-secret.sh` 时可以顺带换模型：

```sh
SUPERAI_LLM_MODEL=gemini-3.8-flash ./deploy/k3s/hive-secret.sh          # 换成同一网关的另一个模型
DEEPSEEK_API_KEY=… SUPERAI_LLM_BASE_URL=https://api.deepseek.com \
  SUPERAI_LLM_KEY_ENV=DEEPSEEK_API_KEY SUPERAI_LLM_MODEL=deepseek-flash \
  ./deploy/k3s/hive-secret.sh                                           # 换成另一家服务商
```

可选项：`SUPERAI_LLM_CONTEXT_TOKENS`、`SUPERAI_LLM_MAX_OUTPUT_TOKENS`（模型的上下文窗口和最大输出，按服务商页面填写）、`SUPERAI_LLM_REASONING_EFFORT`、`SUPERAI_NEW_TOKEN=1`（换一个新 token，之后所有客户端都要更新）。改完 Secret 后执行一次 `rollout.sh`，pod 才会读到新值。

### 谁来干活：`hive.delegation`

queen 只负责计划、拆分、派活、跟进、合并结果、审批和跟人说话，用到工具的活交给 worker。由 `settings-queen.json` 里的 `hive.delegation` 决定（环境变量 `SUPERAI_HIVE_DELEGATION` 优先）：

| 值 | 行为 |
|---|---|
| `max`（queen 默认） | 只要有活着的 worker，queen 自己这一轮就拿不到干活的工具（noted、搜索、网页、文件、shell、CortexDB 检索等），只能通过 `hive_delegate` 交给 worker：空闲的优先，几件互不相干的事同时分给不同的 worker，结果合在一起再回答。要 noted 的活只派给有 noted 的 worker（worker 每次心跳都报告自己有哪些 MCP 服务器）。worker 失败或超时（`hive.delegate_timeout_seconds`，默认 300）就换一个，再不行 queen 自己做，并在回答和事件里说明。一个 worker 都没有时 queen 照常自己做 |
| `auto` | 提供 `hive_delegate` 并优先用它，但不收走 queen 的工具 |
| `off` | 和以前一样，小事 queen 自己做 |

worker 做的每一步都会以 `worker · 工具名`（例如 `superai-worker-0 · mcp_noted_list_events`）出现在 queen 这一轮的 `chat:event` 里（另带 `worker`、`workerTool` 字段），也出现在 `ChatHistory` 的步骤和 `superai_ask` 的 `tools` 里。worker 上需要人批准的工具调用会转到 queen 这边，在手机上批。App 首页和按钮用的 `NotedToday`、`NotedTimeWeek`、`NotedHoldings`、`CallTool` 等仍由 queen 直接读，不经过 worker。

### 更新

```sh
./deploy/k3s/rollout.sh   # 构建、导入、先重启 worker 再重启 queen，等它们就绪
```

镜像标签固定为 `superai:hive`，拉取策略是 `IfNotPresent`，所以是重启这一步让 pod 换上新镜像。

---

## 7. MCP

### 让 Claude Code / Codex 直接和 SuperAI 说话

core（hive 里就是 queen）在 `/mcp` 上提供一个 Streamable HTTP 的 MCP 服务器，和 API 用同一道门：请求要带 `auth.json` 里的 token（或配对设备的 token），`Authorization: Bearer <token>`，没带就是 401。

```sh
# Claude Code
claude mcp add --scope user --transport http superai https://ai.example.com/mcp --header "Authorization: Bearer <token>"
```

```toml
# Codex：~/.codex/config.toml，token 放在环境变量 SUPERAI_TOKEN 里
[mcp_servers.superai]
url = "https://ai.example.com/mcp"
bearer_token_env_var = "SUPERAI_TOKEN"
```

工具：

| 工具 | 作用 |
|---|---|
| `superai_ask` | 像在 App 里打字一样问 SuperAI，等它答完。返回答案、它调用过的工具（名字和成败）和会话 id；把 `conversation` 传回去就是接着聊，不传就开一个新会话（id 以 `mcp-` 开头）。`timeout_seconds` 默认 180，最多 600；超时了这一轮照样在跑，返回已经写出来的部分和 `pending: true`，之后用 `superai_chat_history` 看结果。需要人批准的工具调用列在 `needs_approval` 里，只能在手机上批，这里不会替你批。客户端带了 progress token 的话，等待期间会收到进度通知 |
| `superai_conversations` | 最近的会话：id、标题、条数、更新时间 |
| `superai_chat_history` | 一个会话最近的消息：角色、文字、时间 |
| `superai_schedule_*` | 定时任务的增删改查和立即运行 |
| `superai_hive_*` | hive 的成员、扩缩容、派活和消息（只在 queen 上有意义） |

### 给 SuperAI 接 MCP 服务器

SuperAI 读 `mcpServers.json`（`~/.superai/mcpServers.json`，以及集群写的 `~/.superai/data/mcpServers.json`）。远程服务器写 `url` 和 `headers`；密钥不要写死在文件里，写成 `${变量名}`，启动时从进程的环境变量里取：

```json
{"mcpServers":{"noted":{"type":"http","url":"https://noted.example.com/mcp","headers":{"Authorization":"Bearer ${NOTED_TOKEN}"}}}}
```

`~/.superai/mcpServers.json` 会原样同步给 hive 里的每个 worker，所以里面只有变量名，没有值；哪个成员的环境里没有这个变量，就不启动这个服务器（worker 的同步结果里会写明缺了什么）。worker 自己（或集群的 `SUPERAI_MCP_JSON`）已经配了同名服务器的，以 worker 自己的为准，不会多出第二份。

接上 noted 以后，待办、日程、笔记、项目、目标、时间记录和持仓都交给 noted 的工具，回答里用 `card` / `timeline` / `stat` 块展示，块上的按钮通过 `CallTool` RPC 直接调用 noted 的工具，不再经过模型；只有 `action_mcp_servers`（默认只有 `noted`）里的服务器能这样调用，`delete_*` 要客户端确认过才执行。首页的数据来自 `NotedToday`、`NotedTimeWeek`、`NotedHoldings` 三个 RPC。

---

## 8. 外部通知：`PostNotification`

别的程序（比如 openclaw 的定时任务：早上的备忘、"该出发接三宝了"、起立喝水、价格提醒）可以把通知发给 SuperAI。每一条都会：出现在打开的网页上（SSE 的 `notice` 事件）、留在通知中心（`Notifications`），并以**真正的手机通知**推到每台配对过、注册了推送的 iPhone 上。它们**不发给 webhook**：发送方自己已经发到了 webhook 转去的那个聊天（例如 Telegram），再转一次就重复了。

### 接口

```
POST /api/rpc/PostNotification
Authorization: Bearer <integration token 或 core 的 token>

[{"title":"该出发接三宝了","body":"**17:10** 出发，带水壶","source":"openclaw","kind":"reminder",
  "key":"pickup-2026-10-14","deeplink":"noted://alarm/<id>","time":"2026-10-14T17:05:00+08:00",
  "actions":[{"label":"推迟 10 分钟","say":"接三宝的提醒推迟 10 分钟"}]}]

→ {"ok":true,"id":"ntf_…","duplicate":false}
```

请求体是只有一个对象的 JSON 数组（和其它 RPC 一样）：

| 字段 | 必填 | 说明 |
|---|---|---|
| `title` | 是 | 最多 120 字 |
| `body` | | 最多 4000 字，可以写 Markdown。推到锁屏上的是去掉 Markdown 的前 180 字左右，全文在通知中心 |
| `source` | | 发送方，例如 `openclaw`，最多 60 字 |
| `kind` | | `brief`、`reminder`、`alert`、`info`（默认）。决定手机被打扰的程度，见下表 |
| `key` | | 去重：同一个 `source` + `key` 在 24 小时内只送达一次，重复的请求返回 `duplicate: true` 和第一次的 `id`。窗口存在磁盘上，重启不丢。手机上同一个 key 的通知会互相替换（`apns-collapse-id`） |
| `deeplink` | | 点开通知去哪，例如 `noted://alarm/<id>`、`superai://chat?prompt=…` |
| `time` | | 事情发生的时间，RFC 3339；默认是现在 |
| `actions` | | 最多 3 个按钮 `{"label","say"}`；点了之后 App 把 `say` 当作一句话发给 queen |

校验不过返回 400，正文写着原因。`id` 就是通知中心里这一条的 id（`MarkNotificationsRead` 用它），也在推送的数据里。

手机上的样子：

| kind | 优先级 | 声音 | interruption-level |
|---|---|---|---|
| `reminder`、`alert` | 10 | default | `time-sensitive` |
| `brief` | 10 | 无 | `active` |
| `info` | 5 | 无 | `active` |

推送的 payload：`aps.alert {title, body}`、`aps.thread-id` = kind、`aps.badge` = 通知中心的未读数（算上这一条），自定义数据 `superai: {"kind":"notice","id":…,"source":…,"deeplink":…}`。queen 的 `notify_user` 和定时任务的结果也走同一条路推到手机（它们没有 kind，按 `info` 处理）。

推送要配好 APNs（`APNS_KEY` 或 `APNS_KEY_PATH`、`APNS_KEY_ID`，可选 `APNS_TEAM_ID`、`APNS_TOPIC`、`APNS_ENV`；hive 里放在 Secret 的 `apns_key`、`apns_key_id`）。没配的话手机推送跳过（日志里一行），其余照常。

### integration token

`PostNotification` 可以用 core 的 token 调用，但给外部程序的应该是 **integration token**：它**只能**调用 `PostNotification`，调别的接口一律 403；可以单独撤销，撤销后就是 401。服务器上只存它的 sha256，原文只在创建时显示一次。

在 core 所在的机器上（数据目录就是 `SUPERAI_HOME`）：

```sh
superai integration-token create openclaw   # token 打印到 stdout，只有这一次
superai integration-token list
superai integration-token revoke <id>
```

hive 上在 queen 的 pod 里执行（镜像里 `SUPERAI_HOME=/data`）：

```sh
ssh node1 'sudo k3s kubectl -n superai exec superai-queen-0 -- superai integration-token create openclaw'
```

命令直接写 `integrations.json`，正在运行的服务马上生效，不用重启。也可以用 RPC 管理（需要 core 的 token、配对设备或已登录的网页）：

```
POST /api/rpc/CreateIntegrationToken  ["openclaw"]  → {"id":"…","name":"openclaw","created_at":"…","token":"sai_int_…"}
POST /api/rpc/ListIntegrationTokens   []            → [{"id":"…","name":"openclaw","created_at":"…","last_used":"…"}]
POST /api/rpc/RevokeIntegrationToken  ["<id>"]      → null（id 不存在是 404）
```

---

## 端口和数据

| 端口 | 用途 |
|---|---|
| `43117` | core 的网页和 API（`serve -port`） |
| `43120` | hive 里 queen 的 agent 端口；单机版的 agent 端口由 `-agent-port` 指定 |
| `31743` / `31744` | hive 对外的 NodePort：网页 / agent |
| `43517` | 内置 CLI 代理，只监听本机 |
| `43779` | `superai node` 的默认端口 |
| `47615` | 外部头像渲染用的事件流（见下） |

数据目录默认是 `~/.superai`，可以用 `SUPERAI_HOME` 改：

| 路径 | 内容 |
|---|---|
| `auth.json` | 登录凭据（权限 600） |
| `devices.json`、`integrations.json` | 配对的手机、integration token（都只存 token 的 sha256） |
| `settings.json` | 所有设置，包括模型 key |
| `cliproxy/` | CLI 代理登录的账号 |
| `data/` | 会话、任务、定时任务、本地记忆、MCP 配置、通知中心（`notifications.json`）和通知去重窗口（`notification-keys.json`） |
| `workspace/` | agent 的工作目录 |

备份时，停掉服务，把整个目录拷走即可。

## 外部头像协议

SuperAI 自己不画角色，而是把情绪和 agent 的状态以 SSE 推送出去，任何 Live2D / VRM / Unity / 网页渲染器都可以接：

```
GET http://127.0.0.1:47615/avatar/events   # AvatarEvent 的 text/event-stream
GET http://127.0.0.1:47615/avatar          # 参考用的 2D 占位页
```

`AvatarEvent`：`{ type: "state"|"emotion"|"speech", state, emotion, text, tool, ts }`。

## 开发

上一级目录里有一个不包含本模块的 `go.work`，所以 Go 命令都要带 `GOWORK=off`。Makefile 已经设好了。

```sh
make dev       # 热重载开发（Go + Vite）
make serve     # 构建前端和不带窗口的二进制，在 127.0.0.1:43117 启动
make test      # go vet 加全部 Go 测试
make check     # CI 跑的全部检查：Go 测试、前端类型检查和构建
make bindings  # 改了 App 的方法后，重新生成 Wails 的 TypeScript 绑定
make help      # 所有 target
```

打 `v*` tag 并推送后，`.github/workflows/release.yml` 会构建 6 个平台的二进制并发布到 Releases。
