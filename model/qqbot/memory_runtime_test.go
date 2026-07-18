package qqbot

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"diana-qq-bot/model/llm"
)

func TestMemoryGateUsesMemoryProfileAndExistingKeys(t *testing.T) {
	profiles := &stubLLMProfileStore{set: llm.ProfileSet{
		ActiveID: "default",
		Profiles: []llm.Profile{
			{ID: "default", Group: "default", Config: llm.ProviderConfig{Provider: llm.ProviderOpenAICompatible, APIKey: "default", Model: "default-model"}},
			{ID: "memory", Group: "memory", Config: llm.ProviderConfig{Provider: llm.ProviderOpenAICompatible, APIKey: "memory", Model: "memory-model"}},
		},
	}}
	memory := &testStructuredMemoryStore{items: []StructuredMemoryItem{{
		ID:            "old",
		ScopeKey:      "group:123",
		SubjectUserID: "user",
		Key:           "preference.food.spicy",
		Kind:          MemoryKindPreference,
		Topic:         "饮食偏好",
		Content:       "Alice喜欢辣味食物",
		Confidence:    0.98,
		Importance:    0.7,
		Visibility:    MemoryVisibilitySession,
		Version:       1,
	}}}
	usedModel := ""
	provider := &capturingLLMProvider{reply: `{"memories":[{"action":"upsert","key":"preference.food.spicy","kind":"preference","topic":"饮食偏好","entity":"辣味食物","content":"Alice现在不喜欢辣味食物","evidence":"我现在不吃辣了","source_type":"explicit","confidence":0.99,"importance":0.75,"visibility":"session","sensitive":false,"retention_days":0}]}`}
	runtime := NewRuntime(BotConfig{BotQQ: "bot"}, nilChannel{}, NewPluginManager(), profiles, nil, nil, nil)
	runtime.SetStructuredMemoryStore(memory)
	runtime.SetLLMProviderConfigFactory(func(cfg llm.ProviderConfig) (LLMProvider, error) {
		usedModel = cfg.Model
		return provider, nil
	})
	event := MessageEvent{
		Kind:       EventKindGroup,
		GroupID:    "123",
		UserID:     "user",
		SenderName: "Alice",
		MessageID:  "m2",
		Time:       200,
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "我现在不吃辣了"}}},
	}
	err := runtime.processEventMemoryJob(context.Background(), memory, MemoryJobPayload{
		Kind: MemoryJobEvent, Session: "group:123", Event: event,
	})
	if err != nil {
		t.Fatal(err)
	}
	if usedModel != "memory-model" {
		t.Fatalf("used model = %q, want memory-model", usedModel)
	}
	if len(memory.applied) != 1 || len(memory.applied[0].Candidates) != 1 || memory.applied[0].Candidates[0].Key != "preference.food.spicy" {
		t.Fatalf("applied = %#v", memory.applied)
	}
	prompt := provider.request.Messages[len(provider.request.Messages)-1].Content
	if !strings.Contains(prompt, "preference.food.spicy") || !strings.Contains(prompt, "我现在不吃辣了") || !strings.Contains(provider.request.Messages[0].Content, "当前任务里的格式要求") {
		t.Fatalf("memory gate prompt missing context: %s", prompt)
	}
}

