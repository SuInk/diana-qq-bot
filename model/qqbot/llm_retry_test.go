package qqbot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"diana-qq-bot/model/agent"
	"diana-qq-bot/model/llm"
)

type retryPreservationTool struct {
	calls int
}

type timeoutSequenceProvider struct {
	calls     int
	deadlines []time.Duration
	succeedAt int
}

type managedTimeoutProvider struct {
	timeoutSequenceProvider
}

func (*managedTimeoutProvider) ManagesAttemptTimeout() bool { return true }

type fixedRetryErrorProvider struct {
	calls int
	err   error
}

func (p *fixedRetryErrorProvider) Generate(context.Context, llm.GenerateRequest) (*llm.GenerateResponse, error) {
	p.calls++
	return nil, p.err
}

func (p *timeoutSequenceProvider) Generate(ctx context.Context, _ llm.GenerateRequest) (*llm.GenerateResponse, error) {
	p.calls++
	if deadline, ok := ctx.Deadline(); ok {
		p.deadlines = append(p.deadlines, time.Until(deadline))
	}
	if p.succeedAt > 0 && p.calls >= p.succeedAt {
		return &llm.GenerateResponse{Text: "ok"}, nil
	}
	return nil, context.DeadlineExceeded
}

func TestTransientLLMTimeoutRetriesThreeTimesWithFullTimeout(t *testing.T) {
	provider := &timeoutSequenceProvider{succeedAt: 4}
	resp, err := generateWithTransientRetryPolicy(context.Background(), provider, llm.GenerateRequest{}, true, 8*time.Second, 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil || resp.Text != "ok" || provider.calls != 4 {
		t.Fatalf("resp=%#v calls=%d", resp, provider.calls)
	}
	want := []time.Duration{8 * time.Second, 8 * time.Second, 8 * time.Second, 8 * time.Second}
	if len(provider.deadlines) != len(want) {
		t.Fatalf("deadlines=%v", provider.deadlines)
	}
	for i := range want {
		if delta := provider.deadlines[i] - want[i]; delta < -50*time.Millisecond || delta > 50*time.Millisecond {
			t.Fatalf("attempt %d timeout=%s want about %s", i+1, provider.deadlines[i], want[i])
		}
	}
}

func TestTransientLLMRetryStopsAfterThreeRetries(t *testing.T) {
	provider := &timeoutSequenceProvider{}
	_, err := generateWithTransientRetryPolicy(context.Background(), provider, llm.GenerateRequest{}, true, time.Second, 3, 0)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
	if provider.calls != 4 {
		t.Fatalf("calls=%d, want initial request plus three retries", provider.calls)
	}
}

func TestDefaultTransientLLMRetryStopsAfterOneRetry(t *testing.T) {
	provider := &timeoutSequenceProvider{}
	_, err := generateWithTransientRetryTimeout(context.Background(), provider, llm.GenerateRequest{}, true, time.Second)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
	if provider.calls != 2 {
		t.Fatalf("calls=%d, want initial request plus one retry", provider.calls)
	}
}

func TestEmptyLLMOutputRetriesOnce(t *testing.T) {
	provider := &fixedRetryErrorProvider{err: fmt.Errorf(
		"llm: openai-compatible chat completions output is empty: %w",
		llm.ErrCompletionEmpty,
	)}
	_, err := generateWithTransientRetryTimeout(context.Background(), provider, llm.GenerateRequest{}, true, time.Second)
	if err == nil || !strings.Contains(err.Error(), "output is empty") {
		t.Fatalf("err=%v", err)
	}
	if provider.calls != 2 {
		t.Fatalf("calls=%d, want initial request plus one retry", provider.calls)
	}
}

func TestClassifiedNoTextErrorsSkipSameProfileRetry(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "truncated", err: fmt.Errorf("model result: %w", llm.ErrCompletionTruncatedNoText)},
		{name: "terminal", err: fmt.Errorf("model result: %w", llm.ErrCompletionHasNoText)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &fixedRetryErrorProvider{err: tt.err}
			_, err := generateWithTransientRetryTimeout(context.Background(), provider, llm.GenerateRequest{}, true, time.Second)
			if !errors.Is(err, tt.err) {
				t.Fatalf("err=%v, want %v", err, tt.err)
			}
			if provider.calls != 1 {
				t.Fatalf("calls=%d, want one attempt", provider.calls)
			}
		})
	}
}

