package llm

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestProviderConfigValidate 验证对应功能场景。
func TestProviderConfigValidate(t *testing.T) {
	temp := 0.7
	cfg := ProviderConfig{
		Provider:    ProviderOpenAICompatible,
		APIKey:      "sk-test",
		Model:       "example-chat-model",
		Temperature: &temp,
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

// TestProviderConfigValidateMissingFields 验证对应功能场景。
func TestProviderConfigValidateMissingFields(t *testing.T) {
	tests := []struct {
		name string
		cfg  ProviderConfig
		want error
	}{
		{
			name: "api key",
			cfg: ProviderConfig{
				Provider: ProviderOpenAICompatible,
				Model:    "example-chat-model",
			},
			want: ErrMissingAPIKey,
		},
		{
			name: "model",
			cfg: ProviderConfig{
				Provider: ProviderOpenAICompatible,
				APIKey:   "sk-test",
			},
			want: ErrMissingModel,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.cfg.Validate(); !errors.Is(err, tt.want) {
				t.Fatalf("Validate() error = %v, want %v", err, tt.want)
			}
		})
	}
}

// TestProviderConfigValidateRejectsUnsupportedProvider 验证对应功能场景。
func TestProviderConfigValidateRejectsUnsupportedProvider(t *testing.T) {
	cfg := ProviderConfig{
		Provider: Provider("unknown"),
		APIKey:   "sk-test",
		Model:    "test-model",
	}

	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() error = nil, want unsupported provider")
	}
}

// TestProviderConfigValidateRejectsInvalidBaseURL 验证对应功能场景。
func TestProviderConfigValidateRejectsInvalidBaseURL(t *testing.T) {
	cfg := ProviderConfig{
		Provider: ProviderOpenAICompatible,
		APIKey:   "sk-test",
		BaseURL:  "api.example.com/v1",
		Model:    "example-chat-model",
	}

	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() error = nil, want invalid base_url")
	}
}

func TestProviderConfigAPIFormatDefaultsAndValidation(t *testing.T) {
	defaultConfig := (ProviderConfig{Provider: ProviderOpenAICompatible}).WithDefaults()
	if defaultConfig.APIFormat != APIFormatResponses {
		t.Fatalf("APIFormat = %q, want %q", defaultConfig.APIFormat, APIFormatResponses)
	}

	chatConfig := ProviderConfig{
		Provider:  ProviderOpenAICompatible,
		APIKey:    "sk-test",
		Model:     "test-model",
		APIFormat: APIFormatChatCompletions,
	}
	if err := chatConfig.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	chatConfig.APIFormat = APIFormat("legacy_completions")
	if err := chatConfig.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want unsupported api_format")
	}
}

func TestProviderConfigContextBudgetDefaultsAndValidation(t *testing.T) {
	defaults := (ProviderConfig{Provider: ProviderOpenAICompatible}).WithDefaults()
	if defaults.ContextWindowTokens != DefaultContextWindowTokens || defaults.MaxContextTokens != DefaultMaxContextTokens {
		t.Fatalf("context defaults = window %d max %d", defaults.ContextWindowTokens, defaults.MaxContextTokens)
	}
	if defaults.MaxOutputTokens != 0 {
		t.Fatalf("MaxOutputTokens = %d, want 0 so incompatible gateways do not receive the parameter", defaults.MaxOutputTokens)
	}

	invalid := ProviderConfig{
		Provider:            ProviderOpenAICompatible,
		APIKey:              "sk-test",
		Model:               "test-model",
		ContextWindowTokens: 8192,
		MaxContextTokens:    16384,
	}
	if err := invalid.Validate(); err == nil || !strings.Contains(err.Error(), "cannot exceed") {
		t.Fatalf("Validate() error = %v, want context window error", err)
	}
	if got := invalid.MaxContextTokensWithDefault(); got != 8192 {
		t.Fatalf("effective max context = %d, want 8192", got)
	}
}

