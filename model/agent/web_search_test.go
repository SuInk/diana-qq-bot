package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWebSearchToolUsesDefaultFreeExaProviders(t *testing.T) {
	t.Setenv("DIANA_WEB_SEARCH_CONFIGS", "")
	t.Setenv("DIANA_WEB_SEARCH_CONFIG_FILE", "")
	tool := &WebSearchTool{configPath: filepath.Join(t.TempDir(), "missing.json")}
	providers, err := tool.loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) != 3 {
		t.Fatalf("providers = %#v", providers)
	}
	if providers[0].Name != "exa-free-primary" || providers[0].Tool != "web_search_exa" || providers[0].APIKeyEnv != "" {
		t.Fatalf("primary provider = %#v", providers[0])
	}
	if providers[1].Name != "exa-free-advanced" || providers[1].Tool != "web_search_advanced_exa" {
		t.Fatalf("fallback provider = %#v", providers[1])
	}
	if providers[2].Type != "tavily" || providers[2].APIKeyEnv != "TAVILY_API_KEY" {
		t.Fatalf("tavily provider = %#v", providers[2])
	}
}

func TestWebSearchToolLoadsOrderedConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "web-search.json")
	body := `{"providers":[
  {"name":"first","type":"exa_mcp","url":"https://mcp.exa.ai/mcp","tool":"web_search_exa"},
  {"name":"second","type":"tavily","url":"https://api.tavily.com/search","api_key_env":"TAVILY_TWO"}
]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DIANA_WEB_SEARCH_CONFIGS", "")
	t.Setenv("DIANA_WEB_SEARCH_CONFIG_FILE", path)
	providers, err := (&WebSearchTool{}).loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) != 2 || providers[0].Name != "first" || providers[1].Name != "second" {
		t.Fatalf("providers = %#v", providers)
	}
}

func TestWebSearchToolFallsBackBetweenRemoteMCPConfigs(t *testing.T) {
	primary := newMCPWebSearchServer(t, "primary", http.StatusTooManyRequests, "")
	defer primary.Close()
	backup := newMCPWebSearchServer(t, "backup", http.StatusOK, "https://official.example/result")
	defer backup.Close()
	t.Setenv("DIANA_WEB_SEARCH_CONFIGS", fmt.Sprintf(`{"providers":[
  {"name":"primary","type":"exa_mcp","url":%q,"tool":"web_search_exa","timeout_ms":3000},
  {"name":"backup","type":"exa_mcp","url":%q,"tool":"web_search_exa","timeout_ms":3000}
]}`, primary.URL, backup.URL))
	tool := &WebSearchTool{timeout: 8 * time.Second, maxBytes: 8_000}
	output, err := tool.Run(context.Background(), map[string]any{"query": "latest official release"})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Provider     string             `json:"provider"`
		FallbackUsed bool               `json:"fallback_used"`
		Attempts     []webSearchAttempt `json:"attempts"`
		Content      string             `json:"content"`
	}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatal(err)
	}
	if result.Provider != "backup" || !result.FallbackUsed || len(result.Attempts) != 2 {
		t.Fatalf("result = %#v", result)
	}
	if result.Attempts[0].Status != "failed" || result.Attempts[1].Status != "success" {
		t.Fatalf("attempts = %#v", result.Attempts)
	}
	if !strings.Contains(result.Content, "https://official.example/result") {
		t.Fatalf("output = %s", output)
	}
}

func TestWebSearchToolSkipsMissingKeyAndUsesNextConfig(t *testing.T) {
	backup := newMCPWebSearchServer(t, "backup", http.StatusOK, "https://example.com/ok")
	defer backup.Close()
	t.Setenv("MISSING_TAVILY_KEY", "")
	t.Setenv("DIANA_WEB_SEARCH_CONFIGS", fmt.Sprintf(`[
  {"name":"tavily","type":"tavily","url":"https://api.tavily.com/search","api_key_env":"MISSING_TAVILY_KEY"},
  {"name":"exa","type":"exa_mcp","url":%q,"tool":"web_search_exa"}
]`, backup.URL))
	output, err := (&WebSearchTool{timeout: 5 * time.Second, maxBytes: 8_000}).Run(context.Background(), map[string]any{"query": "query"})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Provider string             `json:"provider"`
		Attempts []webSearchAttempt `json:"attempts"`
	}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatal(err)
	}
	if result.Provider != "exa" || len(result.Attempts) != 2 || result.Attempts[0].Status != "skipped" {
		t.Fatalf("result = %#v", result)
	}
}

func TestWebSearchToolFallsBackToTavily(t *testing.T) {
	primary := newMCPWebSearchServer(t, "primary", http.StatusServiceUnavailable, "")
	defer primary.Close()
	var authorization string
	tavily := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"Official","url":"https://official.example","content":"confirmed","score":0.99}]}`))
	}))
	defer tavily.Close()
	t.Setenv("TAVILY_TEST_KEY", "secret-test-key")
	t.Setenv("DIANA_WEB_SEARCH_CONFIGS", fmt.Sprintf(`[
  {"name":"exa","type":"exa_mcp","url":%q,"tool":"web_search_exa"},
  {"name":"tavily","type":"tavily","url":%q,"api_key_env":"TAVILY_TEST_KEY"}
]`, primary.URL, tavily.URL))
	output, err := (&WebSearchTool{timeout: 5 * time.Second, maxBytes: 8_000}).Run(context.Background(), map[string]any{"query": "query"})
	if err != nil {
		t.Fatal(err)
	}
	if authorization != "Bearer secret-test-key" || !strings.Contains(output, `"provider": "tavily"`) || !strings.Contains(output, "official.example") {
		t.Fatalf("authorization=%q output=%s", authorization, output)
	}
	if strings.Contains(output, "secret-test-key") {
		t.Fatalf("API key leaked in output: %s", output)
	}
}

