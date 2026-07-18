package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"diana-qq-bot/model/qqbot"
	"diana-qq-bot/model/storage"

	"github.com/gin-gonic/gin"
)

type QQBotRuntime interface {
	Start(context.Context) error
	Stop() error
	UpdateConfig(context.Context, qqbot.BotConfig, qqbot.Channel) error
	Config() qqbot.BotConfig
	Status() qqbot.RuntimeStatus
	CallOneBotAPI(context.Context, string, map[string]any) (map[string]any, error)
	SendGroupMessage(context.Context, string, string) (map[string]any, error)
	RunPluginTask(context.Context, qqbot.PluginTask) (qqbot.PluginTaskResult, error)
	Plugins() *qqbot.PluginManager
}

type QQBotChannelFactory func(qqbot.BotConfig) qqbot.Channel

type QQBotHandler struct {
	runtime      QQBotRuntime
	newChannel   QQBotChannelFactory
	ctx          context.Context
	profiles     QQBotProfileStore
	groupConfigs QQBotGroupConfigStore
	groupAdmin   *groupAdminVerifier
	localMedia   qqbot.LocalMediaSharer
	sqlite       *storage.SQLiteStore
	logs         AppLogWriter
	features     QQBotFeatureFlags
}

type QQBotFeatureFlags struct {
	GroupTest bool `json:"group_test"`
}

type pluginEnabledPayload struct {
	Enabled bool `json:"enabled"`
}

type groupTestPayload struct {
	GroupID string `json:"group_id"`
	Message string `json:"message"`
}

type groupTestRecallPayload struct {
	MessageID string `json:"message_id"`
}

type groupTestFilePayload struct {
	GroupID   string `json:"group_id"`
	FileID    string `json:"file_id"`
	BusID     string `json:"busid,omitempty"`
	Name      string `json:"name"`
	LocalPath string `json:"local_path,omitempty"`
}

type groupTestUploadFilePayload struct {
	GroupID string `json:"group_id"`
	File    string `json:"file"`
	Name    string `json:"name"`
}

type groupTestOneBotPayload struct {
	Action string         `json:"action"`
	Params map[string]any `json:"params"`
}

type groupAdminChallengePayload struct {
	GroupID string `json:"group_id"`
	UserID  string `json:"user_id"`
}

type groupAdminChallengeResponse struct {
	GroupID   string    `json:"group_id"`
	UserID    string    `json:"user_id"`
	ExpiresAt time.Time `json:"expires_at"`
	Message   string    `json:"message"`
}

type groupAdminVerifyPayload struct {
	GroupID string `json:"group_id"`
	UserID  string `json:"user_id"`
	Code    string `json:"code"`
}

type groupAdminSessionPayload struct {
	Token  string            `json:"token"`
	Config qqbot.GroupConfig `json:"config,omitempty"`
}

type groupAdminConfigResponse struct {
	GroupID   string              `json:"group_id"`
	UserID    string              `json:"user_id,omitempty"`
	Token     string              `json:"token,omitempty"`
	ExpiresAt time.Time           `json:"expires_at,omitempty"`
	Config    qqbot.GroupConfig   `json:"config"`
	Plugins   []qqbot.PluginState `json:"plugins"`
}

type groupTestResponse struct {
	GroupID      string              `json:"group_id"`
	Message      string              `json:"message,omitempty"`
	MessageID    string              `json:"message_id,omitempty"`
	Sent         bool                `json:"sent"`
	SendResult   map[string]any      `json:"send_result,omitempty"`
	Channel      qqbot.ChannelStatus `json:"channel"`
	RecentEvents []qqbot.EventRecord `json:"recent_events,omitempty"`
	Status       qqbot.RuntimeStatus `json:"status"`
}

type groupTestRecallResponse struct {
	MessageID string         `json:"message_id"`
	Recalled  bool           `json:"recalled"`
	Result    map[string]any `json:"result,omitempty"`
}

type groupTestFileResponse struct {
	GroupID string `json:"group_id"`
	FileID  string `json:"file_id"`
	Name    string `json:"name"`
	Context string `json:"context"`
}

const minQQBotTokenChars = 16

// NewQQBotHandler 创建 QQBotHandler 实例。
func NewQQBotHandler(ctx context.Context, runtime QQBotRuntime) *QQBotHandler {
	return NewQQBotHandlerWithFactory(ctx, runtime, func(cfg qqbot.BotConfig) qqbot.Channel {
		return qqbot.NewOneBotReverseServer(qqbot.OneBotConfig{
			Endpoint:    cfg.OneBotReverseWSEndpoint,
			AccessToken: cfg.OneBotAccessToken,
		})
	})
}

