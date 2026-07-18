package webui

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"diana-qq-bot/model/llm"

	"github.com/gin-gonic/gin"
)

type LLMConfigHandler struct {
	store      LLMProfileStore
	newClient  LLMClientFactory
	listModels LLMModelListFactory
	logs       AppLogWriter
}

type LLMClientFactory func(llm.ProviderConfig) (llm.LLMClient, error)
type LLMModelListFactory func(context.Context, llm.ProviderConfig) ([]llm.ModelInfo, error)

type llmConfigPayload struct {
	ID                  string             `json:"id,omitempty"`
	Name                string             `json:"name,omitempty"`
	Group               string             `json:"group,omitempty"`
	Description         string             `json:"description,omitempty"`
	UpdatedAt           string             `json:"updated_at,omitempty"`
	ActiveProfileID     string             `json:"active_profile_id,omitempty"`
	Profiles            []llmConfigPayload `json:"profiles,omitempty"`
	Provider            llm.Provider       `json:"provider"`
	APIKey              string             `json:"api_key,omitempty"`
	APIKeyConfigured    bool               `json:"api_key_configured,omitempty"`
	BaseURL             string             `json:"base_url,omitempty"`
	APIFormat           llm.APIFormat      `json:"api_format,omitempty"`
	Model               string             `json:"model"`
	ImageModel          string             `json:"image_model,omitempty"`
	ImageBaseURL        string             `json:"image_base_url,omitempty"`
	ImageOrigin         string             `json:"image_origin,omitempty"`
	ImageTimeoutMS      int64              `json:"image_timeout_ms,omitempty"`
	UserAgent           string             `json:"user_agent,omitempty"`
	Headers             map[string]string  `json:"headers,omitempty"`
	Temperature         *float64           `json:"temperature,omitempty"`
	ReasoningEffort     string             `json:"reasoning_effort,omitempty"`
	ContextWindowTokens int64              `json:"context_window_tokens,omitempty"`
	MaxContextTokens    int64              `json:"max_context_tokens,omitempty"`
	MaxOutputTokens     int64              `json:"max_output_tokens,omitempty"`
	TimeoutMS           int64              `json:"timeout_ms,omitempty"`
}

type llmTestPayload struct {
	Message string `json:"message"`
}

type llmModelsPayload struct {
	Models []llm.ModelInfo `json:"models"`
}

const minLLMAPIKeyChars = 8

// NewLLMConfigHandler 创建 LLMConfigHandler 实例。
func NewLLMConfigHandler(store LLMProfileStore) *LLMConfigHandler {
	return NewLLMConfigHandlerWithFactory(store, func(cfg llm.ProviderConfig) (llm.LLMClient, error) {
		return llm.NewClient(cfg)
	})
}

// NewLLMConfigHandlerWithFactory 创建 LLMConfigHandler 实例。
func NewLLMConfigHandlerWithFactory(store LLMProfileStore, factory LLMClientFactory) *LLMConfigHandler {
	return &LLMConfigHandler{
		store:     store,
		newClient: factory,
		listModels: func(ctx context.Context, cfg llm.ProviderConfig) ([]llm.ModelInfo, error) {
			return llm.ListModels(ctx, cfg)
		},
	}
}

// SetModelListFactory 注入模型列表读取实现。
func (h *LLMConfigHandler) SetModelListFactory(factory LLMModelListFactory) {
	h.listModels = factory
}

// SetLogStore 注入 LLM 配置接口的日志写入器。
func (h *LLMConfigHandler) SetLogStore(store AppLogWriter) {
	h.logs = store
}

// Register 注册 LLM 配置、配置集、模型列表和测试接口。
func (h *LLMConfigHandler) Register(router gin.IRouter) {
	router.GET("/api/llm/config", h.getConfig)
	router.GET("/api/llm/config/export", h.exportConfig)
	router.POST("/api/llm/config", h.saveConfig)
	router.POST("/api/llm/config/activate", h.activateProfile)
	router.POST("/api/llm/config/clone", h.cloneProfile)
	router.POST("/api/llm/config/delete", h.deleteProfile)
	router.POST("/api/llm/config/import", h.importProfiles)
	router.GET("/api/llm/models", h.models)
	router.POST("/api/llm/models", h.models)
	router.POST("/api/llm/test", h.test)
}

