package qqbot

import (
	"context"
	"fmt"
	"strings"

	"diana-qq-bot/model/applog"
	"diana-qq-bot/model/llm"
)

type LLMConfigPlugin struct{}

type llmConfigCommand struct {
	Provider    llm.Provider
	ProviderSet bool
	Model       string
}

type llmConfigApplyResult struct {
	Reply       string
	Updated     bool
	ProfileID   string
	ProfileName string
	OldProvider llm.Provider
	NewProvider llm.Provider
	OldModel    string
	NewModel    string
}

// NewLLMConfigPlugin 创建官方内置 LLM 配置技能插件。
func NewLLMConfigPlugin() *LLMConfigPlugin {
	return &LLMConfigPlugin{}
}

// Manifest 返回 LLM 配置技能插件清单。
func (p *LLMConfigPlugin) Manifest() PluginManifest {
	return PluginManifest{
		ID:          "official.llm-config-skill",
		Name:        "LLM 配置技能",
		Version:     "0.1.0",
		Description: "官方内置 LLM 配置技能，允许主人在聊天中修改当前使用的模型提供商和模型名称。",
		Official:    true,
		BuiltIn:     true,
		Permissions: []string{"message:read", "llm:config:write"},
	}
}

// Handle 处理聊天里的 LLM 配置修改请求。
func (p *LLMConfigPlugin) Handle(ctx context.Context, req PluginRequest) (*PluginResponse, error) {
	// 自然语言配置意图由主 Agent 语义判断，再调用 diana.llm_config。
	// 插件不再用字符串匹配抢占普通的模型、API 或 Agent 讨论。
	return nil, nil
}

// applyLLMConfigCommand 将自然语言配置意图应用到当前 LLM profile。
func applyLLMConfigCommand(ctx context.Context, store LLMProfileStore, command llmConfigCommand, listModels LLMModelLister) llmConfigApplyResult {
	set := store.Profiles().WithDefaults()
	current, ok := set.Current()
	if !ok {
		return llmConfigApplyResult{Reply: "当前没有激活的 LLM 配置。"}
	}
	if listModels == nil {
		listModels = defaultLLMModelLister
	}

	nextCfg := current.Config.WithDefaults()
	oldProvider := nextCfg.Provider
	oldModel := nextCfg.Model
	if command.ProviderSet {
		// 只切 provider 且没指定模型时，换到该 provider 的默认模型，避免保留旧 provider 的无效模型名。
		nextCfg.Provider = command.Provider
		if strings.TrimSpace(command.Model) == "" && oldProvider != command.Provider {
			nextCfg.Model = llm.DefaultModel(command.Provider)
		}
	}
	if strings.TrimSpace(command.Model) != "" {
		nextCfg.Model = strings.TrimSpace(command.Model)
	}
	nextCfg = nextCfg.WithDefaults()
	// 必须先问后端模型列表，防止用户切到 provider 里不存在的模型后导致机器人不可用。
	modelInfo, err := ensureLLMModelAvailable(ctx, nextCfg, listModels)
	if err != nil {
		return llmConfigApplyResult{
			Reply:       "更新失败：" + err.Error(),
			ProfileID:   current.ID,
			ProfileName: current.Name,
			OldProvider: oldProvider,
			NewProvider: nextCfg.Provider,
			OldModel:    oldModel,
			NewModel:    nextCfg.Model,
		}
	}
	if modelInfo.ContextWindowTokens > 0 {
		nextCfg.ContextWindowTokens = modelInfo.ContextWindowTokens
	} else if oldProvider != nextCfg.Provider || oldModel != nextCfg.Model {
		nextCfg.ContextWindowTokens = llm.DefaultContextWindowTokens
	}
	if nextCfg.MaxContextTokens <= 0 || nextCfg.MaxContextTokens > nextCfg.ContextWindowTokens {
		nextCfg.MaxContextTokens = min(nextCfg.ContextWindowTokens, llm.DefaultMaxContextTokens)
	}
	if modelInfo.MaxOutputTokens > 0 && nextCfg.MaxOutputTokens > modelInfo.MaxOutputTokens {
		nextCfg.MaxOutputTokens = modelInfo.MaxOutputTokens
	}
	if nextCfg.MaxOutputTokens >= nextCfg.MaxContextTokens {
		nextCfg.MaxOutputTokens = nextCfg.MaxContextTokens / 4
	}
	if err := nextCfg.Validate(); err != nil {
		return llmConfigApplyResult{
			Reply:       "更新失败：" + err.Error(),
			ProfileID:   current.ID,
			ProfileName: current.Name,
			OldProvider: oldProvider,
			NewProvider: nextCfg.Provider,
			OldModel:    oldModel,
			NewModel:    nextCfg.Model,
		}
	}

	for i := range set.Profiles {
		if set.Profiles[i].ID != current.ID {
			continue
		}
		set.Profiles[i].Config = nextCfg
		set.ActiveID = current.ID
		store.SaveProfiles(set)
		return llmConfigApplyResult{
			Reply:       fmt.Sprintf("已更新当前 LLM：%s\nProvider：%s -> %s\nModel：%s -> %s", current.Name, oldProvider, nextCfg.Provider, oldModel, nextCfg.Model),
			Updated:     true,
			ProfileID:   current.ID,
			ProfileName: current.Name,
			OldProvider: oldProvider,
			NewProvider: nextCfg.Provider,
			OldModel:    oldModel,
			NewModel:    nextCfg.Model,
		}
	}
	return llmConfigApplyResult{Reply: "当前没有激活的 LLM 配置。"}
}

