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

### 更新

```sh
./deploy/k3s/rollout.sh   # 构建、导入、先重启 worker 再重启 queen，等它们就绪
```

镜像标签固定为 `superai:hive`，拉取策略是 `IfNotPresent`，所以是重启这一步让 pod 换上新镜像。

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
| `settings.json` | 所有设置，包括模型 key |
| `cliproxy/` | CLI 代理登录的账号 |
| `data/` | 会话、任务、定时任务、本地记忆、MCP 配置 |
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
