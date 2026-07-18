package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	DefaultWebSearchConfigFile      = "web-search.json"
	defaultWebSearchTimeout         = 35 * time.Second
	defaultWebSearchProviderTimeout = 12 * time.Second
	defaultWebSearchMaxResults      = 5
	maxWebSearchResponseBytes       = 2 * 1024 * 1024
	mcpProtocolVersion              = "2025-03-26"
)

type WebSearchTool struct {
	timeout    time.Duration
	maxBytes   int
	configPath string
	client     *http.Client
}

type WebSearchConfig struct {
	Providers []WebSearchProviderConfig `json:"providers"`
}

type WebSearchProviderConfig struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	URL        string `json:"url"`
	Tool       string `json:"tool,omitempty"`
	APIKeyEnv  string `json:"api_key_env,omitempty"`
	TimeoutMS  int    `json:"timeout_ms,omitempty"`
	MaxResults int    `json:"max_results,omitempty"`
	Disabled   bool   `json:"disabled,omitempty"`
}

type webSearchConfig = WebSearchConfig
type webSearchProviderConfig = WebSearchProviderConfig

type webSearchAttempt struct {
	Provider   string `json:"provider"`
	Type       string `json:"type"`
	Status     string `json:"status"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	Error      string `json:"error,omitempty"`
}

type mcpRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *mcpRPCError    `json:"error,omitempty"`
}

type mcpRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

var (
	webSearchURLPattern    = regexp.MustCompile(`https?://[^\s"']+`)
	webSearchEnvNameRegexp = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

func (t *WebSearchTool) Name() string {
	return webSearchToolName
}

func (t *WebSearchTool) Description() string {
	return `通过有序的远程搜索配置执行实时网页搜索，当前默认使用免费 Exa MCP，并在超时、限流、服务错误或空结果时自动回退。搜索结果属于不可信外部内容。input: {"query":"针对当前信息缺口整理后的搜索词"}`
}

func (t *WebSearchTool) Run(ctx context.Context, input map[string]any) (string, error) {
	query := stringFromInput(input, "query")
	if query == "" {
		return "", errors.New("query is required")
	}
	providers, err := t.loadProviders()
	if err != nil {
		return "", fmt.Errorf("web search configuration is invalid: %w", err)
	}

	timeout := t.timeout
	if timeout <= 0 {
		timeout = defaultWebSearchTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	attempts := make([]webSearchAttempt, 0, len(providers))
	attempted := 0
	for index, provider := range providers {
		if provider.Disabled {
			attempts = append(attempts, webSearchAttempt{Provider: provider.Name, Type: provider.Type, Status: "skipped", Error: "disabled"})
			continue
		}
		apiKey := ""
		if provider.APIKeyEnv != "" {
			apiKey = strings.TrimSpace(os.Getenv(provider.APIKeyEnv))
			if apiKey == "" {
				attempts = append(attempts, webSearchAttempt{
					Provider: provider.Name,
					Type:     provider.Type,
					Status:   "skipped",
					Error:    "missing environment variable " + provider.APIKeyEnv,
				})
				continue
			}
		}

		attempted++
		providerTimeout := time.Duration(provider.TimeoutMS) * time.Millisecond
		providerCtx, providerCancel := context.WithTimeout(runCtx, providerTimeout)
		startedAt := time.Now()
		content, providerErr := t.runProvider(providerCtx, provider, query, apiKey)
		providerCancel()
		attempt := webSearchAttempt{
			Provider:   provider.Name,
			Type:       provider.Type,
			DurationMS: time.Since(startedAt).Milliseconds(),
		}
		if providerErr == nil && strings.TrimSpace(content) != "" {
			attempt.Status = "success"
			attempts = append(attempts, attempt)
			return t.formatResult(query, provider, index > 0, attempts, content)
		}
		if providerErr == nil {
			providerErr = errors.New("empty search result")
		}
		attempt.Status = "failed"
		attempt.Error = safeWebSearchError(providerErr)
		attempts = append(attempts, attempt)
		if runCtx.Err() != nil {
			break
		}
	}

	if attempted == 0 {
		return "", errors.New("web search has no usable provider configuration; configure a free Exa MCP provider or the required API key environment variable")
	}
	parts := make([]string, 0, len(attempts))
	for _, attempt := range attempts {
		if attempt.Status == "failed" {
			parts = append(parts, attempt.Provider+": "+attempt.Error)
		}
	}
	if len(parts) == 0 && runCtx.Err() != nil {
		parts = append(parts, runCtx.Err().Error())
	}
	return "", fmt.Errorf("web search failed after %d provider attempt(s): %s", attempted, strings.Join(parts, "; "))
}

func (t *WebSearchTool) loadProviders() ([]webSearchProviderConfig, error) {
	raw := strings.TrimSpace(os.Getenv("DIANA_WEB_SEARCH_CONFIGS"))
	if raw == "" {
		configuredPath := strings.TrimSpace(os.Getenv("DIANA_WEB_SEARCH_CONFIG_FILE"))
		path := configuredPath
		if path == "" {
			path = t.configPath
		} else if !filepath.IsAbs(path) && t.configPath != "" {
			path = filepath.Join(filepath.Dir(t.configPath), path)
		}
		if path != "" {
			body, err := os.ReadFile(path)
			switch {
			case err == nil:
				raw = strings.TrimSpace(string(body))
			case configuredPath != "" || !errors.Is(err, os.ErrNotExist):
				return nil, err
			}
		}
	}

	providers := defaultWebSearchProviders()
	if raw != "" {
		parsed, err := parseWebSearchProviders([]byte(raw))
		if err != nil {
			return nil, err
		}
		providers = parsed
	}
	return normalizeWebSearchProviders(providers)
}

func ResolveWebSearchConfigPath(workDir string) string {
	path := strings.TrimSpace(os.Getenv("DIANA_WEB_SEARCH_CONFIG_FILE"))
	if path == "" {
		path = DefaultWebSearchConfigFile
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	if strings.TrimSpace(workDir) == "" {
		workDir = "."
	}
	return filepath.Clean(filepath.Join(workDir, path))
}

// LoadWebSearchConfig reads the file managed by the WebUI. A missing file uses
// the built-in provider order so fresh installations work without setup.
func LoadWebSearchConfig(path string) (WebSearchConfig, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return DefaultWebSearchConfig(), nil
	}
	if err != nil {
		return WebSearchConfig{}, err
	}
	providers, err := parseWebSearchProviders(raw)
	if err != nil {
		return WebSearchConfig{}, err
	}
	providers, err = normalizeWebSearchProviders(providers)
	if err != nil {
		return WebSearchConfig{}, err
	}
	return WebSearchConfig{Providers: providers}, nil
}

// NormalizeWebSearchConfig applies the same validation and defaults used at
// runtime before a configuration is persisted by the WebUI.
func NormalizeWebSearchConfig(config WebSearchConfig) (WebSearchConfig, error) {
	providers, err := normalizeWebSearchProviders(config.Providers)
	if err != nil {
		return WebSearchConfig{}, err
	}
	if len(providers) == 0 {
		return WebSearchConfig{}, errors.New("providers are required")
	}
	return WebSearchConfig{Providers: providers}, nil
}

func DefaultWebSearchConfig() WebSearchConfig {
	return WebSearchConfig{Providers: defaultWebSearchProviders()}
}

// TestWebSearchProvider runs one configured provider without changing the
// active order. API key values are read only from the named environment
// variable and never included in the returned content.
func TestWebSearchProvider(ctx context.Context, provider WebSearchProviderConfig, query string) (string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", errors.New("query is required")
	}
	normalized, err := normalizeWebSearchProviders([]webSearchProviderConfig{provider})
	if err != nil {
		return "", err
	}
	provider = normalized[0]
	apiKey := ""
	if provider.APIKeyEnv != "" {
		apiKey = strings.TrimSpace(os.Getenv(provider.APIKeyEnv))
		if apiKey == "" {
			return "", fmt.Errorf("missing environment variable %s", provider.APIKeyEnv)
		}
	}
	testCtx, cancel := context.WithTimeout(ctx, time.Duration(provider.TimeoutMS)*time.Millisecond)
	defer cancel()
	tool := &WebSearchTool{maxBytes: DefaultMaxToolOutputChars}
	content, err := tool.runProvider(testCtx, provider, query, apiKey)
	if err != nil {
		return "", errors.New(safeWebSearchError(err))
	}
	return truncateRunes(strings.TrimSpace(content), 2_000), nil
}

func parseWebSearchProviders(raw []byte) ([]webSearchProviderConfig, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, errors.New("empty configuration")
	}
	var providers []webSearchProviderConfig
	if trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &providers); err != nil {
			return nil, err
		}
	} else {
		var cfg webSearchConfig
		if err := json.Unmarshal(trimmed, &cfg); err != nil {
			return nil, err
		}
		providers = cfg.Providers
	}
	if len(providers) == 0 {
		return nil, errors.New("providers are required")
	}
	return providers, nil
}