// NewQQBotHandlerWithFactory 创建 QQBotHandler 实例。
func NewQQBotHandlerWithFactory(ctx context.Context, runtime QQBotRuntime, factory QQBotChannelFactory) *QQBotHandler {
	return &QQBotHandler{
		runtime:    runtime,
		newChannel: factory,
		ctx:        ctx,
		// 没有显式持久化 store 时，至少保证本次进程内也能按配置集语义工作。
		profiles:     NewMemoryQQBotProfileStore(runtime.Config()),
		groupConfigs: NewMemoryQQBotGroupConfigStore(),
		groupAdmin:   newGroupAdminVerifier(),
	}
}

// SetFeatureFlags 配置只应在显式测试环境开放的 WebUI 功能。
func (h *QQBotHandler) SetFeatureFlags(flags QQBotFeatureFlags) {
	h.features = flags
}

// SetLocalMediaSharer lets NapCat fetch local test files over loopback HTTP.
func (h *QQBotHandler) SetLocalMediaSharer(sharer qqbot.LocalMediaSharer) {
	h.localMedia = sharer
}

// SetProfileStore 注入 QQ 机器人配置集存储。
func (h *QQBotHandler) SetProfileStore(store QQBotProfileStore) {
	if store == nil {
		return
	}
	h.profiles = store
}

// SetGroupConfigStore 注入 QQ 群级配置存储。
func (h *QQBotHandler) SetGroupConfigStore(store QQBotGroupConfigStore) {
	if store == nil {
		return
	}
	h.groupConfigs = store
}

// SetSQLiteStore 注入 SQLite，用于插件状态持久化和操作日志。
func (h *QQBotHandler) SetSQLiteStore(store *storage.SQLiteStore) {
	h.sqlite = store
	h.logs = store
}

// Register 注册 QQ 机器人配置、状态和插件接口路由。
func (h *QQBotHandler) Register(router gin.IRouter) {
	router.GET("/api/qqbot/config", h.getConfig)
	router.POST("/api/qqbot/config", h.saveConfig)
	router.POST("/api/qqbot/config/activate", h.activateProfile)
	router.POST("/api/qqbot/config/clone", h.cloneProfile)
	router.POST("/api/qqbot/config/delete", h.deleteProfile)
	router.GET("/api/qqbot/features", h.featuresStatus)
	router.GET("/api/qqbot/status", h.status)
	router.POST("/api/qqbot/start", h.start)
	router.POST("/api/qqbot/stop", h.stop)
	if h.features.GroupTest {
		router.GET("/api/qqbot/group-test", h.getGroupTest)
		router.GET("/api/qqbot/group-test/files", h.listGroupTestFiles)
		router.POST("/api/qqbot/group-test", h.sendGroupTest)
		router.POST("/api/qqbot/group-test/recall", h.recallGroupTestMessage)
		router.POST("/api/qqbot/group-test/file", h.parseGroupTestFile)
		router.POST("/api/qqbot/group-test/napcat-qrcode", h.shareNapCatQRCode)
		router.POST("/api/qqbot/group-test/upload-file", h.uploadGroupTestFile)
		router.POST("/api/qqbot/group-test/onebot", h.callGroupTestOneBot)
	}
	router.GET("/api/qqbot/plugins", h.listPlugins)
	router.POST("/api/qqbot/plugins/:id/install", h.installPlugin)
	router.POST("/api/qqbot/plugins/:id/uninstall", h.uninstallPlugin)
	router.POST("/api/qqbot/plugins/:id/enabled", h.setPluginEnabled)
	router.POST("/api/qqbot/group-admin/challenge", h.startGroupAdminChallenge)
	router.POST("/api/qqbot/group-admin/verify", h.verifyGroupAdminChallenge)
	router.GET("/api/qqbot/group-admin/config", h.getGroupAdminConfig)
	router.POST("/api/qqbot/group-admin/config", h.saveGroupAdminConfig)
}

// shareNapCatQRCode exposes only NapCat's current login QR through an expiring loopback URL.
func (h *QQBotHandler) shareNapCatQRCode(c *gin.Context) {
	if h.localMedia == nil {
		h.writeError(c, http.StatusServiceUnavailable, "qqbot.group_test.napcat_qrcode", fmt.Errorf("local media store is unavailable"), "napcat-qrcode", nil)
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		h.writeError(c, http.StatusInternalServerError, "qqbot.group_test.napcat_qrcode", err, "napcat-qrcode", nil)
		return
	}
	path := filepath.Join(home, "Library", "Containers", "com.tencent.qq", "Data", "Library", "Application Support", "QQ", "NapCat", "cache", "qrcode.png")
	sharedURL, ok := h.localMedia.Share(path, 2*time.Minute)
	if !ok {
		h.writeError(c, http.StatusNotFound, "qqbot.group_test.napcat_qrcode", fmt.Errorf("NapCat login QR code is unavailable"), "napcat-qrcode", nil)
		return
	}
	c.JSON(http.StatusOK, gin.H{"url": sharedURL, "expires_in_seconds": 120})
}

