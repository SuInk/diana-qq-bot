package llm

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"google.golang.org/genai"
)

type geminiClient struct {
	cfg    ProviderConfig
	client *genai.Client
}

const maxGeminiOutputTokens = int64(1<<31 - 1)

// newGeminiClient 创建 Gemini provider 客户端。
func newGeminiClient(cfg ProviderConfig, httpClient *http.Client) (*geminiClient, error) {
	httpOptions := genai.HTTPOptions{}
	if cfg.BaseURL != "" {
		httpOptions.BaseURL = cfg.BaseURL
	}
	if cfg.Timeout > 0 {
		httpOptions.Timeout = &cfg.Timeout
	}

	// Gemini SDK 的 client 创建需要 context，但这里不做网络请求，用 background 即可。
	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:      cfg.APIKey,
		Backend:     genai.BackendGeminiAPI,
		HTTPClient:  httpClient,
		HTTPOptions: httpOptions,
	})
	if err != nil {
		return nil, err
	}

	return &geminiClient{
		cfg:    cfg,
		client: client,
	}, nil
}

// Generate 调用 Gemini 模型生成回复。
func (c *geminiClient) Generate(ctx context.Context, req GenerateRequest) (*GenerateResponse, error) {
	req = req.withDefaults(c.cfg)
	req = applyContextBudget(req, c.cfg)
	if err := validateGenerateRequest(req); err != nil {
		return nil, err
	}

	system, messages := splitSystemPrompt(req.Messages)
	config := &genai.GenerateContentConfig{}
	if system != "" {
		// Gemini 把 system instruction 放在 GenerateContentConfig，而不是普通对话消息里。
		config.SystemInstruction = genai.NewContentFromText(system, genai.RoleUser)
	}
	if req.Temperature != nil {
		temperature := float32(*req.Temperature)
		config.Temperature = &temperature
	}
	if req.MaxOutputTokens > 0 {
		maxOutputTokens, err := geminiOutputTokenLimit(req.MaxOutputTokens)
		if err != nil {
			return nil, err
		}
		config.MaxOutputTokens = maxOutputTokens
	}

	resp, err := c.client.Models.GenerateContent(ctx, req.Model, geminiContents(messages), config)
	if err != nil {
		return nil, err
	}

	text := strings.TrimSpace(resp.Text())
	if text == "" {
		return nil, fmt.Errorf("llm: gemini response has no text")
	}

	return &GenerateResponse{
		Provider: ProviderGemini,
		Model:    req.Model,
		Text:     text,
		Usage: Usage{
			InputTokens:  int64(resp.UsageMetadata.PromptTokenCount),
			OutputTokens: int64(resp.UsageMetadata.CandidatesTokenCount),
			TotalTokens:  int64(resp.UsageMetadata.TotalTokenCount),
		},
	}, nil
}

func geminiOutputTokenLimit(value int64) (int32, error) {
	if value < 0 || value > maxGeminiOutputTokens {
		return 0, fmt.Errorf("llm: Gemini max_output_tokens must be between 0 and %d", maxGeminiOutputTokens)
	}
	return int32(value), nil
}

// geminiContents 将通用消息转换为 Gemini content。
func geminiContents(messages []Message) []*genai.Content {
	out := make([]*genai.Content, 0, len(messages))
	for _, msg := range messages {
		var role genai.Role = genai.RoleUser
		if msg.Role == RoleAssistant {
			// Gemini SDK 用 model 表示 assistant 历史消息。
			role = genai.RoleModel
		}
		out = append(out, geminiContent(msg, role))
	}
	return out
}

func geminiContent(msg Message, role genai.Role) *genai.Content {
	if len(msg.Parts) == 0 {
		return genai.NewContentFromText(messageTextContent(msg), role)
	}

	parts := make([]*genai.Part, 0, len(msg.Parts)+1)
	hasText := false
	for _, part := range msg.Parts {
		switch part.Type {
		case ContentPartText:
			text := strings.TrimSpace(part.Text)
			if text == "" {
				continue
			}
			hasText = true
			parts = append(parts, genai.NewPartFromText(text))
		case ContentPartImageURL:
			input, ok := imageInputFromURL(part.ImageURL)
			if !ok {
				continue
			}
			if len(input.Data) > 0 {
				parts = append(parts, genai.NewPartFromBytes(input.Data, input.MediaType))
				continue
			}
			parts = append(parts, genai.NewPartFromURI(input.URL, input.MediaType))
		}
	}
	if !hasText {
		if text := strings.TrimSpace(msg.Content); text != "" {
			parts = append([]*genai.Part{genai.NewPartFromText(text)}, parts...)
		}
	}
	if len(parts) == 0 {
		return genai.NewContentFromText(messageTextContent(msg), role)
	}
	return genai.NewContentFromParts(parts, role)
}
