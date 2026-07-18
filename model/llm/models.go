package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const DefaultOpenAICompatibleModel = "gpt-4o-mini"
const DefaultGeminiModel = "gemini-2.5-flash"
const DefaultAnthropicModel = "claude-sonnet-4-5"
const DefaultOpenAICompatibleUserAgent = "diana-qq-bot"

type ModelInfo struct {
	ID                  string `json:"id"`
	Name                string `json:"name,omitempty"`
	Object              string `json:"object,omitempty"`
	OwnedBy             string `json:"owned_by,omitempty"`
	Created             int64  `json:"created,omitempty"`
	ContextWindowTokens int64  `json:"context_window_tokens,omitempty"`
	MaxInputTokens      int64  `json:"max_input_tokens,omitempty"`
	MaxOutputTokens     int64  `json:"max_output_tokens,omitempty"`
}

// DefaultModel 返回 provider 对应的默认文本模型。
func DefaultModel(provider Provider) string {
	switch provider {
	case ProviderOpenAICompatible:
		return DefaultOpenAICompatibleModel
	case ProviderGemini:
		return DefaultGeminiModel
	case ProviderAnthropic:
		return DefaultAnthropicModel
	default:
		return ""
	}
}

// ModelPresets 返回 provider 的本地模型预设。
func ModelPresets(provider Provider) []ModelInfo {
	switch provider {
	case ProviderOpenAICompatible:
		return []ModelInfo{
			{ID: DefaultOpenAICompatibleModel, Name: "Default"},
		}
	case ProviderGemini:
		return []ModelInfo{
			{ID: DefaultGeminiModel, Name: "Gemini 2.5 Flash"},
			{ID: "gemini-2.5-pro", Name: "Gemini 2.5 Pro"},
		}
	case ProviderAnthropic:
		return []ModelInfo{
			{ID: DefaultAnthropicModel, Name: "Claude Sonnet 4.5"},
			{ID: "claude-opus-4-6", Name: "Claude Opus 4.6"},
		}
	default:
		return nil
	}
}

// ListModels 读取指定 provider 的可用模型列表。
func ListModels(ctx context.Context, cfg ProviderConfig, opts ...ClientOption) ([]ModelInfo, error) {
	cfg = cfg.WithDefaults()
	options := clientOptions{
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(&options)
	}

	switch cfg.Provider {
	case ProviderOpenAICompatible:
		// OpenAI-compatible 供应商差异最大，必须实时请求后端模型列表。
		return listOpenAICompatibleModels(ctx, cfg, options.httpClient)
	case ProviderGemini, ProviderAnthropic:
		// 官方 SDK 暂未统一暴露简单模型列表接口，这里只给本项目支持的常用预设。
		return ModelPresets(cfg.Provider), nil
	default:
		return nil, fmt.Errorf("llm: model listing is not supported for provider %q", cfg.Provider)
	}
}

// listOpenAICompatibleModels 从 OpenAI-compatible 后端读取模型列表。
func listOpenAICompatibleModels(ctx context.Context, cfg ProviderConfig, httpClient *http.Client) ([]ModelInfo, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, ErrMissingAPIKey
	}
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}

	primary, err := requestOpenAICompatibleModels(ctx, httpClient, baseURL, "model", cfg.APIKey, cfg.UserAgentWithDefault(), cfg.NormalizedHeaders())
	if err == nil {
		return primary, nil
	}
	if !isModelListFallbackError(err) {
		return nil, err
	}
	// 部分网关是 /v1/model，部分是 /v1/models；404/405 时自动尝试复数接口。
	return requestOpenAICompatibleModels(ctx, httpClient, baseURL, "models", cfg.APIKey, cfg.UserAgentWithDefault(), cfg.NormalizedHeaders())
}

// requestOpenAICompatibleModels 请求指定模型列表 endpoint。
func requestOpenAICompatibleModels(ctx context.Context, httpClient *http.Client, baseURL, endpoint, apiKey, userAgent string, headers map[string]string) ([]ModelInfo, error) {
	requestURL, err := joinOpenAICompatibleURL(baseURL, endpoint)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	for name, value := range normalizeHeaders(headers) {
		req.Header.Set(name, value)
	}
	if strings.TrimSpace(userAgent) != "" {
		req.Header.Set("User-Agent", userAgent)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		// 保留响应 body，前端/日志能看到上游拒绝模型列表请求的真实原因。
		return nil, modelListHTTPError{
			statusCode: resp.StatusCode,
			body:       string(body),
		}
	}

	models, err := decodeOpenAICompatibleModels(body)
	if err != nil {
		return nil, err
	}
	return uniqueModels(models), nil
}