// getConfig 处理 LLM 配置读取请求。
func (h *LLMConfigHandler) getConfig(c *gin.Context) {
	// 默认响应不带 API Key；本地配置页需要编辑时显式带 include_secrets=true。
	if queryBool(c.Query("include_secrets")) {
		c.JSON(200, payloadFromProfileSetWithSecrets(h.store.Profiles()))
		return
	}
	c.JSON(200, payloadFromProfileSet(h.store.Profiles()))
}

// exportConfig 导出包含密钥的 LLM 配置集。
func (h *LLMConfigHandler) exportConfig(c *gin.Context) {
	c.JSON(200, payloadFromProfileSetWithSecrets(h.store.Profiles()))
}

// saveConfig 保存当前 LLM 配置或新增配置档。
func (h *LLMConfigHandler) saveConfig(c *gin.Context) {
	var payload llmConfigPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		h.writeError(c, 400, "llm.config.save", err, "", nil)
		return
	}

	set := h.store.Profiles()
	cfg := configFromPayload(payload)
	existing := existingProfileConfig(set, payload)
	// 前端留空 API Key 表示沿用已保存密钥，不表示把密钥清空。
	if cfg.APIKey == "" && existing.Provider == cfg.Provider {
		cfg.APIKey = existing.APIKey
	}
	if strings.TrimSpace(payload.APIKey) != "" && utf8.RuneCountInString(cfg.APIKey) < minLLMAPIKeyChars {
		h.writeError(c, 400, "llm.config.save", fmt.Errorf("api_key must be at least %d characters", minLLMAPIKeyChars), llmLogTarget(payload), llmLogMetadata(cfg, payload.ID))
		return
	}
	if err := cfg.Validate(); err != nil {
		h.writeError(c, 400, "llm.config.save", err, llmLogTarget(payload), llmLogMetadata(cfg, payload.ID))
		return
	}

	next := upsertProfileSet(set, payload, cfg)
	h.store.SaveProfiles(next)
	recordRequestOperation(c, h.logs, "llm.config.save", "LLM 配置已保存", next.ActiveID, llmLogMetadata(cfg, next.ActiveID))
	c.JSON(200, payloadFromProfileSet(next))
}

// activateProfile 切换当前激活的 LLM 配置档。
func (h *LLMConfigHandler) activateProfile(c *gin.Context) {
	var payload llmConfigPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		h.writeError(c, 400, "llm.profile.activate", err, "", nil)
		return
	}
	targetID := strings.TrimSpace(payload.ID)
	if targetID == "" {
		h.writeError(c, 400, "llm.profile.activate", fmt.Errorf("profile id is required"), "", nil)
		return
	}
	set := h.store.Profiles().WithActive(targetID)
	current, ok := set.Current()
	if !ok || current.ID != targetID {
		h.writeError(c, 404, "llm.profile.activate", fmt.Errorf("profile %q not found", targetID), targetID, nil)
		return
	}
	h.store.SaveProfiles(set)
	recordRequestOperation(c, h.logs, "llm.profile.activate", "LLM 配置已切换", targetID, llmLogMetadata(current.Config, targetID))
	c.JSON(200, payloadFromProfileSet(set))
}

// deleteProfile 删除指定 LLM 配置档。
func (h *LLMConfigHandler) deleteProfile(c *gin.Context) {
	var payload llmConfigPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		h.writeError(c, 400, "llm.profile.delete", err, "", nil)
		return
	}
	targetID := strings.TrimSpace(payload.ID)
	if targetID == "" {
		h.writeError(c, 400, "llm.profile.delete", fmt.Errorf("profile id is required"), "", nil)
		return
	}
	set := h.store.Profiles()
	if len(set.Profiles) <= 1 {
		h.writeError(c, 400, "llm.profile.delete", fmt.Errorf("at least one llm profile must remain"), targetID, nil)
		return
	}
	next := set.Delete(targetID)
	if len(next.Profiles) == len(set.Profiles) {
		h.writeError(c, 404, "llm.profile.delete", fmt.Errorf("profile %q not found", targetID), targetID, nil)
		return
	}
	h.store.SaveProfiles(next)
	recordRequestOperation(c, h.logs, "llm.profile.delete", "LLM 配置已删除", targetID, map[string]any{"profile_id": targetID})
	c.JSON(200, payloadFromProfileSet(next))
}

