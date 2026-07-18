package qqbot

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"diana-qq-bot/model/llm"
)

func TestPassiveReplyBatchRoutesOnceAndSelectsTarget(t *testing.T) {
	provider := &sequenceLLMProvider{replies: []string{
		`{"should_reply":true,"confidence":0.97,"category":"needs_response","target_message_id":"message-1","turn_message_ids":["message-1"],"directed_at_bot":false,"answerable":true}`,
	}}
	runtime := NewRuntime(BotConfig{
		BotQQ:                 "42",
		PassiveReplyChance:    1,
		PassiveReplyThreshold: 0.8,
	}, nilChannel{}, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	candidates := []passiveReplyCandidate{
		{
			Event: MessageEvent{Kind: EventKindGroup, GroupID: "group-1", UserID: "user-1", MessageID: "message-1", SenderName: "Alice"},
			Text:  "这个报错应该怎么处理",
		},
		{
			Event: MessageEvent{Kind: EventKindGroup, GroupID: "group-1", UserID: "user-2", MessageID: "message-2", SenderName: "Bob"},
			Text:  "我先去吃饭了",
		},
	}

	event, text, turn, allowed := runtime.routePassiveReplyBatch(context.Background(), candidates)
	if !allowed {
		t.Fatal("batch route should allow the selected question")
	}
	if event.MessageID != "message-1" || text != "这个报错应该怎么处理" {
		t.Fatalf("selected event = %q text = %q", event.MessageID, text)
	}
	if len(turn) != 1 || turn[0].Event.MessageID != "message-1" {
		t.Fatalf("selected turn = %#v, want only message-1", turn)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("router calls = %d, want 1", len(provider.requests))
	}
	requestText := provider.requests[0].Messages[len(provider.requests[0].Messages)-1].Content
	for _, want := range []string{`"message_id":"message-1"`, `"user_id":"user-1"`, `"message_id":"message-2"`, `"user_id":"user-2"`} {
		if !strings.Contains(requestText, want) {
			t.Fatalf("batch payload missing %s: %s", want, requestText)
		}
	}
	routePrompt := provider.requests[0].Messages[0].Content
	for _, want := range []string{"最近 15 秒内最多 3 条候选", "不能仅凭同一发送者或时间相邻就合并", "turn_message_ids", "连续补充的多个问题", "禁止换一种说法重复回答"} {
		if !strings.Contains(routePrompt, want) {
			t.Fatalf("batch route prompt missing %q: %s", want, routePrompt)
		}
	}
}

func TestPassiveReplyBatchUsesConfiguredRouterPrompt(t *testing.T) {
	provider := &sequenceLLMProvider{replies: []string{
		`{"should_reply":true,"confidence":0.97,"category":"needs_response","target_message_id":"message-1","turn_message_ids":["message-1"],"directed_at_bot":false,"answerable":true}`,
	}}
	runtime := NewRuntime(BotConfig{
		BotQQ:                    "42",
		PassiveReplyChance:       1,
		PassiveReplyThreshold:    0.8,
		PassiveReplyRouterPrompt: "custom passive router prompt",
	}, nilChannel{}, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	candidate := passiveReplyCandidate{
		Event: MessageEvent{Kind: EventKindGroup, GroupID: "group-1", UserID: "user-1", MessageID: "message-1"},
		Text:  "这个报错应该怎么处理",
	}

	_, _, _, allowed := runtime.routePassiveReplyBatch(context.Background(), []passiveReplyCandidate{candidate})
	if !allowed {
		t.Fatal("configured passive router should allow the selected question")
	}
	if len(provider.requests) != 1 || len(provider.requests[0].Messages) == 0 {
		t.Fatalf("router requests = %#v", provider.requests)
	}
	if got := provider.requests[0].Messages[0].Content; !strings.Contains(got, "custom passive router prompt") {
		t.Fatalf("router prompt = %q", got)
	}
}

func TestPassiveReplyBatchSelectsCompleteSemanticTurn(t *testing.T) {
	provider := &sequenceLLMProvider{replies: []string{
		`{"should_reply":true,"confidence":0.99,"category":"bot_related","target_message_id":"message-3","turn_message_ids":["message-1","message-2","message-3"],"directed_at_bot":true,"answerable":true}`,
	}}
	runtime := NewRuntime(BotConfig{
		BotQQ:                 "42",
		PassiveReplyChance:    1,
		PassiveReplyThreshold: 0.9,
	}, nilChannel{}, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	candidates := []passiveReplyCandidate{
		{Event: MessageEvent{Kind: EventKindGroup, GroupID: "group-1", UserID: "user-1", MessageID: "message-1"}, Text: "1+1"},
		{Event: MessageEvent{Kind: EventKindGroup, GroupID: "group-1", UserID: "user-1", MessageID: "message-2"}, Text: "5+6"},
		{Event: MessageEvent{Kind: EventKindGroup, GroupID: "group-1", UserID: "user-1", MessageID: "message-3"}, Text: "4+8"},
	}

	event, text, turn, allowed := runtime.routePassiveReplyBatch(context.Background(), candidates)
	if !allowed || event.MessageID != "message-3" || text != "4+8" {
		t.Fatalf("route = event %q text %q allowed %v", event.MessageID, text, allowed)
	}
	if len(turn) != 3 {
		t.Fatalf("turn = %#v, want all three messages", turn)
	}
	for index, want := range []string{"message-1", "message-2", "message-3"} {
		if turn[index].Event.MessageID != want {
			t.Fatalf("turn[%d] = %q, want %q", index, turn[index].Event.MessageID, want)
		}
	}
}

func TestPassiveReplyTurnCombinesThreeMessagesIntoOneReply(t *testing.T) {
	provider := &sequenceLLMProvider{replies: []string{
		`{"action":"none","prompt":"","tools":[],"context_message_ids":[],"keep_older_summary":false}`,
		"1+1=2，5+6=11，4+8=12。",
	}}
	channel := &recordingChannel{}
	runtime := NewRuntime(BotConfig{
		AgentEnabled:   false,
		RequestTimeout: time.Minute,
	}, channel, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	events := []MessageEvent{
		{Kind: EventKindGroup, GroupID: "group-1", UserID: "user-1", MessageID: "message-1", Time: 100, SenderName: "Alice", RawMessage: "1+1", Segments: []MessageSegment{{Type: "text", Data: map[string]string{"text": "1+1"}}}},
		{Kind: EventKindGroup, GroupID: "group-1", UserID: "user-1", MessageID: "message-2", Time: 104, SenderName: "Alice", RawMessage: "5+6", Segments: []MessageSegment{{Type: "text", Data: map[string]string{"text": "5+6"}}}},
		{Kind: EventKindGroup, GroupID: "group-1", UserID: "user-1", MessageID: "message-3", Time: 106, SenderName: "Alice", RawMessage: "4+8", Segments: []MessageSegment{{Type: "text", Data: map[string]string{"text": "4+8"}}}},
	}
	turn := make([]passiveReplyCandidate, 0, len(events))
	for _, event := range events {
		turn = append(turn, passiveReplyCandidate{Event: event, Text: event.RawMessage})
	}
	ctx := withPassiveReplyTurnContext(context.Background(), turn)
	reply, err := runtime.replyTo(ctx, events[2], events[2].RawMessage)
	if err != nil {
		t.Fatalf("replyTo() error = %v", err)
	}
	if reply != "1+1=2，5+6=11，4+8=12。" {
		t.Fatalf("reply = %q", reply)
	}
	if len(channel.sent) != 1 || channel.sent[0].Text != reply {
		t.Fatalf("sent = %#v, want one combined QQ message", channel.sent)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("provider calls = %d, want intent route and final reply", len(provider.requests))
	}
	finalRequest := provider.requests[len(provider.requests)-1]
	joined := make([]string, 0, len(finalRequest.Messages))
	for _, message := range finalRequest.Messages {
		joined = append(joined, message.Content)
	}
	prompt := strings.Join(joined, "\n")
	for _, want := range []string{"覆盖这一轮里的全部实质问题", "【当前同轮补充消息", "1+1", "5+6", "【当前需要回复的消息】", "4+8"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("final prompt missing %q: %s", want, prompt)
		}
	}
}

func TestPassiveReplyDecisionCandidatesUsesBoundedRecentWindow(t *testing.T) {
	base := time.Now()
	items := []passiveReplyCandidate{
		{Event: MessageEvent{MessageID: "message-1"}, QueuedAt: base.Add(-30 * time.Second)},
		{Event: MessageEvent{MessageID: "message-2"}, QueuedAt: base.Add(-12 * time.Second)},
		{Event: MessageEvent{MessageID: "message-3"}, QueuedAt: base.Add(-8 * time.Second)},
		{Event: MessageEvent{MessageID: "message-4"}, QueuedAt: base.Add(-4 * time.Second)},
		{Event: MessageEvent{MessageID: "message-5"}, QueuedAt: base},
	}

	got := passiveReplyDecisionCandidates(items)
	if len(got) != 3 {
		t.Fatalf("bounded candidates = %d, want 3: %#v", len(got), got)
	}
	for i, want := range []string{"message-3", "message-4", "message-5"} {
		if got[i].Event.MessageID != want {
			t.Fatalf("candidate %d = %q, want %q", i, got[i].Event.MessageID, want)
		}
	}

	windowed := passiveReplyDecisionCandidates([]passiveReplyCandidate{
		{Event: MessageEvent{MessageID: "old"}, QueuedAt: base.Add(-20 * time.Second)},
		{Event: MessageEvent{MessageID: "recent"}, QueuedAt: base},
	})
	if len(windowed) != 1 || windowed[0].Event.MessageID != "recent" {
		t.Fatalf("windowed candidates = %#v, want only recent", windowed)
	}
}

func TestPassiveReplyBatchReroutesOnceBeforeSending(t *testing.T) {
	channel := &recordingChannel{}
	first := MessageEvent{
		Kind:       EventKindGroup,
		GroupID:    "group-1",
		UserID:     "user-1",
		MessageID:  "message-1",
		Time:       100,
		SenderName: "Alice",
		RawMessage: "这不是 MyGO 里面的吗",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "这不是 MyGO 里面的吗"}}},
	}
	second := MessageEvent{
		Kind:       EventKindGroup,
		GroupID:    "group-1",
		UserID:     "user-1",
		MessageID:  "message-2",
		Time:       105,
		SenderName: "Alice",
		RawMessage: "[图片]",
		Segments:   []MessageSegment{{Type: "image", Data: map[string]string{"url": "data:image/jpeg;base64,aGVsbG8="}}},
	}
	provider := &passiveReplyRerouteProvider{second: second}
	runtime := NewRuntime(BotConfig{
		BotQQ:                 "42",
		OwnerID:               "owner",
		AgentEnabled:          false,
		PassiveReplyChance:    1,
		PassiveReplyThreshold: 0.8,
	}, channel, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	provider.runtime = runtime
	logs := &captureAppLogs{}
	runtime.SetAppLogWriter(logs)
	runtime.mu.Lock()
	runtime.running = true
	runtime.runCtx = context.Background()
	runtime.mu.Unlock()
	runtime.remember(first)
	key := sessionKey(first)
	runtime.passiveBatches[key] = &passiveReplyBatch{
		items: []passiveReplyCandidate{{
			Event:      first,
			Text:       first.RawMessage,
			QueuedAt:   time.Now(),
			Generation: 1,
		}},
		startedAt:  time.Now(),
		generation: 1,
	}

	runtime.flushPassiveReplyBatch(context.Background(), key, 1)

	channel.mu.Lock()
	sent := append([]OutgoingMessage(nil), channel.sent...)
	channel.mu.Unlock()
	if len(sent) != 1 {
		t.Fatalf("sent = %#v, want exactly one merged reply", sent)
	}
	if sent[0].ReplyMessageID != second.MessageID || sent[0].Text != "对，后一张是要乐奈，前一条和图片应当合在一起看。" {
		t.Fatalf("merged reply = %#v, want reply to the later message", sent[0])
	}
	if provider.routeCalls != 2 || provider.replyCalls != 2 {
		t.Fatalf("route calls=%d reply calls=%d, want one bounded reroute", provider.routeCalls, provider.replyCalls)
	}
	if !strings.Contains(provider.lastRoutePayload, `"message_id":"message-1"`) || !strings.Contains(provider.lastRoutePayload, `"message_id":"message-2"`) {
		t.Fatalf("reroute did not receive both candidates: %s", provider.lastRoutePayload)
	}
	var superseded bool
	for _, entry := range logs.entries {
		if entry.Action == "qqbot.passive_reply_superseded" && entry.Metadata["stage"] == "before_send" {
			superseded = true
		}
	}
	if !superseded {
		t.Fatalf("superseded audit log missing: %#v", logs.entries)
	}
	runtime.passiveBatchMu.Lock()
	_, pending := runtime.passiveBatches[key]
	runtime.passiveBatchMu.Unlock()
	if pending {
		t.Fatal("merged passive batch was not cleared")
	}
}

func TestPassiveReplyBatchCollectsPerGroupAndCanBeCancelled(t *testing.T) {
	runtime := NewRuntime(BotConfig{BotQQ: "42"}, nilChannel{}, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return &capturingLLMProvider{}, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime.mu.Lock()
	runtime.running = true
	runtime.runCtx = ctx
	runtime.mu.Unlock()

	first := MessageEvent{Kind: EventKindGroup, GroupID: "group-1", MessageID: "message-1"}
	second := MessageEvent{Kind: EventKindGroup, GroupID: "group-1", MessageID: "message-2"}
	if !runtime.enqueuePassiveReply(first, "第一条") || !runtime.enqueuePassiveReply(second, "第二条") {
		t.Fatal("running runtime should enqueue passive candidates")
	}
	runtime.passiveBatchMu.Lock()
	batch := runtime.passiveBatches[sessionKey(first)]
	itemCount := 0
	if batch != nil {
		itemCount = len(batch.items)
	}
	runtime.passiveBatchMu.Unlock()
	if itemCount != 2 {
		t.Fatalf("batch item count = %d, want 2", itemCount)
	}

	runtime.cancelPassiveReplyBatch(first)
	runtime.passiveBatchMu.Lock()
	_, exists := runtime.passiveBatches[sessionKey(first)]
	runtime.passiveBatchMu.Unlock()
	if exists {
		t.Fatal("explicit group trigger should cancel its pending passive batch")
	}
}

func TestPassiveReplyBatchAppliesRelationshipDeltaWithoutDoubleCounting(t *testing.T) {
	provider := &sequenceLLMProvider{replies: []string{
		`{"should_reply":true,"confidence":0.97,"category":"needs_response","target_message_id":"message-1","directed_at_bot":false,"answerable":true}`,
		`{"action":"none","prompt":""}`,
		"可以先检查错误日志里的第一条异常。",
		`{"should_update":true,"delta":1,"confidence":0.96,"reason":"初识阶段的一次真实提问会带来轻微熟悉"}`,
	}}
	channel := &recordingChannel{}
	runtime := NewRuntime(BotConfig{
		BotQQ:                 "42",
		OwnerID:               "owner",
		AgentEnabled:          false,
		PassiveReplyChance:    1,
		PassiveReplyThreshold: 0.8,
	}, channel, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	memory := newMemoryUserMemoryStore()
	memory.profiles["user-1"] = UserMemoryProfile{UserID: "user-1", MessageCount: 1}
	runtime.SetUserMemoryStore(memory)
	logs := &captureAppLogs{}
	runtime.SetAppLogWriter(logs)
	event := MessageEvent{
		Kind:       EventKindGroup,
		GroupID:    "group-1",
		UserID:     "user-1",
		MessageID:  "message-1",
		SenderName: "Alice",
		RawMessage: "这个报错应该怎么处理？",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "这个报错应该怎么处理？"}}},
	}
	key := sessionKey(event)
	runtime.passiveBatches[key] = &passiveReplyBatch{
		items:      []passiveReplyCandidate{{Event: event, Text: event.RawMessage}},
		generation: 1,
	}

	runtime.flushPassiveReplyBatch(context.Background(), key, 1)

	profile := memory.profiles[event.UserID]
	if profile.Favorability != 1 || profile.MessageCount != 1 {
		t.Fatalf("profile = %#v, want favorability 1 and existing message count 1", profile)
	}
	if len(channel.sent) != 1 || channel.sent[0].Text != "可以先检查错误日志里的第一条异常。" {
		t.Fatalf("sent = %#v", channel.sent)
	}
	if len(provider.requests) != 4 {
		t.Fatalf("provider calls = %d, want route, visual intent, reply, and relationship", len(provider.requests))
	}
	var relationshipLogFound bool
	for _, entry := range logs.entries {
		if entry.Action == "qqbot.relationship_evaluation" && entry.Metadata["delta"] == 1 {
			relationshipLogFound = true
		}
	}
	if !relationshipLogFound {
		t.Fatalf("relationship evaluation log missing: %#v", logs.entries)
	}
}

