package qqbot

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"diana-qq-bot/model/llm"
)

type transientRetryLLMProvider struct {
	provider LLMProvider
}

type llmAttemptTimeoutManager interface {
	ManagesAttemptTimeout() bool
}

func withTransientLLMRetry(provider LLMProvider, enabled bool) LLMProvider {
	if provider == nil || !enabled {
		return provider
	}
	return &transientRetryLLMProvider{provider: provider}
}

func (p *transientRetryLLMProvider) Generate(ctx context.Context, req llm.GenerateRequest) (*llm.GenerateResponse, error) {
	return generateWithTransientRetry(ctx, p.provider, req, true)
}

func generateWithTransientRetry(ctx context.Context, provider LLMProvider, req llm.GenerateRequest, enabled bool) (*llm.GenerateResponse, error) {
	return generateWithTransientRetryTimeout(ctx, provider, req, enabled, 0)
}

func generateWithTransientRetryTimeout(ctx context.Context, provider LLMProvider, req llm.GenerateRequest, enabled bool, requestTimeout time.Duration) (*llm.GenerateResponse, error) {
	return generateWithTransientRetryPolicy(ctx, provider, req, enabled, requestTimeout, llmTransientMaxRetries, llmTransientRetryDelay)
}

// Non-streaming providers keep the configured per-request timeout. Providers
// with activity-aware timeouts manage their own attempts. All attempts still
// share the caller deadline, so cancellation is respected and never reset.
func generateWithTransientRetryPolicy(ctx context.Context, provider LLMProvider, req llm.GenerateRequest, enabled bool, requestTimeout time.Duration, maxRetries int, retryDelay time.Duration) (*llm.GenerateResponse, error) {
	if provider == nil {
		return nil, fmt.Errorf("qqbot: llm provider is not configured")
	}
	if manager, ok := provider.(llmAttemptTimeoutManager); ok && manager.ManagesAttemptTimeout() {
		requestTimeout = 0
	}
	if !enabled {
		maxRetries = 0
	}
	if maxRetries < 0 {
		maxRetries = 0
	}
	attemptTimeout := boundedInitialRetryTimeout(ctx, requestTimeout, maxRetries, retryDelay)
	for attempt := 0; ; attempt++ {
		attemptCtx := ctx
		cancel := func() {}
		if attemptTimeout > 0 {
			attemptCtx, cancel = context.WithTimeout(ctx, attemptTimeout)
		}
		resp, err := provider.Generate(attemptCtx, req)
		cancel()
		if err == nil || !enabled || !shouldRetryTransientLLMError(err) {
			return resp, err
		}
		if shouldFailoverWithoutSameProfileRetry(err) {
			return resp, err
		}
		if attempt >= maxRetries {
			return resp, err
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if retryDelay <= 0 {
			continue
		}
		timer := time.NewTimer(retryDelay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func shouldFailoverWithoutSameProfileRetry(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	for _, marker := range []string{
		"response header timeout",
		"response body idle timeout",
		"stream idle timeout",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func boundedInitialRetryTimeout(ctx context.Context, configured time.Duration, maxRetries int, retryDelay time.Duration) time.Duration {
	if configured <= 0 {
		return 0
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return configured
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return time.Nanosecond
	}
	if llmRetryBudget(configured, maxRetries, retryDelay) <= remaining {
		return configured
	}
	available := remaining - time.Duration(maxRetries)*retryDelay
	if available <= 0 {
		return remaining
	}
	bounded := available / time.Duration(maxRetries+1)
	if bounded <= 0 {
		return time.Nanosecond
	}
	if bounded < configured {
		return bounded
	}
	return configured
}

func llmRetryBudget(requestTimeout time.Duration, maxRetries int, retryDelay time.Duration) time.Duration {
	total := time.Duration(maxRetries+1) * requestTimeout
	if maxRetries > 0 {
		total += time.Duration(maxRetries) * retryDelay
	}
	return total
}

// profileFailoverLLMProvider retries and switches profiles for one Generate
// call. The caller's Agent loop stays alive, so completed tool observations are
// preserved when a later model step is retried.
type profileFailoverLLMProvider struct {
	mu             sync.Mutex
	profiles       []llm.Profile
	factory        LLMProviderConfigFactory
	retryTransient bool
	activate       func(string)
	wrapGroupError bool
	group          string
	current        int
	clients        []LLMProvider
	clientErrors   []error
	clientLoaded   []bool
}

func newProfileFailoverLLMProvider(
	profiles []llm.Profile,
	factory LLMProviderConfigFactory,
	retryTransient bool,
	activate func(string),
	wrapGroupError bool,
) (*profileFailoverLLMProvider, error) {
	if len(profiles) == 0 {
		return nil, fmt.Errorf("qqbot: no llm profile is configured")
	}
	if factory == nil {
		return nil, fmt.Errorf("qqbot: llm provider factory is not configured")
	}
	group := llm.NormalizeProfileGroup(profiles[0].Group)
	return &profileFailoverLLMProvider{
		profiles:       append([]llm.Profile(nil), profiles...),
		factory:        factory,
		retryTransient: retryTransient,
		activate:       activate,
		wrapGroupError: wrapGroupError,
		group:          group,
		clients:        make([]LLMProvider, len(profiles)),
		clientErrors:   make([]error, len(profiles)),
		clientLoaded:   make([]bool, len(profiles)),
	}, nil
}

func (p *profileFailoverLLMProvider) Generate(ctx context.Context, req llm.GenerateRequest) (*llm.GenerateResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	var lastErr error
	for offset := 0; offset < len(p.profiles); offset++ {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		index := (p.current + offset) % len(p.profiles)
		client, err := p.client(index)
		if err == nil {
			var resp *llm.GenerateResponse
			resp, err = generateWithTransientRetryTimeout(ctx, client, req, p.retryTransient, p.profiles[index].Config.Timeout)
			if err == nil {
				p.current = index
				if p.activate != nil {
					p.activate(p.profiles[index].ID)
				}
				return resp, nil
			}
		}
		lastErr = err
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if !shouldFailoverLLMError(err) {
			return nil, err
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("unknown llm profile error")
	}
	if p.wrapGroupError && len(p.profiles) > 1 {
		return nil, fmt.Errorf("qqbot: llm profiles in group %q are unavailable: %w", p.group, lastErr)
	}
	return nil, lastErr
}

func (p *profileFailoverLLMProvider) client(index int) (LLMProvider, error) {
	if p.clientLoaded[index] {
		return p.clients[index], p.clientErrors[index]
	}
	p.clientLoaded[index] = true
	p.clients[index], p.clientErrors[index] = p.factory(p.profiles[index].Config)
	return p.clients[index], p.clientErrors[index]
}

func activateLLMProfile(store LLMProfileStore, profileID string) {
	if store == nil || profileID == "" {
		return
	}
	set := store.Profiles().WithDefaults()
	if set.ActiveID == profileID {
		return
	}
	set.ActiveID = profileID
	store.SaveProfiles(set)
}