func TestStructuredMemoryRankingExcludesUnrelatedFacts(t *testing.T) {
	now := time.Now()
	items := []StructuredMemoryItem{
		{
			ID: "cat", SubjectUserID: "user", SubjectName: "Alice", Key: "profile.pet.cat.name",
			Kind: MemoryKindFact, Topic: "宠物猫", Entity: "小白", Content: "Alice的猫叫小白",
			SourceType: MemorySourceExplicit, SourceSession: "group:123", Confidence: 0.98, Importance: 0.75, LastVerifiedAt: now,
		},
		{
			ID: "game", SubjectUserID: "user", SubjectName: "Alice", Key: "preference.game.maimai",
			Kind: MemoryKindPreference, Topic: "街机游戏", Entity: "舞萌DX", Content: "Alice喜欢玩舞萌DX",
			SourceType: MemorySourceExplicit, SourceSession: "group:123", Confidence: 0.98, Importance: 0.75, LastVerifiedAt: now,
		},
		{
			ID: "style", SubjectUserID: "user", SubjectName: "Alice", Key: "instruction.reply.concise",
			Kind: MemoryKindInstruction, Topic: "回复风格", Content: "回复Alice时保持简洁",
			SourceType: MemorySourceExplicit, SourceSession: "group:123", Confidence: 0.97, Importance: 0.65, LastVerifiedAt: now,
		},
	}
	ranked := rankStructuredMemories(items, MessageEvent{Kind: EventKindGroup, GroupID: "123", UserID: "user"}, "我家那只猫叫什么来着", now)
	ids := map[string]bool{}
	for _, item := range ranked {
		ids[item.ID] = true
	}
	if !ids["cat"] || !ids["style"] {
		t.Fatalf("expected relevant fact and standing instruction, got %#v", ranked)
	}
	if ids["game"] {
		t.Fatalf("unrelated game preference leaked into cat query: %#v", ranked)
	}
	contextText := formatStructuredMemoryContext(UserMemoryProfile{
		UserID: "user", DisplayName: "Alice", Favorability: 20, MessageCount: 12,
	}, RelationshipPolicyFor(UserMemoryProfile{Favorability: 20, MessageCount: 12}, "owner", "user"), ranked)
	if !strings.Contains(contextText, "稳定事实") || !strings.Contains(contextText, "Alice的猫叫小白") || strings.Contains(contextText, "舞萌DX") {
		t.Fatalf("compiled memory context = %s", contextText)
	}
}

func TestMemoryEnqueueSkipsResolverAndMediaOnlyMessages(t *testing.T) {
	memory := &testStructuredMemoryStore{}
	runtime := NewRuntime(BotConfig{BotQQ: "bot"}, nilChannel{}, NewPluginManager(), nil, nil, nil, nil)
	runtime.SetStructuredMemoryStore(memory)
	resolver := MessageEvent{
		Kind: EventKindGroup, GroupID: "123", UserID: "user", MessageID: "link",
		Segments: []MessageSegment{{Type: "text", Data: map[string]string{"text": "https://www.bilibili.com/video/BV1abc"}}},
	}
	runtime.enqueueEventMemory(resolver, memoryEventText(resolver))
	image := MessageEvent{
		Kind: EventKindGroup, GroupID: "123", UserID: "user", MessageID: "image",
		Segments: []MessageSegment{{Type: "image", Data: map[string]string{"url": "https://example.test/image.png"}}},
	}
	runtime.enqueueEventMemory(image, memoryEventText(image))
	normal := MessageEvent{
		Kind: EventKindGroup, GroupID: "123", UserID: "user", MessageID: "normal",
		Segments: []MessageSegment{{Type: "text", Data: map[string]string{"text": "我养的猫叫小白"}}},
	}
	runtime.enqueueEventMemory(normal, memoryEventText(normal))
	if len(memory.enqueued) != 1 || memory.enqueued[0].Event.MessageID != "normal" {
		t.Fatalf("enqueued = %#v", memory.enqueued)
	}
}

func TestParseMemoryCandidatesRejectsNonJSON(t *testing.T) {
	if _, err := parseMemoryCandidates("不是 JSON"); err == nil {
		t.Fatal("expected invalid response error")
	}
	items, err := parseMemoryCandidates("```json\n{\"memories\":[]}\n```")
	if err != nil || len(items) != 0 {
		t.Fatalf("empty candidate response items=%#v err=%v", items, err)
	}
}

func TestContextCompressionEnqueuesStructuredSummary(t *testing.T) {
	memory := &testStructuredMemoryStore{}
	runtime := NewRuntime(BotConfig{RecentContextLimit: 2, ContextSummaryThreshold: 3}, nilChannel{}, NewPluginManager(), nil, nil, nil, nil)
	runtime.SetStructuredMemoryStore(memory)
	for index, text := range []string{"第一条", "第二条", "第三条", "第四条"} {
		runtime.remember(MessageEvent{
			Kind: EventKindGroup, GroupID: "123", UserID: "user", MessageID: text, Time: int64(100 + index),
			Segments: []MessageSegment{{Type: "text", Data: map[string]string{"text": text}}},
		})
	}
	if len(memory.enqueued) != 1 {
		t.Fatalf("summary jobs = %#v", memory.enqueued)
	}
	job := memory.enqueued[0]
	if job.Kind != MemoryJobSummary || job.Session != "group:123" || len(job.Events) != 2 || job.Events[0].MessageID != "第一条" || job.Events[1].MessageID != "第二条" {
		t.Fatalf("summary job = %#v", job)
	}
	if runtime.contextSummary(MessageEvent{Kind: EventKindGroup, GroupID: "123"}) != "" {
		t.Fatal("raw concatenated summary should be hidden when structured memory is enabled")
	}
}