// listGroupTestFiles returns OneBot's current root file list for a test group.
func (h *QQBotHandler) listGroupTestFiles(c *gin.Context) {
	groupID := strings.TrimSpace(c.Query("group_id"))
	parsedGroupID, err := strconv.ParseInt(groupID, 10, 64)
	if err != nil {
		h.writeError(c, http.StatusBadRequest, "qqbot.group_test.files", fmt.Errorf("valid group_id is required"), groupID, nil)
		return
	}
	result, err := h.runtime.CallOneBotAPI(c.Request.Context(), "get_group_root_files", map[string]any{"group_id": parsedGroupID})
	if err != nil {
		h.writeError(c, http.StatusBadRequest, "qqbot.group_test.files", err, groupID, map[string]any{"group_id": groupID})
		return
	}
	c.JSON(http.StatusOK, gin.H{"group_id": groupID, "result": result})
}

// uploadGroupTestFile uploads a local fixture through OneBot in test environments.
func (h *QQBotHandler) uploadGroupTestFile(c *gin.Context) {
	var payload groupTestUploadFilePayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		h.writeError(c, http.StatusBadRequest, "qqbot.group_test.upload_file", err, "", nil)
		return
	}
	groupID := strings.TrimSpace(payload.GroupID)
	file := strings.TrimSpace(payload.File)
	name := strings.TrimSpace(payload.Name)
	parsedGroupID, err := strconv.ParseInt(groupID, 10, 64)
	if err != nil || file == "" || name == "" {
		h.writeError(c, http.StatusBadRequest, "qqbot.group_test.upload_file", fmt.Errorf("valid group_id, file and name are required"), groupID, nil)
		return
	}
	uploadSource := file
	if h.localMedia != nil {
		if sharedURL, ok := h.localMedia.Share(file, 10*time.Minute); ok {
			uploadSource = sharedURL
		}
	}
	result, err := h.runtime.CallOneBotAPI(c.Request.Context(), "upload_group_file", map[string]any{
		"group_id": parsedGroupID,
		"file":     uploadSource,
		"name":     name,
	})
	if err != nil {
		h.writeError(c, http.StatusBadRequest, "qqbot.group_test.upload_file", err, groupID, map[string]any{"group_id": groupID, "name": name})
		return
	}
	recordRequestOperation(c, h.logs, "qqbot.group_test.upload_file", "QQ群测试文件已上传", groupID, map[string]any{"group_id": groupID, "name": name})
	c.JSON(http.StatusOK, gin.H{"group_id": groupID, "name": name, "result": result})
}

var groupTestOneBotReadActions = map[string]struct{}{
	"get_version_info":      {},
	"get_group_list":        {},
	"get_group_member_info": {},
	"get_group_member_list": {},
	"get_group_msg_history": {},
	"get_forward_msg":       {},
	"get_group_file_url":    {},
	"get_file":              {},
	"get_image":             {},
	"get_msg":               {},
}

// callGroupTestOneBot exposes a fixed read-only OneBot allowlist for local diagnostics.
func (h *QQBotHandler) callGroupTestOneBot(c *gin.Context) {
	var payload groupTestOneBotPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		h.writeError(c, http.StatusBadRequest, "qqbot.group_test.onebot", err, "", nil)
		return
	}
	action := strings.TrimSpace(payload.Action)
	if _, ok := groupTestOneBotReadActions[action]; !ok {
		h.writeError(c, http.StatusBadRequest, "qqbot.group_test.onebot", fmt.Errorf("OneBot action %q is not allowed", action), action, nil)
		return
	}
	if payload.Params == nil {
		payload.Params = map[string]any{}
	}
	result, err := h.runtime.CallOneBotAPI(c.Request.Context(), action, payload.Params)
	if err != nil {
		h.writeError(c, http.StatusBadRequest, "qqbot.group_test.onebot", err, action, nil)
		return
	}
	c.JSON(http.StatusOK, gin.H{"action": action, "result": result})
}

// getConfig 处理 QQ 机器人配置读取请求。
func (h *QQBotHandler) getConfig(c *gin.Context) {
	c.JSON(http.StatusOK, qqbot.PayloadFromProfileSet(h.profiles.Profiles()))
}

// featuresStatus 返回当前 WebUI 暴露的 QQ 机器人测试能力。
func (h *QQBotHandler) featuresStatus(c *gin.Context) {
	c.JSON(http.StatusOK, h.features)
}

