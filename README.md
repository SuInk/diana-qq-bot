# Diana QQ Bot

[English](./README.en.md)

> [!WARNING]
> 本项目仍处于早期开发阶段，接口、配置和行为随时可能发生不兼容变更，升级前请自行备份。项目不对安全性作任何保证；请审查代码和配置，并自行承担运行、开放公网访问及接入真实账号所产生的风险。

Diana QQ Bot 是一个 Go 语言 QQ 机器人服务，内置 LLM 兼容层、NapCat / OneBot v11 反向 WebSocket 接入、Gin WebUI 和插件管理。WebUI 可配置模型、机器人连接、触发词，并可启停官方内置插件。

部署参数保存在 WebUI/SQLite、环境变量或本地配置文件中，仓库不包含真实 QQ 号、群号、聊天记录、Cookie 或 API Key。可从 [`.env.example`](./.env.example) 创建本地配置；`.env`、`runtime.env`、数据库、日志和媒体缓存均被 Git 忽略。贡献代码前请运行 `make audit-public`，详细规则见 [`CONTRIBUTING.md`](./CONTRIBUTING.md)。

## 安装要求

- NapCat，开启 OneBot v11 反向 WebSocket
- 使用源码安装时需要 Go `1.25.8`、Node.js `22` 和 npm
- 使用 Docker 部署时需要 Docker 或 Docker Compose

## Docker 部署

构建镜像：

```sh
docker build -t diana-qq-bot:latest .
```

运行容器：

```sh
export DIANA_ADMIN_TOKEN="$(openssl rand -hex 32)"

docker run -d \
  --name diana-qq-bot \
  --restart unless-stopped \
  -p 18080:18080 \
  -v "$PWD/logs:/app/logs" \
  -e LOG_PATH=/app/logs/diana-qq-bot.log \
  -e DIANA_ADMIN_TOKEN="$DIANA_ADMIN_TOKEN" \
  -e QQBOT_ENABLED=true \
  -e ONEBOT_REVERSE_WS_ENDPOINT=ws://127.0.0.1:18080/onebot/v11/ws \
  -e ONEBOT_ACCESS_TOKEN=your-onebot-token \
  -e QQBOT_QQ=your-bot-qq \
  -e LLM_PROVIDER=openai_compatible \
  -e LLM_API_KEY=your-key \
  -e LLM_MODEL=gpt-4o-mini \
  -e LLM_USER_AGENT=diana-qq-bot \
  diana-qq-bot:latest
```

Docker Compose：

```sh
cp .env.example .env
# 填写 .env 中的管理员 Token、QQ 号、OneBot Token 和 LLM 配置
docker compose up -d --build
```

容器启动后访问：

```text
http://127.0.0.1:18080
```

WebUI 默认从根路径 `/` 进入。首次打开 `/login` 时必须由用户自行填写管理员邮箱和密码，项目不再提供或预填默认邮箱。浏览器使用 15 分钟 JWT access token 和 30 天轮换 refresh token；服务端保留 refresh token 哈希，因此可以在“访问设置”查看登录设备、吊销单个设备或退出其他设备。修改密码会立即吊销其他设备。`DIANA_ADMIN_TOKEN` 仍是独立的自动化 API 凭据，不会作为浏览器 Cookie 保存。

NapCat 反向 WebSocket 连接宿主机暴露的地址：

```text
ws://127.0.0.1:18080/onebot/v11/ws
```

如果 NapCat 和机器人不在同一台机器，`127.0.0.1` 要换成机器人宿主机 IP 或域名。

## 从源码安装

```sh
git clone <your-repo-url> diana-qq-bot
cd diana-qq-bot

go mod download

cd frontend
npm ci
npm run build
cd ..

go build -o dist/diana-qq-bot-webui ./cmd/webui
```

启动：

```sh
./dist/diana-qq-bot-webui
```

默认 WebUI：

```text
http://127.0.0.1:18080
```

## macOS 部署

在 macOS 本机运行并需要读取 QQ/NapCat 下载的文件时，使用带固定本地代码身份的构建脚本：

```sh
make install-local-mac-app
~/Applications/Diana\ QQ\ Bot.app/Contents/MacOS/diana-qq-bot-webui
```