// TestNewClientSelectsProvider 验证对应功能场景。
func TestNewClientSelectsProvider(t *testing.T) {
	tests := []struct {
		name     string
		provider Provider
		wantType any
	}{
		{
			name:     "openai-compatible",
			provider: ProviderOpenAICompatible,
			wantType: &openAICompatibleClient{},
		},
		{
			name:     "gemini",
			provider: ProviderGemini,
			wantType: &geminiClient{},
		},
		{
			name:     "anthropic",
			provider: ProviderAnthropic,
			wantType: &anthropicClient{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewClient(ProviderConfig{
				Provider: tt.provider,
				APIKey:   "test-key",
				Model:    "test-model",
			})
			if err != nil {
				t.Fatalf("NewClient() error = %v", err)
			}

			switch tt.wantType.(type) {
			case *openAICompatibleClient:
				if _, ok := client.(*openAICompatibleClient); !ok {
					t.Fatalf("NewClient() = %T, want *openAICompatibleClient", client)
				}
			case *geminiClient:
				if _, ok := client.(*geminiClient); !ok {
					t.Fatalf("NewClient() = %T, want *geminiClient", client)
				}
			case *anthropicClient:
				if _, ok := client.(*anthropicClient); !ok {
					t.Fatalf("NewClient() = %T, want *anthropicClient", client)
				}
			}
		})
	}
}

// TestGenerateRequestDefaults 验证对应功能场景。
func TestGenerateRequestDefaults(t *testing.T) {
	temp := 0.4
	req := GenerateRequest{
		Messages: []Message{{Role: RoleUser, Content: "hello"}},
	}
	got := req.withDefaults(ProviderConfig{
		Model:           "default-model",
		Temperature:     &temp,
		ReasoningEffort: "high",
		MaxOutputTokens: 256,
	})

	if got.Model != "default-model" {
		t.Fatalf("Model = %q, want default-model", got.Model)
	}
	if got.Temperature == nil || *got.Temperature != temp {
		t.Fatalf("Temperature = %v, want %v", got.Temperature, temp)
	}
	if got.MaxOutputTokens != 256 {
		t.Fatalf("MaxOutputTokens = %d, want 256", got.MaxOutputTokens)
	}
	if got.ReasoningEffort != "high" {
		t.Fatalf("ReasoningEffort = %q, want high", got.ReasoningEffort)
	}
}

// TestProviderConfigImageModelWithDefault 验证对应功能场景。
func TestProviderConfigImageModelWithDefault(t *testing.T) {
	tests := []struct {
		name string
		cfg  ProviderConfig
		want string
	}{
		{
			name: "openai default",
			cfg:  ProviderConfig{Provider: ProviderOpenAICompatible},
			want: "gpt-image-2",
		},
		{
			name: "gemini default",
			cfg:  ProviderConfig{Provider: ProviderGemini},
			want: "imagen-4.0-generate-001",
		},
		{
			name: "custom",
			cfg:  ProviderConfig{Provider: ProviderOpenAICompatible, ImageModel: "custom-image"},
			want: "custom-image",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.ImageModelWithDefault(); got != tt.want {
				t.Fatalf("ImageModelWithDefault() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestProviderConfigWithDefaultsUsesProviderModel 验证对应功能场景。
func TestProviderConfigWithDefaultsUsesProviderModel(t *testing.T) {
	tests := []struct {
		provider Provider
		want     string
	}{
		{ProviderOpenAICompatible, DefaultOpenAICompatibleModel},
		{ProviderGemini, DefaultGeminiModel},
		{ProviderAnthropic, DefaultAnthropicModel},
	}

	for _, tt := range tests {
		t.Run(string(tt.provider), func(t *testing.T) {
			got := (ProviderConfig{Provider: tt.provider}).WithDefaults()
			if got.Model != tt.want {
				t.Fatalf("Model = %q, want %q", got.Model, tt.want)
			}
		})
	}
}

// TestListModelsReturnsLocalPresets 验证对应功能场景。
func TestListModelsReturnsLocalPresets(t *testing.T) {
	models, err := ListModels(context.Background(), ProviderConfig{Provider: ProviderAnthropic})
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if len(models) == 0 || models[0].ID != DefaultAnthropicModel {
		t.Fatalf("models = %#v", models)
	}
}

// TestSplitSystemPrompt 验证对应功能场景。
func TestSplitSystemPrompt(t *testing.T) {
	system, messages := splitSystemPrompt([]Message{
		{Role: RoleSystem, Content: "be brief"},
		{Role: RoleUser, Content: "hello"},
		{Role: RoleSystem, Content: "use zh"},
	})

	if system != "be brief\n\nuse zh" {
		t.Fatalf("system = %q", system)
	}
	if len(messages) != 1 || messages[0].Role != RoleUser {
		t.Fatalf("messages = %#v", messages)
	}
}