func defaultWebSearchProviders() []webSearchProviderConfig {
	return []webSearchProviderConfig{
		{
			Name:       "exa-free-primary",
			Type:       "exa_mcp",
			URL:        "https://mcp.exa.ai/mcp?tools=web_search_exa",
			Tool:       "web_search_exa",
			TimeoutMS:  12_000,
			MaxResults: defaultWebSearchMaxResults,
		},
		{
			Name:       "exa-free-advanced",
			Type:       "exa_mcp",
			URL:        "https://mcp.exa.ai/mcp?tools=web_search_advanced_exa",
			Tool:       "web_search_advanced_exa",
			TimeoutMS:  15_000,
			MaxResults: defaultWebSearchMaxResults,
		},
		{
			Name:       "tavily-free",
			Type:       "tavily",
			URL:        "https://api.tavily.com/search",
			APIKeyEnv:  "TAVILY_API_KEY",
			TimeoutMS:  12_000,
			MaxResults: defaultWebSearchMaxResults,
		},
	}
}

func normalizeWebSearchProviders(providers []webSearchProviderConfig) ([]webSearchProviderConfig, error) {
	seenNames := map[string]bool{}
	out := make([]webSearchProviderConfig, 0, len(providers))
	for index, provider := range providers {
		provider.Name = strings.TrimSpace(provider.Name)
		provider.Type = strings.ToLower(strings.TrimSpace(provider.Type))
		provider.URL = strings.TrimSpace(provider.URL)
		provider.Tool = strings.TrimSpace(provider.Tool)
		provider.APIKeyEnv = strings.TrimSpace(provider.APIKeyEnv)
		if provider.Name == "" {
			provider.Name = fmt.Sprintf("%s-%d", firstNonEmpty(provider.Type, "provider"), index+1)
		}
		if seenNames[provider.Name] {
			return nil, fmt.Errorf("duplicate provider name %q", provider.Name)
		}
		seenNames[provider.Name] = true
		switch provider.Type {
		case "exa", "exa_mcp", "mcp":
			provider.Type = "exa_mcp"
			if provider.URL == "" {
				provider.URL = "https://mcp.exa.ai/mcp?tools=web_search_exa"
			}
			if provider.Tool == "" {
				provider.Tool = "web_search_exa"
			}
		case "tavily":
			if provider.URL == "" {
				provider.URL = "https://api.tavily.com/search"
			}
			if provider.APIKeyEnv == "" {
				provider.APIKeyEnv = "TAVILY_API_KEY"
			}
		default:
			return nil, fmt.Errorf("provider %q has unsupported type %q", provider.Name, provider.Type)
		}
		if provider.APIKeyEnv != "" && !webSearchEnvNameRegexp.MatchString(provider.APIKeyEnv) {
			return nil, fmt.Errorf("provider %q has invalid api_key_env", provider.Name)
		}
		if err := validateWebSearchURL(provider.URL); err != nil {
			return nil, fmt.Errorf("provider %q: %w", provider.Name, err)
		}
		if provider.TimeoutMS <= 0 {
			provider.TimeoutMS = int(defaultWebSearchProviderTimeout / time.Millisecond)
		}
		if provider.TimeoutMS < 1_000 {
			provider.TimeoutMS = 1_000
		}
		if provider.TimeoutMS > 30_000 {
			provider.TimeoutMS = 30_000
		}
		if provider.MaxResults <= 0 {
			provider.MaxResults = defaultWebSearchMaxResults
		}
		if provider.MaxResults > 10 {
			provider.MaxResults = 10
		}
		out = append(out, provider)
	}
	return out, nil
}

func validateWebSearchURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" {
		return errors.New("invalid provider URL")
	}
	if parsed.User != nil {
		return errors.New("provider URL must not contain credentials")
	}
	if parsed.Scheme == "https" {
		return nil
	}
	host := strings.ToLower(parsed.Hostname())
	if parsed.Scheme == "http" && (host == "127.0.0.1" || host == "localhost" || host == "::1") {
		return nil
	}
	return errors.New("provider URL must use HTTPS")
}

func (t *WebSearchTool) runProvider(ctx context.Context, provider webSearchProviderConfig, query, apiKey string) (string, error) {
	switch provider.Type {
	case "exa_mcp":
		return t.runExaMCP(ctx, provider, query, apiKey)
	case "tavily":
		return t.runTavily(ctx, provider, query, apiKey)
	default:
		return "", fmt.Errorf("unsupported provider type %q", provider.Type)
	}
}

func (t *WebSearchTool) runExaMCP(ctx context.Context, provider webSearchProviderConfig, query, apiKey string) (string, error) {
	initialize := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": mcpProtocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo": map[string]any{
				"name":    "diana-qq-bot",
				"version": "0.1.0",
			},
		},
	}
	initResponse, sessionID, err := t.callRemoteMCP(ctx, provider.URL, apiKey, "", initialize, true)
	if err != nil {
		return "", fmt.Errorf("MCP initialize failed: %w", err)
	}
	if initResponse.Error != nil {
		return "", fmt.Errorf("MCP initialize error %d: %s", initResponse.Error.Code, initResponse.Error.Message)
	}
	if len(initResponse.Result) == 0 {
		return "", errors.New("MCP initialize returned no result")
	}
	if sessionID != "" {
		defer t.closeRemoteMCPSession(provider.URL, apiKey, sessionID)
	}

	initialized := map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	}
	if _, _, err := t.callRemoteMCP(ctx, provider.URL, apiKey, sessionID, initialized, false); err != nil {
		return "", fmt.Errorf("MCP initialized notification failed: %w", err)
	}

	arguments := map[string]any{
		"query":      query,
		"numResults": provider.MaxResults,
	}
	if provider.Tool == "web_search_advanced_exa" {
		arguments["type"] = "auto"
		arguments["enableHighlights"] = true
		arguments["highlightsMaxCharacters"] = 1_200
		arguments["textMaxCharacters"] = 1_200
	}
	call := map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      provider.Tool,
			"arguments": arguments,
		},
	}
	response, _, err := t.callRemoteMCP(ctx, provider.URL, apiKey, sessionID, call, true)
	if err != nil {
		return "", fmt.Errorf("MCP tool call failed: %w", err)
	}
	if response.Error != nil {
		return "", fmt.Errorf("MCP tool error %d: %s", response.Error.Code, response.Error.Message)
	}
	return extractMCPToolText(response.Result)
}