func TestPassiveReplyBatchDoesNotEvaluateUnselectedMessages(t *testing.T) {
	provider := &sequenceLLMProvider{replies: []string{
		`{"should_reply":false,"confidence":0.99,"category":"no_response","target_message_id":"none"}`,
	}}
	channel := &recordingChannel{}
	runtime := NewRuntime(BotConfig{
		BotQQ:                 "42",
		OwnerID:               "owner",
		PassiveReplyChance:    1,
		PassiveReplyThreshold: 0.8,
	}, channel, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	memory := newMemoryUserMemoryStore()
	memory.profiles["user-1"] = UserMemoryProfile{UserID: "user-1", MessageCount: 1}
	runtime.SetUserMemoryStore(memory)
	event := MessageEvent{Kind: EventKindGroup, GroupID: "group-1", UserID: "user-1", MessageID: "message-1"}
	key := sessionKey(event)
	runtime.passiveBatches[key] = &passiveReplyBatch{
		items:      []passiveReplyCandidate{{Event: event, Text: "我先去吃饭了"}},
		generation: 1,
	}

	runtime.flushPassiveReplyBatch(context.Background(), key, 1)

	profile := memory.profiles[event.UserID]
	if profile.Favorability != 0 || profile.MessageCount != 1 || len(channel.sent) != 0 || len(provider.requests) != 1 {
		t.Fatalf("profile=%#v sent=%#v provider calls=%d", profile, channel.sent, len(provider.requests))
	}
}

func TestPassiveReplyBatchDoesNotAwardFavorabilityWhenReplyFails(t *testing.T) {
	provider := &passiveBatchReplyFailureProvider{}
	channel := &recordingChannel{}
	runtime := NewRuntime(BotConfig{
		BotQQ:                 "42",
		OwnerID:               "owner",
		AgentEnabled:          false,
		PassiveReplyChance:    1,
		PassiveReplyThreshold: 0.8,
	}, channel, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	memory := newMemoryUserMemoryStore()
	memory.profiles["user-1"] = UserMemoryProfile{UserID: "user-1", MessageCount: 1}
	runtime.SetUserMemoryStore(memory)
	event := MessageEvent{
		Kind:       EventKindGroup,
		GroupID:    "group-1",
		UserID:     "user-1",
		MessageID:  "message-1",
		SenderName: "Alice",
		RawMessage: "这个报错应该怎么处理？",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "这个报错应该怎么处理？"}}},
	}
	key := sessionKey(event)
	runtime.passiveBatches[key] = &passiveReplyBatch{
		items:      []passiveReplyCandidate{{Event: event, Text: event.RawMessage}},
		generation: 1,
	}

	runtime.flushPassiveReplyBatch(context.Background(), key, 1)

	profile := memory.profiles[event.UserID]
	if profile.Favorability != 0 || profile.MessageCount != 1 {
		t.Fatalf("profile = %#v, want unchanged relationship and message count", profile)
	}
	if provider.relationshipCalls != 0 {
		t.Fatalf("relationship evaluator calls = %d, want 0 after failed reply", provider.relationshipCalls)
	}
	if len(channel.sent) != 1 || !strings.HasPrefix(channel.sent[0].Text, "出错了：") {
		t.Fatalf("sent = %#v, want only the generic error reply", channel.sent)
	}
}

