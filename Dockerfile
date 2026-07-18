# syntax=docker/dockerfile:1

FROM node:22-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM golang:1.25-alpine AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /src/frontend/dist ./frontend/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/diana-qq-bot-webui ./cmd/webui

FROM alpine:3.22
WORKDIR /app
RUN apk add --no-cache chromium ffmpeg yt-dlp \
    && adduser -D -H -u 10001 diana
COPY --from=backend /out/diana-qq-bot-webui /app/diana-qq-bot-webui
COPY --from=frontend /src/frontend/dist /app/frontend/dist
ENV PORT=18080
ENV HOST=0.0.0.0
ENV FRONTEND_DIST=/app/frontend/dist
ENV LOG_PATH=/app/logs/diana-qq-bot.log
EXPOSE 18080
USER diana
ENTRYPOINT ["/app/diana-qq-bot-webui"]
