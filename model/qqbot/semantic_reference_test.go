package qqbot

import (
	"context"
	"strings"
	"testing"

	"diana-qq-bot/model/llm"
)

func TestSemanticReferenceCanSelectAnotherUsersOldVideo(t *testing.T) {
	provider := &sequenceLLMProvider{replies: []string{`{"message_id":"video-old","confidence":0.94,"reason":"用户问的是小明发的视频"}`}}
	runtime := NewRuntime(BotConfig{RecentContextLimit: 20}, nilChannel{}, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	runtime.remember(MessageEvent{
		Kind:       EventKindGroup,
		Time:       1,
		GroupID:    "group-1",
		UserID:     "other-user",
		SenderName: "小明",
		MessageID:  "video-old",
		RawMessage: "[视频]",
		Segments:   []MessageSegment{{Type: "video", Data: map[string]string{"file": "old.mp4"}}},
	})
	runtime.remember(MessageEvent{Kind: EventKindGroup, Time: 999999, GroupID: "group-1", UserID: "someone", SenderName: "其他人", MessageID: "text-new", Segments: []MessageSegment{{Type: "text", Data: map[string]string{"text": "中间的聊天"}}}})

	event := runtime.enrichSemanticReference(context.Background(), MessageEvent{
		Kind:       EventKindGroup,
		Time:       1000000,
		GroupID:    "group-1",
		UserID:     "current-user",
		SenderName: "当前用户",
		MessageID:  "question-1",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "小明发的那个视频是什么"}}},
	}, "小明发的那个视频是什么")
	if event.Quoted == nil || event.Quoted.MessageID != "video-old" || event.Quoted.UserID != "other-user" || !event.Quoted.Semantic {
		t.Fatalf("semantic reference = %#v", event.Quoted)
	}
	if len(provider.requests) != 1 || !strings.Contains(provider.requests[0].Messages[1].Content, `"video_count":1`) || !strings.Contains(provider.requests[0].Messages[1].Content, `"age_seconds":999999`) {
		t.Fatalf("routing request = %#v", provider.requests)
	}
}

func TestSemanticReferenceUsesRoutingProfile(t *testing.T) {
	store := &stubLLMProfileStore{set: llm.ProfileSet{
		ActiveID: "main",
		Profiles: []llm.Profile{
			{ID: "main", Group: "default", Config: llm.ProviderConfig{Provider: llm.ProviderOpenAICompatible, Model: "main-model"}},
			{ID: "routing", Group: "routing", Config: llm.ProviderConfig{Provider: llm.ProviderOpenAICompatible, Model: "routing-model"}},
		},
	}}
	usedModels := make([]string, 0, 1)
	runtime := NewRuntime(BotConfig{RecentContextLimit: 20}, nilChannel{}, NewPluginManager(), store, nil, nil, nil)
	runtime.SetLLMProviderConfigFactory(func(cfg llm.ProviderConfig) (LLMProvider, error) {
		return &semanticReferenceModelProvider{model: cfg.Model, usedModels: &usedModels}, nil
	})
	runtime.remember(MessageEvent{
		Kind:      EventKindPrivate,
		UserID:    "user-1",
		MessageID: "image-1",
		Segments:  []MessageSegment{{Type: "image", Data: map[string]string{"url": "https://example.com/a.jpg"}}},
	})

	event := runtime.enrichSemanticReference(context.Background(), MessageEvent{
		Kind:      EventKindPrivate,
		UserID:    "user-1",
		MessageID: "question-1",
		Segments:  []MessageSegment{{Type: "text", Data: map[string]string{"text": "这张图是什么"}}},
	}, "这张图是什么")
	if event.Quoted == nil || event.Quoted.MessageID != "image-1" {
		t.Fatalf("semantic reference = %#v", event.Quoted)
	}
	if len(usedModels) != 1 || usedModels[0] != "routing-model" {
		t.Fatalf("used models = %#v, want routing-model", usedModels)
	}
}

