package llm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Provider string

const (
	ProviderOpenAICompatible Provider = "openai_compatible"
	ProviderGemini           Provider = "gemini"
	ProviderAnthropic        Provider = "anthropic"
)

type APIFormat string

const (
	APIFormatResponses       APIFormat = "responses"
	APIFormatChatCompletions APIFormat = "chat_completions"
)

const (
	DefaultContextWindowTokens int64 = 16384
	DefaultMaxContextTokens    int64 = 16384
	DefaultMaxOutputTokens     int64 = 1024
	minContextWindowTokens     int64 = 1024
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

type Message struct {
	Role     Role            `json:"role"`
	Content  string          `json:"content"`
	Parts    []ContentPart   `json:"parts,omitempty"`
	Priority MessagePriority `json:"-"`
}

type MessagePriority int

const (
	MessagePriorityDefault MessagePriority = 0
	MessagePriorityHistory MessagePriority = 20
	MessagePrioritySummary MessagePriority = 60
	MessagePriorityMemory  MessagePriority = 80
	MessagePrioritySystem  MessagePriority = 120
	MessagePriorityPlugin  MessagePriority = 130
	MessagePriorityCurrent MessagePriority = 140
)

type ContentPartType string

const (
	ContentPartText     ContentPartType = "text"
	ContentPartImageURL ContentPartType = "image_url"
)

type ContentPart struct {
	Type     ContentPartType `json:"type"`
	Text     string          `json:"text,omitempty"`
	ImageURL string          `json:"image_url,omitempty"`
	Detail   string          `json:"detail,omitempty"`
}

type GenerateRequest struct {
	Model           string    `json:"model,omitempty"`
	Messages        []Message `json:"messages"`
	Temperature     *float64  `json:"temperature,omitempty"`
	ReasoningEffort string    `json:"reasoning_effort,omitempty"`
	MaxOutputTokens int64     `json:"max_output_tokens,omitempty"`
}

type Usage struct {
	InputTokens  int64 `json:"input_tokens,omitempty"`
	OutputTokens int64 `json:"output_tokens,omitempty"`
	TotalTokens  int64 `json:"total_tokens,omitempty"`
}

type GenerateResponse struct {
	Provider Provider `json:"provider"`
	Model    string   `json:"model,omitempty"`
	Text     string   `json:"text"`
	Usage    Usage    `json:"usage,omitempty"`
}

type ImageGenerateRequest struct {
	Model  string `json:"model,omitempty"`
	Prompt string `json:"prompt"`
	Size   string `json:"size,omitempty"`
	N      int    `json:"n,omitempty"`
}

type ImageEditRequest struct {
	Model  string   `json:"model,omitempty"`
	Prompt string   `json:"prompt"`
	Images []string `json:"images"`
	Size   string   `json:"size,omitempty"`
	N      int      `json:"n,omitempty"`
}

type ImageGenerateResponse struct {
	Provider Provider `json:"provider"`
	Model    string   `json:"model,omitempty"`
	Images   []string `json:"images"`
}

type ProviderConfig struct {
	Provider            Provider          `json:"provider"`
	APIKey              string            `json:"api_key,omitempty"`
	BaseURL             string            `json:"base_url,omitempty"`
	APIFormat           APIFormat         `json:"api_format,omitempty"`
	Model               string            `json:"model"`
	ImageModel          string            `json:"image_model,omitempty"`
	ImageBaseURL        string            `json:"image_base_url,omitempty"`
	ImageOrigin         string            `json:"image_origin,omitempty"`
	ImageTimeout        time.Duration     `json:"image_timeout,omitempty"`
	UserAgent           string            `json:"user_agent,omitempty"`
	Headers             map[string]string `json:"headers,omitempty"`
	Temperature         *float64          `json:"temperature,omitempty"`
	ReasoningEffort     string            `json:"reasoning_effort,omitempty"`
	ContextWindowTokens int64             `json:"context_window_tokens,omitempty"`
	MaxContextTokens    int64             `json:"max_context_tokens,omitempty"`
	MaxOutputTokens     int64             `json:"max_output_tokens,omitempty"`
	Timeout             time.Duration     `json:"timeout,omitempty"`
}

type ClientOption func(*clientOptions)

type clientOptions struct {
	httpClient *http.Client
}

// WithHTTPClient 注入自定义 HTTP client。
func WithHTTPClient(client *http.Client) ClientOption {
	return func(opts *clientOptions) {
		opts.httpClient = client
	}
}

type LLMClient interface {
	Generate(ctx context.Context, req GenerateRequest) (*GenerateResponse, error)
}

type ImageGenerator interface {
	GenerateImage(ctx context.Context, req ImageGenerateRequest) (*ImageGenerateResponse, error)
}

type ImageEditor interface {
	EditImage(ctx context.Context, req ImageEditRequest) (*ImageGenerateResponse, error)
}

var (
	ErrMissingAPIKey   = errors.New("llm: missing api key")
	ErrMissingModel    = errors.New("llm: missing model")
	ErrMissingMessages = errors.New("llm: missing messages")
)

// NewClient 根据 provider 配置创建 LLM 客户端。
func NewClient(cfg ProviderConfig, opts ...ClientOption) (LLMClient, error) {
	cfg = cfg.WithDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	options := clientOptions{
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(&options)
	}

	// 对外统一 LLMClient 接口，内部按 provider 分发到不同 SDK/HTTP 协议。
	switch cfg.Provider {
	case ProviderOpenAICompatible:
		return newOpenAICompatibleClient(cfg, options.httpClient), nil
	case ProviderGemini:
		return newGeminiClient(cfg, options.httpClient)
	case ProviderAnthropic:
		return newAnthropicClient(cfg, options.httpClient), nil
	default:
		return nil, fmt.Errorf("llm: unsupported provider %q", cfg.Provider)
	}
}

// GenerateImage 根据 provider 配置生成图片。
func GenerateImage(ctx context.Context, cfg ProviderConfig, req ImageGenerateRequest, opts ...ClientOption) (*ImageGenerateResponse, error) {
	client, err := NewClient(cfg, opts...)
	if err != nil {
		return nil, err
	}
	generator, ok := client.(ImageGenerator)
	if !ok {
		return nil, fmt.Errorf("llm: image generation is not supported for provider %q", cfg.Provider)
	}
	return generator.GenerateImage(ctx, req)
}

// EditImage 根据 provider 配置编辑图片。
func EditImage(ctx context.Context, cfg ProviderConfig, req ImageEditRequest, opts ...ClientOption) (*ImageGenerateResponse, error) {
	client, err := NewClient(cfg, opts...)
	if err != nil {
		return nil, err
	}
	editor, ok := client.(ImageEditor)
	if !ok {
		return nil, fmt.Errorf("llm: image editing is not supported for provider %q", cfg.Provider)
	}
	return editor.EditImage(ctx, req)
}

// Validate 校验 provider 配置是否可用于调用。
func (cfg ProviderConfig) Validate() error {
	// Validate 会先规整空白，避免前端输入带空格导致 provider/model 比较失败。
	cfg.Provider = Provider(strings.TrimSpace(string(cfg.Provider)))
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	cfg.BaseURL = strings.TrimSpace(cfg.BaseURL)
	cfg.APIFormat = APIFormat(strings.TrimSpace(string(cfg.APIFormat)))
	cfg.ImageBaseURL = strings.TrimSpace(cfg.ImageBaseURL)
	cfg.ImageOrigin = strings.TrimSpace(cfg.ImageOrigin)
	cfg.Model = strings.TrimSpace(cfg.Model)
	cfg.Headers = normalizeHeaders(cfg.Headers)
	cfg.ReasoningEffort = normalizeReasoningEffort(cfg.ReasoningEffort)
	if cfg.Provider == "" {
		return errors.New("llm: provider is required")
	}
	if !cfg.Provider.Supported() {
		return fmt.Errorf("llm: unsupported provider %q", cfg.Provider)
	}
	if cfg.Provider == ProviderOpenAICompatible {
		if cfg.APIFormat != "" && !cfg.APIFormat.Supported() {
			return fmt.Errorf("llm: unsupported api_format %q", cfg.APIFormat)
		}
	} else if cfg.APIFormat != "" {
		return fmt.Errorf("llm: api_format is only supported for provider %q", ProviderOpenAICompatible)
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return ErrMissingAPIKey
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return ErrMissingModel
	}
	if err := validateBaseURL(cfg.BaseURL); err != nil {
		return err
	}
	if err := validateBaseURL(cfg.ImageBaseURL); err != nil {
		return fmt.Errorf("llm: invalid image_base_url: %w", err)
	}
	if err := validateImageOrigin(cfg.ImageOrigin); err != nil {
		return err
	}
	if cfg.ImageTimeout < 0 {
		return errors.New("llm: image_timeout must be greater than or equal to 0")
	}
	if cfg.MaxOutputTokens < 0 {
		return errors.New("llm: max_output_tokens must be greater than or equal to 0")
	}
	if cfg.ContextWindowTokens < 0 {
		return errors.New("llm: context_window_tokens must be greater than or equal to 0")
	}
	if cfg.MaxContextTokens < 0 {
		return errors.New("llm: max_context_tokens must be greater than or equal to 0")
	}
	if cfg.ContextWindowTokens > 0 && cfg.ContextWindowTokens < minContextWindowTokens {
		return fmt.Errorf("llm: context_window_tokens must be at least %d", minContextWindowTokens)
	}
	if cfg.MaxContextTokens > 0 && cfg.MaxContextTokens < minContextWindowTokens {
		return fmt.Errorf("llm: max_context_tokens must be at least %d", minContextWindowTokens)
	}
	if cfg.ContextWindowTokens > 0 && cfg.MaxContextTokens > cfg.ContextWindowTokens {
		return errors.New("llm: max_context_tokens cannot exceed context_window_tokens")
	}
	if cfg.MaxContextTokens > 0 && cfg.MaxOutputTokens >= cfg.MaxContextTokens {
		return errors.New("llm: max_output_tokens must be smaller than max_context_tokens")
	}
	if cfg.Temperature != nil && (*cfg.Temperature < 0 || *cfg.Temperature > 2) {
		return errors.New("llm: temperature must be between 0 and 2")
	}
	if err := validateReasoningEffort(cfg.ReasoningEffort); err != nil {
		return err
	}
	for name, value := range cfg.Headers {
		if !validHeaderName(name) {
			return fmt.Errorf("llm: invalid header name %q", name)
		}
		if strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("llm: invalid header value for %q", name)
		}
	}
	return nil
}

