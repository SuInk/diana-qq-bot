# Diana QQ Bot

[中文](./README.md)

Diana QQ Bot is a Go-based QQ bot service with an LLM compatibility layer, NapCat / OneBot v11 reverse WebSocket support, a Gin WebUI, and bot plugin management. The WebUI can configure models, bot connection settings, trigger aliases, and official built-in plugins.

Deployment values live in the WebUI/SQLite, environment variables, or ignored local configuration files. The repository must not contain real QQ IDs, group IDs, conversations, cookies, or API keys. Start from [`.env.example`](./.env.example), run `make audit-public` before committing, and see [`CONTRIBUTING.md`](./CONTRIBUTING.md) for fixture rules.

## Requirements

- NapCat with OneBot v11 reverse WebSocket enabled
- Go `1.25.8`, Node.js `22`, and npm when installing from source
- Docker or Docker Compose when deploying with Docker

## Docker Deployment

Build the image:

```sh
docker build -t diana-qq-bot:latest .
```

Run the container:

```sh
export DIANA_ADMIN_TOKEN="$(openssl rand -hex 32)"
export DIANA_ADMIN_LOGIN_PATH="/access-$(openssl rand -hex 16)"
export DIANA_NAPCAT_WEBUI_TOKEN="$(openssl rand -hex 32)"

docker run -d \
  --name diana-qq-bot \
  --restart unless-stopped \
  -p 18080:18080 \
  -v "$PWD/logs:/app/logs" \
  -e LOG_PATH=/app/logs/diana-qq-bot.log \
  -e DIANA_ADMIN_TOKEN="$DIANA_ADMIN_TOKEN" \
  -e DIANA_ADMIN_LOGIN_PATH="$DIANA_ADMIN_LOGIN_PATH" \
  -e DIANA_NAPCAT_WEBUI_URL=http://napcat:6099 \
  -e DIANA_NAPCAT_WEBUI_TOKEN="$DIANA_NAPCAT_WEBUI_TOKEN" \
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

Docker Compose:

```sh
cp .env.example .env
# Fill in the admin token, QQ IDs, OneBot token, and LLM settings in .env.
docker compose up -d --build
```

After startup, open:

```text
http://127.0.0.1:18080${DIANA_ADMIN_LOGIN_PATH}
```

`DIANA_ADMIN_TOKEN` must contain at least 32 characters and should be generated from a cryptographically secure source such as `openssl rand -hex 32`. Generate `DIANA_ADMIN_LOGIN_PATH` independently; it must be a single path segment containing at least 11 characters. When omitted, it is derived from the token and printed in the startup log. The token itself is never logged. With authentication enabled, unauthenticated requests to known console paths such as `/console` return 404. A successful login creates a 12-hour HttpOnly session cookie. Scripts can call management APIs with `Authorization: Bearer <DIANA_ADMIN_TOKEN>`.

To log in to NapCat or switch quick-login accounts directly from Diana, attach both containers to the same Docker network, set `DIANA_NAPCAT_WEBUI_URL` to NapCat's internal WebUI address, and keep `DIANA_NAPCAT_WEBUI_TOKEN` equal to the strong random token in NapCat's `webui.json`. Diana uses this token only on the backend and never sends it to the browser.

Configure NapCat reverse WebSocket to the exposed host endpoint:

```text
ws://127.0.0.1:18080/onebot/v11/ws
```

If NapCat and the bot are not on the same machine, replace `127.0.0.1` with the bot host IP or domain.

## Install From Source

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

Start the service:

```sh
./dist/diana-qq-bot-webui
```

Default WebUI:

```text
http://127.0.0.1:18080
```

Set `DIANA_ADMIN_TOKEN` in production. You can also set a hard-to-guess `DIANA_ADMIN_LOGIN_PATH` as the management entry. Leaving the token empty preserves the unauthenticated local-development behavior.

## macOS Deployment

Apple Silicon:

```sh
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -o dist/diana-qq-bot-webui-darwin-arm64 ./cmd/webui
./dist/diana-qq-bot-webui-darwin-arm64
```

Intel Mac:

```sh
GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -o dist/diana-qq-bot-webui-darwin-amd64 ./cmd/webui
./dist/diana-qq-bot-webui-darwin-amd64
```

You can also download the `darwin-arm64` or `darwin-amd64` binary from GitHub Releases.

## Linux Deployment

amd64:

```sh
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o dist/diana-qq-bot-webui-linux-amd64 ./cmd/webui
./dist/diana-qq-bot-webui-linux-amd64
```

arm64:

```sh
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o dist/diana-qq-bot-webui-linux-arm64 ./cmd/webui
./dist/diana-qq-bot-webui-linux-arm64
```

For background operation, use the systemd example below.

## Windows Deployment

PowerShell:

```powershell
$env:GOOS="windows"
$env:GOARCH="amd64"
$env:CGO_ENABLED="0"
go build -o dist\diana-qq-bot-webui-windows-amd64.exe .\cmd\webui
.\dist\diana-qq-bot-webui-windows-amd64.exe
```

You can also download the `windows-amd64.exe` binary from GitHub Releases.

## Quick Run

For local development or testing, start the Go backend and Vite frontend together:

```sh
make dev
```

By default the backend runs at `http://127.0.0.1:18080` and the frontend runs at `http://127.0.0.1:5173`; Vite proxies `/api` and `/onebot` to the backend. You can change ports with environment variables:

```sh
make dev BACKEND_PORT=18081 FRONTEND_PORT=5174
```

If `make` is not installed, use the cross-platform Node script directly:

```sh
node scripts/dev.mjs
```

For backend-only or production builds:

```sh
make backend
make build
```

## Configure LLM

You can configure LLM settings in the WebUI or through environment variables:

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

Supported providers:

- `openai_compatible`
- `gemini`
- `anthropic`

The WebUI LLM configuration page directly displays the saved API key for local copy/edit workflows. Plain `GET /api/llm/config` still omits secrets by default; the frontend explicitly uses `include_secrets=true` when it needs the full configuration.

## WebUI Log Center

The WebUI `Log Center` page shows persistent operation logs and error logs. Operation logs cover actions such as saving or switching LLM profiles, starting or stopping the bot, managing plugins, and running system updates. Error logs record failed API operations. Logs include an `actor`: WebUI operations default to `web:<client IP>` and can be overridden by a gateway with headers such as `X-Diana-Actor`, `X-Operator`, or `X-Forwarded-User`; the QQ built-in LLM config skill records `qq:<user QQ>`.

```text
GET /api/logs?kind=operation&limit=100
GET /api/logs?kind=error&limit=100
```

These structured logs are stored in the SQLite database pointed to by `APP_DB_PATH`; `LOG_PATH` is still used for plain runtime log file output.

## Configure NapCat

This project directly serves a OneBot v11 reverse WebSocket endpoint:

```text
ws://127.0.0.1:18080/onebot/v11/ws
```

In NapCat, add a OneBot v11 reverse WebSocket connection and set the endpoint to the address above. If you configure an access token, NapCat and this service must use the same token.

Group and private messages are atomically written to a SQLite inbox before routing or reply generation. Pending work resumes after a process restart. After OneBot reconnects, Diana also backfills group and friend history from NapCat and deduplicates it by conversation watermark and `message_id`. Pending or backfilled messages may trigger replies for up to two hours after they were sent; older messages remain in history and the queue audit trail but are not replayed. The inbox provides at-least-once processing: normal restarts do not lose messages, while a crash after QQ accepts a send but before the completion record is committed can rarely produce one duplicate.

Bot startup example:

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

After startup, private messages trigger directly. In group chats, mentioning the bot or starting a message with a configured alias triggers the bot.

## Built-In Agent

You can enable the built-in Agent in the WebUI bot configuration page. When enabled, the bot handles messages through a Codex CLI-style loop: model planning, tool call, observation, and final response.

Built-in tools:

- `list_files`: list files under the Agent working directory.
- `read_file`: read text files under the Agent working directory.
- `run_command`: execute allowlisted local commands inside the Agent working directory, without a shell, with timeout and output limits.
- `web_search.search`: run live web searches in WebUI priority order and fall back after timeouts, rate limits, service errors, or empty results.
- `browser_open` / `browser_text` / `browser_click` / `browser_type` / `browser_screenshot`: control a browser through Chrome DevTools Protocol.

Agent tool calls are written to the operation log with the tool name, input field names, and output length. Tool output bodies and secrets are not stored in the audit entry.

The WebUI `Web Search` page manages multiple Exa MCP and Tavily providers, their failover order, and live per-provider tests. The defaults include keyless Exa MCP configurations and an optional Tavily fallback. Tavily credentials are read from the environment variable named in the provider configuration. See [`web-search.example.json`](./web-search.example.json).

Browser tools require Chrome/Chromium with a remote debugging port, for example:

```sh
chrome --remote-debugging-port=9222
```

Set `Agent work dir` to a dedicated reference directory. Avoid pointing it at directories that contain secrets or production data. Command execution is powerful; in production, set `DIANA_AGENT_COMMAND_ALLOWLIST` to only the commands you need.

## Install Plugins In WebUI

Open the WebUI and go to the bot plugins section:

1. View official built-in plugins.
2. Install or enable a plugin.
3. The default built-in Go version of `nonebot-plugin-resolver` resolves links from Bilibili, YouTube, X, Xiaohongshu, Douyin, and other platforms as LLM context.
4. The default built-in Go file parser handles QQ file segments and text file links. On macOS it reads PDF text layers with PDFKit and recognizes scanned pages locally with Vision. If the native path is unavailable, it falls back to sandboxed embedded PDFium WASM and the vision LLM. The first response carries the task ID, while progress and final messages do not repeatedly quote the original message.
5. The default built-in `LLM config skill` lets the owner change the active provider and model with natural language in chat, for example: `把提供商切到 gemini`, `把模型换成 gemini-2.5-pro`, or `以后用 anthropic 的 claude-sonnet-4-5`; requested models are validated against the backend model list before they are saved.

Text PDFs on macOS are extracted directly with PDFKit without starting per-page OCR. Actual scanned pages prefer local Vision recognition. Windows, Linux, and macOS systems without the native helper keep the embedded PDFium WASM plus vision-LLM fallback without requiring `pdftoppm`, Tesseract, or system Python. Long documents use reduction calls followed by one final synthesis call, and OCR text is cached by PDF SHA-256.

## Use Third-Party NoneBot Plugins

The Go process cannot directly load Python NoneBot plugins. To use third-party NoneBot2 plugins, run a separate NoneBot sidecar:

1. Install third-party plugins in your NoneBot2 project.
2. Configure the OneBot v11 reverse WebSocket driver in NoneBot.
3. Enable `NoneBot plugin bridge` in the Diana WebUI bot page.
4. The default `NoneBot reverse WebSocket` endpoint is:

```text
ws://127.0.0.1:8080/onebot/v11/ws
```

Diana forwards OneBot events received from NapCat to the NoneBot sidecar. When third-party plugins call OneBot APIs such as `send_msg` or `get_group_info`, Diana forwards those API calls back to NapCat. This keeps third-party plugins running in their native NoneBot2 environment.

## Common Environment Variables