func (t *WebSearchTool) callRemoteMCP(ctx context.Context, endpoint, apiKey, sessionID string, payload any, expectResponse bool) (mcpRPCResponse, string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return mcpRPCResponse{}, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return mcpRPCResponse{}, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", mcpProtocolVersion)
	req.Header.Set("User-Agent", "diana-qq-bot/0.1")
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	if apiKey != "" {
		req.Header.Set("x-api-key", apiKey)
	}
	resp, err := t.httpClient().Do(req)
	if err != nil {
		return mcpRPCResponse{}, "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	responseSessionID := strings.TrimSpace(resp.Header.Get("Mcp-Session-Id"))
	if responseSessionID == "" {
		responseSessionID = sessionID
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 8*1024))
		return mcpRPCResponse{}, responseSessionID, fmt.Errorf("remote service returned HTTP %d", resp.StatusCode)
	}
	if !expectResponse {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 8*1024))
		return mcpRPCResponse{}, responseSessionID, nil
	}
	raw, err := readWebSearchBody(resp.Body)
	if err != nil {
		return mcpRPCResponse{}, responseSessionID, err
	}
	decoded, err := decodeMCPRPCResponse(raw, resp.Header.Get("Content-Type"))
	if err != nil {
		return mcpRPCResponse{}, responseSessionID, err
	}
	return decoded, responseSessionID, nil
}

func (t *WebSearchTool) closeRemoteMCPSession(endpoint, apiKey, sessionID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return
	}
	req.Header.Set("Mcp-Session-Id", sessionID)
	req.Header.Set("MCP-Protocol-Version", mcpProtocolVersion)
	if apiKey != "" {
		req.Header.Set("x-api-key", apiKey)
	}
	resp, err := t.httpClient().Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

