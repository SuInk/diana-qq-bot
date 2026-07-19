package webui

import (
	"testing"
	"time"

	"diana-qq-bot/model/llm"
)

func TestLLMImageRouteConfigRoundTrip(t *testing.T) {
	cfg := configFromPayload(llmConfigPayload{
		Provider:       llm.ProviderOpenAICompatible,
		APIKey:         "secret-key",
		BaseURL:        "https://chat.example.test/v1",
		Model:          "gpt-test",
		ImageModel:     "gpt-image-2",
		ImageBaseURL:   "https://image.example.test/v1",
		ImageOrigin:    "203.0.113.10:443",
		ImageTimeoutMS: 600000,
	})
	if cfg.ImageBaseURL != "https://image.example.test/v1" || cfg.ImageOrigin != "203.0.113.10:443" || cfg.ImageTimeout != 10*time.Minute {
		t.Fatalf("config = %#v", cfg)
	}

	payload := payloadFromConfig(cfg)
	if payload.ImageBaseURL != cfg.ImageBaseURL || payload.ImageOrigin != cfg.ImageOrigin || payload.ImageTimeoutMS != 600000 {
		t.Fatalf("payload = %#v", payload)
	}
}
