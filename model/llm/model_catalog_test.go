package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestModelInfoFromPayloadReadsContextLimits(t *testing.T) {
	model := modelInfoFromPayload(map[string]any{
		"id": "test-model",
		"limit": map[string]any{
			"context": float64(200000),
			"input":   float64(180000),
			"output":  float64(20000),
		},
	})
	if model.ContextWindowTokens != 200000 || model.MaxInputTokens != 180000 || model.MaxOutputTokens != 20000 {
		t.Fatalf("model = %#v", model)
	}
}

func TestModelsDevCatalogEnrichesOpenCodeGoAndCaches(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"opencode-go":{"models":{"deepseek-v4-flash":{"name":"DeepSeek V4 Flash","limit":{"context":1000000,"input":900000,"output":384000}}}}}`))
	}))
	defer server.Close()

	catalog := newModelsDevCatalog(server.Client(), server.URL)
	cfg := ProviderConfig{
		Provider: ProviderOpenAICompatible,
		BaseURL:  "https://opencode.ai/zen/go/v1",
	}
	models := []ModelInfo{{ID: "deepseek-v4-flash", OwnedBy: "opencode"}}
	for attempt := 0; attempt < 2; attempt++ {
		got := catalog.Enrich(context.Background(), cfg, models)
		if len(got) != 1 || got[0].ContextWindowTokens != 1000000 || got[0].MaxInputTokens != 900000 || got[0].MaxOutputTokens != 384000 {
			t.Fatalf("models = %#v", got)
		}
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("catalog requests = %d, want 1", got)
	}
}

func TestModelsDevCatalogDoesNotGuessCustomGatewayProvider(t *testing.T) {
	catalog := newModelsDevCatalog(http.DefaultClient, "https://models.invalid/api.json")
	models := []ModelInfo{{ID: "gpt-5.6-sol"}}
	got := catalog.Enrich(context.Background(), ProviderConfig{
		Provider: ProviderOpenAICompatible,
		BaseURL:  "https://custom.example/v1",
	}, models)
	if len(got) != 1 || got[0].ContextWindowTokens != 0 {
		t.Fatalf("models = %#v", got)
	}
}