func TestDecodeMCPRPCResponseAcceptsEventStream(t *testing.T) {
	raw := []byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"ok\":true}}\n\n")
	response, err := decodeMCPRPCResponse(raw, "text/event-stream")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(response.Result), `"ok":true`) {
		t.Fatalf("response = %#v", response)
	}
}

func TestWebSearchToolLiveExaMCP(t *testing.T) {
	if os.Getenv("DIANA_LIVE_WEB_SEARCH") != "1" {
		t.Skip("set DIANA_LIVE_WEB_SEARCH=1 to run a real Exa MCP search")
	}
	t.Setenv("DIANA_WEB_SEARCH_CONFIG_FILE", "")
	t.Setenv("DIANA_WEB_SEARCH_CONFIGS", `[{"name":"exa-live","type":"exa_mcp","url":"https://mcp.exa.ai/mcp?tools=web_search_exa","tool":"web_search_exa","timeout_ms":20000,"max_results":3}]`)
	output, err := (&WebSearchTool{timeout: 25 * time.Second, maxBytes: 12_000}).Run(context.Background(), map[string]any{"query": "Exa official MCP documentation no API key required"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(output), "exa") || !strings.Contains(output, "https://") {
		t.Fatalf("unexpected search output: %s", output)
	}
}

func TestWebSearchToolLiveExaMCPFallback(t *testing.T) {
	if os.Getenv("DIANA_LIVE_WEB_SEARCH") != "1" {
		t.Skip("set DIANA_LIVE_WEB_SEARCH=1 to run a real Exa MCP fallback search")
	}
	t.Setenv("DIANA_WEB_SEARCH_CONFIG_FILE", "")
	t.Setenv("DIANA_WEB_SEARCH_CONFIGS", `[
  {"name":"forced-failure","type":"exa_mcp","url":"http://127.0.0.1:1/mcp","tool":"web_search_exa","timeout_ms":1000},
  {"name":"exa-live-fallback","type":"exa_mcp","url":"https://mcp.exa.ai/mcp?tools=web_search_exa","tool":"web_search_exa","timeout_ms":20000,"max_results":3}
]`)
	output, err := (&WebSearchTool{timeout: 25 * time.Second, maxBytes: 12_000}).Run(context.Background(), map[string]any{"query": "Exa official MCP documentation"})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Provider     string             `json:"provider"`
		FallbackUsed bool               `json:"fallback_used"`
		Attempts     []webSearchAttempt `json:"attempts"`
	}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatal(err)
	}
	if result.Provider != "exa-live-fallback" || !result.FallbackUsed || len(result.Attempts) != 2 || result.Attempts[0].Status != "failed" || result.Attempts[1].Status != "success" {
		t.Fatalf("fallback result = %#v", result)
	}
}

func TestWebSearchToolLiveExaAdvancedMCP(t *testing.T) {
	if os.Getenv("DIANA_LIVE_WEB_SEARCH") != "1" {
		t.Skip("set DIANA_LIVE_WEB_SEARCH=1 to run a real advanced Exa MCP search")
	}
	t.Setenv("DIANA_WEB_SEARCH_CONFIG_FILE", "")
	t.Setenv("DIANA_WEB_SEARCH_CONFIGS", `[{
  "name":"exa-advanced-live",
  "type":"exa_mcp",
  "url":"https://mcp.exa.ai/mcp?tools=web_search_advanced_exa",
  "tool":"web_search_advanced_exa",
  "timeout_ms":20000,
  "max_results":3
}]`)
	output, err := (&WebSearchTool{timeout: 25 * time.Second, maxBytes: 12_000}).Run(context.Background(), map[string]any{"query": "Exa official MCP documentation"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(output), "exa") || !strings.Contains(output, "https://") {
		t.Fatalf("unexpected advanced search output: %s", output)
	}
}

func newMCPWebSearchServer(t *testing.T, name string, initStatus int, resultURL string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var request struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		switch request.Method {
		case "initialize":
			if initStatus != http.StatusOK {
				w.WriteHeader(initStatus)
				return
			}
			w.Header().Set("Mcp-Session-Id", "session-"+name)
			writeMCPEvent(w, fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":%q,"capabilities":{"tools":{}},"serverInfo":{"name":%q,"version":"1"}}}`, mcpProtocolVersion, name))
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/call":
			content := fmt.Sprintf("Search result from %s: %s", name, resultURL)
			writeMCPEvent(w, fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"text","text":%q}],"isError":false}}`, content))
		default:
			http.Error(w, "unknown method", http.StatusBadRequest)
		}
	}))
}

func writeMCPEvent(w http.ResponseWriter, payload string) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", payload)
}