也可以通过 Makefile 构建：

```sh
make build-local-mac
```

首次读取 `~/Library/Containers/com.tencent.qq` 时，macOS 会在“系统设置 -> 隐私与安全性 -> 文件与文件夹”中增加 Diana QQ Bot。展开 Diana 并允许访问 QQ。这个权限由 App Bundle 和固定代码身份 `com.suink.diana-qq-bot` 保持，后续继续使用上述命令构建即可避免普通 Go 重编译导致授权失效。

本机版启动脚本会在 Diana 启动时检查 QQ：QQ 未运行时会自动使用 NapCat 所需的 `--no-sandbox` 参数启动；已经以该参数运行时不会重复打开。也可以单独执行：

```sh
make start-napcat-mac
```

如需关闭自动启动，在 `runtime.env` 中设置 `DIANA_START_NAPCAT=false`。若 QQ 不在默认的 `/Applications/QQ.app`，可通过 `DIANA_QQ_APP` 指定路径。

GitHub Release 中的 `darwin-arm64` 或 `darwin-amd64` 二进制仍可用于不需要读取其他 App 数据的部署。

## WebUI 升级

源码部署可以点击 WebUI 左上角的版本号检查并安装更新。升级器只使用当前仓库已配置的 `origin`，并执行以下流程：

1. 获取远端分支，拒绝脏工作区、detached HEAD 和非快进更新。
2. 快进源码后，在暂存目录中安装前端依赖并构建前端、后端和 macOS App。
3. 保留上一份程序与前端产物为 `.backup`，再替换为新构建；替换失败会恢复旧版本。
4. 当前进程不会被强制终止。WebUI 显示“升级已就绪”后，手动重启 Diana QQ Bot 使新版本生效。

源码目录由 `DIANA_UPDATE_ROOT` 指定，本机启动脚本会自动设为仓库根目录。自动构建可用 `DIANA_UPDATE_APPLY_ENABLED=false` 关闭。此功能需要 Git、Go、Node.js、npm，以及 macOS App 构建所需的 Xcode Command Line Tools。

Docker 镜像不包含 Git 工作区和构建工具，因此不支持 WebUI 原地升级；请拉取或重新构建镜像后执行 `docker compose up -d`。

## Linux 部署

amd64：

```sh
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o dist/diana-qq-bot-webui-linux-amd64 ./cmd/webui
./dist/diana-qq-bot-webui-linux-amd64
```

arm64：

```sh
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o dist/diana-qq-bot-webui-linux-arm64 ./cmd/webui
./dist/diana-qq-bot-webui-linux-arm64
```

后台运行建议使用下面的 systemd 示例。

## Windows 部署

PowerShell：

```powershell
$env:GOOS="windows"
$env:GOARCH="amd64"
$env:CGO_ENABLED="0"
go build -o dist\diana-qq-bot-webui-windows-amd64.exe .\cmd\webui
.\dist\diana-qq-bot-webui-windows-amd64.exe
```

Windows 下也可以直接下载 GitHub Release 中的 `windows-amd64.exe`。

## 快速运行

开发或本机测试可以一键同时启动 Go 后端和 Vite 前端：

```sh
make dev
```

默认后端是 `http://127.0.0.1:18080`，前端是 `http://127.0.0.1:5173`；Vite 会代理 `/api` 和 `/onebot` 到后端。端口可用环境变量调整：

```sh
make dev BACKEND_PORT=18081 FRONTEND_PORT=5174
```

没有安装 `make` 时，也可以直接使用跨平台 Node 脚本：

```sh
node scripts/dev.mjs
```

只运行后端或生产构建时：

```sh
make backend
make build
```

## 配置 LLM

可以在 WebUI 中配置，也可以使用环境变量：

```sh
LLM_PROVIDER=openai_compatible \
LLM_API_KEY=your-key \
LLM_BASE_URL=https://example.com/v1 \
LLM_API_FORMAT=responses \
LLM_MODEL=gpt-4o-mini \
LLM_USER_AGENT=diana-qq-bot \
LLM_IMAGE_MODEL=gpt-image-2 \
LLM_IMAGE_BASE_URL=https://image.example.com/v1 \
LLM_IMAGE_ORIGIN=203.0.113.10:443 \
LLM_IMAGE_TIMEOUT_MS=600000 \
LLM_CONTEXT_WINDOW_TOKENS=128000 \
LLM_MAX_CONTEXT_TOKENS=16384 \
LLM_MAX_OUTPUT_TOKENS=1024 \
./dist/diana-qq-bot-webui
```

