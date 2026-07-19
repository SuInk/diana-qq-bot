package main

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"diana-qq-bot/model/llm"
	"github.com/gin-gonic/gin"
)

// TestLLMConfigFromEnvUsesProviderDefaultModel 验证对应功能场景。
func TestLLMConfigFromEnvUsesProviderDefaultModel(t *testing.T) {
	t.Setenv("LLM_PROVIDER", "gemini")
	t.Setenv("LLM_MODEL", "")

	cfg := llmConfigFromEnv()
	if cfg.Provider != llm.ProviderGemini {
		t.Fatalf("Provider = %q", cfg.Provider)
	}
	if cfg.Model != llm.DefaultGeminiModel {
		t.Fatalf("Model = %q, want %q", cfg.Model, llm.DefaultGeminiModel)
	}
}

// TestLLMConfigFromEnvUsesOpenAICompatibleOverrides 验证 BaseURL/Key/生图模型来自环境变量。
func TestLLMConfigFromEnvUsesOpenAICompatibleOverrides(t *testing.T) {
	t.Setenv("LLM_PROVIDER", "openai_compatible")
	t.Setenv("LLM_API_KEY", "test-api-key")
	t.Setenv("LLM_BASE_URL", "https://api.example.test/v1")
	t.Setenv("LLM_API_FORMAT", "chat_completions")
	t.Setenv("LLM_MODEL", "gpt-test")
	t.Setenv("LLM_IMAGE_MODEL", "gpt-image-test")
	t.Setenv("LLM_REASONING_EFFORT", "high")
	t.Setenv("LLM_CONTEXT_WINDOW_TOKENS", "200000")
	t.Setenv("LLM_MAX_CONTEXT_TOKENS", "12000")

	cfg := llmConfigFromEnv()
	if cfg.Provider != llm.ProviderOpenAICompatible {
		t.Fatalf("Provider = %q", cfg.Provider)
	}
	if cfg.APIKey != "test-api-key" || cfg.BaseURL != "https://api.example.test/v1" || cfg.APIFormat != llm.APIFormatChatCompletions || cfg.Model != "gpt-test" || cfg.ImageModel != "gpt-image-test" || cfg.ReasoningEffort != "high" || cfg.ContextWindowTokens != 200000 || cfg.MaxContextTokens != 12000 {
		t.Fatalf("unexpected env config: %#v", cfg)
	}
}

func TestLimitRequestBodyRejectsKnownOversizeBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(limitRequestBody(8))
	router.POST("/", func(c *gin.Context) {
		_, _ = io.ReadAll(c.Request.Body)
		c.Status(http.StatusNoContent)
	})

	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("123456789"))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestSetupLoggingRestrictsFilePermissions(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "private-logs", "app.log")
	t.Setenv("LOG_PATH", logPath)
	previousWriter := log.Writer()
	defer log.SetOutput(previousWriter)

	_, closeLog := setupLogging()
	closeLog()
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("log mode = %#o, want 0600", got)
	}
	dirInfo, err := os.Stat(filepath.Dir(logPath))
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("log directory mode = %#o, want 0700", got)
	}
}