// saveConfig 保存当前机器人配置或新增机器人配置档。
func (h *QQBotHandler) saveConfig(c *gin.Context) {
	var payload qqbot.ConfigPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		h.writeError(c, http.StatusBadRequest, "qqbot.config.save", err, "", nil)
		return
	}

	set := h.profiles.Profiles()
	existing := existingQQBotProfileConfig(set, payload)
	cfg := qqbot.ConfigFromPayload(payload, existing)
	if err := validateTokenLength("onebot_access_token", payload.OneBotAccessToken); err != nil {
		h.writeError(c, http.StatusBadRequest, "qqbot.config.save", err, qqbotLogTarget(cfg), botLogMetadata(cfg))
		return
	}
	if err := validateTokenLength("nonebot_bridge_token", payload.NoneBotBridgeToken); err != nil {
		h.writeError(c, http.StatusBadRequest, "qqbot.config.save", err, qqbotLogTarget(cfg), botLogMetadata(cfg))
		return
	}
	if err := cfg.Validate(); err != nil {
		h.writeError(c, http.StatusBadRequest, "qqbot.config.save", err, qqbotLogTarget(cfg), botLogMetadata(cfg))
		return
	}

	next := upsertQQBotProfileSet(set, payload, cfg)
	current, ok := next.Current()
	if !ok {
		h.writeError(c, http.StatusBadRequest, "qqbot.config.save", fmt.Errorf("qqbot profile set is empty"), "", nil)
		return
	}
	// 当前激活机器人配置发生变化时，运行时要同步切换并按需重启连接。
	if err := h.runtime.UpdateConfig(h.ctx, current, h.newChannel(current)); err != nil && !errors.Is(err, qqbot.ErrBotDisabled) {
		h.writeError(c, http.StatusBadRequest, "qqbot.config.save", err, qqbotLogTarget(current), botLogMetadata(current))
		return
	}
	h.profiles.SaveProfiles(next)
	recordRequestOperation(c, h.logs, "qqbot.config.save", "QQ 机器人配置已保存", current.ID, botLogMetadata(current))
	c.JSON(http.StatusOK, qqbot.PayloadFromProfileSet(next))
}

// activateProfile 切换当前激活的 QQ 机器人配置档。
func (h *QQBotHandler) activateProfile(c *gin.Context) {
	var payload qqbot.ConfigPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		h.writeError(c, http.StatusBadRequest, "qqbot.profile.activate", err, "", nil)
		return
	}
	targetID := strings.TrimSpace(payload.ID)
	if targetID == "" {
		h.writeError(c, http.StatusBadRequest, "qqbot.profile.activate", fmt.Errorf("profile id is required"), "", nil)
		return
	}
	next := h.profiles.Profiles().WithActive(targetID)
	current, ok := next.Current()
	if !ok || current.ID != targetID {
		h.writeError(c, http.StatusNotFound, "qqbot.profile.activate", fmt.Errorf("profile %q not found", targetID), targetID, nil)
		return
	}
	if err := h.runtime.UpdateConfig(h.ctx, current, h.newChannel(current)); err != nil && !errors.Is(err, qqbot.ErrBotDisabled) {
		h.writeError(c, http.StatusBadRequest, "qqbot.profile.activate", err, qqbotLogTarget(current), botLogMetadata(current))
		return
	}
	h.profiles.SaveProfiles(next)
	recordRequestOperation(c, h.logs, "qqbot.profile.activate", "QQ 机器人配置已切换", targetID, botLogMetadata(current))
	c.JSON(http.StatusOK, qqbot.PayloadFromProfileSet(next))
}

// cloneProfile 复制指定 QQ 机器人配置档。
func (h *QQBotHandler) cloneProfile(c *gin.Context) {
	var payload qqbot.ConfigPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		h.writeError(c, http.StatusBadRequest, "qqbot.profile.clone", err, "", nil)
		return
	}
	sourceID := strings.TrimSpace(payload.ID)
	set := h.profiles.Profiles()
	if sourceID == "" {
		sourceID = set.ActiveID
	}
	for _, profile := range set.Profiles {
		if profile.ID != sourceID {
			continue
		}
		cloned := profile
		cloned.ID = ""
		cloned.Name = profile.Name + " 副本"
		next := upsertQQBotProfileSet(set, qqbot.ConfigPayload{Name: cloned.Name}, cloned)
		current, ok := next.Current()
		if !ok {
			h.writeError(c, http.StatusBadRequest, "qqbot.profile.clone", fmt.Errorf("qqbot profile set is empty"), "", nil)
			return
		}
		if err := h.runtime.UpdateConfig(h.ctx, current, h.newChannel(current)); err != nil && !errors.Is(err, qqbot.ErrBotDisabled) {
			h.writeError(c, http.StatusBadRequest, "qqbot.profile.clone", err, qqbotLogTarget(current), botLogMetadata(current))
			return
		}
		h.profiles.SaveProfiles(next)
		recordRequestOperation(c, h.logs, "qqbot.profile.clone", "QQ 机器人配置已复制", sourceID, botLogMetadata(profile))
		c.JSON(http.StatusOK, qqbot.PayloadFromProfileSet(next))
		return
	}
	h.writeError(c, http.StatusNotFound, "qqbot.profile.clone", fmt.Errorf("profile %q not found", sourceID), sourceID, nil)
}