支持的 provider：

- `openai_compatible`
- `gemini`
- `anthropic`

WebUI 的 LLM 配置页会直接显示当前保存的 API Key，方便本地控制台复制和修改；普通 `GET /api/llm/config` 默认仍不返回密钥，前端会用 `include_secrets=true` 显式读取完整配置。

## WebUI 日志中心

WebUI 的“日志中心”页可查看持久化的操作日志和错误日志。操作日志会记录 LLM 配置保存/切换、机器人启停、插件管理、系统更新等动作；错误日志会记录这些接口返回失败时的错误信息。日志会带 `actor` 操作人：WebUI 默认记录 `web:<客户端 IP>`，也可由网关通过 `X-Diana-Actor`、`X-Operator`、`X-Forwarded-User` 等请求头传入；QQ 内置 LLM 配置技能记录 `qq:<用户 QQ>`。

```text
GET /api/logs?kind=operation&limit=100
GET /api/logs?kind=error&limit=100
```

这些结构化日志存储在 `APP_DB_PATH` 指向的 SQLite 数据库中；`LOG_PATH` 仍用于普通运行日志文件输出。

## 配置 NapCat

本项目直接提供 OneBot v11 反向 WebSocket endpoint：

```text
ws://127.0.0.1:18080/onebot/v11/ws
```

在 NapCat 中添加 OneBot v11 反向 WebSocket，连接地址填写上面的地址。如果配置了 access token，NapCat 和本项目必须使用同一个 token。

群聊和私聊消息会在路由或生成回复前原子写入 SQLite 收件队列。进程重启后会继续处理未完成消息；OneBot 重连后还会从 NapCat 补拉群聊和好友历史，并按会话水位及 `message_id` 去重。待处理或补拉消息会在发送时间后的 2 小时内继续触发回复；更早的消息仍保留在历史和队列审计中，但不会补回复。队列采用至少一次处理语义：正常重启不会丢消息，极端情况下若 QQ 已发送成功但完成状态尚未落库，可能重复发送一次。

机器人启动示例：

```sh
QQBOT_ENABLED=true \
ONEBOT_REVERSE_WS_ENDPOINT=ws://127.0.0.1:18080/onebot/v11/ws \
ONEBOT_ACCESS_TOKEN=your-onebot-token \
QQBOT_QQ=your-bot-qq \
DIANA_GROUP_TRIGGERS=Diana,diana \
LLM_PROVIDER=openai_compatible \
LLM_API_KEY=your-key \
LLM_MODEL=gpt-4o-mini \
LLM_USER_AGENT=diana-qq-bot \
./dist/diana-qq-bot-webui
```

启动后，私聊会直接触发；群聊中 `@机器人` 或以触发词开头会触发。

## 内置 Agent

WebUI 的“QQ 机器人配置”页可以启用内置 Agent。启用后，机器人会使用 Codex CLI 风格的“模型规划、工具调用、观察结果、最终回复”循环处理消息。

当前内置工具：

- `list_files`：列出 Agent 工作目录内文件。
- `read_file`：读取 Agent 工作目录内文本文件。
- `run_command`：在 Agent 工作目录内执行白名单命令，不经过 shell，带超时和输出截断。
- `web_search.search`：按 WebUI 中的优先级执行实时网页搜索；遇到超时、限流、服务错误或空结果时自动切换到下一项。
- `browser_render`：由“沙盒无头浏览器网页渲染”官方插件提供；每次使用全新临时配置运行 Chrome/Chromium，执行 JavaScript 后提取可见正文，不读取用户浏览器登录态。
- `browser_open` / `browser_text` / `browser_click` / `browser_type` / `browser_screenshot`：通过 Chrome DevTools Protocol 操纵浏览器。

Agent 工具调用会写入操作日志，记录工具名、输入字段名和输出长度；不会把工具输出正文或密钥写入审计记录。

