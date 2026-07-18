package webui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"diana-qq-bot/model/agent"

	"github.com/gin-gonic/gin"
)

func TestWebSearchConfigHandlerDefaultsAndPersistsOrderedProviders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "web-search.json")
	router := webSearchConfigTestRouter(NewWebSearchConfigHandler(path))

	get := httptest.NewRecorder()
	router.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/web-search/config", nil))
	if get.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body = %s", get.Code, get.Body.String())
	}
	var defaults webSearchConfigResponse
	if err := json.NewDecoder(get.Body).Decode(&defaults); err != nil {
		t.Fatal(err)
	}
	if len(defaults.Providers) != 3 || defaults.Providers[0].Name != "exa-free-primary" {
		t.Fatalf("defaults = %#v", defaults)
	}

	body := []byte(`{"providers":[
  {"name":"tavily-backup","type":"tavily","url":"https://api.tavily.com/search","api_key_env":"TAVILY_SECOND","timeout_ms":9000,"max_results":4},
  {"name":"exa-primary","type":"exa_mcp","url":"https://mcp.exa.ai/mcp","tool":"web_search_exa","timeout_ms":7000,"max_results":3}
]}`)
	post := httptest.NewRecorder()
	router.ServeHTTP(post, httptest.NewRequest(http.MethodPost, "/api/web-search/config", bytes.NewReader(body)))
	if post.Code != http.StatusOK {
		t.Fatalf("POST status = %d, body = %s", post.Code, post.Body.String())
	}
	config, err := agent.LoadWebSearchConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Providers) != 2 || config.Providers[0].Name != "tavily-backup" || config.Providers[1].Name != "exa-primary" {
		t.Fatalf("config = %#v", config)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
}

func TestWebSearchConfigHandlerTestsRemoteMCPProvider(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var request struct {
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		switch request.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "webui-test")
			_, _ = fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"protocolVersion\":\"2025-03-26\",\"capabilities\":{}}}\n\n")
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/call":
			_, _ = fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":2,\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"official result https://example.test\"}]}}\n\n")
		}
	}))
	defer remote.Close()

	payload := fmt.Sprintf(`{"query":"official release","provider":{"name":"test","type":"exa_mcp","url":%q,"tool":"web_search_exa","timeout_ms":3000,"max_results":2}}`, remote.URL)
	recorder := httptest.NewRecorder()
	router := webSearchConfigTestRouter(NewWebSearchConfigHandler(filepath.Join(t.TempDir(), "web-search.json")))
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/web-search/test", strings.NewReader(payload)))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "official result") {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func webSearchConfigTestRouter(handler *WebSearchConfigHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler.Register(router)
	return router
}