func TestSemanticReferenceCanSelectImageFileOrText(t *testing.T) {
	runtime := NewRuntime(BotConfig{}, nilChannel{}, NewPluginManager(), nil, nil, nil, nil)
	runtime.remember(MessageEvent{Kind: EventKindPrivate, Time: 100, UserID: "user-1", MessageID: "mixed", Segments: []MessageSegment{
		{Type: "text", Data: map[string]string{"text": "方案说明"}},
		{Type: "image", Data: map[string]string{"url": "https://example.com/a.jpg"}},
		{Type: "file", Data: map[string]string{"name": "report.pdf"}},
	}})
	candidates, _, _ := runtime.semanticReferenceCandidates(context.Background(), MessageEvent{Kind: EventKindPrivate, Time: 130, UserID: "user-1", MessageID: "current"})
	if len(candidates) != 1 || candidates[0].ImageCount != 1 || candidates[0].FileCount != 1 {
		t.Fatalf("candidates = %#v", candidates)
	}
	if candidates[0].EventTime != 100 || candidates[0].AgeSeconds == nil || *candidates[0].AgeSeconds != 30 {
		t.Fatalf("candidate timing = %#v", candidates[0])
	}
	for _, want := range []string{"text", "image", "file"} {
		if !containsSemanticString(candidates[0].Content, want) {
			t.Fatalf("candidate missing %q: %#v", want, candidates[0])
		}
	}
}

func TestSemanticReferenceRejectsUnknownCandidate(t *testing.T) {
	provider := &sequenceLLMProvider{replies: []string{`{"message_id":"invented","confidence":1,"reason":"bad"}`}}
	runtime := NewRuntime(BotConfig{}, nilChannel{}, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) { return provider, nil })
	runtime.remember(MessageEvent{Kind: EventKindPrivate, UserID: "user-1", MessageID: "real", Segments: []MessageSegment{{Type: "text", Data: map[string]string{"text": "真实消息"}}}})
	event := runtime.enrichSemanticReference(context.Background(), MessageEvent{Kind: EventKindPrivate, UserID: "user-1", MessageID: "current"}, "那个呢")
	if event.Quoted != nil {
		t.Fatalf("invented candidate accepted: %#v", event.Quoted)
	}
}

func TestSemanticReferenceSkipsExplicitQuoteAndCurrentMedia(t *testing.T) {
	provider := &sequenceLLMProvider{replies: []string{`{"message_id":"old","confidence":1}`}}
	runtime := NewRuntime(BotConfig{}, nilChannel{}, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) { return provider, nil })
	runtime.remember(MessageEvent{Kind: EventKindPrivate, UserID: "user-1", MessageID: "old", Segments: []MessageSegment{{Type: "text", Data: map[string]string{"text": "旧消息"}}}})
	explicit := &QuotedMessage{MessageID: "quoted"}
	withQuote := runtime.enrichSemanticReference(context.Background(), MessageEvent{Kind: EventKindPrivate, UserID: "user-1", MessageID: "current", Quoted: explicit}, "这是什么")
	withImage := runtime.enrichSemanticReference(context.Background(), MessageEvent{Kind: EventKindPrivate, UserID: "user-1", MessageID: "current-2", Segments: []MessageSegment{{Type: "image", Data: map[string]string{"url": "https://example.com/a.jpg"}}}}, "这是什么")
	if withQuote.Quoted != explicit || withImage.Quoted != nil || len(provider.requests) != 0 {
		t.Fatalf("resolver should have been skipped: quote=%#v image=%#v requests=%d", withQuote.Quoted, withImage.Quoted, len(provider.requests))
	}
}