// cloneProfile 复制指定 LLM 配置档。
func (h *LLMConfigHandler) cloneProfile(c *gin.Context) {
	var payload llmConfigPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		h.writeError(c, 400, "llm.profile.clone", err, "", nil)
		return
	}
	sourceID := strings.TrimSpace(payload.ID)
	if sourceID == "" {
		sourceID = h.store.Profiles().ActiveID
	}
	set := h.store.Profiles()
	for _, profile := range set.Profiles {
		if profile.ID != sourceID {
			continue
		}
		cloned := payloadFromConfig(profile.Config)
		cloned.Name = profile.Name + " 副本"
		cloned.Group = profile.Group
		cloned.Description = profile.Description
		next := upsertProfileSet(set, llmConfigPayload{Name: cloned.Name, Group: cloned.Group, Description: cloned.Description}, profile.Config)
		h.store.SaveProfiles(next)
		recordRequestOperation(c, h.logs, "llm.profile.clone", "LLM 配置已复制", sourceID, llmLogMetadata(profile.Config, sourceID))
		c.JSON(200, payloadFromProfileSet(next))
		return
	}
	h.writeError(c, 404, "llm.profile.clone", fmt.Errorf("profile %q not found", sourceID), sourceID, nil)
}

// importProfiles 导入一组 LLM 配置档。
func (h *LLMConfigHandler) importProfiles(c *gin.Context) {
	var payload struct {
		ActiveProfileID string             `json:"active_profile_id,omitempty"`
		Profiles        []llmConfigPayload `json:"profiles"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		h.writeError(c, 400, "llm.profile.import", err, "", nil)
		return
	}
	if len(payload.Profiles) == 0 {
		h.writeError(c, 400, "llm.profile.import", fmt.Errorf("profiles are required"), "", nil)
		return
	}
	next := llm.ProfileSet{Profiles: make([]llm.Profile, 0, len(payload.Profiles))}
	seenIDs := make(map[string]struct{}, len(payload.Profiles))
	for _, item := range payload.Profiles {
		// 导入文件必须自带密钥，避免导入后看似成功但实际无法调用模型。
		cfg := configFromPayload(item)
		if cfg.APIKey == "" {
			h.writeError(c, 400, "llm.profile.import", fmt.Errorf("profile %q missing api_key", firstNonEmpty(item.Name, item.ID)), firstNonEmpty(item.ID, item.Name), nil)
			return
		}
		if err := cfg.Validate(); err != nil {
			h.writeError(c, 400, "llm.profile.import", err, firstNonEmpty(item.ID, item.Name), llmLogMetadata(cfg, item.ID))
			return
		}
		id := firstNonEmpty(strings.TrimSpace(item.ID), llm.NewProfileSet(cfg).ActiveID)
		if _, ok := seenIDs[id]; ok {
			h.writeError(c, 400, "llm.profile.import", fmt.Errorf("duplicate profile id %q", id), id, nil)
			return
		}
		seenIDs[id] = struct{}{}
		updatedAt := time.Now()
		if item.UpdatedAt != "" {
			if parsed, err := time.Parse(time.RFC3339, item.UpdatedAt); err == nil {
				updatedAt = parsed
			}
		}
		next.Profiles = append(next.Profiles, llm.Profile{
			ID:          id,
			Name:        llm.NormalizeProfileName(item.Name),
			Group:       llm.NormalizeProfileGroup(item.Group),
			Description: strings.TrimSpace(item.Description),
			UpdatedAt:   updatedAt,
			Config:      cfg,
		})
	}
	next.ActiveID = firstNonEmpty(payload.ActiveProfileID, next.Profiles[0].ID)
	if current, ok := next.Current(); !ok || current.ID == "" {
		next.ActiveID = next.Profiles[0].ID
	}
	h.store.SaveProfiles(next)
	recordRequestOperation(c, h.logs, "llm.profile.import", "LLM 配置已导入", next.ActiveID, map[string]any{"profile_count": len(next.Profiles), "active_profile_id": next.ActiveID})
	c.JSON(200, payloadFromProfileSet(next))
}

// models 根据当前或草稿配置读取可用模型列表。
func (h *LLMConfigHandler) models(c *gin.Context) {
	cfg := h.store.Current()
	if c.Request.Method == http.MethodPost {
		// POST 用于前端在保存前拿“草稿配置”的模型列表，例如刚改了 Base URL 或 provider。
		var payload llmConfigPayload
		if err := c.ShouldBindJSON(&payload); err != nil {
			h.writeError(c, 400, "llm.models.list", err, "", nil)
			return
		}
		cfg = configFromPayload(payload)
		existing := existingProfileConfig(h.store.Profiles(), payload)
		if cfg.APIKey == "" && existing.Provider == cfg.Provider {
			cfg.APIKey = existing.APIKey
		}
	}

	models, err := h.listModels(c.Request.Context(), cfg)
	if err != nil {
		h.writeError(c, 502, "llm.models.list", err, cfg.Model, llmLogMetadata(cfg, ""))
		return
	}
	recordRequestOperation(c, h.logs, "llm.models.list", "LLM 模型列表已读取", cfg.Model, map[string]any{
		"provider": string(cfg.Provider),
		"model":    cfg.Model,
		"count":    len(models),
	})
	c.JSON(200, llmModelsPayload{Models: models})
}

// test 使用当前或草稿配置执行 LLM 连通测试。
func (h *LLMConfigHandler) test(c *gin.Context) {
	var payload struct {
		llmTestPayload
		llmConfigPayload
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		h.writeError(c, 400, "llm.test", err, "", nil)
		return
	}
	if payload.Message == "" {
		payload.Message = "ping"
	}

	cfg := h.store.Current()
	// 连通测试允许直接使用表单里的临时配置，成功与否不影响当前已保存配置。
	if payload.Provider != "" || payload.Model != "" || payload.BaseURL != "" || payload.APIFormat != "" || payload.APIKey != "" || payload.UserAgent != "" || payload.ImageModel != "" || payload.ImageBaseURL != "" || payload.ImageOrigin != "" || payload.ImageTimeoutMS != 0 || payload.ContextWindowTokens != 0 || payload.MaxContextTokens != 0 || payload.MaxOutputTokens != 0 || payload.TimeoutMS != 0 || payload.Temperature != nil || payload.ReasoningEffort != "" {
		cfg = configFromPayload(payload.llmConfigPayload)
		existing := existingProfileConfig(h.store.Profiles(), payload.llmConfigPayload)
		if cfg.APIKey == "" && existing.Provider == cfg.Provider {
			cfg.APIKey = existing.APIKey
		}
	}
	client, err := h.newClient(cfg)
	if err != nil {
		h.writeError(c, 400, "llm.test", err, cfg.Model, llmLogMetadata(cfg, ""))
		return
	}

	resp, err := client.Generate(c.Request.Context(), llm.GenerateRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: payload.Message}},
	})
	if err != nil {
		h.writeError(c, 502, "llm.test", err, cfg.Model, llmLogMetadata(cfg, ""))
		return
	}

	recordRequestOperation(c, h.logs, "llm.test", "LLM 连通测试成功", cfg.Model, llmLogMetadata(cfg, ""))
	c.JSON(200, resp)
}

// payloadFromConfig 把 LLM provider 配置转换为前端 payload。
func payloadFromConfig(cfg llm.ProviderConfig) llmConfigPayload {
	cfg = cfg.WithDefaults()
	// API Key 只暴露“是否已配置”，实际值由 WithSecrets 版本在可信场景下返回。
	payload := llmConfigPayload{
		Provider:            cfg.Provider,
		APIKeyConfigured:    cfg.APIKey != "",
		BaseURL:             cfg.BaseURL,
		APIFormat:           cfg.APIFormatWithDefault(),
		Model:               cfg.Model,
		ImageModel:          cfg.ImageModelWithDefault(),
		ImageBaseURL:        cfg.ImageBaseURL,
		ImageOrigin:         cfg.ImageOrigin,
		ImageTimeoutMS:      cfg.ImageTimeout.Milliseconds(),
		UserAgent:           cfg.UserAgentWithDefault(),
		Headers:             cfg.NormalizedHeaders(),
		Temperature:         cfg.Temperature,
		ReasoningEffort:     cfg.ReasoningEffort,
		ContextWindowTokens: cfg.ContextWindowTokens,
		MaxContextTokens:    cfg.MaxContextTokensWithDefault(),
		MaxOutputTokens:     cfg.MaxOutputTokens,
		TimeoutMS:           cfg.Timeout.Milliseconds(),
	}
	return payload
}

// payloadFromConfigWithSecrets 把 LLM 配置转换为包含密钥的 payload。
func payloadFromConfigWithSecrets(cfg llm.ProviderConfig) llmConfigPayload {
	payload := payloadFromConfig(cfg)
	payload.APIKey = cfg.APIKey
	return payload
}

// payloadFromProfile 把单个 LLM 配置档转换为前端 payload。
func payloadFromProfile(profile llm.Profile, activeID string) llmConfigPayload {
	payload := payloadFromConfig(profile.Config)
	payload.ID = profile.ID
	payload.Name = profile.Name
	payload.Group = llm.NormalizeProfileGroup(profile.Group)
	payload.Description = profile.Description
	if !profile.UpdatedAt.IsZero() {
		payload.UpdatedAt = profile.UpdatedAt.Format(time.RFC3339)
	}
	payload.ActiveProfileID = activeID
	return payload
}

// payloadFromProfileWithSecrets 把单个配置档转换为包含密钥的 payload。
func payloadFromProfileWithSecrets(profile llm.Profile, activeID string) llmConfigPayload {
	payload := payloadFromConfigWithSecrets(profile.Config)
	payload.ID = profile.ID
	payload.Name = profile.Name
	payload.Group = llm.NormalizeProfileGroup(profile.Group)
	payload.Description = profile.Description
	if !profile.UpdatedAt.IsZero() {
		payload.UpdatedAt = profile.UpdatedAt.Format(time.RFC3339)
	}
	payload.ActiveProfileID = activeID
	return payload
}

// payloadFromProfileSet 把 LLM 配置集转换为前端安全 payload。
func payloadFromProfileSet(set llm.ProfileSet) llmConfigPayload {
	current, ok := set.Current()
	if !ok {
		return llmConfigPayload{}
	}
	payload := payloadFromProfile(current, set.ActiveID)
	payload.Profiles = make([]llmConfigPayload, 0, len(set.Profiles))
	for _, profile := range set.Profiles {
		payload.Profiles = append(payload.Profiles, payloadFromProfile(profile, set.ActiveID))
	}
	return payload
}

// payloadFromProfileSetWithSecrets 把配置集转换为包含密钥的导出 payload。
func payloadFromProfileSetWithSecrets(set llm.ProfileSet) llmConfigPayload {
	current, ok := set.Current()
	if !ok {
		return llmConfigPayload{}
	}
	payload := payloadFromProfileWithSecrets(current, set.ActiveID)
	payload.Profiles = make([]llmConfigPayload, 0, len(set.Profiles))
	for _, profile := range set.Profiles {
		payload.Profiles = append(payload.Profiles, payloadFromProfileWithSecrets(profile, set.ActiveID))
	}
	return payload
}

// configFromPayload 把前端 LLM payload 转回内部 provider 配置。
func configFromPayload(payload llmConfigPayload) llm.ProviderConfig {
	return llm.ProviderConfig{
		Provider:            payload.Provider,
		APIKey:              payload.APIKey,
		BaseURL:             payload.BaseURL,
		APIFormat:           payload.APIFormat,
		Model:               payload.Model,
		ImageModel:          payload.ImageModel,
		ImageBaseURL:        payload.ImageBaseURL,
		ImageOrigin:         payload.ImageOrigin,
		ImageTimeout:        time.Duration(payload.ImageTimeoutMS) * time.Millisecond,
		UserAgent:           payload.UserAgent,
		Headers:             payload.Headers,
		Temperature:         payload.Temperature,
		ReasoningEffort:     payload.ReasoningEffort,
		ContextWindowTokens: payload.ContextWindowTokens,
		MaxContextTokens:    payload.MaxContextTokens,
		MaxOutputTokens:     payload.MaxOutputTokens,
		Timeout:             time.Duration(payload.TimeoutMS) * time.Millisecond,
	}.WithDefaults()
}

// existingProfileConfig 在配置集中查找 payload 对应的旧配置。
func existingProfileConfig(set llm.ProfileSet, payload llmConfigPayload) llm.ProviderConfig {
	// 保存、测试、拉模型列表都可能只传 profile id；这里统一找回已有配置以复用密钥。
	targetID := strings.TrimSpace(payload.ID)
	if targetID == "" {
		targetID = strings.TrimSpace(payload.ActiveProfileID)
	}
	if targetID == "" {
		targetID = set.ActiveID
	}
	for _, profile := range set.Profiles {
		if profile.ID == targetID {
			return profile.Config
		}
	}
	return llm.ProviderConfig{}
}

// upsertProfileSet 在配置集中更新现有 profile 或新增 profile。
func upsertProfileSet(set llm.ProfileSet, payload llmConfigPayload, cfg llm.ProviderConfig) llm.ProfileSet {
	now := time.Now()
	if len(set.Profiles) == 0 {
		// 首次保存时从单个 provider 配置升级为配置集。
		set = llm.NewProfileSet(cfg)
		set.Profiles[0].Name = llm.NormalizeProfileName(payload.Name)
		set.Profiles[0].Group = llm.NormalizeProfileGroup(payload.Group)
		set.Profiles[0].Description = strings.TrimSpace(payload.Description)
		set.Profiles[0].UpdatedAt = now
		return set
	}

	targetID := strings.TrimSpace(payload.ID)
	if targetID != "" {
		targetID = strings.TrimSpace(payload.ID)
	} else if len(set.Profiles) == 0 {
		targetID = set.ActiveID
	}

	for i := range set.Profiles {
		if set.Profiles[i].ID != targetID {
			continue
		}
		if strings.TrimSpace(payload.Name) != "" {
			set.Profiles[i].Name = llm.NormalizeProfileName(payload.Name)
		}
		set.Profiles[i].Group = llm.NormalizeProfileGroup(payload.Group)
		set.Profiles[i].Description = strings.TrimSpace(payload.Description)
		set.Profiles[i].UpdatedAt = now
		set.Profiles[i].Config = cfg
		set.ActiveID = set.Profiles[i].ID
		return set
	}

	newProfile := llm.Profile{
		ID:          targetID,
		Name:        llm.NormalizeProfileName(payload.Name),
		Group:       llm.NormalizeProfileGroup(payload.Group),
		Description: strings.TrimSpace(payload.Description),
		UpdatedAt:   now,
		Config:      cfg,
	}
	// 新建配置如果没有前端传入 ID，就生成稳定 UUID，后续切换/删除都靠它定位。
	if newProfile.ID == "" {
		newProfile.ID = llm.NewProfileSet(cfg).ActiveID
	}
	set.Profiles = append(set.Profiles, newProfile)
	set.ActiveID = newProfile.ID
	return set
}

// firstNonEmpty 返回第一个去空白后非空的字符串。
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// queryBool 将查询参数解析为布尔值。
func queryBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// writeError 写入 LLM 配置接口错误日志并返回响应。
func (h *LLMConfigHandler) writeError(c *gin.Context, status int, action string, err error, target string, metadata map[string]any) {
	logAndWriteError(c, h.logs, status, action, err, target, metadata)
}

// llmLogTarget 封装当前模块的 llmLogTarget 逻辑。
func llmLogTarget(payload llmConfigPayload) string {
	return firstNonEmpty(payload.ID, payload.ActiveProfileID, payload.Name, payload.Model)
}

// llmLogMetadata 封装当前模块的 llmLogMetadata 逻辑。
func llmLogMetadata(cfg llm.ProviderConfig, profileID string) map[string]any {
	metadata := map[string]any{
		"provider": string(cfg.Provider),
		"model":    cfg.Model,
	}
	if profileID = strings.TrimSpace(profileID); profileID != "" {
		metadata["profile_id"] = profileID
	}
	if cfg.BaseURL != "" {
		metadata["base_url"] = cfg.BaseURL
	}
	if format := cfg.APIFormatWithDefault(); format != "" {
		metadata["api_format"] = string(format)
	}
	if cfg.ReasoningEffort != "" {
		metadata["reasoning_effort"] = cfg.ReasoningEffort
	}
	metadata["context_window_tokens"] = cfg.WithDefaults().ContextWindowTokens
	metadata["max_context_tokens"] = cfg.MaxContextTokensWithDefault()
	if cfg.ImageBaseURL != "" {
		metadata["image_base_url"] = cfg.ImageBaseURL
	}
	if cfg.ImageOrigin != "" {
		metadata["image_origin"] = cfg.ImageOrigin
	}
	return metadata
}

// writeError 写出统一 JSON 错误响应。
func writeError(c *gin.Context, status int, err error) {
	c.JSON(status, gin.H{"error": err.Error()})
}