// Supported 判断 provider 是否被当前项目支持。
func (provider Provider) Supported() bool {
	switch provider {
	case ProviderOpenAICompatible, ProviderGemini, ProviderAnthropic:
		return true
	default:
		return false
	}
}

// Supported 判断 OpenAI-compatible 文本 API 格式是否受支持。
func (format APIFormat) Supported() bool {
	switch format {
	case APIFormatResponses, APIFormatChatCompletions:
		return true
	default:
		return false
	}
}

// WithDefaults 补齐 provider 配置默认值。
func (cfg ProviderConfig) WithDefaults() ProviderConfig {
	// WithDefaults 只补配置默认值，不校验密钥；这样 WebUI 可以展示未填 key 的草稿配置。
	cfg.Provider = Provider(strings.TrimSpace(string(cfg.Provider)))
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	cfg.BaseURL = strings.TrimSpace(cfg.BaseURL)
	cfg.APIFormat = APIFormat(strings.TrimSpace(string(cfg.APIFormat)))
	cfg.ImageBaseURL = strings.TrimSpace(cfg.ImageBaseURL)
	cfg.ImageOrigin = strings.TrimSpace(cfg.ImageOrigin)
	cfg.Model = strings.TrimSpace(cfg.Model)
	cfg.ImageModel = strings.TrimSpace(cfg.ImageModel)
	cfg.UserAgent = strings.TrimSpace(cfg.UserAgent)
	cfg.ReasoningEffort = normalizeReasoningEffort(cfg.ReasoningEffort)
	cfg.Headers = normalizeHeaders(cfg.Headers)
	if cfg.Provider == ProviderOpenAICompatible {
		if cfg.APIFormat == "" {
			cfg.APIFormat = APIFormatResponses
		}
	} else {
		// Provider 切换会复用当前配置对象，OpenAI 专用格式不能跟到 Gemini/Anthropic。
		cfg.APIFormat = ""
	}
	if cfg.Model == "" {
		cfg.Model = DefaultModel(cfg.Provider)
	}
	if cfg.ImageModel == "" {
		cfg.ImageModel = DefaultImageModel(cfg.Provider)
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = DefaultUserAgent(cfg.Provider)
	}
	if cfg.ContextWindowTokens == 0 {
		cfg.ContextWindowTokens = DefaultContextWindowTokens
	}
	if cfg.MaxContextTokens == 0 {
		cfg.MaxContextTokens = DefaultMaxContextTokens
		if cfg.ContextWindowTokens > 0 && cfg.MaxContextTokens > cfg.ContextWindowTokens {
			cfg.MaxContextTokens = cfg.ContextWindowTokens
		}
	}
	return cfg
}

