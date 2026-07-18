package webui

import (
	"context"
	"errors"
	"net/http"

	"diana-qq-bot/model/updater"

	"github.com/gin-gonic/gin"
)

type SystemUpdater interface {
	Status(context.Context) (updater.Status, error)
	Update(context.Context) (updater.Result, error)
}

type SystemUpdateHandler struct {
	updater SystemUpdater
	logs    AppLogWriter
}

// NewSystemUpdateHandler 创建系统更新接口处理器。
func NewSystemUpdateHandler(updater SystemUpdater) *SystemUpdateHandler {
	return &SystemUpdateHandler{updater: updater}
}

// SetLogStore 注入系统更新操作日志写入器。
func (h *SystemUpdateHandler) SetLogStore(store AppLogWriter) {
	h.logs = store
}

// Register 注册系统更新状态和执行接口。
func (h *SystemUpdateHandler) Register(router gin.IRouter) {
	router.GET("/api/system/update", h.status)
	router.POST("/api/system/update", h.update)
}

// status 处理系统更新状态查询请求。
func (h *SystemUpdateHandler) status(c *gin.Context) {
	status, err := h.updater.Status(c.Request.Context())
	if err != nil {
		// 状态页会频繁轮询，查询失败只返回 HTTP 错误，避免把日志中心刷满。
		writeUpdateHTTPError(c, err)
		return
	}
	c.JSON(http.StatusOK, status)
}

// update 执行系统更新并记录操作日志。
func (h *SystemUpdateHandler) update(c *gin.Context) {
	result, err := h.updater.Update(c.Request.Context())
	if err != nil {
		// 真正的更新动作属于运维操作，失败需要写入错误日志。
		h.writeUpdateError(c, "system.update.pull", err)
		return
	}
	message := "系统更新已执行"
	if !result.Updated {
		message = "系统更新已检查"
	}
	recordRequestOperation(c, h.logs, "system.update.pull", message, result.Status.Root, map[string]any{
		"branch":  result.Status.Branch,
		"remote":  result.Status.RemoteURL,
		"updated": result.Updated,
		"fetched": result.Fetched,
	})
	c.JSON(http.StatusOK, result)
}

// writeUpdateError 记录系统更新错误并返回响应。
func (h *SystemUpdateHandler) writeUpdateError(c *gin.Context, action string, err error) {
	if errors.Is(err, updater.ErrRemoteNotConfigured) {
		logAndWriteError(c, h.logs, http.StatusBadRequest, action, err, "", nil)
		return
	}
	logAndWriteError(c, h.logs, http.StatusBadRequest, action, err, "", nil)
}

// writeUpdateHTTPError 返回系统更新状态查询错误。
func writeUpdateHTTPError(c *gin.Context, err error) {
	if errors.Is(err, updater.ErrRemoteNotConfigured) {
		writeError(c, http.StatusBadRequest, err)
		return
	}
	writeError(c, http.StatusBadRequest, err)
}
