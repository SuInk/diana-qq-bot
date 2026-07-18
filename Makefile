# Diana QQ Bot common commands.
#
# Usage examples:
#   make dev
#   make dev BACKEND_PORT=18081 FRONTEND_PORT=5174
#   make test
#   make build

BACKEND_HOST ?= 127.0.0.1
BACKEND_PORT ?= 18080
FRONTEND_HOST ?= 127.0.0.1
FRONTEND_PORT ?= 5173
VITE_BACKEND_TARGET ?= http://$(BACKEND_HOST):$(BACKEND_PORT)

GO ?= go
NODE ?= node
NPM ?= npm
DOCKER ?= docker
DOCKER_COMPOSE ?= docker compose

ifeq ($(OS),Windows_NT)
BIN_EXT := .exe
else
BIN_EXT :=
endif

DIST_BIN := dist/diana-qq-bot-webui$(BIN_EXT)

export BACKEND_HOST
export BACKEND_PORT
export FRONTEND_HOST
export FRONTEND_PORT
export VITE_BACKEND_TARGET

.DEFAULT_GOAL := help

.PHONY: help dev backend frontend deps fmt audit-public test test-go test-web build build-go build-local-mac install-local-mac-app start-napcat-mac build-web run preview clean docker-build docker-up docker-down

help:
	@$(NODE) -e "console.log(['Diana QQ Bot Makefile','', 'Usage:', '  make dev                         Start Go backend and Vite frontend', '  make dev BACKEND_PORT=18081      Start with custom backend port', '  make backend                     Start Go backend only', '  make frontend                    Start Vite frontend only', '  make deps                        Install Go and frontend dependencies', '  make fmt                         Format Go code', '  make audit-public                Scan tracked files for private data and secrets', '  make test                        Run public audit, Go tests, and frontend build', '  make build                       Build frontend and backend binary', '  make build-local-mac             Build a stable macOS-signed binary', '  make install-local-mac-app       Install the macOS app identity', '  make start-napcat-mac            Start QQ with NapCat on macOS', '  make run                         Build frontend, then run backend', '  make clean                       Remove build artifacts', '  make docker-build                Build Docker image', '  make docker-up                   Start Docker Compose stack', '  make docker-down                 Stop Docker Compose stack'].join('\n'))"

dev:
	$(NODE) scripts/dev.mjs

backend:
	$(GO) run ./cmd/webui

frontend:
	cd frontend && $(NPM) run dev -- --host $(FRONTEND_HOST) --port $(FRONTEND_PORT) --strictPort

deps:
	$(GO) mod download
	cd frontend && $(NPM) ci

fmt:
	$(GO) fmt ./...

audit-public:
	./scripts/check-public-repo.sh

test: audit-public test-go test-web

test-go:
	$(GO) test ./...

test-web:
	cd frontend && $(NPM) run build

build: build-web build-go

build-go:
	$(NODE) -e "require('fs').mkdirSync('dist', { recursive: true })"
	$(GO) build -o $(DIST_BIN) ./cmd/webui

build-local-mac:
	GO="$(GO)" ./scripts/build-local-mac.sh "$(CURDIR)/dist/diana-qq-bot-webui"

install-local-mac-app: build-web
	GO="$(GO)" ./scripts/build-local-mac.sh "$(HOME)/Applications/Diana QQ Bot.app"
	$(NODE) -e "const fs=require('fs'); const src='$(CURDIR)/frontend/dist'; const dst='$(HOME)/Library/Application Support/diana-qq-bot/frontend/dist'; fs.rmSync(dst,{recursive:true,force:true}); fs.mkdirSync(require('path').dirname(dst),{recursive:true}); fs.cpSync(src,dst,{recursive:true})"

start-napcat-mac:
	./scripts/start-napcat-mac.sh

build-web:
	cd frontend && $(NPM) run build

run: build-web
	$(GO) run ./cmd/webui

preview:
	cd frontend && $(NPM) run preview -- --host $(FRONTEND_HOST)

clean:
	$(NODE) -e "const fs=require('fs'); for (const p of ['dist','frontend/dist']) fs.rmSync(p,{recursive:true,force:true})"

docker-build:
	$(DOCKER) build -t diana-qq-bot:latest .

docker-up:
	$(DOCKER_COMPOSE) up -d --build

docker-down:
	$(DOCKER_COMPOSE) down