func TestRoutingEmptyOutputRetriesThenFailsOverWithinGroup(t *testing.T) {
	store := &stubLLMProfileStore{set: llm.ProfileSet{
		ActiveID: "main",
		Profiles: []llm.Profile{
			{ID: "main", Group: "default", Config: llm.ProviderConfig{Provider: llm.ProviderOpenAICompatible, Model: "main-model"}},
			{ID: "routing-a", Group: "routing", Config: llm.ProviderConfig{Provider: llm.ProviderOpenAICompatible, Model: "routing-a"}},
			{ID: "routing-b", Group: "routing", Config: llm.ProviderConfig{Provider: llm.ProviderOpenAICompatible, Model: "routing-b"}},
		},
	}}
	primary := &fixedRetryErrorProvider{err: fmt.Errorf(
		"llm: openai-compatible chat completions output is empty: %w",
		llm.ErrCompletionEmpty,
	)}
	secondary := &capturingLLMProvider{reply: `{"reply":true}`}
	runtime := NewRuntime(BotConfig{}, nilChannel{}, NewPluginManager(), store, nil, nil, nil)
	var configuredModels []string
	runtime.SetLLMProviderConfigFactory(func(cfg llm.ProviderConfig) (LLMProvider, error) {
		configuredModels = append(configuredModels, cfg.Model)
		switch cfg.Model {
		case "routing-a":
			return primary, nil
		case "routing-b":
			return secondary, nil
		default:
			return nil, fmt.Errorf("unexpected model %q", cfg.Model)
		}
	})

	reply, err := runtime.runLLMRouterProvider(context.Background(), func(client LLMProvider) (string, error) {
		resp, generateErr := client.Generate(context.Background(), llm.GenerateRequest{})
		if generateErr != nil {
			return "", generateErr
		}
		return resp.Text, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if reply != `{"reply":true}` {
		t.Fatalf("reply=%q", reply)
	}
	if primary.calls != 2 {
		t.Fatalf("primary calls=%d, want initial request plus one retry", primary.calls)
	}
	if got, want := strings.Join(configuredModels, ","), "routing-a,routing-b"; got != want {
		t.Fatalf("configured models=%q, want %q", got, want)
	}
	if store.set.ActiveID != "main" {
		t.Fatalf("routing failover changed active chat profile to %q", store.set.ActiveID)
	}
}

func TestManagedAttemptTimeoutDoesNotReceiveWholeRequestDeadline(t *testing.T) {
	provider := &managedTimeoutProvider{}
	_, err := generateWithTransientRetryPolicy(context.Background(), provider, llm.GenerateRequest{}, false, 8*time.Second, 3, 0)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
	if provider.calls != 1 || len(provider.deadlines) != 0 {
		t.Fatalf("calls=%d deadlines=%v, want one attempt without a wrapper deadline", provider.calls, provider.deadlines)
	}
}

func TestKnownHeaderAndIdleTimeoutsSkipSameProfileRetry(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "response header", err: fmt.Errorf("llm: response header timeout after 60s: %w", context.DeadlineExceeded)},
		{name: "stream idle", err: fmt.Errorf("llm: stream idle timeout after 60s: %w", context.DeadlineExceeded)},
		{name: "json body idle", err: fmt.Errorf("llm: response body idle timeout after 60s: %w", context.DeadlineExceeded)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &fixedRetryErrorProvider{err: tt.err}
			_, err := generateWithTransientRetryPolicy(context.Background(), provider, llm.GenerateRequest{}, true, time.Second, 3, 0)
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("err=%v", err)
			}
			if provider.calls != 1 {
				t.Fatalf("calls=%d, want direct failover after one attempt", provider.calls)
			}
		})
	}
}

func TestTransientLLMRetryDisabledUsesOneFullAttempt(t *testing.T) {
	provider := &timeoutSequenceProvider{}
	_, err := generateWithTransientRetryPolicy(context.Background(), provider, llm.GenerateRequest{}, false, 8*time.Second, 3, 0)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
	if provider.calls != 1 || len(provider.deadlines) != 1 {
		t.Fatalf("calls=%d deadlines=%v", provider.calls, provider.deadlines)
	}
	if delta := provider.deadlines[0] - 8*time.Second; delta < -50*time.Millisecond || delta > 50*time.Millisecond {
		t.Fatalf("timeout=%s want about 8s", provider.deadlines[0])
	}
}