// deleteProfile 删除指定 QQ 机器人配置档。
func (h *QQBotHandler) deleteProfile(c *gin.Context) {
	var payload qqbot.ConfigPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		h.writeError(c, http.StatusBadRequest, "qqbot.profile.delete", err, "", nil)
		return
	}
	targetID := strings.TrimSpace(payload.ID)
	if targetID == "" {
		h.writeError(c, http.StatusBadRequest, "qqbot.profile.delete", fmt.Errorf("profile id is required"), "", nil)
		return
	}
	set := h.profiles.Profiles()
	if len(set.Profiles) <= 1 {
		h.writeError(c, http.StatusBadRequest, "qqbot.profile.delete", fmt.Errorf("at least one qqbot profile must remain"), targetID, nil)
		return
	}
	next := set.Delete(targetID)
	if len(next.Profiles) == len(set.Profiles) {
		h.writeError(c, http.StatusNotFound, "qqbot.profile.delete", fmt.Errorf("profile %q not found", targetID), targetID, nil)
		return
	}
	current, ok := next.Current()
	if !ok {
		h.writeError(c, http.StatusBadRequest, "qqbot.profile.delete", fmt.Errorf("qqbot profile set is empty"), "", nil)
		return
	}
	if err := h.runtime.UpdateConfig(h.ctx, current, h.newChannel(current)); err != nil && !errors.Is(err, qqbot.ErrBotDisabled) {
		h.writeError(c, http.StatusBadRequest, "qqbot.profile.delete", err, qqbotLogTarget(current), botLogMetadata(current))
		return
	}
	h.profiles.SaveProfiles(next)
	recordRequestOperation(c, h.logs, "qqbot.profile.delete", "QQ 机器人配置已删除", targetID, map[string]any{"profile_id": targetID})
	c.JSON(http.StatusOK, qqbot.PayloadFromProfileSet(next))
}

// validateTokenLength 校验用户显式填写的 token 长度。
func validateTokenLength(field string, value string) error {
	// 空 token 表示不鉴权或沿用旧值；只有用户显式填写时才检查强度。
	if value == "" {
		return nil
	}
	if utf8.RuneCountInString(value) < minQQBotTokenChars {
		return fmt.Errorf("%s must be at least %d characters", field, minQQBotTokenChars)
	}
	return nil
}

// status 返回 QQ 机器人运行状态快照。
func (h *QQBotHandler) status(c *gin.Context) {
	c.JSON(http.StatusOK, h.runtime.Status())
}

// start 处理启动 QQ 机器人的请求。
func (h *QQBotHandler) start(c *gin.Context) {
	if err := h.runtime.Start(h.ctx); err != nil {
		h.writeError(c, http.StatusBadRequest, "qqbot.start", err, qqbotLogTarget(h.runtime.Config()), botLogMetadata(h.runtime.Config()))
		return
	}
	recordRequestOperation(c, h.logs, "qqbot.start", "QQ 机器人已启动", h.runtime.Config().ID, botLogMetadata(h.runtime.Config()))
	c.JSON(http.StatusOK, h.runtime.Status())
}

// stop 处理停止 QQ 机器人的请求。
func (h *QQBotHandler) stop(c *gin.Context) {
	if err := h.runtime.Stop(); err != nil {
		h.writeError(c, http.StatusBadRequest, "qqbot.stop", err, qqbotLogTarget(h.runtime.Config()), botLogMetadata(h.runtime.Config()))
		return
	}
	recordRequestOperation(c, h.logs, "qqbot.stop", "QQ 机器人已停止", h.runtime.Config().ID, botLogMetadata(h.runtime.Config()))
	c.JSON(http.StatusOK, h.runtime.Status())
}

// getGroupTest 返回指定群最近收发事件，辅助真实 QQ 群联调。
func (h *QQBotHandler) getGroupTest(c *gin.Context) {
	groupID := strings.TrimSpace(c.Query("group_id"))
	if groupID == "" {
		h.writeError(c, http.StatusBadRequest, "qqbot.group_test.status", fmt.Errorf("group_id is required"), "", nil)
		return
	}
	status := h.runtime.Status()
	c.JSON(http.StatusOK, groupTestResponse{
		GroupID:      groupID,
		Channel:      status.Channel,
		RecentEvents: groupEvents(status.RecentEvents, groupID),
		Status:       status,
	})
}