func TestPassiveReplyBatchRechecksSuppressionAfterRouting(t *testing.T) {
	provider := &passiveRouteSuppressionProvider{}
	channel := &recordingChannel{}
	runtime := NewRuntime(BotConfig{
		BotQQ:                 "42",
		OwnerID:               "owner",
		PassiveReplyChance:    1,
		PassiveReplyThreshold: 0.8,
	}, channel, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	event := MessageEvent{
		Kind:       EventKindGroup,
		GroupID:    "group-1",
		UserID:     "user-1",
		MessageID:  "message-1",
		SenderName: "Alice",
		RawMessage: "这个报错应该怎么处理？",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "这个报错应该怎么处理？"}}},
	}
	provider.runtime = runtime
	provider.event = event
	key := sessionKey(event)
	runtime.passiveBatches[key] = &passiveReplyBatch{
		items:      []passiveReplyCandidate{{Event: event, Text: event.RawMessage}},
		generation: 1,
	}

	runtime.flushPassiveReplyBatch(context.Background(), key, 1)

	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want only passive routing", provider.calls)
	}
	if len(channel.sent) != 0 {
		t.Fatalf("suppressed passive candidate still replied: %#v", channel.sent)
	}
	if _, active := runtime.activeReplySuppression(event, time.Now()); !active {
		t.Fatal("route-time response suppression was not activated")
	}
}