// MaxContextTokensWithDefault 返回不超过模型窗口的请求总 token 预算。
func (cfg ProviderConfig) MaxContextTokensWithDefault() int64 {
	cfg = cfg.WithDefaults()
	if cfg.ContextWindowTokens > 0 && cfg.MaxContextTokens > cfg.ContextWindowTokens {
		return cfg.ContextWindowTokens
	}
	return cfg.MaxContextTokens
}

// APIFormatWithDefault 返回 OpenAI-compatible 文本 API 格式。
func (cfg ProviderConfig) APIFormatWithDefault() APIFormat {
	if format := APIFormat(strings.TrimSpace(string(cfg.APIFormat))); format != "" {
		return format
	}
	if cfg.Provider == ProviderOpenAICompatible {
		return APIFormatResponses
	}
	return ""
}

// NormalizedHeaders 返回规整后的自定义 HTTP headers。
func (cfg ProviderConfig) NormalizedHeaders() map[string]string {
	return normalizeHeaders(cfg.Headers)
}

// normalizeHeaders 去掉空键值并规整 header 名称。
func normalizeHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for name, value := range headers {
		name = http.CanonicalHeaderKey(strings.TrimSpace(name))
		value = strings.TrimSpace(value)
		if name == "" || value == "" {
			continue
		}
		out[name] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// validHeaderName 保守校验 HTTP header 名称，避免换行、冒号等破坏请求结构。
func validHeaderName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	for _, r := range name {
		if r <= 32 || r >= 127 {
			return false
		}
		switch r {
		case '(', ')', '<', '>', '@', ',', ';', ':', '\\', '"', '/', '[', ']', '?', '=', '{', '}':
			return false
		}
	}
	return true
}