// sendGroupTest 通过当前 OneBot 连接向 QQ 群发送测试消息，并返回近期收到的同群事件。
func (h *QQBotHandler) sendGroupTest(c *gin.Context) {
	var payload groupTestPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		h.writeError(c, http.StatusBadRequest, "qqbot.group_test.send", err, "", nil)
		return
	}
	groupID := strings.TrimSpace(payload.GroupID)
	message := strings.TrimSpace(payload.Message)
	if groupID == "" {
		h.writeError(c, http.StatusBadRequest, "qqbot.group_test.send", fmt.Errorf("group_id is required"), "", nil)
		return
	}
	if message == "" {
		h.writeError(c, http.StatusBadRequest, "qqbot.group_test.send", fmt.Errorf("message is required"), groupID, map[string]any{"group_id": groupID})
		return
	}
	sendResult, err := h.runtime.SendGroupMessage(c.Request.Context(), groupID, message)
	if err != nil {
		h.writeError(c, http.StatusBadRequest, "qqbot.group_test.send", err, groupID, map[string]any{"group_id": groupID})
		return
	}
	messageID := oneBotMessageID(sendResult)
	status := h.runtime.Status()
	recordRequestOperation(c, h.logs, "qqbot.group_test.send", "QQ群测试消息已发送", groupID, map[string]any{
		"group_id":   groupID,
		"message_id": messageID,
	})
	c.JSON(http.StatusOK, groupTestResponse{
		GroupID:      groupID,
		Message:      message,
		MessageID:    messageID,
		Sent:         true,
		SendResult:   sendResult,
		Channel:      status.Channel,
		RecentEvents: groupEvents(status.RecentEvents, groupID),
		Status:       status,
	})
}

// recallGroupTestMessage uses OneBot delete_msg in explicitly enabled test environments.
func (h *QQBotHandler) recallGroupTestMessage(c *gin.Context) {
	var payload groupTestRecallPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		h.writeError(c, http.StatusBadRequest, "qqbot.group_test.recall", err, "", nil)
		return
	}
	messageID := strings.TrimSpace(payload.MessageID)
	if messageID == "" {
		h.writeError(c, http.StatusBadRequest, "qqbot.group_test.recall", fmt.Errorf("message_id is required"), "", nil)
		return
	}
	result, err := h.runtime.CallOneBotAPI(c.Request.Context(), "delete_msg", map[string]any{
		"message_id": oneBotIDParam(messageID),
	})
	if err != nil {
		h.writeError(c, http.StatusBadRequest, "qqbot.group_test.recall", err, messageID, map[string]any{"message_id": messageID})
		return
	}
	recordRequestOperation(c, h.logs, "qqbot.group_test.recall", "QQ群测试消息已撤回", messageID, map[string]any{"message_id": messageID})
	c.JSON(http.StatusOK, groupTestRecallResponse{MessageID: messageID, Recalled: true, Result: result})
}

// parseGroupTestFile runs the production file parser against a OneBot file or local diagnostic path.
func (h *QQBotHandler) parseGroupTestFile(c *gin.Context) {
	var payload groupTestFilePayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		h.writeError(c, http.StatusBadRequest, "qqbot.group_test.file", err, "", nil)
		return
	}
	groupID := strings.TrimSpace(payload.GroupID)
	fileID := strings.TrimSpace(payload.FileID)
	name := strings.TrimSpace(payload.Name)
	localPath := strings.TrimSpace(payload.LocalPath)
	if name == "" || (localPath == "" && (groupID == "" || fileID == "")) {
		h.writeError(c, http.StatusBadRequest, "qqbot.group_test.file", fmt.Errorf("name and either local_path or group_id plus file_id are required"), groupID, nil)
		return
	}
	if localPath != "" && !filepath.IsAbs(localPath) {
		h.writeError(c, http.StatusBadRequest, "qqbot.group_test.file", fmt.Errorf("local_path must be absolute"), name, nil)
		return
	}
	if groupID != "" {
		if _, err := strconv.ParseInt(groupID, 10, 64); err != nil {
			h.writeError(c, http.StatusBadRequest, "qqbot.group_test.file", fmt.Errorf("invalid group_id %q", groupID), groupID, nil)
			return
		}
	}
	if localPath == "" && groupID == "" {
		h.writeError(c, http.StatusBadRequest, "qqbot.group_test.file", fmt.Errorf("invalid group_id %q", groupID), groupID, nil)
		return
	}
	segmentData := map[string]string{
		"name":    name,
		"file_id": fileID,
		"busid":   strings.TrimSpace(payload.BusID),
	}
	if localPath != "" {
		segmentData["path"] = localPath
	}
	logTarget := fileID
	if logTarget == "" {
		logTarget = name
	}
	plugin := qqbot.NewFileParserPlugin(nil)
	resp, err := plugin.Handle(c.Request.Context(), qqbot.PluginRequest{
		Channel: runtimeAPICallChannel{runtime: h.runtime},
		Event: qqbot.MessageEvent{
			Kind:    qqbot.EventKindGroup,
			GroupID: groupID,
			Segments: []qqbot.MessageSegment{{
				Type: "file",
				Data: segmentData,
			}},
		},
		Text: "QQ群文件解析测试",
	})
	if err != nil {
		h.writeError(c, http.StatusBadRequest, "qqbot.group_test.file", err, logTarget, map[string]any{"group_id": groupID, "file_id": fileID})
		return
	}
	if resp == nil {
		h.writeError(c, http.StatusBadRequest, "qqbot.group_test.file", fmt.Errorf("file parser returned no result"), logTarget, map[string]any{"group_id": groupID, "file_id": fileID})
		return
	}
	contextText := strings.TrimSpace(resp.Context)
	if contextText == "" && len(resp.Tasks) > 0 {
		results := make([]string, 0, len(resp.Tasks))
		for _, task := range resp.Tasks {
			result, taskErr := h.runtime.RunPluginTask(c.Request.Context(), task)
			if taskErr != nil {
				h.writeError(c, http.StatusBadRequest, "qqbot.group_test.file", taskErr, logTarget, map[string]any{"group_id": groupID, "file_id": fileID})
				return
			}
			if text := strings.TrimSpace(result.Reply); text != "" {
				results = append(results, text)
			}
		}
		contextText = strings.Join(results, "\n\n")
	}
	if contextText == "" {
		h.writeError(c, http.StatusBadRequest, "qqbot.group_test.file", fmt.Errorf("file parser returned no result"), logTarget, map[string]any{"group_id": groupID, "file_id": fileID})
		return
	}
	recordRequestOperation(c, h.logs, "qqbot.group_test.file", "QQ群文件解析测试完成", logTarget, map[string]any{"group_id": groupID, "file_id": fileID, "name": name})
	c.JSON(http.StatusOK, groupTestFileResponse{GroupID: groupID, FileID: fileID, Name: name, Context: contextText})
}