func TestTransientLLMRetrySharesAndDoesNotResetParentDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	parentDeadline, _ := ctx.Deadline()
	provider := &timeoutSequenceProvider{succeedAt: 4}
	if _, err := generateWithTransientRetryPolicy(ctx, provider, llm.GenerateRequest{}, true, 8*time.Second, 3, 0); err != nil {
		t.Fatal(err)
	}
	for i, remaining := range provider.deadlines {
		attemptDeadline := time.Now().Add(remaining)
		if attemptDeadline.After(parentDeadline.Add(50 * time.Millisecond)) {
			t.Fatalf("attempt %d reset parent deadline: attempt=%s parent=%s", i+1, attemptDeadline, parentDeadline)
		}
	}
}

func TestTransientLLMRetryDoesNotExtendCallerDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	before, _ := ctx.Deadline()
	provider := &timeoutSequenceProvider{succeedAt: 2}
	if _, err := generateWithTransientRetryPolicy(ctx, provider, llm.GenerateRequest{}, true, 8*time.Second, 1, 0); err != nil {
		t.Fatal(err)
	}
	after, _ := ctx.Deadline()
	if after != before {
		t.Fatalf("caller deadline changed from %s to %s", before, after)
	}
}

func (t *retryPreservationTool) Name() string { return "test.lookup" }

func (t *retryPreservationTool) Description() string {
	return `测试检索工具。input: {}`
}

func (t *retryPreservationTool) Run(context.Context, map[string]any) (string, error) {
	t.calls++
	return "已经取得的工具证据", nil
}

type retryPreservationProvider struct {
	calls       int
	sawEvidence bool
}

func (p *retryPreservationProvider) Generate(_ context.Context, req llm.GenerateRequest) (*llm.GenerateResponse, error) {
	p.calls++
	switch p.calls {
	case 1:
		return &llm.GenerateResponse{Text: `{"action":"tool","tool":"test.lookup","input":{}}`}, nil
	case 2:
		return nil, fmt.Errorf("request failed: %w", context.DeadlineExceeded)
	case 3:
		for _, message := range req.Messages {
			if strings.Contains(message.Content, "已经取得的工具证据") {
				p.sawEvidence = true
				break
			}
		}
		return &llm.GenerateResponse{Text: `{"action":"final","content":"根据已取得的证据完成回答"}`}, nil
	default:
		return nil, fmt.Errorf("unexpected Generate call %d", p.calls)
	}
}

func TestAgentTransientRetryPreservesCompletedToolResults(t *testing.T) {
	store := &stubLLMProfileStore{set: llm.ProfileSet{
		ActiveID: "main",
		Profiles: []llm.Profile{{
			ID:     "main",
			Group:  "default",
			Config: llm.ProviderConfig{Provider: llm.ProviderOpenAICompatible, Model: "main"},
		}},
	}}
	provider := &retryPreservationProvider{}
	runtime := NewRuntime(BotConfig{}, nilChannel{}, NewPluginManager(), store, nil, nil, nil)
	runtime.SetLLMProviderConfigFactory(func(llm.ProviderConfig) (LLMProvider, error) {
		return provider, nil
	})
	tool := &retryPreservationTool{}
	runCalls := 0
	reply, err := runtime.runLLMProvider(context.Background(), func(client LLMProvider) (string, error) {
		runCalls++
		runner, err := agent.NewRunner(client, agent.Config{WorkDir: t.TempDir(), MaxSteps: 3}, agent.NewToolRegistry(tool))
		if err != nil {
			return "", err
		}
		defer runner.Close()
		response, err := runner.Run(context.Background(), agent.Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: "请检索后回答"}}})
		if err != nil {
			return "", err
		}
		return response.Text, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if reply != "根据已取得的证据完成回答" {
		t.Fatalf("reply = %q", reply)
	}
	if runCalls != 1 || tool.calls != 1 || provider.calls != 3 || !provider.sawEvidence {
		t.Fatalf("runCalls=%d toolCalls=%d providerCalls=%d sawEvidence=%v", runCalls, tool.calls, provider.calls, provider.sawEvidence)
	}
}