// validateBaseURL 校验自定义 BaseURL。
func validateBaseURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		// 空 BaseURL 表示使用各 provider SDK 默认地址。
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("llm: invalid base_url %q", raw)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("llm: base_url scheme must be http or https")
	}
	return nil
}

func validateImageOrigin(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	host, port, err := net.SplitHostPort(raw)
	if err != nil || strings.TrimSpace(host) == "" {
		return fmt.Errorf("llm: image_origin must use host:port format")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return fmt.Errorf("llm: image_origin port must be between 1 and 65535")
	}
	return nil
}

// DefaultImageModel 返回 provider 对应的默认图片模型。
func DefaultImageModel(provider Provider) string {
	switch provider {
	case ProviderOpenAICompatible:
		return "gpt-image-2"
	case ProviderGemini:
		return "imagen-4.0-generate-001"
	default:
		return ""
	}
}

// DefaultUserAgent 返回 provider 对应的默认 User-Agent。
func DefaultUserAgent(provider Provider) string {
	switch provider {
	case ProviderOpenAICompatible:
		return DefaultOpenAICompatibleUserAgent
	default:
		return ""
	}
}

// ImageModelWithDefault 返回图片模型配置或默认值。
func (cfg ProviderConfig) ImageModelWithDefault() string {
	if strings.TrimSpace(cfg.ImageModel) != "" {
		return cfg.ImageModel
	}
	return DefaultImageModel(cfg.Provider)
}

