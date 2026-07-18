package webui

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"diana-qq-bot/model/storage"

	"github.com/gin-gonic/gin"
)

type AppLogStore interface {
	AppendLog(context.Context, storage.AppLogEntry) error
	ListLogs(context.Context, storage.AppLogFilter) ([]storage.AppLogEntry, error)
}

// AppLogWriter 只暴露写入能力，避免只记录审计事件的 handler 依赖日志查询接口。
type AppLogWriter interface {
	AppendLog(context.Context, storage.AppLogEntry) error
}

type AppLogHandler struct {
	store AppLogStore
}

type appLogsResponse struct {
	Logs []storage.AppLogEntry `json:"logs"`
}

// NewAppLogHandler 创建 AppLogHandler 实例。
func NewAppLogHandler(store AppLogStore) *AppLogHandler {
	return &AppLogHandler{store: store}
}

// Register 注册当前模块的路由或能力。
func (h *AppLogHandler) Register(router gin.IRouter) {
	router.GET("/api/logs", h.list)
}

// list 封装当前模块的 list 逻辑。
func (h *AppLogHandler) list(c *gin.Context) {
	kind := storage.AppLogKind(strings.TrimSpace(c.Query("kind")))
	if kind != "" && kind != storage.LogKindOperation && kind != storage.LogKindError {
		writeError(c, http.StatusBadRequest, fmt.Errorf("unsupported log kind %q", kind))
		return
	}
	limit, err := parseLogLimit(c.Query("limit"))
	if err != nil {
		writeError(c, http.StatusBadRequest, err)
		return
	}
	// 日志存储不可用时返回空列表，前端日志页仍可正常打开。
	if h.store == nil {
		c.JSON(http.StatusOK, appLogsResponse{Logs: []storage.AppLogEntry{}})
		return
	}
	logs, err := h.store.ListLogs(c.Request.Context(), storage.AppLogFilter{
		Kind:  kind,
		Limit: limit,
	})
	if err != nil {
		writeError(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, appLogsResponse{Logs: logs})
}

// parseLogLimit 解析日志列表接口的 limit 参数。
func parseLogLimit(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 0 {
		return 0, fmt.Errorf("limit must be a non-negative integer")
	}
	return limit, nil
}

// recordOperation 写入一条后台操作日志。
func recordOperation(ctx context.Context, logger AppLogWriter, action string, message string, target string, metadata map[string]any) {
	recordAppLog(ctx, logger, storage.AppLogEntry{
		Kind:     storage.LogKindOperation,
		Level:    storage.LogLevelInfo,
		Action:   action,
		Message:  message,
		Target:   target,
		Metadata: metadata,
	})
}

// recordRequestOperation 写入带 HTTP 操作者信息的操作日志。
func recordRequestOperation(c *gin.Context, logger AppLogWriter, action string, message string, target string, metadata map[string]any) {
	recordAppLog(c.Request.Context(), logger, storage.AppLogEntry{
		Kind:     storage.LogKindOperation,
		Level:    storage.LogLevelInfo,
		Action:   action,
		Message:  message,
		Actor:    requestActor(c),
		Target:   target,
		Metadata: metadata,
	})
}

// recordError 用于没有 HTTP 请求上下文的后台任务，例如保存运行态失败。
func recordError(ctx context.Context, logger AppLogWriter, action string, err error, target string, metadata map[string]any) {
	if err == nil {
		return
	}
	recordAppLog(ctx, logger, storage.AppLogEntry{
		Kind:     storage.LogKindError,
		Level:    storage.LogLevelError,
		Action:   action,
		Message:  err.Error(),
		Detail:   err.Error(),
		Target:   target,
		Metadata: metadata,
	})
}

// logAndWriteError 记录接口错误并返回 HTTP 错误响应。
func logAndWriteError(c *gin.Context, logger AppLogWriter, status int, action string, err error, target string, metadata map[string]any) {
	if err != nil {
		recordAppLog(c.Request.Context(), logger, storage.AppLogEntry{
			Kind:     storage.LogKindError,
			Level:    storage.LogLevelError,
			Action:   action,
			Message:  err.Error(),
			Detail:   err.Error(),
			Actor:    requestActor(c),
			Target:   target,
			Metadata: metadata,
		})
	}
	writeError(c, status, err)
}

// 日志写入失败不能影响原始业务请求，失败时只写普通运行日志便于排查。
func recordAppLog(ctx context.Context, logger AppLogWriter, entry storage.AppLogEntry) {
	if logger == nil {
		return
	}
	if err := logger.AppendLog(ctx, entry); err != nil {
		log.Printf("app log skipped: %v", err)
	}
}

// 优先使用网关传入的操作者身份；本地直连时退回到客户端 IP，日志里仍能看到操作人。
func requestActor(c *gin.Context) string {
	for _, header := range []string{
		"X-Diana-Actor",
		"X-Operator",
		"X-Forwarded-User",
		"X-Remote-User",
		"X-User",
		"X-User-ID",
	} {
		if value := strings.TrimSpace(c.GetHeader(header)); value != "" {
			return value
		}
	}
	if ip := strings.TrimSpace(c.ClientIP()); ip != "" {
		return "web:" + ip
	}
	return "web:unknown"
}