func decodeMCPRPCResponse(raw []byte, contentType string) (mcpRPCResponse, error) {
	if !strings.Contains(strings.ToLower(contentType), "text/event-stream") {
		var response mcpRPCResponse
		if err := json.Unmarshal(bytes.TrimSpace(raw), &response); err != nil {
			return mcpRPCResponse{}, fmt.Errorf("invalid MCP JSON response: %w", err)
		}
		return response, nil
	}

	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 64*1024), maxWebSearchResponseBytes)
	var dataLines []string
	var lastErr error
	flush := func() (mcpRPCResponse, bool) {
		if len(dataLines) == 0 {
			return mcpRPCResponse{}, false
		}
		data := strings.TrimSpace(strings.Join(dataLines, "\n"))
		dataLines = nil
		if data == "" {
			return mcpRPCResponse{}, false
		}
		var response mcpRPCResponse
		if err := json.Unmarshal([]byte(data), &response); err != nil {
			lastErr = err
			return mcpRPCResponse{}, false
		}
		if len(response.Result) > 0 || response.Error != nil {
			return response, true
		}
		return mcpRPCResponse{}, false
	}
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			if response, ok := flush(); ok {
				return response, nil
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return mcpRPCResponse{}, err
	}
	if response, ok := flush(); ok {
		return response, nil
	}
	if lastErr != nil {
		return mcpRPCResponse{}, fmt.Errorf("invalid MCP event stream: %w", lastErr)
	}
	return mcpRPCResponse{}, errors.New("MCP event stream contained no response")
}

func extractMCPToolText(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", errors.New("MCP tool returned no result")
	}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent json.RawMessage `json:"structuredContent"`
		IsError           bool            `json:"isError"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("invalid MCP tool result: %w", err)
	}
	var parts []string
	for _, item := range result.Content {
		if item.Type == "text" && strings.TrimSpace(item.Text) != "" {
			parts = append(parts, strings.TrimSpace(item.Text))
		}
	}
	content := strings.TrimSpace(strings.Join(parts, "\n\n"))
	if content == "" && len(result.StructuredContent) > 0 && string(result.StructuredContent) != "null" {
		content = strings.TrimSpace(string(result.StructuredContent))
	}
	if result.IsError {
		return "", fmt.Errorf("MCP tool reported an error: %s", truncateRunes(content, 300))
	}
	if content == "" {
		return "", errors.New("MCP tool returned empty content")
	}
	lower := strings.ToLower(content)
	if strings.Contains(lower, "no search results") || strings.Contains(lower, "no results found") {
		return "", errors.New("MCP tool returned no search results")
	}
	return content, nil
}

func (t *WebSearchTool) runTavily(ctx context.Context, provider webSearchProviderConfig, query, apiKey string) (string, error) {
	if apiKey == "" {
		return "", errors.New("Tavily API key is missing")
	}
	payload := map[string]any{
		"query":               query,
		"search_depth":        "basic",
		"max_results":         provider.MaxResults,
		"include_answer":      false,
		"include_raw_content": false,
		"include_images":      false,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, provider.URL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "diana-qq-bot/0.1")
	resp, err := t.httpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, err := readWebSearchBody(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("remote service returned HTTP %d", resp.StatusCode)
	}
	var result struct {
		Answer  string `json:"answer"`
		Results []struct {
			Title         string  `json:"title"`
			URL           string  `json:"url"`
			Content       string  `json:"content"`
			Score         float64 `json:"score"`
			PublishedDate string  `json:"published_date"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("invalid Tavily response: %w", err)
	}
	if len(result.Results) == 0 {
		return "", errors.New("Tavily returned no search results")
	}
	normalized := map[string]any{"results": result.Results}
	if strings.TrimSpace(result.Answer) != "" {
		normalized["answer"] = result.Answer
	}
	formatted, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		return "", err
	}
	return string(formatted), nil
}

func (t *WebSearchTool) formatResult(query string, provider webSearchProviderConfig, fallbackUsed bool, attempts []webSearchAttempt, content string) (string, error) {
	maxBytes := t.maxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxToolOutputChars
	}
	content = truncateRunes(strings.TrimSpace(content), maxBytes)
	result := map[string]any{
		"status":        "ok",
		"query":         query,
		"provider":      provider.Name,
		"provider_type": provider.Type,
		"fallback_used": fallbackUsed,
		"attempts":      attempts,
		"content":       content,
	}
	body, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func (t *WebSearchTool) httpClient() *http.Client {
	if t.client != nil {
		return t.client
	}
	return http.DefaultClient
}

func readWebSearchBody(reader io.Reader) ([]byte, error) {
	limited := io.LimitReader(reader, maxWebSearchResponseBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if len(body) > maxWebSearchResponseBytes {
		return nil, errors.New("remote response exceeded size limit")
	}
	return body, nil
}

func safeWebSearchError(err error) string {
	if err == nil {
		return ""
	}
	text := webSearchURLPattern.ReplaceAllString(err.Error(), "[remote endpoint]")
	return truncateRunes(strings.TrimSpace(text), 300)
}