func (cfg ProviderConfig) ImageBaseURLWithDefault() string {
	if strings.TrimSpace(cfg.ImageBaseURL) != "" {
		return cfg.ImageBaseURL
	}
	return cfg.BaseURL
}

func (cfg ProviderConfig) ImageTimeoutWithDefault() time.Duration {
	if cfg.ImageTimeout > 0 {
		return cfg.ImageTimeout
	}
	return cfg.Timeout
}

// UserAgentWithDefault 返回 User-Agent 配置或默认值。
func (cfg ProviderConfig) UserAgentWithDefault() string {
	if strings.TrimSpace(cfg.UserAgent) != "" {
		return cfg.UserAgent
	}
	return DefaultUserAgent(cfg.Provider)
}

// withDefaults 用 provider 配置补齐生成请求。
func (req GenerateRequest) withDefaults(cfg ProviderConfig) GenerateRequest {
	if strings.TrimSpace(req.Model) == "" {
		req.Model = cfg.Model
	}
	if req.Temperature == nil {
		req.Temperature = cfg.Temperature
	}
	if strings.TrimSpace(req.ReasoningEffort) == "" {
		req.ReasoningEffort = cfg.ReasoningEffort
	}
	req.ReasoningEffort = normalizeReasoningEffort(req.ReasoningEffort)
	if req.MaxOutputTokens == 0 {
		// 0 表示调用方没覆盖，沿用 provider config；负数会在 Validate 阶段拒绝。
		req.MaxOutputTokens = cfg.MaxOutputTokens
	}
	return req
}

// validateGenerateRequest 校验通用生成请求。
func validateGenerateRequest(req GenerateRequest) error {
	if strings.TrimSpace(req.Model) == "" {
		return ErrMissingModel
	}
	if len(req.Messages) == 0 {
		return ErrMissingMessages
	}
	if err := validateReasoningEffort(req.ReasoningEffort); err != nil {
		return err
	}
	for i, msg := range req.Messages {
		if msg.Role == "" {
			return fmt.Errorf("llm: messages[%d].role is required", i)
		}
		if !messageHasContent(msg) {
			return fmt.Errorf("llm: messages[%d].content is required", i)
		}
	}
	return nil
}

func normalizeReasoningEffort(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func validateReasoningEffort(value string) error {
	switch normalizeReasoningEffort(value) {
	case "", "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra":
		return nil
	default:
		return fmt.Errorf("llm: unsupported reasoning_effort %q", value)
	}
}

func messageHasContent(msg Message) bool {
	if strings.TrimSpace(msg.Content) != "" {
		return true
	}
	for _, part := range msg.Parts {
		switch part.Type {
		case ContentPartText:
			if strings.TrimSpace(part.Text) != "" {
				return true
			}
		case ContentPartImageURL:
			if strings.TrimSpace(part.ImageURL) != "" {
				return true
			}
		}
	}
	return false
}

func messageTextContent(msg Message) string {
	if text := strings.TrimSpace(msg.Content); text != "" {
		return text
	}
	parts := make([]string, 0, len(msg.Parts))
	for _, part := range msg.Parts {
		switch part.Type {
		case ContentPartText:
			if text := strings.TrimSpace(part.Text); text != "" {
				parts = append(parts, text)
			}
		case ContentPartImageURL:
			if strings.TrimSpace(part.ImageURL) != "" {
				parts = append(parts, "[图片]")
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// splitSystemPrompt 将 system 消息和普通对话消息拆开。
func splitSystemPrompt(messages []Message) (string, []Message) {
	// Gemini/Anthropic/OpenAI Responses 对 system prompt 的位置要求不同，这里统一拆出来。
	var system []string
	chat := make([]Message, 0, len(messages))
	for _, msg := range messages {
		if msg.Role == RoleSystem {
			system = append(system, messageTextContent(msg))
			continue
		}
		chat = append(chat, msg)
	}
	return strings.Join(system, "\n\n"), chat
}