func TestSemanticReferenceCanResolveMediaBehindTextQuote(t *testing.T) {
	provider := &sequenceLLMProvider{replies: []string{`{"message_id":"recent-image","confidence":0.96,"reason":"用户说发了，指向刚发送的图片"}`}}
	runtime := NewRuntime(BotConfig{RecentContextLimit: 20}, nilChannel{}, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	runtime.remember(MessageEvent{
		Kind:       EventKindGroup,
		Time:       100,
		GroupID:    "group-1",
		UserID:     "other-user",
		SenderName: "群友",
		MessageID:  "recent-image",
		Segments:   []MessageSegment{{Type: "image", Data: map[string]string{"cached_file": "/tmp/cached.jpg"}}},
	})
	event := runtime.enrichSemanticReference(context.Background(), MessageEvent{
		Kind:      EventKindGroup,
		Time:      105,
		GroupID:   "group-1",
		UserID:    "owner",
		MessageID: "question",
		Quoted: &QuotedMessage{
			MessageID: "bot-text",
			UserID:    "bot",
			Segments:  []MessageSegment{{Type: "text", Data: map[string]string{"text": "把版本号发我"}}},
		},
		Segments: []MessageSegment{{Type: "text", Data: map[string]string{"text": "发了"}}},
	}, "发了")
	if event.Quoted == nil || event.Quoted.MessageID != "recent-image" || !event.Quoted.Semantic {
		t.Fatalf("semantic media reference = %#v", event.Quoted)
	}
}

func TestSemanticReferenceFindsPersistedImageBeyondShortContext(t *testing.T) {
	provider := &sequenceLLMProvider{replies: []string{`{"message_id":"target-image","confidence":0.98,"reason":"错误回复之前的图片是原任务来源"}`}}
	runtime := NewRuntime(BotConfig{BotQQ: "42", RecentContextLimit: 20, ContextSummaryThreshold: 20}, nilChannel{}, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	store := newSemanticTimelineStore()
	runtime.SetMessageHistoryStore(store)
	runtime.remember(MessageEvent{
		Kind:       EventKindGroup,
		Time:       100,
		GroupID:    "group-1",
		UserID:     "owner",
		SenderName: "TestOwner",
		MessageID:  "target-image",
		Segments:   []MessageSegment{{Type: "image", Data: map[string]string{"cached_file": "/tmp/target.jpg"}}},
	})
	runtime.remember(MessageEvent{
		Kind:       EventKindGroup,
		Time:       101,
		GroupID:    "group-1",
		UserID:     "owner",
		SenderName: "TestOwner",
		MessageID:  "task-text",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "我把图片发出来了"}}},
	})
	runtime.remember(MessageEvent{
		Kind:                    EventKindGroup,
		Time:                    102,
		GroupID:                 "group-1",
		UserID:                  "42",
		SenderName:              "Diana",
		MessageID:               "bot-timeout",
		SemanticSourceMessageID: "target-image",
		Segments: []MessageSegment{
			{Type: "reply", Data: map[string]string{"id": "task-text"}},
			{Type: "text", Data: map[string]string{"text": "出错了：请求处理超时，请稍后重试。"}},
		},
	})
	for index := 0; index < 25; index++ {
		runtime.remember(MessageEvent{
			Kind:      EventKindGroup,
			Time:      int64(103 + index),
			GroupID:   "group-1",
			UserID:    "other",
			MessageID: "filler-" + string(rune('a'+index)),
			Segments:  []MessageSegment{{Type: "text", Data: map[string]string{"text": "中间群聊"}}},
		})
	}
	if history := runtime.contextHistory(MessageEvent{Kind: EventKindGroup, GroupID: "group-1"}); semanticHistoryContainsMessage(history, "target-image") {
		t.Fatal("target image unexpectedly remained in the 20-message short context")
	}

	event := runtime.enrichSemanticReference(context.Background(), MessageEvent{
		Kind:      EventKindGroup,
		Time:      140,
		SelfID:    "42",
		GroupID:   "group-1",
		UserID:    "owner",
		MessageID: "retry-request",
		Quoted: &QuotedMessage{
			MessageID:               "bot-timeout",
			UserID:                  "42",
			SenderName:              "Diana",
			SemanticSourceMessageID: "target-image",
			Segments: []MessageSegment{
				{Type: "reply", Data: map[string]string{"id": "task-text"}},
				{Type: "text", Data: map[string]string{"text": "出错了：请求处理超时，请稍后重试。"}},
			},
		},
		Segments: []MessageSegment{{Type: "text", Data: map[string]string{"text": "重试这个"}}},
	}, "重试这个")
	if event.Quoted == nil || event.Quoted.MessageID != "target-image" || !event.Quoted.Semantic {
		t.Fatalf("semantic reference = %#v", event.Quoted)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("routing requests = %d", len(provider.requests))
	}
	prompt := provider.requests[0].Messages[1].Content
	for _, want := range []string{`"message_id":"target-image"`, `"semantic_source_message_id":"target-image"`, `"is_error_wrapper":true`, "我把图片发出来了"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("routing prompt missing %q: %s", want, prompt)
		}
	}
}