// listPlugins 返回机器人插件列表。
func (h *QQBotHandler) listPlugins(c *gin.Context) {
	c.JSON(http.StatusOK, h.runtime.Plugins().List())
}

// installPlugin 处理插件安装请求。
func (h *QQBotHandler) installPlugin(c *gin.Context) {
	state, err := h.runtime.Plugins().Install(c.Param("id"))
	if err != nil {
		h.writePluginError(c, "qqbot.plugin.install", err, c.Param("id"))
		return
	}
	h.persistState()
	recordRequestOperation(c, h.logs, "qqbot.plugin.install", "机器人插件已安装", state.Manifest.ID, pluginLogMetadata(state))
	c.JSON(http.StatusOK, state)
}

// uninstallPlugin 处理插件卸载请求。
func (h *QQBotHandler) uninstallPlugin(c *gin.Context) {
	state, err := h.runtime.Plugins().Uninstall(c.Param("id"))
	if err != nil {
		h.writePluginError(c, "qqbot.plugin.uninstall", err, c.Param("id"))
		return
	}
	h.persistState()
	recordRequestOperation(c, h.logs, "qqbot.plugin.uninstall", "机器人插件已卸载", state.Manifest.ID, pluginLogMetadata(state))
	c.JSON(http.StatusOK, state)
}

// setPluginEnabled 处理插件启用状态变更请求。
func (h *QQBotHandler) setPluginEnabled(c *gin.Context) {
	var payload pluginEnabledPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		h.writeError(c, http.StatusBadRequest, "qqbot.plugin.enabled", err, c.Param("id"), map[string]any{"plugin_id": c.Param("id")})
		return
	}
	state, err := h.runtime.Plugins().SetEnabled(c.Param("id"), payload.Enabled)
	if err != nil {
		h.writePluginError(c, "qqbot.plugin.enabled", err, c.Param("id"))
		return
	}
	h.persistState()
	recordRequestOperation(c, h.logs, "qqbot.plugin.enabled", "机器人插件开关已更新", state.Manifest.ID, pluginLogMetadata(state))
	c.JSON(http.StatusOK, state)
}

// writePluginError 按插件错误类型返回合适的 HTTP 状态码。
func (h *QQBotHandler) writePluginError(c *gin.Context, action string, err error, target string) {
	if errors.Is(err, qqbot.ErrPluginNotFound) {
		h.writeError(c, http.StatusNotFound, action, err, target, map[string]any{"plugin_id": target})
		return
	}
	h.writeError(c, http.StatusBadRequest, action, err, target, map[string]any{"plugin_id": target})
}

// writeError 写出统一 JSON 错误响应。
func (h *QQBotHandler) writeError(c *gin.Context, status int, action string, err error, target string, metadata map[string]any) {
	logAndWriteError(c, h.logs, status, action, err, target, metadata)
}

// persistState 将插件状态写入 SQLite。
func (h *QQBotHandler) persistState() {
	if h.sqlite == nil {
		return
	}
	// 插件开关/安装状态不在 runtime.Config 里，因此单独持久化。
	if err := h.sqlite.SavePluginStates(h.ctx, h.runtime.Plugins().Snapshot()); err != nil {
		recordError(h.ctx, h.logs, "qqbot.persist", err, "plugin_states", nil)
	}
}

