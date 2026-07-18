package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
)

type openAICompatibleClient struct {
	cfg             ProviderConfig
	client          openai.Client
	httpClient      *http.Client
	imageHTTPClient *http.Client
}

// newOpenAICompatibleClient 创建 OpenAI-compatible provider 客户端。
func newOpenAICompatibleClient(cfg ProviderConfig, httpClient *http.Client) *openAICompatibleClient {
	textHTTPClient := newTextHTTPClient(httpClient, cfg)
	opts := []option.RequestOption{
		option.WithAPIKey(cfg.APIKey),
		option.WithHTTPClient(textHTTPClient),
		// Retry policy is owned by the bot/profile layer so one gateway outage does
		// not multiply the SDK's hidden retries with application-level retries.
		option.WithMaxRetries(0),
	}
	for name, value := range cfg.NormalizedHeaders() {
		opts = append(opts, option.WithHeader(name, value))
	}
	if userAgent := cfg.UserAgentWithDefault(); userAgent != "" {
		opts = append(opts, option.WithHeader("User-Agent", userAgent))
	}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	if cfg.Timeout > 0 {
		opts = append(opts, option.WithRequestTimeout(cfg.Timeout))
	}

	return &openAICompatibleClient{
		cfg:             cfg,
		client:          openai.NewClient(opts...),
		httpClient:      textHTTPClient,
		imageHTTPClient: newImageHTTPClient(httpClient, cfg),
	}
}

// Generate 调用 OpenAI-compatible 模型生成回复。
func (c *openAICompatibleClient) Generate(ctx context.Context, req GenerateRequest) (*GenerateResponse, error) {
	req = req.withDefaults(c.cfg)
	req = applyContextBudget(req, c.cfg)
	if err := validateGenerateRequest(req); err != nil {
		return nil, err
	}
	switch c.cfg.APIFormatWithDefault() {
	case APIFormatChatCompletions:
		return c.generateChatCompletion(ctx, req)
	default:
		return c.generateResponse(ctx, req)
	}
}

// ManagesAttemptTimeout reports that Chat Completions uses separate response
// header and stream-idle timeouts instead of one deadline for the whole reply.
func (c *openAICompatibleClient) ManagesAttemptTimeout() bool {
	return c.cfg.APIFormatWithDefault() == APIFormatChatCompletions
}

// GenerateImage 调用 OpenAI-compatible 图片生成接口。
func (c *openAICompatibleClient) GenerateImage(ctx context.Context, req ImageGenerateRequest) (*ImageGenerateResponse, error) {
	req = imageRequestWithDefaults(req, c.cfg)
	if strings.TrimSpace(req.Prompt) == "" {
		return nil, errors.New("llm: image prompt is required")
	}
	body, err := json.Marshal(map[string]any{
		"model":  req.Model,
		"prompt": req.Prompt,
		"size":   req.Size,
		"n":      req.N,
	})
	if err != nil {
		return nil, err
	}
	httpReq, err := c.newImageRequest(ctx, "images/generations", body)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	resp, err := c.imageHTTPClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, openAICompatibleError(fmt.Errorf("openai-compatible images failed"), &openAIErrorCapture{statusCode: resp.StatusCode, body: string(errBody)})
	}
	images, err := decodeOpenAIImagesResponse(resp.Body)
	if err != nil {
		return nil, err
	}
	return &ImageGenerateResponse{Provider: ProviderOpenAICompatible, Model: req.Model, Images: images}, nil
}

// EditImage 调用 OpenAI-compatible 图片编辑接口。
func (c *openAICompatibleClient) EditImage(ctx context.Context, req ImageEditRequest) (*ImageGenerateResponse, error) {
	req = imageEditRequestWithDefaults(req, c.cfg)
	if strings.TrimSpace(req.Prompt) == "" {
		return nil, errors.New("llm: image edit prompt is required")
	}
	if len(req.Images) == 0 {
		return nil, errors.New("llm: image edit requires at least one source image")
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("model", req.Model); err != nil {
		return nil, err
	}
	if err := writer.WriteField("prompt", req.Prompt); err != nil {
		return nil, err
	}
	if err := writer.WriteField("size", req.Size); err != nil {
		return nil, err
	}
	if err := writer.WriteField("n", fmt.Sprintf("%d", req.N)); err != nil {
		return nil, err
	}
	for index, imageURL := range req.Images {
		input, err := c.imageEditInput(ctx, imageURL, index)
		if err != nil {
			return nil, err
		}
		part, err := writer.CreatePart(imageEditPartHeader(input.filename, input.mediaType))
		if err != nil {
			return nil, err
		}
		if _, err := part.Write(input.data); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}

	httpReq, err := c.newImageRequest(ctx, "images/edits", body.Bytes())
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", writer.FormDataContentType())
	httpReq.Header.Set("Accept", "application/json")
	resp, err := c.imageHTTPClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, openAICompatibleError(fmt.Errorf("openai-compatible images edit failed"), &openAIErrorCapture{statusCode: resp.StatusCode, body: string(errBody)})
	}
	images, err := decodeOpenAIImagesResponse(resp.Body)
	if err != nil {
		return nil, err
	}
	return &ImageGenerateResponse{Provider: ProviderOpenAICompatible, Model: req.Model, Images: images}, nil
}

func imageRequestWithDefaults(req ImageGenerateRequest, cfg ProviderConfig) ImageGenerateRequest {
	if strings.TrimSpace(req.Model) == "" {
		req.Model = cfg.ImageModelWithDefault()
	}
	if strings.TrimSpace(req.Size) == "" {
		req.Size = "1024x1024"
	}
	if req.N <= 0 {
		req.N = 1
	}
	return req
}

func imageEditRequestWithDefaults(req ImageEditRequest, cfg ProviderConfig) ImageEditRequest {
	if strings.TrimSpace(req.Model) == "" {
		req.Model = cfg.ImageModelWithDefault()
	}
	if strings.TrimSpace(req.Size) == "" {
		req.Size = "1024x1024"
	}
	if req.N <= 0 {
		req.N = 1
	}
	return req
}

func decodeOpenAIImagesResponse(reader io.Reader) ([]string, error) {
	var payload struct {
		Data []struct {
			URL     string `json:"url,omitempty"`
			B64JSON string `json:"b64_json,omitempty"`
		} `json:"data"`
	}
	if err := json.NewDecoder(reader).Decode(&payload); err != nil {
		return nil, err
	}
	images := make([]string, 0, len(payload.Data))
	for _, item := range payload.Data {
		if value := strings.TrimSpace(item.URL); value != "" {
			images = append(images, value)
			continue
		}
		if value := strings.TrimSpace(item.B64JSON); value != "" {
			images = append(images, "data:image/png;base64,"+value)
		}
	}
	if len(images) == 0 {
		return nil, errors.New("llm: image output is empty")
	}
	return images, nil
}

