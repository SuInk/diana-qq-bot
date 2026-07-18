package qqbot

import (
	"context"
	"strings"
	"testing"

	"diana-qq-bot/model/llm"
)

func TestRelationshipEvaluationDecisionValidation(t *testing.T) {
	tests := []struct {
		name  string
		raw   string
		ok    bool
		delta int
	}{
		{name: "semantic update", raw: `{"should_update":true,"delta":-2,"confidence":0.91,"reason":"明确针对机器人的攻击"}`, ok: true, delta: -2},
		{name: "code fence", raw: "```json\n{\"should_update\":true,\"delta\":2,\"confidence\":0.8,\"reason\":\"真诚感谢\"}\n```", ok: true, delta: 2},
		{name: "no update normalizes delta", raw: `{"should_update":false,"delta":3,"confidence":0.99,"reason":"只是讨论规则"}`, ok: true, delta: 0},
		{name: "low confidence applies zero", raw: `{"should_update":true,"delta":3,"confidence":0.5,"reason":"不确定"}`, ok: true, delta: 0},
		{name: "out of range delta", raw: `{"should_update":true,"delta":4,"confidence":0.9,"reason":"bad"}`, ok: false},
		{name: "missing reason", raw: `{"should_update":true,"delta":1,"confidence":0.9}`, ok: false},
		{name: "invalid json", raw: `{"should_update":true`, ok: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision, ok := parseRelationshipEvaluationDecision(test.raw)
			if ok != test.ok {
				t.Fatalf("decision=%#v ok=%v, want %v", decision, ok, test.ok)
			}
			if ok && decision.effectiveDelta() != test.delta {
				t.Fatalf("effective delta=%d, want %d: %#v", decision.effectiveDelta(), test.delta, decision)
			}
		})
	}
}

func TestRelationshipEvaluationUsesRouterSemantics(t *testing.T) {
	provider := &capturingLLMProvider{reply: `{"should_update":false,"delta":0,"confidence":0.98,"reason":"消息在讨论计分规则，并非攻击机器人"}`}
	store := &stubLLMProfileStore{set: llm.ProfileSet{
		ActiveID: "main",
		Profiles: []llm.Profile{
			{ID: "main", Name: "主聊天", Group: "chat", Config: llm.ProviderConfig{Provider: llm.ProviderOpenAICompatible, APIKey: "main-key", Model: "main-model"}},
			{ID: "routing", Name: "快速语义判定", Group: "routing", Config: llm.ProviderConfig{Provider: llm.ProviderOpenAICompatible, APIKey: "routing-key", Model: "routing-model"}},
		},
	}}
	memory := newMemoryUserMemoryStore()
	memory.profiles["user"] = UserMemoryProfile{UserID: "user", Favorability: 17, MessageCount: 8}
	runtime := NewRuntime(BotConfig{BotQQ: "bot", OwnerID: "owner"}, nilChannel{}, NewPluginManager(), store, nil, nil, nil)
	runtime.SetUserMemoryStore(memory)
	var usedModel string
	runtime.SetLLMProviderConfigFactory(func(cfg llm.ProviderConfig) (LLMProvider, error) {
		usedModel = cfg.Model
		return provider, nil
	})
	event := MessageEvent{
		Kind:       EventKindGroup,
		GroupID:    "group",
		UserID:     "user",
		MessageID:  "message",
		SenderName: "Alice",
		ToMe:       true,
		RawMessage: "还是说骂笨蛋，然后减几滴",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "还是说骂笨蛋，然后减几滴"}}},
	}
	decision, before, evaluated := runtime.evaluateRelationshipUpdate(context.Background(), event, PlainText(event.Segments), true)
	if !evaluated || decision.effectiveDelta() != 0 || before.Favorability != 17 {
		t.Fatalf("decision=%#v before=%#v evaluated=%v", decision, before, evaluated)
	}
	if usedModel != "routing-model" {
		t.Fatalf("used model = %q", usedModel)
	}
	if !strings.Contains(provider.request.Messages[0].Content, "不得按关键词") || !strings.Contains(provider.request.Messages[1].Content, event.RawMessage) || !strings.Contains(provider.request.Messages[1].Content, `"natural_interaction_gain_enabled":true`) {
		t.Fatalf("evaluation request = %#v", provider.request.Messages)
	}
}

func TestRelationshipEvaluationAllowsNaturalInteractionBeforeThreshold(t *testing.T) {
	provider := &capturingLLMProvider{reply: `{"should_update":true,"delta":1,"confidence":0.96,"reason":"初识阶段的一次真实提问会带来轻微熟悉"}`}
	memory := newMemoryUserMemoryStore()
	memory.profiles["user"] = UserMemoryProfile{UserID: "user", Favorability: 19, MessageCount: 8}
	runtime := NewRuntime(BotConfig{BotQQ: "bot", OwnerID: "owner"}, nilChannel{}, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	runtime.SetUserMemoryStore(memory)
	event := MessageEvent{
		Kind:       EventKindPrivate,
		UserID:     "user",
		MessageID:  "message",
		SenderName: "Alice",
		RawMessage: "今天有什么适合散步的地方？",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "今天有什么适合散步的地方？"}}},
	}

	decision, before, evaluated := runtime.evaluateRelationshipUpdate(context.Background(), event, PlainText(event.Segments), true)
	if !evaluated || decision.effectiveDelta() != 1 || before.Favorability != 19 {
		t.Fatalf("decision=%#v before=%#v evaluated=%v", decision, before, evaluated)
	}
	if !requestMessagesContain(provider.request.Messages, `"natural_interaction_gain_enabled":true`) || !requestMessagesContain(provider.request.Messages, `"natural_interaction_threshold":20`) {
		t.Fatalf("natural interaction phase missing: %#v", provider.request.Messages)
	}
	if !requestMessagesContain(provider.request.Messages, "默认应 should_update=true、delta=1") || !requestMessagesContain(provider.request.Messages, "不能仅以“普通提问”“功能请求”或“任务指令”为理由判为 0") {
		t.Fatalf("natural interaction rule is not explicit enough: %#v", provider.request.Messages)
	}
}