WebUI 的“联网搜索”页可以配置多个 Exa MCP / Tavily 提供方、调整回退顺序并逐项执行真实连通测试。默认配置包含无需密钥的 Exa MCP 和可选的 Tavily 回退；Tavily 密钥通过配置中指定的环境变量注入。配置文件示例见 [`web-search.example.json`](./web-search.example.json)。

浏览器工具需要 Chrome/Chromium 开启远程调试端口，例如：

```sh
chrome --remote-debugging-port=9222
```

建议把 `Agent 工作目录` 配到独立的资料目录，不要直接指向包含密钥或生产数据的目录。命令执行能力风险较高，生产环境建议配置 `DIANA_AGENT_COMMAND_ALLOWLIST` 只允许必要命令。

## WebUI 安装插件

打开 WebUI 后进入“机器人插件”区域：

1. 查看官方内置插件。
2. 点击安装或启用。
3. 默认内置 Go 版 `nonebot-plugin-resolver`，用于解析 B 站、YouTube、X、小红书、抖音等链接并作为上下文交给 LLM。
4. 默认内置 Go 文件解析插件，支持 QQ 文件段和文本类文件链接。macOS 先用 PDFKit 读取 PDF 文字层，扫描页由 Vision 在本地 OCR；原生路径不可用时回退到内嵌 PDFium WASM 和视觉 LLM。主消息先收到任务编号，后续进度和最终结果不会重复引用原消息。
5. 默认内置 `LLM 配置技能`，主人可用自然语言修改当前配置的 provider 和模型，例如“把提供商切到 gemini”“把模型换成 gemini-2.5-pro”“以后用 anthropic 的 claude-sonnet-4-5”；指定模型会先通过后端模型列表校验，列表里没有就不会保存。
6. 默认启用“沙盒无头浏览器网页渲染”官方插件。当前消息或引用消息包含普通网页 URL 时，插件会自动使用一次性 Chrome/Chromium 配置渲染页面，并将标题、描述和可见正文作为不可信上下文交给 LLM；`file://`、带凭据 URL、本机与私网地址会被拒绝。

macOS 的文本 PDF 由 PDFKit 直接提取，不创建逐页 OCR 子任务；真正的扫描页优先使用系统 Vision 本地识别。Windows、Linux 以及 macOS 原生 helper 不可用时，继续使用内嵌 PDFium WASM 渲染并调用视觉 LLM，不依赖 `pdftoppm`、Tesseract 或系统 Python。长文由整理子调用和最后一次 LLM 汇总，OCR 原文按 PDF SHA-256 缓存。

## 使用第三方 NoneBot 插件

Go 主程序不能直接加载 Python NoneBot 插件。要使用第三方 NoneBot2 插件时，推荐单独运行一个 NoneBot sidecar：

1. 在 NoneBot2 项目中安装第三方插件。
2. 给 NoneBot 配置 OneBot v11 反向 WebSocket driver。
3. 在 Diana WebUI 的“QQ 机器人”页面启用 `NoneBot 插件桥`。
4. `NoneBot 反向 WebSocket` 默认填写：

```text
ws://127.0.0.1:8080/onebot/v11/ws
```

Diana 会把 NapCat 收到的 OneBot 事件转发给 NoneBot sidecar；第三方插件调用 `send_msg`、`get_group_info` 等 OneBot API 时，Diana 会再转发给 NapCat。这样第三方插件仍然在原生 NoneBot2 运行环境中工作。