// joinOpenAICompatibleURL 拼接 OpenAI-compatible BaseURL 和 endpoint。
func joinOpenAICompatibleURL(baseURL, endpoint string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return "", fmt.Errorf("llm: invalid base url: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("llm: invalid base url %q", baseURL)
	}
	// BaseURL 可能已经包含 /v1，拼接 endpoint 时保留路径但清掉 query/fragment。
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/" + strings.TrimLeft(endpoint, "/")
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

type modelListHTTPError struct {
	statusCode int
	body       string
}

// Error 返回模型列表 HTTP 错误文本。
func (e modelListHTTPError) Error() string {
	return "llm: openai-compatible model list failed: " + formatOpenAIStatusError(e.statusCode, "", "", "", e.body)
}

// isModelListFallbackError 判断模型列表错误是否可以尝试备用 endpoint。
func isModelListFallbackError(err error) bool {
	httpErr, ok := err.(modelListHTTPError)
	if !ok {
		return false
	}
	return httpErr.statusCode == http.StatusNotFound || httpErr.statusCode == http.StatusMethodNotAllowed
}

// decodeOpenAICompatibleModels 解码 OpenAI-compatible 模型列表响应。
func decodeOpenAICompatibleModels(body []byte) ([]ModelInfo, error) {
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("llm: decode model list: %w", err)
	}

	models := modelInfosFromPayload(payload)
	if len(models) == 0 {
		return nil, fmt.Errorf("llm: model list response has no models")
	}
	return models, nil
}

// modelInfosFromPayload 从任意响应结构中提取模型列表。
func modelInfosFromPayload(payload any) []ModelInfo {
	switch value := payload.(type) {
	case []any:
		return modelInfosFromArray(value)
	case map[string]any:
		// 兼容 OpenAI 标准 data，也兼容一些聚合商返回 models/model/model_list。
		for _, key := range []string{"data", "models", "model", "model_list"} {
			if nested, ok := value[key]; ok {
				if models := modelInfosFromPayload(nested); len(models) > 0 {
					return models
				}
			}
		}
		if model := modelInfoFromPayload(value); model.ID != "" {
			return []ModelInfo{model}
		}
	}
	return nil
}

// modelInfosFromArray 从数组响应中提取模型列表。
func modelInfosFromArray(items []any) []ModelInfo {
	out := make([]ModelInfo, 0, len(items))
	for _, item := range items {
		model := modelInfoFromPayload(item)
		if model.ID != "" {
			out = append(out, model)
		}
	}
	return out
}

// modelInfoFromPayload 从单个响应项中提取模型信息。
func modelInfoFromPayload(payload any) ModelInfo {
	switch value := payload.(type) {
	case string:
		// 有些后端直接返回 ["model-a","model-b"]。
		return ModelInfo{ID: value}
	case map[string]any:
		model := ModelInfo{
			ID:                  stringField(value, "id", "model", "name"),
			Name:                stringField(value, "name"),
			Object:              stringField(value, "object"),
			OwnedBy:             stringField(value, "owned_by", "ownedBy", "owner"),
			Created:             int64Field(value, "created", "created_at"),
			ContextWindowTokens: int64Field(value, "context_window_tokens", "context_window", "context_length", "max_context_length"),
			MaxInputTokens:      int64Field(value, "max_input_tokens", "input_token_limit"),
			MaxOutputTokens:     int64Field(value, "max_output_tokens", "output_token_limit"),
		}
		if limit := nestedObject(value, "limit", "limits"); limit != nil {
			if model.ContextWindowTokens == 0 {
				model.ContextWindowTokens = int64Field(limit, "context", "context_window", "context_window_tokens")
			}
			if model.MaxInputTokens == 0 {
				model.MaxInputTokens = int64Field(limit, "input", "max_input_tokens")
			}
			if model.MaxOutputTokens == 0 {
				model.MaxOutputTokens = int64Field(limit, "output", "max_output_tokens")
			}
		}
		if model.ID == model.Name {
			model.Name = ""
		}
		return model
	default:
		return ModelInfo{}
	}
}

func nestedObject(values map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		if value, ok := values[key].(map[string]any); ok {
			return value
		}
	}
	return nil
}

// stringField 从 map 中按候选 key 读取字符串字段。
func stringField(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key].(string); ok {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

// int64Field 从 map 中按候选 key 读取 int64 字段。
func int64Field(values map[string]any, keys ...string) int64 {
	for _, key := range keys {
		switch value := values[key].(type) {
		case float64:
			return int64(value)
		case int64:
			return value
		case json.Number:
			parsed, _ := value.Int64()
			return parsed
		}
	}
	return 0
}

// uniqueModels 按模型 ID 去重并保持原顺序。
func uniqueModels(models []ModelInfo) []ModelInfo {
	seen := make(map[string]struct{}, len(models))
	out := make([]ModelInfo, 0, len(models))
	for _, model := range models {
		if model.ID == "" {
			continue
		}
		if _, ok := seen[model.ID]; ok {
			continue
		}
		// 保留后端原顺序，只去掉重复 ID，前端下拉展示更接近供应商返回。
		seen[model.ID] = struct{}{}
		out = append(out, model)
	}
	return out
}