type passiveBatchReplyFailureProvider struct {
	relationshipCalls int
}

func (p *passiveBatchReplyFailureProvider) Generate(_ context.Context, req llm.GenerateRequest) (*llm.GenerateResponse, error) {
	switch {
	case requestMessagesContain(req.Messages, "严格被动插话路由器"):
		return &llm.GenerateResponse{Provider: llm.ProviderOpenAICompatible, Model: "test", Text: `{"should_reply":true,"confidence":0.97,"category":"needs_response","target_message_id":"message-1","directed_at_bot":false,"answerable":true}`}, nil
	case requestMessagesContain(req.Messages, "功能路由器"):
		return &llm.GenerateResponse{Provider: llm.ProviderOpenAICompatible, Model: "test", Text: `{"action":"none","prompt":""}`}, nil
	case requestMessagesContain(req.Messages, "关系变化评估器"):
		p.relationshipCalls++
		return &llm.GenerateResponse{Provider: llm.ProviderOpenAICompatible, Model: "test", Text: `{"should_update":true,"delta":1,"confidence":0.96,"reason":"初识阶段的一次真实互动"}`}, nil
	default:
		return nil, errors.New("reply failed")
	}
}

type passiveRouteSuppressionProvider struct {
	runtime *Runtime
	event   MessageEvent
	calls   int
}