## 常用环境变量

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `HOST` | `127.0.0.1` | HTTP 监听地址；Docker 镜像显式使用 `0.0.0.0` |
| `PORT` | `18080` | WebUI 和 OneBot endpoint 监听端口 |
| `FRONTEND_DIST` | `frontend/dist` | 前端构建产物目录 |
| `DIANA_UPDATE_ROOT` | 当前目录 | WebUI 升级器使用的源码 Git 工作区；本机启动脚本自动设为仓库根目录 |
| `DIANA_UPDATE_APPLY_ENABLED` | `true` | 是否允许升级器构建并替换本地程序；Docker 中不可用 |
| `LOG_PATH` | 空 | 日志文件路径；设置后同时输出到 stdout 和文件 |
| `DIANA_LOG_PATH` | 空 | `LOG_PATH` 的兼容别名 |
| `APP_DB_PATH` | `data/diana-qq-bot.db` | 本地 SQLite 配置数据库路径 |
| `DIANA_ADMIN_TOKEN` | 空 | 自动化管理 API 的静态 Bearer Token，至少 32 字符；浏览器登录不直接保存它 |
| `DIANA_ADMIN_EMAIL` | 空 | 可选的环境变量初始化邮箱；默认留空并由用户在首次设置页填写 |
| `DIANA_ADMIN_LOGIN_PATH` | 空 | 可选的环境变量托管登录路径；设置后 WebUI 不能修改随机后缀模式 |
| `DIANA_ADMIN_AUTH_CONFIG_FILE` | SQLite 同目录下的 `admin-auth.json` | WebUI 随机登录后缀配置文件 |
| `DIANA_ADMIN_CREDENTIALS_FILE` | SQLite 同目录下的 `admin-credentials.json` | 管理员邮箱、bcrypt 密码哈希和 JWT 签名密钥；文件权限为 `0600` |
| `LLM_PROVIDER` | `openai_compatible` | LLM provider |
| `LLM_API_KEY` | 空 | LLM API Key |
| `LLM_BASE_URL` | 空 | OpenAI-compatible 自定义 Base URL |
| `LLM_API_FORMAT` | `responses` | OpenAI-compatible 文本接口格式：`responses` 或 `chat_completions` |
| `LLM_MODEL` | `gpt-4o-mini` | 默认模型 |
| WebUI LLM 配置集 | 多配置 | 支持命名保存多套 LLM 配置，并切换当前激活项 |
| `LLM_USER_AGENT` | `diana-qq-bot` | OpenAI-compatible User-Agent；可按兼容服务要求覆盖 |
| `LLM_IMAGE_MODEL` | provider 默认值 | 生图模型；OpenAI-compatible 默认 `gpt-image-2`，Gemini 默认 `imagen-4.0-generate-001` |
| `LLM_IMAGE_BASE_URL` | 沿用 `LLM_BASE_URL` | OpenAI-compatible 生图请求使用的独立 Base URL |
| `LLM_IMAGE_ORIGIN` | 空 | 生图请求直接连接的 `host:port`；保留 URL 的 Host/SNI，可绕过 CDN |
| `LLM_IMAGE_TIMEOUT_MS` | 沿用 `LLM_TIMEOUT_MS` | 生图请求独立超时，单位毫秒 |
| `LLM_TEMPERATURE` | 空 | temperature |
| `LLM_CONTEXT_WINDOW_TOKENS` | `16384` | 模型允许的上下文窗口；模型列表提供限制时 WebUI 会自动填充 |
| `LLM_MAX_CONTEXT_TOKENS` | `16384` | 单次请求的总上下文预算，运行时会保留输出空间且绝不高于模型窗口 |
| `LLM_MAX_OUTPUT_TOKENS` | `1024` | 文本模型最大输出 token 数；设为 `0` 时不向兼容接口发送该参数 |
| `LLM_TIMEOUT_MS` | `60000` | 单次 LLM 请求超时，单位毫秒；瞬时网络错误会重试一次 |
| `QQBOT_ENABLED` | `false` | 启动时是否自动启用机器人 |
| `ONEBOT_REVERSE_WS_ENDPOINT` | `ws://127.0.0.1:<PORT>/onebot/v11/ws` | 给 NapCat 连接的反向 WebSocket 地址 |
| `ONEBOT_ACCESS_TOKEN` | 空 | OneBot access token |
| `DIANA_PASSIVE_REPLY_CHANCE` | `1` | 群聊未直接唤醒时的主动插话采样率，`1` 表示严格路由判定后必回 |
| `DIANA_PASSIVE_REPLY_THRESHOLD` | `0.8` | 主动插话语义路由的最低置信度；仅明确需要机器人回复或与机器人相关的消息可通过 |
| `DIANA_HEADLESS_BROWSER_EXECUTABLE` | 自动查找 | 沙盒网页渲染使用的 Chrome/Chromium 可执行文件路径或命令名 |
| `DIANA_HEADLESS_BROWSER_TIMEOUT_MS` | `25000` | 单个网页的无头浏览器渲染超时 |
| `DIANA_HEADLESS_BROWSER_VIRTUAL_TIME_MS` | `8000` | 页面加载后允许 JavaScript 继续执行的虚拟时间预算 |
| `DIANA_HEADLESS_BROWSER_MAX_CHARS` | `8000` | 单个网页交给 LLM 的最大可见正文字符数 |
| `DIANA_OCR_CONCURRENCY` | `3` | 单个扫描 PDF 并行渲染和视觉 OCR 的页数 |
| `DIANA_OCR_RENDER_CONCURRENCY` | `3` | 内嵌 PDFium WASM 渲染 worker 数 |
| `DIANA_OCR_MAX_PAGES_PER_FILE` | `48` | 单个 PDF 默认最多识别页数 |
| `DIANA_OCR_TASK_TIMEOUT_MINUTES` | `60` | 单个后台 OCR 子任务总超时 |
| `DIANA_OCR_RENDER_DPI` | `180` | PDF 页面视觉 OCR 渲染 DPI |
| `DIANA_OCR_FINAL_CONTEXT_CHARS` | `60000` | 超过该长度时先由多个子代理分块整理 |
| `DIANA_OCR_CACHE_DIR` | SQLite 同目录下的 `ocr-cache` | OCR 逐页结果缓存目录 |
| `DIANA_TTS_VOICE_NAME` | `自定义` | TTS 插件中显示的音色名称；实际音色由模型或参考音频决定 |
| `DIANA_TTS_ENDPOINT` | `http://127.0.0.1:9880/tts` | 语音插件使用的 GPT-SoVITS `/tts` 接口 |
| `DIANA_TTS_API_KEY` | 空 | 可选的 TTS Bearer Token |
| `DIANA_TTS_REF_AUDIO_PATH` | 空 | GPT-SoVITS 参考音频路径 |
| `DIANA_TTS_PROMPT_TEXT` | 空 | 参考音频对应文本；使用微调模型时可留空 |
| `DIANA_TTS_TEXT_LANG` | `zh` | 合成文本语言 |
| `DIANA_TTS_PROMPT_LANG` | `zh` | 参考音频语言 |
| `DIANA_TTS_OUTPUT_DIR` | SQLite 同目录下的 `tts-cache` | 临时语音缓存目录 |
| `DIANA_TTS_TIMEOUT_SECONDS` | `120` | 单次语音合成超时 |
| `DIANA_TTS_MAX_CHARS` | `500` | 单条语音最大字符数 |
| `DIANA_TTS_SPEED_FACTOR` | `1.0` | GPT-SoVITS 语速，允许 `0.5` 到 `2.0` |
| `DIANA_TTS_FFMPEG_PATH` | `ffmpeg` | 将合成 WAV 转为 24 kHz 单声道 PCM 的系统 ffmpeg |
| `DIANA_TTS_SILK_ENCODER_PATH` | 空 | 可选的 Tencent Silk 编码器；macOS NapCat 发送 QQ 语音时应配置 |
| `DIANA_TTS_SILK_BITRATE` | `25000` | Tencent Silk 编码码率，单位 bit/s |
| `NONEBOT_BRIDGE_ENABLED` | `false` | 是否启用第三方 NoneBot 插件桥 |
| `NONEBOT_BRIDGE_ENDPOINT` | `ws://127.0.0.1:8080/onebot/v11/ws` | NoneBot sidecar 的反向 WebSocket 地址 |
| `NONEBOT_BRIDGE_TOKEN` | 空 | NoneBot 插件桥 access token |
| `QQBOT_QQ` | 空 | 机器人 QQ 号 |
| `DIANA_OWNER_ID` | 空 | 主人 QQ 号 |
| `DIANA_GROUP_TRIGGERS` | `Diana,diana` | 群聊触发词 |
| `DIANA_SYSTEM_PROMPT` | 内置提示词 | 机器人系统提示词 |
| `DIANA_LLM_QQ_ID_MASKING_ENABLED` | `true` | 发送到 LLM 前把 QQ 号和群号替换为角色化别名，并在本地执行工具或发送回复前还原 |
| `DIANA_MAX_INPUT_CHARS` | `2000` | 单次输入最大字符数 |
| `DIANA_MAX_REPLY_CHARS` | `3500` | 单次回复最大字符数 |
| `DIANA_DIRECT_REPLY_CHUNK_SIZE` | `900` | 文本分段发送字符数 |
| `DIANA_MAX_BOT_CONCURRENCY` | `8` | 全局并发数 |
| `DIANA_AGENT_ENABLED` | `true` | 是否启用内置 Agent |
| `DIANA_AGENT_WORK_DIR` | `.` | Agent 可访问的工作目录 |
| `AGENT_WORK_DIR` | `.` | `DIANA_AGENT_WORK_DIR` 的兼容别名 |
| `DIANA_AGENT_MAX_STEPS` | `8` | Agent 单次回复最大工具循环步数，最高 `8` |
| `DIANA_AGENT_COMMAND_ALLOWLIST` | 常见开发命令 | Agent `run_command` 可执行命令，逗号分隔；填 `*` 允许全部命令 |
| `DIANA_AGENT_COMMAND_TIMEOUT_MS` | `10000` | Agent 本地命令执行超时，最高 `60000` |
| `DIANA_WEB_SEARCH_CONFIG_FILE` | `<Agent 工作目录>/web-search.json` | 联网搜索配置文件；可直接在 WebUI 的“联网搜索”页管理 |
| `DIANA_WEB_SEARCH_CONFIGS` | 空 | 内联 JSON 配置；设置后优先于配置文件，主要用于只读部署环境 |
| `TAVILY_API_KEY` | 空 | 默认 Tavily 回退配置使用的 API Key；也可在 WebUI 中改成其他环境变量名 |
| `DIANA_AGENT_BROWSER_CDP_URL` | `http://127.0.0.1:9222` | 浏览器工具连接的 Chrome DevTools 地址 |
| `AGENT_BROWSER_CDP_URL` | 同上 | `DIANA_AGENT_BROWSER_CDP_URL` 的兼容别名 |
| `DIANA_AGENT_BROWSER_TIMEOUT_MS` | `15000` | 浏览器工具调用超时，最高 `60000` |

