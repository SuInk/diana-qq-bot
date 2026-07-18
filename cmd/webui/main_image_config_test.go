package main

import (
	"testing"
	"time"
)

func TestLLMConfigFromEnvUsesImageRouteOverrides(t *testing.T) {
	t.Setenv("LLM_PROVIDER", "openai_compatible")
	t.Setenv("LLM_API_KEY", "test-api-key")
	t.Setenv("LLM_MODEL", "gpt-test")
	t.Setenv("LLM_IMAGE_BASE_URL", "https://image.example.test/v1")
	t.Setenv("LLM_IMAGE_ORIGIN", "129.153.75.15:443")
	t.Setenv("LLM_IMAGE_TIMEOUT_MS", "600000")

	cfg := llmConfigFromEnv()
	if cfg.ImageBaseURL != "https://image.example.test/v1" || cfg.ImageOrigin != "129.153.75.15:443" || cfg.ImageTimeout != 10*time.Minute {
		t.Fatalf("config = %#v", cfg)
	}
}
