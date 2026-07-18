package qqbot

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"diana-qq-bot/model/llm"
)

type dianaLLMConfigTool struct {
	runtime *Runtime
	event   MessageEvent
}

func newDianaLLMConfigTool(runtime *Runtime, event MessageEvent) *dianaLLMConfigTool {
	return &dianaLLMConfigTool{runtime: runtime, event: event}
}

func (t *dianaLLMConfigTool) Name() string {
	return "diana.llm_config"
}

func (t *dianaLLMConfigTool) Description() string {
	return `修改 Diana 自己当前激活的 LLM provider 和 model。只有主人明确要求更改机器人自身配置时才能调用；讨论模型、推荐 API 中转项目、分析别人的 Agent/模型、说“我用某模型”都不得调用。input: {"operation":"update","provider":"openai_compatible|gemini|anthropic，可选","model":"模型 ID，可选"}`
}

func (t *dianaLLMConfigTool) Run(ctx context.Context, input map[string]any) (string, error) {
	if t == nil || t.runtime == nil {
		return "", fmt.Errorf("diana LLM config: runtime is not configured")
	}
	if !t.runtime.relationshipPolicy(ctx, t.event).Owner {
		return "", fmt.Errorf("只有主人可以修改 LLM 配置")
	}
	operation := strings.ToLower(strings.TrimSpace(configToolString(input, "operation")))
	if operation == "" {
		operation = "update"
	}
	if operation != "update" {
		return "", fmt.Errorf("operation 必须是 update")
	}
	providerRaw := strings.ToLower(strings.TrimSpace(configToolString(input, "provider")))
	model := strings.TrimSpace(configToolString(input, "model"))
	if providerRaw == "" && model == "" {
		return "", fmt.Errorf("至少提供 provider 或 model")
	}
	command := llmConfigCommand{Model: model}
	if providerRaw != "" {
		provider, err := structuredLLMProvider(providerRaw)
		if err != nil {
			return "", err
		}
		command.Provider = provider
		command.ProviderSet = true
	}
	if t.runtime.llmStore == nil {
		return "", fmt.Errorf("当前未接入 LLM 配置集")
	}
	result := applyLLMConfigCommand(ctx, t.runtime.llmStore, command, t.runtime.llmModelLister())
	recordLLMConfigSkillLog(ctx, PluginRequest{
		Event:    t.event,
		Text:     fmt.Sprintf("diana.llm_config provider=%s model=%s", providerRaw, model),
		OwnerID:  t.runtime.effectiveConfigForEvent(t.event).OwnerID,
		LLMStore: t.runtime.llmStore,
		AppLogs:  t.runtime.appLogWriter(),
	}, result, nil)
	if !result.Updated {
		return "", fmt.Errorf("%s", result.Reply)
	}
	body, err := json.Marshal(map[string]any{
		"ok":           true,
		"action":       "updated",
		"message":      result.Reply,
		"profile_id":   result.ProfileID,
		"profile_name": result.ProfileName,
		"old_provider": result.OldProvider,
		"new_provider": result.NewProvider,
		"old_model":    result.OldModel,
		"new_model":    result.NewModel,
	})
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func structuredLLMProvider(raw string) (llm.Provider, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "openai", "openai_compatible", "openai-compatible":
		return llm.ProviderOpenAICompatible, nil
	case "gemini", "google", "google_genai":
		return llm.ProviderGemini, nil
	case "anthropic", "claude":
		return llm.ProviderAnthropic, nil
	default:
		return "", fmt.Errorf("不支持的 provider %q", raw)
	}
}