// recordLLMConfigSkillLog 记录聊天修改 LLM 配置的审计日志。
func recordLLMConfigSkillLog(ctx context.Context, req PluginRequest, result llmConfigApplyResult, err error) {
	if req.AppLogs == nil {
		return
	}
	// 聊天修改 LLM 配置会影响运行时行为，所以和 WebUI 配置变更写入同一条审计流。
	// 成功算操作日志，被拒绝或失败算错误日志，操作者记录为 QQ 用户。
	kind := applog.KindError
	level := applog.LevelError
	message := result.Reply
	if result.Updated {
		kind = applog.KindOperation
		level = applog.LevelInfo
		message = "聊天修改 LLM 配置成功"
	}
	metadata := map[string]any{
		"user_id": req.Event.UserID,
		"kind":    string(req.Event.Kind),
		"command": req.Text,
	}
	if req.Event.GroupID != "" {
		metadata["group_id"] = req.Event.GroupID
	}
	if result.ProfileID != "" {
		metadata["profile_id"] = result.ProfileID
	}
	if result.ProfileName != "" {
		metadata["profile_name"] = result.ProfileName
	}
	if result.OldProvider != "" {
		metadata["old_provider"] = string(result.OldProvider)
	}
	if result.NewProvider != "" {
		metadata["new_provider"] = string(result.NewProvider)
	}
	if result.OldModel != "" {
		metadata["old_model"] = result.OldModel
	}
	if result.NewModel != "" {
		metadata["new_model"] = result.NewModel
	}
	detail := result.Reply
	if err != nil {
		detail = err.Error()
	}
	// 审计日志失败不影响聊天命令结果；有效 owner 命令不能因为日志系统异常而失败。
	_ = req.AppLogs.AppendLog(ctx, applog.Entry{
		Kind:     kind,
		Level:    level,
		Action:   "qqbot.llm_config_skill",
		Message:  message,
		Detail:   detail,
		Actor:    qqEventActor(req.Event),
		Target:   firstNonEmpty(result.ProfileID, result.NewModel, result.OldModel),
		Metadata: metadata,
	})
}

// qqEventActor 将 QQ 事件转换为日志操作者标识。
func qqEventActor(event MessageEvent) string {
	// 给 actor 加命名空间，日志中心里能区分 WebUI 操作者和 QQ 用户。
	if userID := strings.TrimSpace(event.UserID); userID != "" {
		return "qq:" + userID
	}
	return "qq:unknown"
}

// defaultLLMModelLister 使用默认 LLM 模型列表实现。
func defaultLLMModelLister(ctx context.Context, cfg llm.ProviderConfig) ([]llm.ModelInfo, error) {
	return llm.ListModels(ctx, cfg)
}

// ensureLLMModelAvailable 校验目标模型是否存在于 provider 后端列表。
func ensureLLMModelAvailable(ctx context.Context, cfg llm.ProviderConfig, listModels LLMModelLister) (llm.ModelInfo, error) {
	model := strings.TrimSpace(cfg.Model)
	// listModels 会走当前 provider 的真实后端接口；不能靠本地硬编码模型名判断。
	models, err := listModels(ctx, cfg)
	if err != nil {
		return llm.ModelInfo{}, fmt.Errorf("无法读取 %s 的模型列表，未保存；请先在 WebUI 的模型列表里选择可用模型。%v", cfg.Provider, err)
	}
	for _, candidate := range models {
		if strings.EqualFold(strings.TrimSpace(candidate.ID), model) {
			return candidate, nil
		}
	}
	return llm.ModelInfo{}, fmt.Errorf("模型 %s 不在 %s 的模型列表中，未保存。可选：%s", model, cfg.Provider, summarizeModelIDs(models))
}

// modelInList 判断模型名是否存在于模型列表中。
func modelInList(model string, models []llm.ModelInfo) bool {
	model = strings.TrimSpace(model)
	for _, candidate := range models {
		if strings.EqualFold(strings.TrimSpace(candidate.ID), model) {
			return true
		}
	}
	return false
}

// summarizeModelIDs 摘要展示可选模型 ID。
func summarizeModelIDs(models []llm.ModelInfo) string {
	ids := make([]string, 0, len(models))
	seen := map[string]struct{}{}
	for _, model := range models {
		id := strings.TrimSpace(model.ID)
		if id == "" {
			continue
		}
		key := strings.ToLower(id)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		ids = append(ids, id)
		if len(ids) >= 8 {
			break
		}
	}
	if len(ids) == 0 {
		return "暂无可用模型"
	}
	return strings.Join(ids, "、")
}
