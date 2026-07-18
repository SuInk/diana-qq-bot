package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOpenAICompatibleImageRequestUsesDirectOrigin(t *testing.T) {
	var gotHost string
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{{"b64_json": "YWJjZA=="}},
		})
	}))
	defer server.Close()

	resp, err := GenerateImage(context.Background(), ProviderConfig{
		Provider:     ProviderOpenAICompatible,
		APIKey:       "secret",
		BaseURL:      "https://chat.example.test/v1",
		Model:        "gpt-test",
		ImageModel:   "gpt-image-2",
		ImageBaseURL: "http://image.example.test/v1",
		ImageOrigin:  strings.TrimPrefix(server.URL, "http://"),
		ImageTimeout: 2 * time.Second,
	}, ImageGenerateRequest{Prompt: "画一只猫"}, WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	if gotHost != "image.example.test" || gotPath != "/v1/images/generations" {
		t.Fatalf("host=%q path=%q", gotHost, gotPath)
	}
	if len(resp.Images) != 1 || resp.Images[0] != "data:image/png;base64,YWJjZA==" {
		t.Fatalf("response = %#v", resp)
	}
}

func TestOpenAICompatibleImageRequestUsesIndependentTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"YWJjZA=="}]}`))
	}))
	defer server.Close()

	_, err := GenerateImage(context.Background(), ProviderConfig{
		Provider:     ProviderOpenAICompatible,
		APIKey:       "secret",
		BaseURL:      server.URL + "/v1",
		Model:        "gpt-test",
		ImageModel:   "gpt-image-2",
		ImageTimeout: 20 * time.Millisecond,
	}, ImageGenerateRequest{Prompt: "画一只猫"}, WithHTTPClient(server.Client()))
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "timeout") {
		t.Fatalf("error = %v", err)
	}
}

func TestProviderConfigRejectsInvalidImageOrigin(t *testing.T) {
	cfg := ProviderConfig{
		Provider:    ProviderOpenAICompatible,
		APIKey:      "secret",
		Model:       "gpt-test",
		ImageOrigin: "129.153.75.15",
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "host:port") {
		t.Fatalf("error = %v", err)
	}
}
