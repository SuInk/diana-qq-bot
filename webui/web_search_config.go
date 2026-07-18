package webui

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"diana-qq-bot/model/agent"

	"github.com/gin-gonic/gin"
)

type WebSearchConfigHandler struct {
	path string
	logs AppLogWriter
}

type webSearchConfigResponse struct {
	Providers       []webSearchProviderResponse `json:"providers"`
	ConfigPath      string                      `json:"config_path"`
	OverriddenByEnv bool                        `json:"overridden_by_env"`
}

type webSearchProviderResponse struct {
	agent.WebSearchProviderConfig
	APIKeyConfigured bool `json:"api_key_configured"`
}

type webSearchTestPayload struct {
	Provider agent.WebSearchProviderConfig `json:"provider"`
	Query    string                        `json:"query"`
}

type webSearchTestResponse struct {
	Provider   string `json:"provider"`
	DurationMS int64  `json:"duration_ms"`
	Content    string `json:"content"`
}

func NewWebSearchConfigHandler(path string) *WebSearchConfigHandler {
	return &WebSearchConfigHandler{path: filepath.Clean(path)}
}

func (h *WebSearchConfigHandler) SetLogStore(store AppLogWriter) {
	h.logs = store
}

func (h *WebSearchConfigHandler) Register(router gin.IRouter) {
	router.GET("/api/web-search/config", h.getConfig)
	router.POST("/api/web-search/config", h.saveConfig)
	router.POST("/api/web-search/test", h.testProvider)
}

func (h *WebSearchConfigHandler) getConfig(c *gin.Context) {
	config, err := agent.LoadWebSearchConfig(h.path)
	if err != nil {
		logAndWriteError(c, h.logs, http.StatusInternalServerError, "web_search.config.read", sanitizedWebSearchConfigError(err), "", nil)
		return
	}
	c.JSON(http.StatusOK, h.response(config))
}

func (h *WebSearchConfigHandler) saveConfig(c *gin.Context) {
	var payload agent.WebSearchConfig
	if err := c.ShouldBindJSON(&payload); err != nil {
		logAndWriteError(c, h.logs, http.StatusBadRequest, "web_search.config.save", err, "", nil)
		return
	}
	config, err := agent.NormalizeWebSearchConfig(payload)
	if err != nil {
		logAndWriteError(c, h.logs, http.StatusBadRequest, "web_search.config.save", err, "", nil)
		return
	}
	if err := writeWebSearchConfig(h.path, config); err != nil {
		logAndWriteError(c, h.logs, http.StatusInternalServerError, "web_search.config.save", sanitizedWebSearchConfigError(err), "", nil)
		return
	}
	recordRequestOperation(c, h.logs, "web_search.config.save", "联网搜索配置已保存", "web-search", map[string]any{
		"provider_count": len(config.Providers),
		"provider_names": webSearchProviderNames(config.Providers),
	})
	c.JSON(http.StatusOK, h.response(config))
}

func (h *WebSearchConfigHandler) testProvider(c *gin.Context) {
	var payload webSearchTestPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		logAndWriteError(c, h.logs, http.StatusBadRequest, "web_search.provider.test", err, "", nil)
		return
	}
	startedAt := time.Now()
	content, err := agent.TestWebSearchProvider(c.Request.Context(), payload.Provider, payload.Query)
	duration := time.Since(startedAt).Milliseconds()
	if err != nil {
		logAndWriteError(c, h.logs, http.StatusBadGateway, "web_search.provider.test", err, payload.Provider.Name, map[string]any{
			"provider":    payload.Provider.Name,
			"type":        payload.Provider.Type,
			"duration_ms": duration,
		})
		return
	}
	recordRequestOperation(c, h.logs, "web_search.provider.test", "联网搜索配置测试成功", payload.Provider.Name, map[string]any{
		"provider":    payload.Provider.Name,
		"type":        payload.Provider.Type,
		"duration_ms": duration,
	})
	c.JSON(http.StatusOK, webSearchTestResponse{Provider: payload.Provider.Name, DurationMS: duration, Content: content})
}

func (h *WebSearchConfigHandler) response(config agent.WebSearchConfig) webSearchConfigResponse {
	providers := make([]webSearchProviderResponse, 0, len(config.Providers))
	for _, provider := range config.Providers {
		configured := provider.APIKeyEnv == ""
		if provider.APIKeyEnv != "" {
			configured = strings.TrimSpace(os.Getenv(provider.APIKeyEnv)) != ""
		}
		providers = append(providers, webSearchProviderResponse{
			WebSearchProviderConfig: provider,
			APIKeyConfigured:        configured,
		})
	}
	return webSearchConfigResponse{
		Providers:       providers,
		ConfigPath:      h.path,
		OverriddenByEnv: strings.TrimSpace(os.Getenv("DIANA_WEB_SEARCH_CONFIGS")) != "",
	}
}

func writeWebSearchConfig(path string, config agent.WebSearchConfig) error {
	if strings.TrimSpace(path) == "" || path == "." {
		return errors.New("web search config path is required")
	}
	body, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".web-search-*.json")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(body); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func webSearchProviderNames(providers []agent.WebSearchProviderConfig) []string {
	names := make([]string, 0, len(providers))
	for _, provider := range providers {
		names = append(names, provider.Name)
	}
	return names
}

func sanitizedWebSearchConfigError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("unable to access web search configuration: %s", filepath.Base(err.Error()))
}
