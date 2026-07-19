package webui

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"diana-qq-bot/model/updater"

	"github.com/gin-gonic/gin"
)

const systemUpdateTimeout = 30 * time.Minute

type SystemUpdater interface {
	Status(context.Context) (updater.Status, error)
	Check(context.Context) (updater.Status, error)
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
	refresh, _ := strconv.ParseBool(c.Query("refresh"))
	var (
		status updater.Status
		err    error
	)
	if refresh {
		status, err = h.updater.Check(c.Request.Context())
	} else {
		status, err = h.updater.Status(c.Request.Context())
	}
	if err != nil {
		// 状态页会频繁轮询，查询失败只返回 HTTP 错误，避免把日志中心刷满。
		writeUpdateHTTPError(c, err)
		return
	}
	c.JSON(http.StatusOK, status)
}

// update 执行系统更新并记录操作日志。
func (h *SystemUpdateHandler) update(c *gin.Context) {
	// Once replacement starts it must survive a closed browser tab; otherwise a
	// canceled request could kill the apply script between backup and restore.
	updateCtx, cancel := context.WithTimeout(context.Background(), systemUpdateTimeout)
	defer cancel()
	result, err := h.updater.Update(updateCtx)
	if err != nil {
		// 真正的更新动作属于运维操作，失败需要写入错误日志。
		h.writeUpdateError(c, "system.update.apply", err)
		return
	}
	message := "系统更新已执行"
	if !result.Updated {
		message = "系统更新已检查"
	}
	recordRequestOperation(c, h.logs, "system.update.apply", message, result.Status.Root, map[string]any{
		"branch":           result.Status.Branch,
		"remote":           result.Status.RemoteURL,
		"updated":          result.Updated,
		"source_updated":   result.SourceUpdated,
		"applied":          result.Applied,
		"restart_required": result.RestartRequired,
		"fetched":          result.Fetched,
	})
	c.JSON(http.StatusOK, result)
}

// writeUpdateError 记录系统更新错误并返回响应。
func (h *SystemUpdateHandler) writeUpdateError(c *gin.Context, action string, err error) {
	switch {
	case errors.Is(err, updater.ErrUpdateInProgress),
		errors.Is(err, updater.ErrWorkingTreeDirty),
		errors.Is(err, updater.ErrNonFastForward):
		logAndWriteError(c, h.logs, http.StatusConflict, action, err, "", nil)
	case errors.Is(err, updater.ErrRemoteNotConfigured),
		errors.Is(err, updater.ErrRemoteBranchMissing),
		errors.Is(err, updater.ErrDetachedHead),
		errors.Is(err, updater.ErrRepositoryNotFound):
		logAndWriteError(c, h.logs, http.StatusBadRequest, action, err, "", nil)
	case errors.Is(err, updater.ErrFetchFailed):
		logAndWriteError(c, h.logs, http.StatusBadGateway, action, err, "", nil)
	default:
		logAndWriteError(c, h.logs, http.StatusInternalServerError, action, err, "", nil)
	}
}

// writeUpdateHTTPError 返回系统更新状态查询错误。
func writeUpdateHTTPError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, updater.ErrUpdateInProgress):
		writeError(c, http.StatusConflict, err)
	case errors.Is(err, updater.ErrRemoteNotConfigured),
		errors.Is(err, updater.ErrDetachedHead),
		errors.Is(err, updater.ErrRepositoryNotFound):
		writeError(c, http.StatusBadRequest, err)
	case errors.Is(err, updater.ErrFetchFailed):
		writeError(c, http.StatusBadGateway, err)
	default:
		writeError(c, http.StatusInternalServerError, err)
	}
}