## systemd 示例

先创建日志目录：

```sh
sudo mkdir -p /var/log/diana-qq-bot
sudo chown -R $USER:$USER /var/log/diana-qq-bot
```

```ini
[Unit]
Description=Diana QQ Bot
After=network.target

[Service]
Type=simple
WorkingDirectory=/opt/diana-qq-bot
Environment=PORT=18080
Environment=LOG_PATH=/var/log/diana-qq-bot/diana-qq-bot.log
Environment=QQBOT_ENABLED=true
Environment=ONEBOT_REVERSE_WS_ENDPOINT=ws://127.0.0.1:18080/onebot/v11/ws
Environment=ONEBOT_ACCESS_TOKEN=change-me
Environment=QQBOT_QQ=your-bot-qq
Environment=LLM_PROVIDER=openai_compatible
Environment=LLM_API_KEY=change-me
Environment=LLM_MODEL=gpt-4o-mini
Environment=LLM_USER_AGENT=diana-qq-bot
ExecStart=/opt/diana-qq-bot/diana-qq-bot-webui
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
```

## 开发命令

后端测试：

```sh
go test ./...
```

前端开发：

```sh
cd frontend
npm run dev
```

生产构建：

```sh
cd frontend
npm run build
cd ..
go build -o dist/diana-qq-bot-webui ./cmd/webui
```

## 目录

```text
.
├── cmd/webui/              # Gin WebUI 和 OneBot endpoint 入口
├── frontend/               # Vue + TypeScript 前端
├── model/llm/              # LLM 统一接口和 provider adapters
├── model/qqbot/            # QQ 机器人运行时、OneBot 通道、插件系统
├── webui/                  # WebUI API handler
├── .github/workflows/      # GitHub Actions CI/CD
├── LICENSE
└── go.mod
```

## 许可证

本项目使用 `Limited Redistribution License (SuInk)`，详见 [LICENSE](./LICENSE)。