type imageEditInputData struct {
	data      []byte
	mediaType string
	filename  string
}

const maxImageEditInputBytes = 20 << 20

func (c *openAICompatibleClient) imageEditInput(ctx context.Context, value string, index int) (imageEditInputData, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return imageEditInputData{}, errors.New("llm: image edit input is empty")
	}
	if input, ok := dataImageInput(value); ok {
		return imageEditInputData{
			data:      input.Data,
			mediaType: input.MediaType,
			filename:  imageEditFilename("image", input.MediaType, index),
		}, nil
	}
	if strings.HasPrefix(value, "file://") {
		value = strings.TrimPrefix(value, "file://")
	}
	if filepath.IsAbs(value) {
		return localImageEditInput(value, index)
	}
	if parsed, err := url.Parse(value); err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" {
		return c.remoteImageEditInput(ctx, value, parsed, index)
	}
	return imageEditInputData{}, fmt.Errorf("llm: unsupported image edit input %q", value)
}

func localImageEditInput(path string, index int) (imageEditInputData, error) {
	file, err := os.Open(path)
	if err != nil {
		return imageEditInputData{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxImageEditInputBytes+1))
	if err != nil {
		return imageEditInputData{}, err
	}
	if len(data) == 0 {
		return imageEditInputData{}, errors.New("llm: local image is empty")
	}
	if len(data) > maxImageEditInputBytes {
		return imageEditInputData{}, errors.New("llm: local image is too large")
	}
	mediaType := imageContentTypeForEdit(imageMediaTypeFromPath(path), data)
	return imageEditInputData{
		data:      data,
		mediaType: mediaType,
		filename:  imageEditFilename(filepath.Base(path), mediaType, index),
	}, nil
}

func (c *openAICompatibleClient) remoteImageEditInput(ctx context.Context, value string, parsed *url.URL, index int) (imageEditInputData, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, value, nil)
	if err != nil {
		return imageEditInputData{}, err
	}
	req.Header.Set("Accept", "image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return imageEditInputData{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return imageEditInputData{}, fmt.Errorf("llm: image download failed: status=%d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageEditInputBytes+1))
	if err != nil {
		return imageEditInputData{}, err
	}
	if len(data) == 0 {
		return imageEditInputData{}, errors.New("llm: downloaded image is empty")
	}
	if len(data) > maxImageEditInputBytes {
		return imageEditInputData{}, errors.New("llm: downloaded image is too large")
	}
	mediaType := imageContentTypeForEdit(resp.Header.Get("Content-Type"), data)
	return imageEditInputData{
		data:      data,
		mediaType: mediaType,
		filename:  imageEditFilename(filepath.Base(parsed.Path), mediaType, index),
	}, nil
}