// botLogMetadata 构造 QQ 机器人操作日志的附加信息。
func botLogMetadata(cfg qqbot.BotConfig) map[string]any {
	return map[string]any{
		"profile_id":              cfg.ID,
		"profile_name":            cfg.Name,
		"platform":                cfg.Platform,
		"enabled":                 cfg.Enabled,
		"onebot_reverse_ws":       cfg.OneBotReverseWSEndpoint,
		"nonebot_bridge_enabled":  cfg.NoneBotBridgeEnabled,
		"nonebot_bridge_endpoint": cfg.NoneBotBridgeEndpoint,
		"bot_qq":                  cfg.BotQQ,
		"owner_id":                cfg.OwnerID,
	}
}

// pluginLogMetadata 构造插件操作日志的附加信息。
func pluginLogMetadata(state qqbot.PluginState) map[string]any {
	return map[string]any{
		"plugin_id": state.Manifest.ID,
		"name":      state.Manifest.Name,
		"installed": state.Installed,
		"enabled":   state.Enabled,
		"official":  state.Manifest.Official,
	}
}

// groupEvents 从运行时最近事件里筛出指定群的收发记录。
func groupEvents(events []qqbot.EventRecord, groupID string) []qqbot.EventRecord {
	groupID = strings.TrimSpace(groupID)
	out := make([]qqbot.EventRecord, 0, len(events))
	for _, event := range events {
		if event.GroupID == groupID {
			out = append(out, event)
		}
	}
	return out
}

func oneBotMessageID(data map[string]any) string {
	if len(data) == 0 {
		return ""
	}
	value, ok := data["message_id"]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case int:
		return strconv.Itoa(typed)
	case int32:
		return strconv.FormatInt(int64(typed), 10)
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		return strconv.FormatInt(int64(typed), 10)
	case json.Number:
		return typed.String()
	default:
		return fmt.Sprint(typed)
	}
}

func oneBotIDParam(value string) any {
	value = strings.TrimSpace(value)
	if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
		return parsed
	}
	return value
}

type runtimeAPICallChannel struct {
	runtime QQBotRuntime
}

func (c runtimeAPICallChannel) Connect(context.Context, qqbot.EventHandler) error { return nil }
func (c runtimeAPICallChannel) Send(context.Context, qqbot.OutgoingMessage) error { return nil }
func (c runtimeAPICallChannel) CallAPI(ctx context.Context, action string, params map[string]any) (map[string]any, error) {
	return c.runtime.CallOneBotAPI(ctx, action, params)
}
func (c runtimeAPICallChannel) Status() qqbot.ChannelStatus { return c.runtime.Status().Channel }
func (c runtimeAPICallChannel) Close() error                { return nil }

// existingQQBotProfileConfig 根据 payload 推断“编辑的是哪个机器人配置档”。
func existingQQBotProfileConfig(set qqbot.ProfileSet, payload qqbot.ConfigPayload) qqbot.BotConfig {
	targetID := strings.TrimSpace(payload.ID)
	if targetID == "" {
		targetID = strings.TrimSpace(payload.ActiveProfileID)
	}
	if targetID == "" {
		targetID = strings.TrimSpace(set.ActiveID)
	}
	for _, profile := range set.WithDefaults().Profiles {
		if profile.ID == targetID {
			return profile.WithDefaults()
		}
	}
	if current, ok := set.Current(); ok {
		return current.WithDefaults()
	}
	return qqbot.DefaultBotConfig()
}

// upsertQQBotProfileSet 把当前表单保存为配置档，并让它成为新的激活机器人。
func upsertQQBotProfileSet(set qqbot.ProfileSet, payload qqbot.ConfigPayload, cfg qqbot.BotConfig) qqbot.ProfileSet {
	set = set.WithDefaults()
	targetID := strings.TrimSpace(payload.ID)
	if targetID == "" {
		targetID = strings.TrimSpace(cfg.ID)
	}
	cfg = cfg.WithDefaults()
	if targetID == "" {
		targetID = qqbot.NewProfileSet(cfg).ActiveID
	}
	cfg.ID = targetID
	for i := range set.Profiles {
		if set.Profiles[i].ID != targetID {
			continue
		}
		set.Profiles[i] = cfg
		set.ActiveID = targetID
		return set.WithDefaults()
	}
	set.Profiles = append(set.Profiles, cfg)
	set.ActiveID = targetID
	return set.WithDefaults()
}

// qqbotLogTarget 选择更适合日志索引的机器人配置目标。
func qqbotLogTarget(cfg qqbot.BotConfig) string {
	for _, value := range []string{cfg.ID, cfg.BotQQ, cfg.OneBotReverseWSEndpoint, cfg.Name} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