func TestRuntimeAppliesNaturalInteractionFavorability(t *testing.T) {
	provider := &sequenceLLMProvider{replies: []string{`{"should_update":true,"delta":1,"confidence":0.96,"reason":"初识阶段的真实任务互动"}`}}
	memory := newMemoryUserMemoryStore()
	channel := &recordingChannel{}
	runtime := NewRuntime(BotConfig{BotQQ: "bot", OwnerID: "owner"}, channel, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	runtime.SetUserMemoryStore(memory)
	logs := &captureAppLogs{}
	runtime.SetAppLogWriter(logs)
	event := MessageEvent{
		Kind:       EventKindPrivate,
		UserID:     "user",
		MessageID:  "message",
		SenderName: "Alice",
		RawMessage: "帮我整理一下今天的学习计划",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "帮我整理一下今天的学习计划"}}},
	}

	_, _, handled, outcome := runtime.prepareMessageEvent(context.Background(), event)
	profile := memory.profiles[event.UserID]
	if !handled || outcome != "replied" || profile.Favorability != 1 || profile.MessageCount != 1 {
		t.Fatalf("handled=%v outcome=%q profile=%#v", handled, outcome, profile)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("provider calls = %d, want one relationship evaluation", len(provider.requests))
	}
	if len(logs.entries) != 1 || logs.entries[0].Action != "qqbot.relationship_evaluation" || logs.entries[0].Metadata["delta"] != 1 {
		t.Fatalf("logs = %#v", logs.entries)
	}
}

func TestRelationshipEvaluationDisablesNaturalInteractionAtThreshold(t *testing.T) {
	provider := &capturingLLMProvider{reply: `{"should_update":false,"delta":0,"confidence":0.98,"reason":"已达到自然熟悉阈值，普通提问不再加分"}`}
	memory := newMemoryUserMemoryStore()
	memory.profiles["user"] = UserMemoryProfile{UserID: "user", Favorability: naturalInteractionFavorabilityThreshold, MessageCount: 10}
	runtime := NewRuntime(BotConfig{BotQQ: "bot", OwnerID: "owner"}, nilChannel{}, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	runtime.SetUserMemoryStore(memory)
	event := MessageEvent{
		Kind:       EventKindPrivate,
		UserID:     "user",
		MessageID:  "message",
		SenderName: "Alice",
		RawMessage: "今天有什么适合散步的地方？",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "今天有什么适合散步的地方？"}}},
	}

	decision, _, evaluated := runtime.evaluateRelationshipUpdate(context.Background(), event, PlainText(event.Segments), true)
	if !evaluated || decision.effectiveDelta() != 0 {
		t.Fatalf("decision=%#v evaluated=%v", decision, evaluated)
	}
	if !requestMessagesContain(provider.request.Messages, `"natural_interaction_gain_enabled":false`) {
		t.Fatalf("natural interaction phase should be disabled: %#v", provider.request.Messages)
	}
}

func TestRelationshipQuestionUsesNormalLLMReply(t *testing.T) {
	provider := &sequenceLLMProvider{replies: []string{
		`{"action":"none","prompt":""}`,
		"按我们最近的相处来看，现在是朋友；你离下一阶段还差一点稳定互动。",
	}}
	channel := &recordingChannel{}
	runtime := NewRuntime(BotConfig{AgentEnabled: false, OwnerID: "owner"}, channel, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	memory := newMemoryUserMemoryStore()
	memory.profiles["user"] = UserMemoryProfile{UserID: "user", DisplayName: "Alice", Favorability: 60, MessageCount: 30}
	runtime.SetUserMemoryStore(memory)
	event := MessageEvent{
		Kind:       EventKindPrivate,
		UserID:     "user",
		MessageID:  "question",
		SenderName: "Alice",
		RawMessage: "我和最高关系还有哪些差距？",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "我和最高关系还有哪些差距？"}}},
	}
	reply, err := runtime.replyTo(context.Background(), event, PlainText(event.Segments))
	if err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) != 2 || reply != "按我们最近的相处来看，现在是朋友；你离下一阶段还差一点稳定互动。" {
		t.Fatalf("reply=%q requests=%#v", reply, provider.requests)
	}
	if !requestMessagesContain(provider.requests[1].Messages, "好感度：60") {
		t.Fatalf("relationship context missing: %#v", provider.requests[1].Messages)
	}
}

func requestMessagesContain(messages []llm.Message, want string) bool {
	for _, message := range messages {
		if strings.Contains(message.Content, want) {
			return true
		}
	}
	return false
}