| Variable | Default | Description |
| --- | --- | --- |
| `HOST` | `127.0.0.1` | HTTP listen address; the Docker image explicitly uses `0.0.0.0` |
| `PORT` | `18080` | WebUI and OneBot endpoint listen port |
| `DIANA_ADMIN_TOKEN` | empty | WebUI management token; must be at least 32 characters and protects console pages and APIs when set |
| `DIANA_ADMIN_LOGIN_PATH` | derived from token | Hidden login path; must be a single path segment containing at least 11 characters |
| `DIANA_NAPCAT_WEBUI_URL` | empty | Internal NapCat WebUI URL; enables login management when configured with the token |
| `DIANA_NAPCAT_WEBUI_TOKEN` | empty | Strong NapCat WebUI token; retained only in the Diana backend environment |
| `FRONTEND_DIST` | `frontend/dist` | Frontend build output directory |
| `LOG_PATH` | empty | Log file path; when set, logs are written to both stdout and the file |
| `DIANA_LOG_PATH` | empty | Compatibility alias for `LOG_PATH` |
| `APP_DB_PATH` | `data/diana-qq-bot.db` | Local SQLite configuration database path |
| `LLM_PROVIDER` | `openai_compatible` | LLM provider |
| `LLM_API_KEY` | empty | LLM API key |
| `LLM_BASE_URL` | empty | Custom OpenAI-compatible base URL |
| `LLM_API_FORMAT` | `responses` | OpenAI-compatible text API format: `responses` or `chat_completions` |
| `LLM_MODEL` | `gpt-4o-mini` | Default model |
| WebUI LLM profiles | multi-profile | Supports named LLM configuration profiles and switching the active one |
| `LLM_USER_AGENT` | `diana-qq-bot` | OpenAI-compatible User-Agent; override it when a compatible service requires another value |
| `LLM_IMAGE_MODEL` | provider default | Image generation model; defaults to `gpt-image-2` for OpenAI-compatible and `imagen-4.0-generate-001` for Gemini |
| `LLM_IMAGE_BASE_URL` | inherits `LLM_BASE_URL` | Independent base URL for OpenAI-compatible image requests |
| `LLM_IMAGE_ORIGIN` | empty | Direct `host:port` for image requests; preserves the URL Host/SNI and can bypass a CDN |
| `LLM_IMAGE_TIMEOUT_MS` | inherits `LLM_TIMEOUT_MS` | Independent image request timeout in milliseconds |
| `LLM_TEMPERATURE` | empty | temperature |
| `LLM_CONTEXT_WINDOW_TOKENS` | `16384` | Model context limit; the WebUI fills it from model metadata when available |
| `LLM_MAX_CONTEXT_TOKENS` | `16384` | Total request context budget; runtime reserves output space and never exceeds the model window |
| `LLM_MAX_OUTPUT_TOKENS` | `1024` | Maximum output tokens for text generation; `0` omits the parameter for compatible APIs |
| `LLM_TIMEOUT_MS` | `60000` | Per-attempt LLM timeout in milliseconds; transient network failures are retried once |
| `QQBOT_ENABLED` | `false` | Enable the bot automatically on startup |
| `ONEBOT_REVERSE_WS_ENDPOINT` | `ws://127.0.0.1:<PORT>/onebot/v11/ws` | Reverse WebSocket URL for NapCat |
| `ONEBOT_ACCESS_TOKEN` | empty | OneBot access token |
| `DIANA_PASSIVE_REPLY_CHANCE` | `1` | Sampling rate for proactive group replies when the bot is not directly invoked; `1` means always reply after the strict router accepts it |
| `DIANA_PASSIVE_REPLY_THRESHOLD` | `0.8` | Minimum semantic-router confidence; only messages that clearly need the bot or concern the bot may pass |
| `DIANA_OCR_CONCURRENCY` | `3` | Concurrent page render and vision OCR calls per scanned PDF |
| `DIANA_OCR_RENDER_CONCURRENCY` | `3` | Embedded PDFium WASM render workers |
| `DIANA_OCR_MAX_PAGES_PER_FILE` | `48` | Default maximum pages processed per PDF |
| `DIANA_OCR_TASK_TIMEOUT_MINUTES` | `60` | Total timeout for one background OCR task |
| `DIANA_OCR_RENDER_DPI` | `180` | PDF page render DPI for vision OCR |
| `DIANA_OCR_FINAL_CONTEXT_CHARS` | `60000` | Context size that triggers parallel reduction subagents |
| `DIANA_OCR_CACHE_DIR` | `ocr-cache` next to SQLite | Per-page OCR cache directory |
| `DIANA_TTS_VOICE_NAME` | `custom` | Voice name shown by the TTS plugin; the model or reference audio controls the actual voice |
| `DIANA_TTS_ENDPOINT` | `http://127.0.0.1:9880/tts` | GPT-SoVITS `/tts` endpoint |
| `DIANA_TTS_REF_AUDIO_PATH` | empty | GPT-SoVITS reference audio path |
| `NONEBOT_BRIDGE_ENABLED` | `false` | Enable the third-party NoneBot plugin bridge |
| `NONEBOT_BRIDGE_ENDPOINT` | `ws://127.0.0.1:8080/onebot/v11/ws` | Reverse WebSocket endpoint for the NoneBot sidecar |
| `NONEBOT_BRIDGE_TOKEN` | empty | NoneBot bridge access token |
| `QQBOT_QQ` | empty | Bot QQ number |
| `DIANA_OWNER_ID` | empty | Owner QQ number |
| `DIANA_GROUP_TRIGGERS` | `Diana,diana` | Group chat trigger aliases |
| `DIANA_SYSTEM_PROMPT` | built-in prompt | Bot system prompt |
| `DIANA_MAX_INPUT_CHARS` | `2000` | Max input characters per request |
| `DIANA_MAX_REPLY_CHARS` | `3500` | Max reply characters per request |
| `DIANA_DIRECT_REPLY_CHUNK_SIZE` | `900` | Text chunk size for direct sends |
| `DIANA_MAX_BOT_CONCURRENCY` | `8` | Global concurrency |
| `DIANA_AGENT_ENABLED` | `true` | Enable the built-in Agent |
| `DIANA_AGENT_WORK_DIR` | `.` | Working directory available to Agent tools |
| `AGENT_WORK_DIR` | `.` | Compatibility alias for `DIANA_AGENT_WORK_DIR` |
| `DIANA_AGENT_MAX_STEPS` | `8` | Max Agent tool-loop steps per reply, capped at `8` |
| `DIANA_AGENT_COMMAND_ALLOWLIST` | common dev commands | Commands available to Agent `run_command`, comma-separated; `*` allows all commands |
| `DIANA_AGENT_COMMAND_TIMEOUT_MS` | `10000` | Local command timeout, capped at `60000` |
| `DIANA_WEB_SEARCH_CONFIG_FILE` | `<Agent work dir>/web-search.json` | Web search configuration file, also managed by the WebUI `Web Search` page |
| `DIANA_WEB_SEARCH_CONFIGS` | empty | Inline JSON configuration; overrides the file for read-only deployments |
| `TAVILY_API_KEY` | empty | API key used by the default Tavily fallback; the environment variable name is configurable in WebUI |
| `DIANA_AGENT_BROWSER_CDP_URL` | `http://127.0.0.1:9222` | Chrome DevTools URL for browser tools |
| `AGENT_BROWSER_CDP_URL` | same | Compatibility alias for `DIANA_AGENT_BROWSER_CDP_URL` |
| `DIANA_AGENT_BROWSER_TIMEOUT_MS` | `15000` | Browser tool timeout, capped at `60000` |

## systemd Example

Create the log directory first:

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

## Development Commands

Backend tests:

```sh
go test ./...
```

Frontend development:

```sh
cd frontend
npm run dev
```

Production build:

```sh
cd frontend
npm run build
cd ..
go build -o dist/diana-qq-bot-webui ./cmd/webui
```

## Project Layout

```text
.
├── cmd/webui/              # Gin WebUI and OneBot endpoint entrypoint
├── frontend/               # Vue + TypeScript frontend
├── model/llm/              # Unified LLM interface and provider adapters
├── model/qqbot/            # QQ bot runtime, OneBot channel, and plugin system
├── webui/                  # WebUI API handlers
├── .github/workflows/      # GitHub Actions CI/CD
├── LICENSE
└── go.mod
```

## License

This project uses the `Limited Redistribution License (SuInk)`. See [LICENSE](./LICENSE).