type passiveReplyRerouteProvider struct {
	runtime          *Runtime
	second           MessageEvent
	injected         bool
	routeCalls       int
	replyCalls       int
	lastRoutePayload string
}

func (p *passiveReplyRerouteProvider) Generate(_ context.Context, req llm.GenerateRequest) (*llm.GenerateResponse, error) {
	switch {
	case requestMessagesContain(req.Messages, "严格被动插话路由器"):
		p.routeCalls++
		p.lastRoutePayload = req.Messages[len(req.Messages)-1].Content
		target := "message-1"
		if p.routeCalls > 1 {
			target = "message-2"
		}
		return &llm.GenerateResponse{
			Provider: llm.ProviderOpenAICompatible,
			Model:    "test",
			Text:     `{"should_reply":true,"confidence":0.97,"category":"needs_response","target_message_id":"` + target + `","directed_at_bot":false,"answerable":true}`,
		}, nil
	case requestMessagesContain(req.Messages, "功能路由器"):
		return &llm.GenerateResponse{Provider: llm.ProviderOpenAICompatible, Model: "test", Text: `{"action":"none","prompt":"","tools":[],"context_message_ids":[],"keep_older_summary":false}`}, nil
	default:
		p.replyCalls++
		if !p.injected {
			p.injected = true
			p.runtime.remember(p.second)
			if !p.runtime.enqueuePassiveReply(p.second, p.second.RawMessage) {
				return nil, errors.New("could not enqueue continuation")
			}
			return &llm.GenerateResponse{Provider: llm.ProviderOpenAICompatible, Model: "test", Text: "这是第一条的旧回复。"}, nil
		}
		return &llm.GenerateResponse{Provider: llm.ProviderOpenAICompatible, Model: "test", Text: "对，后一张是要乐奈，前一条和图片应当合在一起看。"}, nil
	}
}

func (p *passiveRouteSuppressionProvider) Generate(_ context.Context, req llm.GenerateRequest) (*llm.GenerateResponse, error) {
	p.calls++
	if !requestMessagesContain(req.Messages, "严格被动插话路由器") {
		return nil, errors.New("suppressed passive candidate reached reply generation")
	}
	p.runtime.activateReplySuppression(p.event, "test threshold reached", time.Now())
	return &llm.GenerateResponse{
		Provider: llm.ProviderOpenAICompatible,
		Model:    "test",
		Text:     `{"should_reply":true,"confidence":0.97,"category":"needs_response","target_message_id":"message-1","directed_at_bot":false,"answerable":true}`,
	}, nil
}