func TestOutgoingHistoryPreservesReplyAndSemanticSource(t *testing.T) {
	runtime := NewRuntime(BotConfig{BotQQ: "42"}, nilChannel{}, NewPluginManager(), nil, nil, nil, nil)
	source := MessageEvent{Kind: EventKindGroup, GroupID: "group-1", UserID: "owner", MessageID: "request"}
	remembered := source
	remembered.SemanticSourceMessageID = "target-image"
	runtime.remember(remembered)

	outgoing := runtime.outgoingHistoryEvent(source, OutgoingMessage{
		Text:           "出错了：请求处理超时，请稍后重试。",
		ReplyMessageID: "request",
		MentionUserID:  "owner",
	})
	if outgoing.SemanticSourceMessageID != "target-image" {
		t.Fatalf("semantic source = %q", outgoing.SemanticSourceMessageID)
	}
	if len(outgoing.Segments) < 3 || outgoing.Segments[0].Type != "reply" || outgoing.Segments[0].Data["id"] != "request" || outgoing.Segments[1].Type != "at" {
		t.Fatalf("outgoing segments = %#v", outgoing.Segments)
	}
}

func TestSemanticReferencePromptLabel(t *testing.T) {
	got := quotedPromptText(&QuotedMessage{Semantic: true, SenderName: "Alice", Segments: []MessageSegment{{Type: "text", Data: map[string]string{"text": "目标内容"}}}})
	if !strings.Contains(got, "指代判断选中的历史消息") {
		t.Fatalf("quotedPromptText() = %q", got)
	}
}

func containsSemanticString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func semanticHistoryContainsMessage(events []MessageEvent, messageID string) bool {
	for _, event := range events {
		if event.MessageID == messageID {
			return true
		}
	}
	return false
}

type semanticTimelineStore struct {
	events map[string][]MessageEvent
}

func newSemanticTimelineStore() *semanticTimelineStore {
	return &semanticTimelineStore{events: map[string][]MessageEvent{}}
}

func (s *semanticTimelineStore) AppendMessageEvent(_ context.Context, session string, event MessageEvent) error {
	for index := range s.events[session] {
		if s.events[session][index].MessageID == event.MessageID {
			s.events[session][index] = event
			return nil
		}
	}
	s.events[session] = append(s.events[session], event)
	return nil
}

func (s *semanticTimelineStore) ListRecentMessageEvents(_ context.Context, session string, limit int) ([]MessageEvent, error) {
	events := append([]MessageEvent(nil), s.events[session]...)
	if limit > 0 && len(events) > limit {
		events = events[len(events)-limit:]
	}
	return events, nil
}

func (s *semanticTimelineStore) ListMessageEventsBetween(_ context.Context, session string, fromTime, throughTime int64) ([]MessageEvent, error) {
	var events []MessageEvent
	for _, event := range s.events[session] {
		if event.Time >= fromTime && event.Time <= throughTime {
			events = append(events, event)
		}
	}
	return events, nil
}

func (s *semanticTimelineStore) FindMessageEvent(_ context.Context, session, messageID string) (MessageEvent, bool, error) {
	for _, event := range s.events[session] {
		if event.MessageID == messageID {
			return event, true, nil
		}
	}
	return MessageEvent{}, false, nil
}

var _ LLMProvider = (*semanticReferenceTestProvider)(nil)

type semanticReferenceTestProvider struct{}

func (*semanticReferenceTestProvider) Generate(context.Context, llm.GenerateRequest) (*llm.GenerateResponse, error) {
	return &llm.GenerateResponse{}, nil
}

type semanticReferenceModelProvider struct {
	model      string
	usedModels *[]string
}

func (p *semanticReferenceModelProvider) Generate(context.Context, llm.GenerateRequest) (*llm.GenerateResponse, error) {
	*p.usedModels = append(*p.usedModels, p.model)
	return &llm.GenerateResponse{Text: `{"message_id":"image-1","confidence":0.99,"reason":"当前问题指向图片"}`}, nil
}