func TestSummaryMemoryJobStoresTopicSummary(t *testing.T) {
	memory := &testStructuredMemoryStore{}
	provider := &capturingLLMProvider{reply: `{"memories":[{"action":"upsert","key":"summary.2026-07-15.memory-design","kind":"summary","topic":"记忆系统设计","entity":"Diana","content":"群友讨论将记忆拆分为事实、情景、任务与摘要，并按相关性召回。","evidence":"较早会话整合","source_type":"summary","confidence":0.97,"importance":0.82,"visibility":"session","sensitive":false,"retention_days":365}]}`}
	runtime := NewRuntime(BotConfig{}, nilChannel{}, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	events := []MessageEvent{
		{Kind: EventKindGroup, GroupID: "123", UserID: "a", SenderName: "Alice", MessageID: "m1", Time: 100, Segments: []MessageSegment{{Type: "text", Data: map[string]string{"text": "记忆要分层"}}}},
		{Kind: EventKindGroup, GroupID: "123", UserID: "b", SenderName: "Bob", MessageID: "m2", Time: 200, Segments: []MessageSegment{{Type: "text", Data: map[string]string{"text": "还要按相关性召回"}}}},
	}
	err := runtime.processSummaryMemoryJob(context.Background(), memory, MemoryJob{
		ID: "summary-job", Payload: MemoryJobPayload{Kind: MemoryJobSummary, Session: "group:123", Events: events},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(memory.applied) != 1 || len(memory.applied[0].Candidates) != 1 {
		t.Fatalf("applied summaries = %#v", memory.applied)
	}
	request := memory.applied[0]
	candidate := request.Candidates[0]
	if request.SubjectUserID != "" || request.SourceMessageID != "summary:summary-job" || candidate.Kind != MemoryKindSummary || candidate.SourceType != MemorySourceSummary || candidate.Visibility != MemoryVisibilitySession {
		t.Fatalf("summary request = %#v", request)
	}
}

type testStructuredMemoryStore struct {
	mu       sync.Mutex
	items    []StructuredMemoryItem
	applied  []MemoryWriteRequest
	enqueued []MemoryJobPayload
}

func (s *testStructuredMemoryStore) EnqueueMemoryJob(_ context.Context, payload MemoryJobPayload) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enqueued = append(s.enqueued, payload)
	return "job", true, nil
}

func (s *testStructuredMemoryStore) ClaimNextMemoryJob(context.Context, string, time.Time) (MemoryJob, bool, error) {
	return MemoryJob{}, false, nil
}

func (s *testStructuredMemoryStore) CompleteMemoryJob(context.Context, string, string) error {
	return nil
}

func (s *testStructuredMemoryStore) RetryMemoryJob(context.Context, string, string, time.Time, string) error {
	return nil
}

func (s *testStructuredMemoryStore) ReleaseMemoryJobLeases(context.Context, string) error {
	return nil
}

func (s *testStructuredMemoryStore) ApplyMemoryCandidates(_ context.Context, request MemoryWriteRequest) ([]StructuredMemoryItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	request.Candidates = append([]MemoryCandidate(nil), request.Candidates...)
	s.applied = append(s.applied, request)
	return nil, nil
}

func (s *testStructuredMemoryStore) ListStructuredMemories(_ context.Context, query StructuredMemoryQuery) ([]StructuredMemoryItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]StructuredMemoryItem, 0, len(s.items))
	for _, item := range s.items {
		if len(query.Kinds) > 0 {
			matched := false
			for _, kind := range query.Kinds {
				if item.Kind == kind {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		items = append(items, item)
	}
	return items, nil
}