func imageEditPartHeader(filename, mediaType string) textproto.MIMEHeader {
	header := make(textproto.MIMEHeader)
	filename = strings.ReplaceAll(filename, `"`, "")
	if strings.TrimSpace(filename) == "" {
		filename = "image.png"
	}
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="image"; filename="%s"`, filename))
	header.Set("Content-Type", mediaType)
	return header
}

func imageEditFilename(base string, mediaType string, index int) string {
	base = strings.TrimSpace(base)
	if base == "" || base == "." || base == "/" {
		base = fmt.Sprintf("image-%d", index+1)
	}
	if filepath.Ext(base) != "" {
		return base
	}
	switch strings.ToLower(mediaType) {
	case "image/jpeg":
		return base + ".jpg"
	case "image/webp":
		return base + ".webp"
	case "image/gif":
		return base + ".gif"
	default:
		return base + ".png"
	}
}

func imageContentTypeForEdit(header string, body []byte) string {
	if mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(header)); err == nil && strings.HasPrefix(mediaType, "image/") {
		return mediaType
	}
	if len(body) > 0 {
		detected := http.DetectContentType(body)
		if strings.HasPrefix(detected, "image/") {
			return detected
		}
	}
	return "image/png"
}

// generateResponse 使用 Responses API 生成回复。
func (c *openAICompatibleClient) generateResponse(ctx context.Context, req GenerateRequest) (*GenerateResponse, error) {
	system, messages := splitSystemPrompt(req.Messages)
	// Responses API 把 system prompt 放到 Instructions，普通对话放 InputItemList。
	params := responses.ResponseNewParams{
		Model: shared.ResponsesModel(req.Model),
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: openAIResponsesInput(messages),
		},
	}
	if system != "" {
		params.Instructions = param.NewOpt(system)
	}
	if req.Temperature != nil {
		params.Temperature = param.NewOpt(*req.Temperature)
	}
	if req.ReasoningEffort != "" {
		params.Reasoning = shared.ReasoningParam{
			Effort: shared.ReasoningEffort(req.ReasoningEffort),
		}
	}
	if req.MaxOutputTokens > 0 {
		params.MaxOutputTokens = param.NewOpt(req.MaxOutputTokens)
	}

	resp, err, capture := c.newResponse(ctx, params)
	if err != nil {
		return nil, openAICompatibleError(err, capture)
	}
	text := strings.TrimSpace(resp.OutputText())
	if text == "" {
		return nil, fmt.Errorf("llm: openai-compatible responses output is empty")
	}

	return &GenerateResponse{
		Provider: ProviderOpenAICompatible,
		Model:    string(resp.Model),
		Text:     text,
		Usage: Usage{
			InputTokens:  resp.Usage.InputTokens,
			OutputTokens: resp.Usage.OutputTokens,
			TotalTokens:  resp.Usage.TotalTokens,
		},
	}, nil
}

type openAIChatCompletionRequest struct {
	Model           string                        `json:"model"`
	Messages        []openAIChatCompletionMessage `json:"messages"`
	Temperature     *float64                      `json:"temperature,omitempty"`
	ReasoningEffort string                        `json:"reasoning_effort,omitempty"`
	MaxTokens       int64                         `json:"max_tokens,omitempty"`
	Stream          bool                          `json:"stream"`
}

type openAIChatCompletionMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type openAIChatCompletionContentPart struct {
	Type     string                               `json:"type"`
	Text     string                               `json:"text,omitempty"`
	ImageURL *openAIChatCompletionImageURLContent `json:"image_url,omitempty"`
}

type openAIChatCompletionImageURLContent struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

// generateChatCompletion 使用 Chat Completions API 生成回复。
func (c *openAICompatibleClient) generateChatCompletion(ctx context.Context, req GenerateRequest) (*GenerateResponse, error) {
	params := openAIChatCompletionRequest{
		Model:           req.Model,
		Messages:        openAIChatCompletionMessages(req.Messages),
		Temperature:     req.Temperature,
		ReasoningEffort: req.ReasoningEffort,
		MaxTokens:       req.MaxOutputTokens,
		Stream:          true,
	}
	body, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	httpReq, err := c.newOpenAIRequest(ctx, "chat/completions", body)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")

	resp, cancelRequest, err := c.doChatCompletionRequest(ctx, httpReq)
	if err != nil {
		return nil, err
	}
	defer cancelRequest()
	defer resp.Body.Close()
	capture := &openAIErrorCapture{}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		var errBody []byte
		if err := readOpenAIResponseBodyWithIdleTimeout(ctx, resp.Body, c.cfg.Timeout, "response body", func(reader io.Reader) error {
			var readErr error
			errBody, readErr = io.ReadAll(io.LimitReader(reader, 1<<20))
			return readErr
		}); err != nil {
			return nil, err
		}
		capture.statusCode = resp.StatusCode
		capture.body = string(errBody)
		return nil, openAICompatibleError(fmt.Errorf("openai-compatible chat completions failed"), capture)
	}
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		result, err := decodeOpenAITextEventStreamWithIdleTimeout(ctx, resp.Body, c.cfg.Timeout)
		if err != nil {
			return nil, err
		}
		if result.Text == "" {
			return nil, openAIChatCompletionTextError(result.Diagnostics)
		}
		return &GenerateResponse{Provider: ProviderOpenAICompatible, Model: req.Model, Text: result.Text, Usage: result.Usage}, nil
	}

	var payload map[string]any
	if err := readOpenAIResponseBodyWithIdleTimeout(ctx, resp.Body, c.cfg.Timeout, "response body", func(reader io.Reader) error {
		return json.NewDecoder(reader).Decode(&payload)
	}); err != nil {
		return nil, err
	}
	if message := streamErrorMessage(payload); message != "" {
		return nil, errors.New(message)
	}
	result := chatCompletionResultFromPayload(payload)
	text := strings.TrimSpace(result.Text())
	if text == "" {
		return nil, openAIChatCompletionTextError(openAIChatCompletionDiagnostics{
			FinishReasons: result.FinishReasons,
			ReasoningSeen: result.ReasoningSeen,
			RefusalSeen:   result.RefusalSeen,
			ToolCallsSeen: result.ToolCallsSeen,
			FunctionSeen:  result.FunctionSeen,
			Usage:         usageFromPayload(payload["usage"]),
		})
	}
	return &GenerateResponse{
		Provider: ProviderOpenAICompatible,
		Model:    firstNonEmptyString(stringField(payload, "model"), req.Model),
		Text:     text,
		Usage:    usageFromPayload(payload["usage"]),
	}, nil
}

// doChatCompletionRequest removes http.Client's whole-request timeout and
// applies the configured timeout only while waiting for response headers.
func (c *openAICompatibleClient) doChatCompletionRequest(ctx context.Context, req *http.Request) (*http.Response, context.CancelFunc, error) {
	client := *c.httpClient
	client.Timeout = 0
	timeout := c.cfg.Timeout
	if timeout <= 0 {
		resp, err := client.Do(req)
		return resp, func() {}, err
	}

	requestCtx, cancel := context.WithCancel(ctx)
	timerDone := make(chan struct{})
	timer := time.AfterFunc(timeout, func() {
		cancel()
		close(timerDone)
	})
	resp, err := client.Do(req.Clone(requestCtx))
	if !timer.Stop() {
		<-timerDone
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, func() {}, ctxErr
		}
		return nil, func() {}, fmt.Errorf("llm: response header timeout after %s: %w", timeout, context.DeadlineExceeded)
	}
	if err != nil {
		cancel()
		return nil, func() {}, err
	}
	return resp, cancel, nil
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func openAIChatCompletionMessages(messages []Message) []openAIChatCompletionMessage {
	out := make([]openAIChatCompletionMessage, 0, len(messages))
	for _, message := range messages {
		out = append(out, openAIChatCompletionMessage{
			Role:    openAIChatCompletionRole(message.Role),
			Content: openAIChatCompletionContent(message),
		})
	}
	return out
}

func openAIChatCompletionContent(message Message) any {
	if len(message.Parts) == 0 {
		return message.Content
	}
	parts := make([]openAIChatCompletionContentPart, 0, len(message.Parts)+1)
	hasText := false
	for _, part := range message.Parts {
		switch part.Type {
		case ContentPartText:
			if text := strings.TrimSpace(part.Text); text != "" {
				hasText = true
				parts = append(parts, openAIChatCompletionContentPart{Type: "text", Text: text})
			}
		case ContentPartImageURL:
			if imageURL := strings.TrimSpace(part.ImageURL); imageURL != "" {
				detail := strings.ToLower(strings.TrimSpace(part.Detail))
				if detail != "low" && detail != "high" {
					detail = "auto"
				}
				parts = append(parts, openAIChatCompletionContentPart{
					Type:     "image_url",
					ImageURL: &openAIChatCompletionImageURLContent{URL: imageURL, Detail: detail},
				})
			}
		}
	}
	if !hasText {
		if text := strings.TrimSpace(message.Content); text != "" {
			parts = append([]openAIChatCompletionContentPart{{Type: "text", Text: text}}, parts...)
		}
	}
	if len(parts) == 0 {
		return message.Content
	}
	return parts
}

func openAIChatCompletionRole(role Role) string {
	switch role {
	case RoleSystem:
		return "system"
	case RoleAssistant:
		return "assistant"
	default:
		return "user"
	}
}

// newResponse 执行 Responses API 请求并返回错误捕获器。
func (c *openAICompatibleClient) newResponse(ctx context.Context, params responses.ResponseNewParams) (*responses.Response, error, *openAIErrorCapture) {
	capture := &openAIErrorCapture{}
	body, err := json.Marshal(params)
	if err != nil {
		return nil, err, capture
	}
	req, err := c.newOpenAIRequest(ctx, "responses", body)
	if err != nil {
		return nil, err, capture
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err, capture
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		capture.statusCode = resp.StatusCode
		capture.body = string(errBody)
		return nil, fmt.Errorf("openai-compatible responses failed"), capture
	}
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(contentType, "text/event-stream") {
		return decodeOpenAIResponseSSE(resp.Body, params.Model)
	}
	var out responses.Response
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err, capture
	}
	return &out, nil, capture
}

func (c *openAICompatibleClient) newOpenAIRequest(ctx context.Context, endpoint string, body []byte) (*http.Request, error) {
	return c.newOpenAIRequestWithBaseURL(ctx, c.cfg.BaseURL, endpoint, body)
}

func (c *openAICompatibleClient) newImageRequest(ctx context.Context, endpoint string, body []byte) (*http.Request, error) {
	return c.newOpenAIRequestWithBaseURL(ctx, c.cfg.ImageBaseURLWithDefault(), endpoint, body)
}

func (c *openAICompatibleClient) newOpenAIRequestWithBaseURL(ctx context.Context, configuredBaseURL string, endpoint string, body []byte) (*http.Request, error) {
	baseURL := strings.TrimSpace(configuredBaseURL)
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	requestURL, err := joinOpenAICompatibleURL(baseURL, endpoint)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	for name, value := range normalizeHeaders(c.cfg.NormalizedHeaders()) {
		req.Header.Set(name, value)
	}
	if userAgent := c.cfg.UserAgentWithDefault(); strings.TrimSpace(userAgent) != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	return req, nil
}

func newTextHTTPClient(base *http.Client, cfg ProviderConfig) *http.Client {
	if base == nil {
		base = http.DefaultClient
	}
	client := *base
	if cfg.Timeout > 0 {
		client.Timeout = cfg.Timeout
	}
	return &client
}

func newImageHTTPClient(base *http.Client, cfg ProviderConfig) *http.Client {
	if base == nil {
		base = http.DefaultClient
	}
	client := *base
	if timeout := cfg.ImageTimeoutWithDefault(); timeout > 0 {
		client.Timeout = timeout
	}
	origin := strings.TrimSpace(cfg.ImageOrigin)
	if origin == "" {
		return &client
	}

	transport := cloneHTTPTransport(base.Transport)
	transport.Proxy = nil
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	transport.DialContext = func(ctx context.Context, network string, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, origin)
	}
	client.Transport = transport
	return &client
}

func cloneHTTPTransport(roundTripper http.RoundTripper) *http.Transport {
	if roundTripper == nil {
		return http.DefaultTransport.(*http.Transport).Clone()
	}
	if transport, ok := roundTripper.(*http.Transport); ok {
		return transport.Clone()
	}
	return http.DefaultTransport.(*http.Transport).Clone()
}

func decodeOpenAIResponseSSE(reader io.Reader, model shared.ResponsesModel) (*responses.Response, error, *openAIErrorCapture) {
	text, usage, err := decodeOpenAITextEventStream(reader)
	if err != nil {
		return nil, err, &openAIErrorCapture{}
	}
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("llm: openai-compatible event stream output is empty"), &openAIErrorCapture{}
	}
	return &responses.Response{
		Model: model,
		Output: []responses.ResponseOutputItemUnion{{
			Type: "message",
			Role: "assistant",
			Content: []responses.ResponseOutputMessageContentUnion{{
				Type: "output_text",
				Text: text,
			}},
		}},
		Usage: responses.ResponseUsage{
			InputTokens:  usage.InputTokens,
			OutputTokens: usage.OutputTokens,
			TotalTokens:  usage.TotalTokens,
		},
	}, nil, &openAIErrorCapture{}
}

type openAIChatCompletionDiagnostics struct {
	FinishReasons []string
	ReasoningSeen bool
	RefusalSeen   bool
	ToolCallsSeen bool
	FunctionSeen  bool
	Usage         Usage
}

type openAITextEventStreamResult struct {
	Text        string
	Usage       Usage
	Diagnostics openAIChatCompletionDiagnostics
}

type openAIStreamEventResult struct {
	DeltaText          string
	DeltaTextKey       string
	FinalText          string
	FinalTextKey       string
	FinalTextAggregate bool
	Usage              Usage
	FinishReasons      []string
	ReasoningSeen      bool
	RefusalSeen        bool
	ToolCallsSeen      bool
	FunctionSeen       bool
}

type openAIStreamTextPart struct {
	key  string
	text string
}

type openAIStreamTextAccumulator struct {
	parts []openAIStreamTextPart
}

func (a *openAIStreamTextAccumulator) append(key string, text string) {
	if text == "" {
		return
	}
	part := a.part(key)
	part.text += text
}

func (a *openAIStreamTextAccumulator) applySnapshot(key string, text string) {
	if text == "" {
		return
	}
	part := a.part(key)
	part.text = reconcileOpenAITextSnapshot(part.text, text)
}

func (a *openAIStreamTextAccumulator) applyGlobalSnapshot(text string) {
	if text == "" {
		return
	}
	merged := reconcileOpenAITextSnapshot(a.text(), text)
	a.parts = []openAIStreamTextPart{{key: "default", text: merged}}
}

func (a *openAIStreamTextAccumulator) part(key string) *openAIStreamTextPart {
	if key == "" {
		key = "default"
	}
	for index := range a.parts {
		if a.parts[index].key == key {
			return &a.parts[index]
		}
	}
	a.parts = append(a.parts, openAIStreamTextPart{key: key})
	return &a.parts[len(a.parts)-1]
}

func (a *openAIStreamTextAccumulator) text() string {
	var builder strings.Builder
	for _, part := range a.parts {
		builder.WriteString(part.text)
	}
	return builder.String()
}

func decodeOpenAITextEventStream(reader io.Reader) (string, Usage, error) {
	result, err := decodeOpenAITextEventStreamResult(reader)
	return result.Text, result.Usage, err
}

func decodeOpenAITextEventStreamResult(reader io.Reader) (openAITextEventStreamResult, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var textAccumulator openAIStreamTextAccumulator
	var usage Usage
	var finishReasons []string
	var reasoningSeen bool
	var refusalSeen bool
	var toolCallsSeen bool
	var functionSeen bool
	var eventName string
	var dataLines []string
	result := func() openAITextEventStreamResult {
		text := strings.TrimSpace(textAccumulator.text())
		return openAITextEventStreamResult{
			Text:  text,
			Usage: usage,
			Diagnostics: openAIChatCompletionDiagnostics{
				FinishReasons: finishReasons,
				ReasoningSeen: reasoningSeen,
				RefusalSeen:   refusalSeen,
				ToolCallsSeen: toolCallsSeen,
				FunctionSeen:  functionSeen,
				Usage:         usage,
			},
		}
	}
	flush := func() (bool, error) {
		if len(dataLines) == 0 {
			eventName = ""
			return false, nil
		}
		data := strings.TrimSpace(strings.Join(dataLines, "\n"))
		eventName = strings.TrimSpace(eventName)
		dataLines = nil
		if data == "" {
			return false, nil
		}
		if data == "[DONE]" {
			return true, nil
		}
		eventResult, err := textFromOpenAIStreamEvent(eventName, []byte(data))
		if err != nil {
			return false, err
		}
		textAccumulator.append(eventResult.DeltaTextKey, eventResult.DeltaText)
		if strings.TrimSpace(eventResult.FinalText) != "" {
			switch {
			case !eventResult.FinalTextAggregate:
				textAccumulator.append(eventResult.FinalTextKey, eventResult.FinalText)
			case eventResult.FinalTextKey == "":
				textAccumulator.applyGlobalSnapshot(eventResult.FinalText)
			default:
				textAccumulator.applySnapshot(eventResult.FinalTextKey, eventResult.FinalText)
			}
		}
		if hasOpenAIUsage(eventResult.Usage) {
			usage = eventResult.Usage
		}
		finishReasons = appendOpenAIFinishReasons(finishReasons, eventResult.FinishReasons...)
		reasoningSeen = reasoningSeen || eventResult.ReasoningSeen
		refusalSeen = refusalSeen || eventResult.RefusalSeen
		toolCallsSeen = toolCallsSeen || eventResult.ToolCallsSeen
		functionSeen = functionSeen || eventResult.FunctionSeen
		eventName = ""
		return false, nil
	}
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			done, err := flush()
			if err != nil {
				return openAITextEventStreamResult{}, err
			}
			if done {
				return result(), nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "event:"); ok {
			eventName = strings.TrimSpace(rest)
			continue
		}
		if rest, ok := strings.CutPrefix(line, "data:"); ok {
			dataLines = append(dataLines, strings.TrimSpace(rest))
		}
	}
	if err := scanner.Err(); err != nil {
		return openAITextEventStreamResult{}, err
	}
	_, err := flush()
	if err != nil {
		return openAITextEventStreamResult{}, err
	}
	return result(), nil
}

func resolveOpenAIStreamText(deltaText string, finalText string) string {
	return strings.TrimSpace(reconcileOpenAITextSnapshot(deltaText, finalText))
}

func reconcileOpenAITextSnapshot(current string, snapshot string) string {
	switch {
	case snapshot == "":
		return current
	case current == "":
		return snapshot
	case strings.HasPrefix(snapshot, current):
		return snapshot
	case strings.HasPrefix(current, snapshot):
		return current
	default:
		return current + snapshot
	}
}

type openAIActivityReader struct {
	reader   io.Reader
	activity chan<- struct{}
}

func (r openAIActivityReader) Read(buffer []byte) (int, error) {
	n, err := r.reader.Read(buffer)
	if n > 0 {
		select {
		case r.activity <- struct{}{}:
		default:
		}
	}
	return n, err
}

func readOpenAIResponseBodyWithIdleTimeout(
	ctx context.Context,
	body io.ReadCloser,
	timeout time.Duration,
	timeoutKind string,
	read func(io.Reader) error,
) error {
	if timeout <= 0 {
		return read(body)
	}

	activity := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- read(openAIActivityReader{reader: body, activity: activity})
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case err := <-done:
			return err
		case <-activity:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(timeout)
		case <-timer.C:
			_ = body.Close()
			return fmt.Errorf("llm: %s idle timeout after %s: %w", timeoutKind, timeout, context.DeadlineExceeded)
		case <-ctx.Done():
			_ = body.Close()
			return ctx.Err()
		}
	}
}

func decodeOpenAITextEventStreamWithIdleTimeout(ctx context.Context, body io.ReadCloser, timeout time.Duration) (openAITextEventStreamResult, error) {
	var result openAITextEventStreamResult
	err := readOpenAIResponseBodyWithIdleTimeout(ctx, body, timeout, "stream", func(reader io.Reader) error {
		var decodeErr error
		result, decodeErr = decodeOpenAITextEventStreamResult(reader)
		return decodeErr
	})
	return result, err
}

func textFromOpenAIStreamEvent(eventName string, data []byte) (openAIStreamEventResult, error) {
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return openAIStreamEventResult{}, fmt.Errorf("llm: decode openai-compatible event stream: %w", err)
	}
	if errMessage := streamErrorMessage(root); errMessage != "" {
		return openAIStreamEventResult{}, errors.New(errMessage)
	}
	eventType := stringField(root, "type")
	if eventName == "" {
		eventName = eventType
	}
	eventName = strings.ToLower(strings.TrimSpace(eventName))
	usage := usageFromPayload(root["usage"])
	switch eventName {
	case "response.output_text.delta":
		return openAIStreamEventResult{DeltaText: openAIContentText(root["delta"]), DeltaTextKey: openAIResponseTextKey(root), Usage: usage}, nil
	case "response.output_text.done":
		text := firstOpenAIContentText(root, "text", "output_text")
		if key := openAIResponseTextKey(root); key != "" {
			return openAIStreamEventResult{FinalText: text, FinalTextKey: key, FinalTextAggregate: true, Usage: usage}, nil
		}
		return openAIStreamEventResult{DeltaText: text, Usage: usage}, nil
	case "response.completed":
		return openAIStreamEventResult{FinalText: textFromCompletedResponse(root), FinalTextAggregate: true, Usage: usageFromCompletedResponse(root)}, nil
	case "response.failed", "error":
		if msg := streamErrorMessage(root); msg != "" {
			return openAIStreamEventResult{}, errors.New(msg)
		}
	}
	if strings.HasPrefix(eventName, "response.reasoning") {
		return openAIStreamEventResult{ReasoningSeen: true, Usage: usage}, nil
	}
	if strings.HasPrefix(eventName, "response.refusal") {
		return openAIStreamEventResult{RefusalSeen: true, Usage: usage}, nil
	}
	if strings.HasPrefix(eventName, "response.") {
		return openAIStreamEventResult{Usage: usage}, nil
	}
	completion := chatCompletionResultFromPayload(root)
	if completion.HasProtocolData() {
		deltaText := completion.DeltaText
		finalText := completion.FinalText
		finalAggregate := openAIStreamEventIsFinal(eventName, root)
		if !finalAggregate {
			deltaText += finalText
			finalText = ""
		}
		return openAIStreamEventResult{
			DeltaText:          deltaText,
			FinalText:          finalText,
			FinalTextAggregate: finalAggregate,
			Usage:              usage,
			FinishReasons:      completion.FinishReasons,
			ReasoningSeen:      completion.ReasoningSeen,
			RefusalSeen:        completion.RefusalSeen,
			ToolCallsSeen:      completion.ToolCallsSeen,
			FunctionSeen:       completion.FunctionSeen,
		}, nil
	}
	return openAIStreamEventResult{DeltaText: firstOpenAIContentText(root, "delta", "text", "output_text"), Usage: usage}, nil
}

func openAIStreamEventIsFinal(eventName string, root map[string]any) bool {
	if done, ok := root["done"].(bool); ok && done {
		return true
	}
	switch eventName {
	case "done", "final", "message.done", "completion.done":
		return true
	default:
		return false
	}
}

func openAIResponseTextKey(root map[string]any) string {
	outputIndex, outputOK := openAIStreamIndex(root["output_index"])
	contentIndex, contentOK := openAIStreamIndex(root["content_index"])
	if !outputOK && !contentOK {
		return ""
	}
	return "response:" + outputIndex + ":" + contentIndex
}

func openAIStreamIndex(value any) (string, bool) {
	switch value := value.(type) {
	case float64:
		return fmt.Sprintf("%.0f", value), true
	case string:
		if value = strings.TrimSpace(value); value != "" {
			return value, true
		}
	}
	return "-", false
}

func streamErrorMessage(root map[string]any) string {
	if message := stringField(root, "message"); message != "" {
		return message
	}
	if errPayload, ok := root["error"].(map[string]any); ok {
		if message := stringField(errPayload, "message"); message != "" {
			return message
		}
	}
	if response, ok := root["response"].(map[string]any); ok {
		if errPayload, ok := response["error"].(map[string]any); ok {
			if message := stringField(errPayload, "message"); message != "" {
				return message
			}
		}
	}
	return ""
}

func usageFromCompletedResponse(root map[string]any) Usage {
	if response, ok := root["response"].(map[string]any); ok {
		if usage := usageFromPayload(response["usage"]); usage.TotalTokens > 0 || usage.InputTokens > 0 || usage.OutputTokens > 0 {
			return usage
		}
	}
	return usageFromPayload(root["usage"])
}

func textFromCompletedResponse(root map[string]any) string {
	response, ok := root["response"].(map[string]any)
	if !ok {
		return ""
	}
	if text := firstOpenAIContentText(response, "output_text"); text != "" {
		return text
	}
	output, ok := response["output"].([]any)
	if !ok {
		return ""
	}
	var builder strings.Builder
	for _, item := range output {
		message, ok := item.(map[string]any)
		if !ok || strings.ToLower(strings.TrimSpace(stringField(message, "type"))) != "message" {
			continue
		}
		builder.WriteString(openAIContentText(message["content"]))
	}
	return builder.String()
}

func usageFromPayload(payload any) Usage {
	values, ok := payload.(map[string]any)
	if !ok {
		return Usage{}
	}
	return Usage{
		InputTokens:  int64Field(values, "input_tokens", "prompt_tokens"),
		OutputTokens: int64Field(values, "output_tokens", "completion_tokens"),
		TotalTokens:  int64Field(values, "total_tokens"),
	}
}

type openAIChatCompletionResult struct {
	DeltaText     string
	FinalText     string
	FinishReasons []string
	ReasoningSeen bool
	RefusalSeen   bool
	ToolCallsSeen bool
	FunctionSeen  bool
	ProtocolSeen  bool
}

func (r openAIChatCompletionResult) Text() string {
	return resolveOpenAIStreamText(r.DeltaText, r.FinalText)
}

func (r openAIChatCompletionResult) HasProtocolData() bool {
	return r.ProtocolSeen || r.DeltaText != "" || r.FinalText != "" || r.ReasoningSeen || r.RefusalSeen || r.ToolCallsSeen || r.FunctionSeen || len(r.FinishReasons) > 0
}

func chatCompletionResultFromPayload(root map[string]any) openAIChatCompletionResult {
	var result openAIChatCompletionResult
	if choices, ok := root["choices"].([]any); ok {
		result.ProtocolSeen = true
		for _, item := range choices {
			choice, ok := item.(map[string]any)
			if !ok {
				continue
			}
			result.FinishReasons = appendOpenAIFinishReasons(result.FinishReasons, stringField(choice, "finish_reason"))
			if delta, ok := choice["delta"].(map[string]any); ok {
				result.DeltaText += openAIContentText(delta["content"])
				result.observeNonTextPayload(delta)
			}
			if message, ok := choice["message"].(map[string]any); ok {
				result.FinalText += openAIContentText(message["content"])
				result.observeNonTextPayload(message)
			}
			if text := openAIContentText(choice["text"]); text != "" {
				result.DeltaText += text
			}
			result.observeNonTextPayload(choice)
		}
	}
	if delta, ok := root["delta"].(map[string]any); ok {
		result.ProtocolSeen = true
		result.DeltaText += openAIContentText(delta["content"])
		result.observeNonTextPayload(delta)
	}
	if message, ok := root["message"].(map[string]any); ok {
		result.ProtocolSeen = true
		result.FinalText += openAIContentText(message["content"])
		result.observeNonTextPayload(message)
	}
	result.FinishReasons = appendOpenAIFinishReasons(result.FinishReasons, stringField(root, "finish_reason"))
	result.observeNonTextPayload(root)
	return result
}

func (r *openAIChatCompletionResult) observeNonTextPayload(values map[string]any) {
	r.ReasoningSeen = r.ReasoningSeen || openAIReasoningSeen(values)
	r.RefusalSeen = r.RefusalSeen || openAIRefusalSeen(values)
	r.ToolCallsSeen = r.ToolCallsSeen || openAIToolCallsSeen(values)
	r.FunctionSeen = r.FunctionSeen || openAIFunctionCallSeen(values)
}

func openAIContentText(value any) string {
	switch value := value.(type) {
	case string:
		return value
	case []any:
		var builder strings.Builder
		for _, part := range value {
			switch part := part.(type) {
			case string:
				builder.WriteString(part)
			case map[string]any:
				builder.WriteString(openAIContentPartText(part))
			}
		}
		return builder.String()
	case map[string]any:
		return openAIContentPartText(value)
	default:
		return ""
	}
}

func openAIContentPartText(part map[string]any) string {
	switch strings.ToLower(strings.TrimSpace(stringField(part, "type"))) {
	case "", "text", "output_text":
	default:
		return ""
	}
	if text := openAITextValue(part["text"]); text != "" {
		return text
	}
	return openAITextValue(part["content"])
}

func openAITextValue(value any) string {
	switch value := value.(type) {
	case string:
		return value
	case map[string]any:
		return firstOpenAIContentText(value, "value", "text")
	case []any:
		return openAIContentText(value)
	default:
		return ""
	}
}

func firstOpenAIContentText(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if text := openAIContentText(values[key]); text != "" {
			return text
		}
	}
	return ""
}

func openAIReasoningSeen(values map[string]any) bool {
	for _, key := range []string{"reasoning_content", "reasoning", "thinking"} {
		if openAIPayloadHasValue(values[key]) {
			return true
		}
	}
	if parts, ok := values["content"].([]any); ok {
		for _, item := range parts {
			part, ok := item.(map[string]any)
			if !ok {
				continue
			}
			switch strings.ToLower(strings.TrimSpace(stringField(part, "type"))) {
			case "reasoning", "reasoning_content", "thinking":
				if openAIPayloadHasValue(part) {
					return true
				}
			}
		}
	}
	return false
}

func openAIRefusalSeen(values map[string]any) bool {
	if openAIPayloadHasValue(values["refusal"]) {
		return true
	}
	parts, ok := values["content"].([]any)
	if !ok {
		return false
	}
	for _, item := range parts {
		part, ok := item.(map[string]any)
		if !ok || strings.ToLower(strings.TrimSpace(stringField(part, "type"))) != "refusal" {
			continue
		}
		if openAIPayloadHasValue(part) {
			return true
		}
	}
	return false
}

func openAIToolCallsSeen(values map[string]any) bool {
	return openAIPayloadHasValue(values["tool_calls"]) || openAIContentPartTypeSeen(values, "tool_call", "tool_calls")
}

func openAIFunctionCallSeen(values map[string]any) bool {
	return openAIPayloadHasValue(values["function_call"]) || openAIContentPartTypeSeen(values, "function_call")
}

func openAIContentPartTypeSeen(values map[string]any, types ...string) bool {
	parts, ok := values["content"].([]any)
	if !ok {
		return false
	}
	for _, item := range parts {
		part, ok := item.(map[string]any)
		if !ok || !openAIPayloadHasValue(part) {
			continue
		}
		partType := strings.ToLower(strings.TrimSpace(stringField(part, "type")))
		for _, wanted := range types {
			if partType == wanted {
				return true
			}
		}
	}
	return false
}

func openAIPayloadHasValue(value any) bool {
	switch value := value.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(value) != ""
	case []any:
		for _, item := range value {
			if openAIPayloadHasValue(item) {
				return true
			}
		}
		return false
	case map[string]any:
		for _, item := range value {
			if openAIPayloadHasValue(item) {
				return true
			}
		}
		return false
	default:
		return true
	}
}

func hasOpenAIUsage(usage Usage) bool {
	return usage.InputTokens > 0 || usage.OutputTokens > 0 || usage.TotalTokens > 0
}

func appendOpenAIFinishReasons(existing []string, values ...string) []string {
	for _, value := range values {
		value = normalizedOpenAIFinishReason(value)
		if value == "" {
			continue
		}
		found := false
		for _, current := range existing {
			if current == value {
				found = true
				break
			}
		}
		if !found {
			existing = append(existing, value)
		}
	}
	return existing
}

func normalizedOpenAIFinishReason(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "stop", "length", "content_filter", "tool_calls", "function_call", "max_tokens", "error", "cancelled":
		return strings.ToLower(strings.TrimSpace(value))
	case "":
		return ""
	default:
		return "other"
	}
}

// ErrCompletionHasNoText marks a successful terminal model outcome that does
// not contain user-facing text, such as a refusal or tool call.
var ErrCompletionHasNoText = errors.New("llm: completion has no user-facing text")

// ErrCompletionEmpty marks an unexplained empty Chat Completions result that
// may be retried once and then failed over to another profile.
var ErrCompletionEmpty = errors.New("llm: chat completion output is unexpectedly empty")

// ErrCompletionTruncatedNoText marks an empty completion that should skip a
// repeat attempt on the same model but may be retried through profile failover.
var ErrCompletionTruncatedNoText = errors.New("llm: completion exhausted output before user-facing text")

type openAICompletionHasNoTextError struct {
	diagnostics openAIChatCompletionDiagnostics
}

func (e *openAICompletionHasNoTextError) Error() string {
	return "llm: openai-compatible chat completions completed without text " + formatOpenAIChatCompletionDiagnostics(e.diagnostics)
}

func (e *openAICompletionHasNoTextError) Unwrap() error {
	return ErrCompletionHasNoText
}

type openAICompletionTruncatedNoTextError struct {
	diagnostics openAIChatCompletionDiagnostics
}

func (e *openAICompletionTruncatedNoTextError) Error() string {
	return "llm: openai-compatible chat completions output is empty after truncation " + formatOpenAIChatCompletionDiagnostics(e.diagnostics)
}

func (e *openAICompletionTruncatedNoTextError) Unwrap() error {
	return ErrCompletionTruncatedNoText
}

type openAICompletionEmptyError struct {
	diagnostics openAIChatCompletionDiagnostics
}

func (e *openAICompletionEmptyError) Error() string {
	return "llm: openai-compatible chat completions output is empty " + formatOpenAIChatCompletionDiagnostics(e.diagnostics)
}

func (e *openAICompletionEmptyError) Unwrap() error {
	return ErrCompletionEmpty
}

func openAIChatCompletionTextError(diagnostics openAIChatCompletionDiagnostics) error {
	if diagnostics.hasTerminalNoTextOutcome() {
		return &openAICompletionHasNoTextError{diagnostics: diagnostics}
	}
	if diagnostics.hasTruncatedNoTextOutcome() {
		return &openAICompletionTruncatedNoTextError{diagnostics: diagnostics}
	}
	return emptyOpenAIChatCompletionError(diagnostics)
}

func (d openAIChatCompletionDiagnostics) hasTerminalNoTextOutcome() bool {
	if d.RefusalSeen || d.ToolCallsSeen || d.FunctionSeen {
		return true
	}
	for _, reason := range d.FinishReasons {
		switch reason {
		case "content_filter", "tool_calls", "function_call":
			return true
		}
	}
	return false
}

func (d openAIChatCompletionDiagnostics) hasTruncatedNoTextOutcome() bool {
	if d.ReasoningSeen {
		return true
	}
	for _, reason := range d.FinishReasons {
		switch reason {
		case "length", "max_tokens":
			return true
		}
	}
	return false
}

func emptyOpenAIChatCompletionError(diagnostics openAIChatCompletionDiagnostics) error {
	return &openAICompletionEmptyError{diagnostics: diagnostics}
}

func formatOpenAIChatCompletionDiagnostics(diagnostics openAIChatCompletionDiagnostics) string {
	finishReason := "none"
	if len(diagnostics.FinishReasons) > 0 {
		finishReason = strings.Join(diagnostics.FinishReasons, ",")
	}
	return fmt.Sprintf(
		"(finish_reason=%s reasoning_seen=%t refusal_seen=%t tool_calls_seen=%t function_call_seen=%t usage={input_tokens:%d output_tokens:%d total_tokens:%d})",
		finishReason,
		diagnostics.ReasoningSeen,
		diagnostics.RefusalSeen,
		diagnostics.ToolCallsSeen,
		diagnostics.FunctionSeen,
		diagnostics.Usage.InputTokens,
		diagnostics.Usage.OutputTokens,
		diagnostics.Usage.TotalTokens,
	)
}

// openAIResponsesInput 将通用消息转换为 Responses API 输入。
func openAIResponsesInput(messages []Message) responses.ResponseInputParam {
	out := make(responses.ResponseInputParam, 0, len(messages))
	for _, msg := range messages {
		content := openAIResponseContent(msg)
		if len(content) == 0 {
			out = append(out, responses.ResponseInputItemParamOfMessage(msg.Content, openAIResponseRole(msg.Role)))
			continue
		}
		out = append(out, responses.ResponseInputItemParamOfMessage(content, openAIResponseRole(msg.Role)))
	}
	return out
}

// openAIResponseContent 将多模态消息转换为 Responses API content list。
func openAIResponseContent(msg Message) responses.ResponseInputMessageContentListParam {
	if len(msg.Parts) == 0 {
		return nil
	}
	content := make(responses.ResponseInputMessageContentListParam, 0, len(msg.Parts)+1)
	hasText := false
	for _, part := range msg.Parts {
		switch part.Type {
		case ContentPartText:
			text := strings.TrimSpace(part.Text)
			if text == "" {
				continue
			}
			hasText = true
			content = append(content, responses.ResponseInputContentParamOfInputText(text))
		case ContentPartImageURL:
			imageURL := strings.TrimSpace(part.ImageURL)
			if imageURL == "" {
				continue
			}
			detail := openAIImageDetail(part.Detail)
			image := responses.ResponseInputContentParamOfInputImage(detail)
			image.OfInputImage.ImageURL = param.NewOpt(imageURL)
			content = append(content, image)
		}
	}
	if !hasText {
		if text := strings.TrimSpace(msg.Content); text != "" {
			content = append([]responses.ResponseInputContentUnionParam{responses.ResponseInputContentParamOfInputText(text)}, content...)
		}
	}
	return content
}

func openAIImageDetail(detail string) responses.ResponseInputImageDetail {
	switch strings.ToLower(strings.TrimSpace(detail)) {
	case "low":
		return responses.ResponseInputImageDetailLow
	case "high":
		return responses.ResponseInputImageDetailHigh
	case "original":
		return responses.ResponseInputImageDetailOriginal
	default:
		return responses.ResponseInputImageDetailAuto
	}
}

// openAIResponseRole 将通用角色转换为 Responses API 角色。
func openAIResponseRole(role Role) responses.EasyInputMessageRole {
	switch role {
	case RoleSystem:
		return responses.EasyInputMessageRoleSystem
	case RoleAssistant:
		return responses.EasyInputMessageRoleAssistant
	default:
		return responses.EasyInputMessageRoleUser
	}
}

type openAIErrorCapture struct {
	statusCode int
	body       string
}

// captureOpenAIErrorBody 创建捕获 OpenAI 错误响应体的中间件。
func captureOpenAIErrorBody(capture *openAIErrorCapture) option.Middleware {
	return func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		res, err := next(req)
		if res == nil || res.Body == nil || res.StatusCode < http.StatusBadRequest {
			return res, err
		}

		// SDK 解析错误前先复制 body，再放回去，既保留原 SDK 行为也能给用户更清楚的错误。
		body, readErr := io.ReadAll(res.Body)
		_ = res.Body.Close()
		res.Body = io.NopCloser(bytes.NewReader(body))
		if readErr == nil {
			capture.statusCode = res.StatusCode
			capture.body = string(body)
		}
		return res, err
	}
}

// openAIRequestOptions 构造 OpenAI 请求选项。
func openAIRequestOptions(userAgent string, headers map[string]string, capture *openAIErrorCapture) []option.RequestOption {
	opts := []option.RequestOption{option.WithMiddleware(captureOpenAIErrorBody(capture))}
	for name, value := range normalizeHeaders(headers) {
		opts = append(opts, option.WithHeader(name, value))
	}
	if strings.TrimSpace(userAgent) != "" {
		opts = append(opts, option.WithHeader("User-Agent", userAgent))
	}
	return opts
}

// openAICompatibleError 规范化 OpenAI-compatible 请求错误。
func openAICompatibleError(err error, capture *openAIErrorCapture) error {
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		statusCode := apiErr.StatusCode
		if statusCode == 0 && apiErr.Response != nil {
			statusCode = apiErr.Response.StatusCode
		}
		body := strings.TrimSpace(apiErr.RawJSON())
		if body == "" && capture != nil {
			body = capture.body
		}
		// 聚合商错误格式差异很大，统一压成 status/code/type/message/body 便于前端展示。
		return fmt.Errorf("llm: openai-compatible request failed: %s", formatOpenAIStatusError(statusCode, apiErr.Code, apiErr.Type, apiErr.Message, body))
	}
	if capture != nil && capture.statusCode >= http.StatusBadRequest {
		return fmt.Errorf("llm: openai-compatible request failed: %s", formatOpenAIStatusError(capture.statusCode, "", "", "", capture.body))
	}
	return err
}

// formatOpenAIStatusError 格式化 OpenAI-compatible HTTP 错误。
func formatOpenAIStatusError(statusCode int, code, typ, message, body string) string {
	if looksLikeCloudflareBlock(body) {
		// Cloudflare HTML 页对普通用户没帮助，直接转成可读原因。
		return openAIStatusLabel(statusCode) + ": Cloudflare blocked the API request before it reached the upstream service"
	}
	body = compactErrorBody(body)
	if code == "" && typ == "" && message == "" {
		// 有些 SDK 错误没有字段，但 body 里有 {"error":{...}}，再尝试解析一次。
		code, typ, message = openAIErrorFieldsFromBody(body)
	}

	details := make([]string, 0, 4)
	if code != "" {
		details = append(details, "code="+code)
	}
	if typ != "" {
		details = append(details, "type="+typ)
	}
	if message != "" {
		details = append(details, "message="+message)
	}
	if body != "" && len(details) == 0 {
		details = append(details, "body="+body)
	}
	if len(details) == 0 {
		return openAIStatusLabel(statusCode)
	}
	return openAIStatusLabel(statusCode) + ": " + strings.Join(details, "; ")
}

// looksLikeCloudflareBlock 判断错误页面是否像 Cloudflare 拦截。
func looksLikeCloudflareBlock(body string) bool {
	lower := strings.ToLower(body)
	return strings.Contains(lower, "<title>attention required!") && strings.Contains(lower, "cloudflare")
}

// openAIStatusLabel 将 HTTP 状态码转换为可读标签。
func openAIStatusLabel(statusCode int) string {
	if statusCode <= 0 {
		return "request failed"
	}
	if text := http.StatusText(statusCode); text != "" {
		return fmt.Sprintf("%d %s", statusCode, text)
	}
	return fmt.Sprintf("%d", statusCode)
}

// compactErrorBody 压缩并截断上游错误响应体。
func compactErrorBody(body string) string {
	body = strings.Join(strings.Fields(strings.TrimSpace(body)), " ")
	const maxRunes = 1000
	runes := []rune(body)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes]) + "..."
	}
	return body
}

// openAIErrorFieldsFromBody 从 JSON 错误响应中提取 code/type/message。
func openAIErrorFieldsFromBody(body string) (string, string, string) {
	var root struct {
		Code    string          `json:"code"`
		Type    string          `json:"type"`
		Message string          `json:"message"`
		Error   json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &root); err != nil {
		return "", "", ""
	}
	if len(root.Error) > 0 {
		var nested struct {
			Code    string `json:"code"`
			Type    string `json:"type"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(root.Error, &nested); err == nil {
			if root.Code == "" {
				root.Code = nested.Code
			}
			if root.Type == "" {
				root.Type = nested.Type
			}
			if root.Message == "" {
				root.Message = nested.Message
			}
		}
	}
	return root.Code, root.Type, root.Message
}
