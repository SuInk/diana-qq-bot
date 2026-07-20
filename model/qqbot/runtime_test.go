package qqbot

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"diana-qq-bot/model/llm"
)

// TestRuntimeShouldHandleGroupMentionAndTrigger 验证对应功能场景。
func TestRuntimeShouldHandleGroupMentionAndTrigger(t *testing.T) {
	runtime := NewRuntime(BotConfig{
		GroupTriggers:  []string{"Diana"},
		BotQQ:          "42",
		DisabledGroups: []string{"999"},
	}, nilChannel{}, NewPluginManager(), nil, nil, nil, nil)

	if !runtime.shouldHandle(MessageEvent{Kind: EventKindGroup, ToMe: true}, "hello") {
		t.Fatal("mention should trigger")
	}
	if !runtime.shouldHandle(MessageEvent{Kind: EventKindGroup}, "Diana 帮我看看") {
		t.Fatal("group trigger should trigger directly")
	}
	if !runtime.shouldHandle(MessageEvent{Kind: EventKindGroup}, "[回复:30006]Diana你怎么看") {
		t.Fatal("group trigger after reply marker should trigger directly")
	}
	if !runtime.shouldHandle(MessageEvent{Kind: EventKindGroup}, "我觉得Diana这事得看上下文") {
		t.Fatal("group trigger inside normal text should trigger directly")
	}
	if runtime.shouldHandle(MessageEvent{Kind: EventKindGroup}, "普通群聊") {
		t.Fatal("plain group message should not trigger")
	}
	if runtime.shouldHandle(MessageEvent{Kind: EventKindGroup}, "画图 一只猫") {
		t.Fatal("image words alone should not trigger group chat")
	}
	if runtime.shouldHandle(MessageEvent{Kind: EventKindGroup}, "嘉然画图 一只猫") {
		t.Fatal("image words with unconfigured alias should not trigger group chat")
	}
	if runtime.shouldHandle(MessageEvent{Kind: EventKindGroup}, "改图 把肤色变黑一点") {
		t.Fatal("image edit words alone should not trigger group chat")
	}
	if !runtime.shouldHandle(MessageEvent{Kind: EventKindPrivate}, "hello") {
		t.Fatal("private message should trigger")
	}
	if runtime.shouldHandle(MessageEvent{Kind: EventKindGroup, GroupID: "999", ToMe: true}, "hello") {
		t.Fatal("disabled group should not trigger")
	}
}

func TestRuntimeDirectTriggersBypassPassiveRouter(t *testing.T) {
	tests := []struct {
		name  string
		event MessageEvent
		text  string
	}{
		{
			name: "keyword anywhere",
			event: MessageEvent{
				Kind:       EventKindGroup,
				GroupID:    "123456",
				UserID:     "10001",
				MessageID:  "keyword-1",
				RawMessage: "我觉得Diana这事得看上下文",
				Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "我觉得Diana这事得看上下文"}}},
			},
			text: "我觉得Diana这事得看上下文",
		},
		{
			name: "mention",
			event: MessageEvent{
				Kind:       EventKindGroup,
				GroupID:    "123456",
				UserID:     "10001",
				MessageID:  "mention-1",
				ToMe:       true,
				RawMessage: "[CQ:at,qq=42] 帮我看看",
				Segments: []MessageSegment{
					{Type: "at", Data: map[string]string{"qq": "42"}},
					{Type: "text", Data: map[string]string{"text": " 帮我看看"}},
				},
			},
			text: "帮我看看",
		},
		{
			name: "reply to bot",
			event: MessageEvent{
				Kind:       EventKindGroup,
				GroupID:    "123456",
				UserID:     "10001",
				MessageID:  "reply-1",
				ToMe:       true,
				RawMessage: "[CQ:reply,id=bot-message] 再解释一下",
				Segments: []MessageSegment{
					{Type: "reply", Data: map[string]string{"id": "bot-message"}},
					{Type: "text", Data: map[string]string{"text": " 再解释一下"}},
				},
			},
			text: "再解释一下",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channel := &recordingChannel{}
			provider := &sequenceLLMProvider{replies: []string{
				`{"automated_ai_reply":false,"confidence":0.99,"reason":"普通真人直接发言"}`,
				`{"action":"none","prompt":""}`,
				"直接触发成功",
			}}
			runtime := NewRuntime(BotConfig{BotQQ: "42", GroupTriggers: []string{"Diana"}}, channel, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
				return provider, nil
			})
			if err := runtime.HandleEvent(context.Background(), tt.event); err != nil {
				t.Fatal(err)
			}
			waitForCondition(t, time.Second, func() bool {
				return len(channel.sentSnapshot()) == 1
			})
			requests := provider.requestsSnapshot()
			sent := channel.sentSnapshot()
			if len(requests) != 3 || sent[0].Text != "直接触发成功" {
				t.Fatalf("requests=%d sent=%#v", len(requests), sent)
			}
			for _, request := range requests {
				if len(request.Messages) > 0 && strings.Contains(request.Messages[0].Content, "严格被动插话路由器") {
					t.Fatalf("direct trigger unexpectedly entered passive router: %#v", request.Messages)
				}
			}
		})
	}
}

func TestRuntimeUpdateConfigIgnoresPreviousRunExit(t *testing.T) {
	first := newDelayedExitChannel()
	second := newDelayedExitChannel()
	cfg := BotConfig{Enabled: true, BotQQ: "42"}
	runtime := NewRuntime(cfg, first, NewPluginManager(), nil, nil, nil, nil)
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForSignal(t, first.started)
	if err := runtime.UpdateConfig(context.Background(), cfg, second); err != nil {
		t.Fatal(err)
	}
	waitForSignal(t, second.started)

	close(first.release)
	waitForSignal(t, first.finished)
	if !runtime.Status().Running {
		t.Fatal("previous run exit marked the replacement runtime as stopped")
	}

	if err := runtime.Stop(); err != nil {
		t.Fatal(err)
	}
	close(second.release)
	waitForSignal(t, second.finished)
}

func waitForSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for runtime signal")
	}
}

func TestRuntimeShouldHandleKnownPlatformURLWhenResolverEnabled(t *testing.T) {
	runtime := NewRuntime(BotConfig{}, nilChannel{}, NewDefaultPluginManager(), nil, nil, nil, nil)
	if !runtime.shouldHandle(MessageEvent{Kind: EventKindGroup}, "看这个 https://www.bilibili.com/video/BV1xx411c7mD") {
		t.Fatal("known platform url should trigger resolver")
	}
	if !runtime.shouldHandleResolver(MessageEvent{Kind: EventKindPrivate}, "避雷立充 http://xhslink.com/o/20YWuppICeI 留住这段口令") {
		t.Fatal("private xiaohongshu short link should trigger resolver")
	}
	if runtime.shouldHandle(MessageEvent{Kind: EventKindGroup}, "看这个 https://example.com/video.mp4") {
		t.Fatal("generic url should not trigger resolver")
	}
}

func TestLLMGatewayErrorCanRetryAndFailover(t *testing.T) {
	err := errors.New("llm: openai-compatible request failed: 502 Bad Gateway")
	if !shouldRetryTransientLLMError(err) {
		t.Fatal("502 should be retried once")
	}
	if !shouldFailoverLLMError(err) {
		t.Fatal("502 should be eligible for profile failover")
	}
	err = errors.New(`Post "https://api.example.test/v1/responses": EOF`)
	if !shouldRetryTransientLLMError(err) {
		t.Fatal("EOF should be retried once")
	}
	err = fmt.Errorf("request failed: %w", context.DeadlineExceeded)
	if !shouldRetryTransientLLMError(err) {
		t.Fatal("request timeout should be retried once")
	}
	err = fmt.Errorf("model result: %w", llm.ErrCompletionEmpty)
	if !shouldRetryTransientLLMError(err) {
		t.Fatal("empty model output should be retried once")
	}
	if !shouldFailoverLLMError(err) {
		t.Fatal("empty model output should be eligible for profile failover")
	}
}

func TestLLMNoTextErrorRetryAndFailoverMatrix(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		wantRetry    bool
		wantFailover bool
	}{
		{
			name:         "genuinely empty",
			err:          fmt.Errorf("model result: %w", llm.ErrCompletionEmpty),
			wantRetry:    true,
			wantFailover: true,
		},
		{
			name:         "untyped empty string",
			err:          errors.New("llm: openai-compatible responses output is empty"),
			wantRetry:    false,
			wantFailover: false,
		},
		{
			name:         "truncated before text",
			err:          fmt.Errorf("provider result: %w", llm.ErrCompletionTruncatedNoText),
			wantRetry:    false,
			wantFailover: true,
		},
		{
			name:         "terminal no text",
			err:          fmt.Errorf("provider result: %w", llm.ErrCompletionHasNoText),
			wantRetry:    false,
			wantFailover: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldRetryTransientLLMError(tt.err); got != tt.wantRetry {
				t.Fatalf("shouldRetryTransientLLMError() = %t, want %t", got, tt.wantRetry)
			}
			if got := shouldFailoverLLMError(tt.err); got != tt.wantFailover {
				t.Fatalf("shouldFailoverLLMError() = %t, want %t", got, tt.wantFailover)
			}
		})
	}
}

func TestRuntimeCompressesOlderContextAfterThreshold(t *testing.T) {
	runtime := NewRuntime(BotConfig{RecentContextLimit: 3, ContextSummaryThreshold: 5}, nilChannel{}, NewPluginManager(), nil, nil, nil, nil)
	event := MessageEvent{Kind: EventKindGroup, GroupID: "123", UserID: "10001"}
	for i := 1; i <= 6; i++ {
		next := event
		next.MessageID = strconv.Itoa(i)
		next.RawMessage = "消息" + strconv.Itoa(i)
		next.Segments = []MessageSegment{{Type: "text", Data: map[string]string{"text": next.RawMessage}}}
		runtime.remember(next)
	}
	history := runtime.contextHistory(event)
	if len(history) != 3 || history[0].RawMessage != "消息4" || history[2].RawMessage != "消息6" {
		t.Fatalf("history = %#v", history)
	}
	summary := runtime.contextSummary(event)
	if !strings.Contains(summary, "消息1") || !strings.Contains(summary, "消息3") {
		t.Fatalf("summary = %q", summary)
	}
	runtime.clearSessionHistory(event)
	if runtime.contextSummary(event) != "" || len(runtime.contextHistory(event)) != 0 {
		t.Fatalf("context should be cleared")
	}
}

func TestRuntimeLoadsPersistentMessageHistory(t *testing.T) {
	runtime := NewRuntime(BotConfig{RecentContextLimit: 2}, nilChannel{}, NewPluginManager(), nil, nil, nil, nil)
	store := newMemoryMessageHistoryStore()
	runtime.SetMessageHistoryStore(store)
	event := MessageEvent{
		Kind:       EventKindGroup,
		GroupID:    "123",
		UserID:     "10001",
		MessageID:  "m1",
		RawMessage: "刚刚在聊最近的漫展",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "刚刚在聊最近的漫展"}}},
	}
	if err := store.AppendMessageEvent(context.Background(), sessionKey(event), event); err != nil {
		t.Fatal(err)
	}
	history := runtime.contextHistory(MessageEvent{Kind: EventKindGroup, GroupID: "123", UserID: "20002"})
	if len(history) != 1 || history[0].RawMessage != "刚刚在聊最近的漫展" {
		t.Fatalf("history = %#v", history)
	}
}

func TestDefaultBotConfigKeepsTwentyMessagesAndCompressesAtOneHundred(t *testing.T) {
	cfg := DefaultBotConfig()
	if cfg.RecentContextLimit != 20 || cfg.ContextSummaryThreshold != 100 {
		t.Fatalf("context defaults = recent %d threshold %d", cfg.RecentContextLimit, cfg.ContextSummaryThreshold)
	}
	if cfg.PassiveReplyChance != 1 {
		t.Fatalf("passive reply chance = %v, want 1", cfg.PassiveReplyChance)
	}
	if cfg.PassiveReplyThreshold != 0.8 {
		t.Fatalf("passive reply threshold = %v, want 0.8", cfg.PassiveReplyThreshold)
	}
	if strings.TrimSpace(cfg.SystemPrompt) == "" || strings.TrimSpace(cfg.PassiveReplyRouterPrompt) == "" || strings.TrimSpace(cfg.PassiveReplyPrompt) == "" {
		t.Fatal("editable prompt defaults must not be empty")
	}
}

func TestRuntimeUpdatesUserMemoryForPassiveGroupMessage(t *testing.T) {
	runtime := NewRuntime(BotConfig{GroupTriggers: []string{"Diana"}}, nilChannel{}, NewPluginManager(), nil, nil, nil, nil)
	store := newMemoryUserMemoryStore()
	runtime.SetUserMemoryStore(store)

	event := MessageEvent{
		Kind:       EventKindGroup,
		GroupID:    "123",
		UserID:     "10001",
		MessageID:  "m1",
		SenderName: "Alice",
		RawMessage: "我最近在看漫展",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "我最近在看漫展"}}},
	}
	if err := runtime.HandleEvent(context.Background(), event); err != nil {
		t.Fatalf("HandleEvent() error = %v", err)
	}
	profile, ok, err := store.GetUserMemory(context.Background(), "10001")
	if err != nil || !ok {
		t.Fatalf("GetUserMemory() ok=%v err=%v", ok, err)
	}
	if profile.DisplayName != "Alice" || profile.MessageCount != 1 || len(profile.Memories) != 1 || profile.Memories[0].Text != "我最近在看漫展" {
		t.Fatalf("profile = %#v", profile)
	}
	if profile.Favorability != 0 {
		t.Fatalf("passive first message favorability = %d, want 0", profile.Favorability)
	}
}

func TestRuntimeAddsUserMemoryContextToLLMPrompt(t *testing.T) {
	channel := &recordingChannel{}
	provider := &capturingLLMProvider{reply: "记住啦"}
	runtime := NewRuntime(BotConfig{}, channel, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	store := newMemoryUserMemoryStore()
	store.profiles["10001"] = UserMemoryProfile{
		UserID:       "10001",
		DisplayName:  "Alice",
		Favorability: 16,
		MessageCount: 7,
		Memories: []UserMemoryItem{
			{Text: "我最近在看漫展"},
		},
	}
	runtime.SetUserMemoryStore(store)

	_, err := runtime.replyTo(context.Background(), MessageEvent{
		Kind:       EventKindPrivate,
		UserID:     "10001",
		MessageID:  "m2",
		RawMessage: "还记得我最近在看啥吗",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "还记得我最近在看啥吗"}}},
	}, "还记得我最近在看啥吗")
	if err != nil {
		t.Fatalf("replyTo() error = %v", err)
	}
	if len(provider.request.Messages) < 3 {
		t.Fatalf("messages = %#v", provider.request.Messages)
	}
	var memoryPrompt string
	for _, message := range provider.request.Messages {
		if strings.Contains(message.Content, "好感度：16") {
			memoryPrompt = message.Content
			break
		}
	}
	if !strings.Contains(memoryPrompt, "好感度：16") || !strings.Contains(memoryPrompt, "我最近在看漫展") {
		t.Fatalf("memory prompt = %q", memoryPrompt)
	}
	if provider.request.Messages[0].Priority != llm.MessagePrioritySystem {
		t.Fatalf("system priority = %d", provider.request.Messages[0].Priority)
	}
	if provider.request.Messages[len(provider.request.Messages)-1].Priority != llm.MessagePriorityCurrent {
		t.Fatalf("current priority = %d", provider.request.Messages[len(provider.request.Messages)-1].Priority)
	}
	for _, message := range provider.request.Messages {
		if strings.Contains(message.Content, "好感度：16") && message.Priority != llm.MessagePriorityMemory {
			t.Fatalf("memory priority = %d", message.Priority)
		}
	}
}

func TestRuntimeEnrichesReplyReferenceFromOneBot(t *testing.T) {
	channel := &recordingChannel{apiResponses: map[string]map[string]any{
		"get_msg": {
			"message_id":  "abc",
			"raw_message": "被引用内容",
			"sender":      map[string]any{"user_id": "20002", "nickname": "Alice"},
			"message": []any{
				map[string]any{"type": "text", "data": map[string]any{"text": "被引用内容"}},
			},
		},
	}}
	runtime := NewRuntime(BotConfig{}, channel, NewPluginManager(), nil, nil, nil, nil)
	event := runtime.enrichReplyReference(context.Background(), MessageEvent{
		Kind:      EventKindGroup,
		GroupID:   "123",
		UserID:    "10001",
		MessageID: "new",
		Segments:  []MessageSegment{{Type: "reply", Data: map[string]string{"id": "abc"}}},
	})
	if event.Quoted == nil || event.Quoted.RawMessage != "被引用内容" || event.Quoted.SenderName != "Alice" {
		t.Fatalf("quoted = %#v", event.Quoted)
	}
	prompt := currentPromptText(event, "[回复:abc] 这是什么意思")
	if !strings.Contains(prompt, "【被引用的消息】Alice: 被引用内容") {
		t.Fatalf("prompt = %q", prompt)
	}
}

func TestRuntimeReplyReferenceKeepsPersistedMedia(t *testing.T) {
	cachedPath := filepath.Join(t.TempDir(), "cached.jpg")
	if err := os.WriteFile(cachedPath, tinyJPEGBytes(t), 0o600); err != nil {
		t.Fatal(err)
	}
	channel := &recordingChannel{apiResponses: map[string]map[string]any{
		"get_msg": {
			"message_id": "image-1",
			"sender":     map[string]any{"user_id": "20002", "nickname": "Alice"},
			"message": []any{map[string]any{
				"type": "image",
				"data": map[string]any{"file": "same.jpg"},
			}},
		},
	}}
	runtime := NewRuntime(BotConfig{}, channel, NewPluginManager(), nil, nil, nil, nil)
	runtime.remember(MessageEvent{
		Kind:      EventKindGroup,
		GroupID:   "123",
		MessageID: "image-1",
		Segments: []MessageSegment{{Type: "image", Data: map[string]string{
			"file": "same.jpg", "cached_file": cachedPath,
		}}},
	})
	event := runtime.enrichReplyReference(context.Background(), MessageEvent{
		Kind:     EventKindGroup,
		GroupID:  "123",
		Segments: []MessageSegment{{Type: "reply", Data: map[string]string{"id": "image-1"}}},
	})
	if event.Quoted == nil || event.Quoted.Segments[0].Data["cached_file"] != cachedPath {
		t.Fatalf("persisted media was lost after get_msg: %#v", event.Quoted)
	}
}

func TestRuntimeEnrichesForwardMessageFromOneBot(t *testing.T) {
	channel := &recordingChannel{apiResponses: map[string]map[string]any{
		"get_forward_msg": {
			"messages": []any{
				map[string]any{
					"type": "node",
					"data": map[string]any{
						"name": "Alice",
						"uin":  "20002",
						"content": []any{
							map[string]any{"type": "text", "data": map[string]any{"text": "第一条内容"}},
						},
					},
				},
				map[string]any{
					"type": "node",
					"data": map[string]any{
						"name":    "Bob",
						"content": "[CQ:at,qq=42] 第二条内容",
					},
				},
			},
		},
	}}
	runtime := NewRuntime(BotConfig{}, channel, NewPluginManager(), nil, nil, nil, nil)
	event := runtime.enrichForwardMessages(context.Background(), MessageEvent{
		Kind:      EventKindGroup,
		GroupID:   "123",
		UserID:    "10001",
		MessageID: "new",
		Segments:  []MessageSegment{{Type: "forward", Data: map[string]string{"id": "forward-1"}}},
	})
	text := PlainText(event.Segments)
	if !strings.Contains(text, "Alice: 第一条内容") || !strings.Contains(text, "Bob: @42  第二条内容") {
		t.Fatalf("forward text = %q", text)
	}
	if len(channel.calls) != 1 || channel.calls[0].action != "get_forward_msg" || channel.calls[0].params["id"] != "forward-1" {
		t.Fatalf("calls = %#v", channel.calls)
	}
}

func TestRuntimeEnrichesQuotedForwardMediaAndCachesVideoFrames(t *testing.T) {
	t.Setenv("DIANA_HISTORY_MEDIA_DIR", t.TempDir())
	videoPath := createTimelineVideo(t)
	imageBody := tinyJPEGBytes(t)
	imageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(imageBody)
	}))
	defer imageServer.Close()

	channel := &recordingChannel{apiResponses: map[string]map[string]any{
		"get_forward_msg": {
			"messages": []any{
				map[string]any{
					"message_id": 30001,
					"group_id":   20004,
					"user_id":    10006,
					"sender":     map[string]any{"nickname": "Alice", "user_id": 10006},
					"message": []any{map[string]any{
						"type": "video",
						"data": map[string]any{"file": "forward-video.mp4", "file_size": 12345, "url": videoPath},
					}},
				},
				map[string]any{
					"message_id": 30002,
					"group_id":   20004,
					"user_id":    10006,
					"sender":     map[string]any{"nickname": "Alice", "user_id": 10006},
					"message": []any{map[string]any{
						"type": "image",
						"data": map[string]any{"file": "forward-image.jpg", "sub_type": 0, "url": imageServer.URL + "/image.jpg"},
					}},
				},
			},
		},
	}}
	runtime := NewRuntime(BotConfig{}, channel, NewPluginManager(), nil, nil, nil, nil)
	event := MessageEvent{
		Kind:      EventKindGroup,
		GroupID:   "20003",
		UserID:    "10001",
		MessageID: "question-1",
		Segments:  []MessageSegment{{Type: "text", Data: map[string]string{"text": "里面视频讲了什么"}}},
		Quoted: &QuotedMessage{
			MessageID:  "forward-parent",
			UserID:     "10002",
			GroupID:    "20003",
			SenderName: "Alice",
			Segments:   []MessageSegment{{Type: "forward", Data: map[string]string{"id": "forward-1"}}},
		},
	}
	event = runtime.enrichForwardMessages(context.Background(), event)
	event = runtime.enrichMediaReferences(context.Background(), event)
	event = cacheMessageEventImages(context.Background(), event)
	event = cacheMessageEventVideos(context.Background(), event)

	if event.Quoted == nil {
		t.Fatal("quoted forward was lost")
	}
	quotedText := PlainText(event.Quoted.Segments)
	if !strings.Contains(quotedText, "Alice: [视频]") || !strings.Contains(quotedText, "Alice: [图片]") || strings.Contains(quotedText, "map[") {
		t.Fatalf("quoted forward text = %q", quotedText)
	}
	var video, image MessageSegment
	for _, segment := range event.Quoted.Segments {
		switch segment.Type {
		case "video":
			video = segment
		case "image":
			if segment.Data["source_type"] != "video_frame" {
				image = segment
			}
		}
	}
	if video.Data["source_message_id"] != "30001" || video.Data["source_group_id"] != "20004" || video.Data["forward_id"] != "forward-1" {
		t.Fatalf("forward video metadata = %#v", video.Data)
	}
	if image.Data["cached_file"] == "" {
		t.Fatalf("forward image was not cached: %#v", image)
	}
	if frames := cachedVideoFrameURLs(event.Quoted.Segments); len(frames) != 4 {
		t.Fatalf("forward video frame count = %d, want 4: %#v", len(frames), event.Quoted.Segments)
	}
	message := llmMessageFromEventWithVideoFrames(context.Background(), event, "里面视频讲了什么", nil)
	if len(message.Parts) < 6 {
		t.Fatalf("multimodal parts = %#v", message.Parts)
	}
	if len(channel.calls) != 1 || channel.calls[0].action != "get_forward_msg" {
		t.Fatalf("OneBot calls = %#v", channel.calls)
	}
}

func TestRuntimeResolvesForwardVideoUsingSourceMetadata(t *testing.T) {
	videoPath := createTimelineVideo(t)
	missingPath := filepath.Join(t.TempDir(), "QQ", "Video", "Ori", "forward-video.mp4")
	channel := &recordingChannel{apiResponses: map[string]map[string]any{
		"get_forward_msg": {
			"messages": []any{map[string]any{
				"message_id": 30001,
				"group_id":   20004,
				"user_id":    10006,
				"message": []any{map[string]any{
					"type": "video",
					"data": map[string]any{"file": "forward-video.mp4", "url": missingPath},
				}},
			}},
		},
		"get_file": {"path": videoPath},
	}}
	runtime := NewRuntime(BotConfig{}, channel, NewPluginManager(), nil, nil, nil, nil)
	event := runtime.enrichForwardMessages(context.Background(), MessageEvent{
		Kind:      EventKindGroup,
		GroupID:   "20003",
		MessageID: "forward-parent",
		Segments:  []MessageSegment{{Type: "forward", Data: map[string]string{"id": "forward-1"}}},
	})
	event = runtime.enrichMediaReferences(context.Background(), event)

	var video MessageSegment
	for _, segment := range event.Segments {
		if segment.Type == "video" {
			video = segment
			break
		}
	}
	if video.Data["path"] != videoPath {
		t.Fatalf("resolved forward video = %#v", video)
	}
	if len(channel.calls) != 2 || channel.calls[1].action != "get_file" {
		t.Fatalf("source-aware OneBot calls = %#v", channel.calls)
	}
	if channel.calls[1].params["file"] != "forward-video.mp4" {
		t.Fatalf("get_file call = %#v", channel.calls[1])
	}
}

func TestRuntimeCountsForwardMessageAsSingleHistoryEvent(t *testing.T) {
	channel := &recordingChannel{apiResponses: map[string]map[string]any{
		"get_forward_msg": {
			"messages": []any{
				map[string]any{
					"type": "node",
					"data": map[string]any{
						"name":    "Alice",
						"content": "第一条内容",
					},
				},
				map[string]any{
					"type": "node",
					"data": map[string]any{
						"name":    "Bob",
						"content": "第二条内容",
					},
				},
			},
		},
	}}
	runtime := NewRuntime(BotConfig{}, channel, NewDefaultPluginManager(), nil, nil, nil, nil)
	event := MessageEvent{
		Kind:      EventKindGroup,
		GroupID:   "123",
		UserID:    "10001",
		MessageID: "forward-msg",
		Segments:  []MessageSegment{{Type: "forward", Data: map[string]string{"id": "forward-1"}}},
	}
	if err := runtime.HandleEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	history := runtime.contextHistory(event)
	if len(history) != 1 {
		t.Fatalf("history len = %d, history = %#v", len(history), history)
	}
	text := PlainText(history[0].Segments)
	if !strings.Contains(text, "Alice: 第一条内容") || !strings.Contains(text, "Bob: 第二条内容") {
		t.Fatalf("history text = %q", text)
	}
}

func TestDefaultPluginManagerIncludesMessageHistory(t *testing.T) {
	manager := NewDefaultPluginManager()
	if !manager.Enabled(messageHistoryPluginID) {
		t.Fatal("message history plugin should be enabled by default")
	}
}

func TestMessageHistoryPluginEnrichesQuotedMessageFromCache(t *testing.T) {
	plugin := NewMessageHistoryPlugin()
	plugin.Observe(context.Background(), MessageEvent{
		Kind:       EventKindGroup,
		GroupID:    "123",
		UserID:     "20002",
		MessageID:  "old-1",
		RawMessage: "被引用内容",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "被引用内容"}}},
		SenderName: "Alice",
	})
	event := plugin.Observe(context.Background(), MessageEvent{
		Kind:      EventKindGroup,
		GroupID:   "123",
		UserID:    "10001",
		MessageID: "new-1",
		Segments:  []MessageSegment{{Type: "reply", Data: map[string]string{"id": "old-1"}}},
	})
	if event.Quoted == nil || event.Quoted.RawMessage != "被引用内容" || event.Quoted.SenderName != "Alice" {
		t.Fatalf("quoted = %#v", event.Quoted)
	}
}

func TestMessageHistoryPluginAddsRecallContext(t *testing.T) {
	plugin := NewMessageHistoryPlugin()
	plugin.Observe(context.Background(), MessageEvent{
		Kind:       EventKindGroup,
		GroupID:    "123",
		UserID:     "20002",
		MessageID:  "old-1",
		RawMessage: "要撤回的内容",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "要撤回的内容"}}},
		SenderName: "Alice",
	})
	plugin.Observe(context.Background(), messageEventFromEnvelope(oneBotEnvelope{
		PostType:   "notice",
		NoticeType: "group_recall",
		GroupID:    "123",
		UserID:     "20002",
		MessageID:  "old-1",
	}))
	resp, err := plugin.Handle(context.Background(), PluginRequest{
		Event: MessageEvent{Kind: EventKindGroup, GroupID: "123"},
		Text:  "撤回了什么",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil || !strings.Contains(resp.Context, "最近24小时群撤回消息") || !strings.Contains(resp.Context, "要撤回的内容") || !resp.NestedForward {
		t.Fatalf("response = %#v", resp)
	}
	if resp.Reply != "" {
		t.Fatalf("response = %#v", resp)
	}
}

func TestRuntimeRecallDefaultsToLLMReplyWithoutOriginalForward(t *testing.T) {
	history := NewMessageHistoryPlugin()
	history.Observe(context.Background(), MessageEvent{
		Kind:       EventKindGroup,
		GroupID:    "123",
		UserID:     "20002",
		MessageID:  "old-1",
		RawMessage: "撤回前的完整内容",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "撤回前的完整内容"}}},
		SenderName: "Alice",
	})
	history.Observe(context.Background(), messageEventFromEnvelope(oneBotEnvelope{
		PostType:   "notice",
		NoticeType: "group_recall",
		GroupID:    "123",
		UserID:     "20002",
		MessageID:  "old-1",
	}))
	channel := &recordingChannel{}
	provider := &sequenceLLMProvider{replies: []string{"这是 LLM 根据撤回记录生成的回复。"}}
	runtime := NewRuntime(BotConfig{
		AgentEnabled:          false,
		DirectReplyChunkSize:  900,
		ForwardReplyThreshold: 900,
	}, channel, NewPluginManager(history), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	event := MessageEvent{
		Kind:       EventKindGroup,
		GroupID:    "123",
		UserID:     "10001",
		MessageID:  "query-1",
		RawMessage: "查看撤回记录",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "查看撤回记录"}}},
	}
	reply, err := runtime.replyTo(context.Background(), event, event.RawMessage)
	if err != nil {
		t.Fatal(err)
	}
	if reply != "这是 LLM 根据撤回记录生成的回复。" || len(channel.sent) != 1 || channel.sent[0].Text != reply {
		t.Fatalf("reply=%q sent=%#v", reply, channel.sent)
	}
	for _, call := range channel.calls {
		if strings.Contains(call.action, "forward") {
			t.Fatalf("default recall mode sent original forward: %#v", channel.calls)
		}
	}
	if len(provider.requests) != 1 {
		t.Fatalf("recall query should use one LLM request, got %d: %#v", len(provider.requests), provider.requests)
	}
	var pluginMessage *llm.Message
	for index := range provider.requests[0].Messages {
		message := &provider.requests[0].Messages[index]
		if strings.Contains(message.Content, "撤回前的完整内容") {
			pluginMessage = message
			break
		}
	}
	if pluginMessage == nil || pluginMessage.Priority != llm.MessagePriorityPlugin || !strings.Contains(pluginMessage.Content, "【插件事实结果，必须完整使用】") {
		t.Fatalf("LLM did not receive recall context: %#v", provider.requests)
	}
	if strings.Contains(provider.requests[0].Messages[0].Content, "撤回前的完整内容") {
		t.Fatalf("recall context is still embedded inside the system prompt: %#v", provider.requests[0].Messages)
	}
}

func TestMessageHistoryPluginHandlesRecallBeforeCachedMessage(t *testing.T) {
	plugin := NewMessageHistoryPlugin()
	plugin.Observe(context.Background(), messageEventFromEnvelope(oneBotEnvelope{
		PostType:   "notice",
		NoticeType: "group_recall",
		GroupID:    "123",
		UserID:     "20002",
		MessageID:  "missing-1",
	}))
	resp, err := plugin.Handle(context.Background(), PluginRequest{
		Event: MessageEvent{Kind: EventKindGroup, GroupID: "123"},
		Text:  "刚才撤回了什么",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil || !strings.Contains(resp.Context, "missing-1") || !strings.Contains(resp.Context, "[已撤回消息]") {
		t.Fatalf("response = %#v", resp)
	}
}

func TestDianaConfigToolReturnsRedactedBotConfigAndSkills(t *testing.T) {
	store := &stubLLMProfileStore{
		set: llm.ProfileSet{
			ActiveID: "profile-1",
			Profiles: []llm.Profile{{
				ID:   "profile-1",
				Name: "主配置",
				Config: llm.ProviderConfig{
					Provider: llm.ProviderOpenAICompatible,
					APIKey:   "llm-secret-value",
					BaseURL:  "https://example.com/v1",
					Model:    "gpt-test",
					Headers:  map[string]string{"Authorization": "Bearer hidden"},
				},
			}},
		},
	}
	runtime := NewRuntime(BotConfig{
		Enabled:            true,
		OneBotAccessToken:  "onebot-secret-value",
		NoneBotBridgeToken: "bridge-secret-value",
		AgentEnabled:       true,
		AgentWorkDir:       "/tmp/diana",
		AgentSkillRoots:    []string{"/tmp/diana/skills"},
		AgentMCPConfigPath: "/tmp/diana/.mcp.json",
	}, nilChannel{}, NewDefaultPluginManager(), store, nil, nil, nil)

	got, err := newDianaConfigTool(runtime).Run(context.Background(), map[string]any{"section": "all"})
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid([]byte(got)) {
		t.Fatalf("tool output is not JSON: %s", got)
	}
	for _, leaked := range []string{"llm-secret-value", "onebot-secret-value", "bridge-secret-value", "Bearer hidden"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("tool output leaked %q: %s", leaked, got)
		}
	}
	for _, want := range []string{"official.llm-config-skill", "api_key_configured", "onebot_access_token_configured", "installed_skills"} {
		if !strings.Contains(got, want) {
			t.Fatalf("tool output missing %q: %s", want, got)
		}
	}
}

// TestRuntimeSystemPromptMentionsHomophoneJokes 验证系统提示包含中文谐音梗处理要求。
func TestRuntimeSystemPromptMentionsHomophoneJokes(t *testing.T) {
	runtime := NewRuntime(BotConfig{}, nilChannel{}, NewPluginManager(), nil, nil, nil, nil)
	prompt := runtime.systemPrompt(MessageEvent{Kind: EventKindGroup}, nil)
	if !strings.Contains(prompt, "谐音梗") || !strings.Contains(prompt, "能接梗就自然接") {
		t.Fatalf("system prompt missing homophone guidance: %q", prompt)
	}
	if !strings.Contains(prompt, "当前需要回复的消息") || !strings.Contains(prompt, "不要主动回复旧消息") {
		t.Fatalf("system prompt missing current-message guidance: %q", prompt)
	}
	if !strings.Contains(prompt, "同一发送者紧邻补发") || !strings.Contains(prompt, "发送一条完整回复") || !strings.Contains(prompt, "不要按历史消息逐条作答") {
		t.Fatalf("system prompt missing adjacent-message merge guidance: %q", prompt)
	}
	if !strings.Contains(prompt, "纯文本") || !strings.Contains(prompt, "不要使用 Markdown") || !strings.Contains(prompt, "都必须放在同一条 QQ 消息里") || !strings.Contains(prompt, "严禁在每个列表项或普通段落前使用 <botbr>") || !strings.Contains(prompt, "语义上确实是下一次独立发言") {
		t.Fatalf("system prompt missing QQ plain-text guidance: %q", prompt)
	}
	if strings.Contains(prompt, "需要分段时直接使用换行") {
		t.Fatalf("system prompt still asks the model to split with newlines: %q", prompt)
	}
}

func TestRuntimeSystemPromptExplainsMatchedAliasRoles(t *testing.T) {
	runtime := NewRuntime(BotConfig{GroupTriggers: []string{"嘉然", "Diana"}}, nilChannel{}, NewPluginManager(), nil, nil, nil, nil)
	prompt := runtime.systemPrompt(MessageEvent{
		Kind:       EventKindGroup,
		RawMessage: "嘉然晚晚是向晚还是御坂晚",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "嘉然晚晚是向晚还是御坂晚"}}},
	}, nil)
	for _, want := range []string{
		`"嘉然"、"Diana"`,
		`当前消息命中的配置别名："嘉然"`,
		"命中只表示这条消息的触发来源",
		"以第一人称理解和回应",
		"固定词组",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("system prompt missing %q: %q", want, prompt)
		}
	}
}

func TestRuntimeSystemPromptIncludesTrustedRuntimeClock(t *testing.T) {
	runtime := NewRuntime(BotConfig{}, nilChannel{}, NewPluginManager(), nil, nil, nil, nil)
	prompt := runtime.systemPrompt(MessageEvent{Kind: EventKindPrivate}, nil)
	if !strings.Contains(prompt, "当前运行时钟") || !strings.Contains(prompt, time.Now().Format("2006-01-02")) {
		t.Fatalf("system prompt missing current date: %q", prompt)
	}
	if !strings.Contains(prompt, "UTC") || !strings.Contains(prompt, "不要声称无法访问实时时钟") {
		t.Fatalf("system prompt missing trusted clock guidance: %q", prompt)
	}
}

func TestRuntimeSystemPromptOmitsDeprecatedPoliticalRule(t *testing.T) {
	runtime := NewRuntime(BotConfig{}, nilChannel{}, NewPluginManager(), nil, nil, nil, nil)
	prompt := runtime.systemPrompt(MessageEvent{Kind: EventKindGroup}, nil)
	for _, removed := range []string{"禁止回复、展开、评价、搜索或协助生成任何政治相关内容", "简短说明群规不方便聊政治"} {
		if strings.Contains(prompt, removed) {
			t.Fatalf("system prompt still contains deprecated political rule %q: %q", removed, prompt)
		}
	}

	legacy := BotConfig{SystemPrompt: "前置人格。" + deprecatedPoliticalPromptRule + "后置人格。"}
	migrated := legacy.WithDefaults().SystemPrompt
	if migrated != "前置人格。后置人格。" {
		t.Fatalf("legacy political rule migration = %q", migrated)
	}
}

// TestRuntimeIgnoresSelfMessage 验证对应功能场景。
func TestRuntimeIgnoresSelfMessage(t *testing.T) {
	runtime := NewRuntime(BotConfig{
		BotQQ: "42",
	}, nilChannel{}, NewPluginManager(), nil, nil, nil, nil)
	if !runtime.isSelfMessage(MessageEvent{UserID: "42"}) {
		t.Fatal("self message should be ignored")
	}
	if runtime.isSelfMessage(MessageEvent{UserID: "10001"}) {
		t.Fatal("other user should not be treated as self")
	}
}

func TestRuntimeRecordsSelfMessageWithoutReply(t *testing.T) {
	channel := &recordingChannel{}
	runtime := NewRuntime(BotConfig{
		BotQQ: "42",
	}, channel, NewPluginManager(), nil, nil, nil, nil)
	event := MessageEvent{
		Kind:       EventKindGroup,
		GroupID:    "123456",
		UserID:     "42",
		MessageID:  "real-msg-1",
		RawMessage: "刚刚撤回的是两条消息",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "刚刚撤回的是两条消息"}}},
	}
	if err := runtime.HandleEvent(context.Background(), event); err != nil {
		t.Fatalf("HandleEvent() error = %v", err)
	}
	if len(channel.sent) != 0 || len(channel.calls) != 0 {
		t.Fatalf("self message should not trigger replies: sent=%#v calls=%#v", channel.sent, channel.calls)
	}
	history := runtime.contextHistory(MessageEvent{Kind: EventKindGroup, GroupID: "123456"})
	if len(history) != 1 || history[0].MessageID != "real-msg-1" || history[0].RawMessage != "刚刚撤回的是两条消息" {
		t.Fatalf("history = %#v", history)
	}
}

func TestRuntimeDisabledUserRecordsWithoutReply(t *testing.T) {
	channel := &recordingChannel{}
	runtime := NewRuntime(BotConfig{
		BotQQ:         "42",
		OwnerID:       "10000",
		DisabledUsers: []string{"10007"},
		GroupTriggers: []string{"Diana"},
	}, channel, NewPluginManager(), nil, nil, nil, nil)
	event := MessageEvent{
		Kind:       EventKindGroup,
		GroupID:    "123456",
		UserID:     "10007",
		MessageID:  "disabled-user-1",
		RawMessage: "[CQ:at,qq=42] Diana 看一下这个链接 https://v.douyin.com/abc",
		Segments: []MessageSegment{
			{Type: "at", Data: map[string]string{"qq": "42"}},
			{Type: "text", Data: map[string]string{"text": " Diana 看一下这个链接 https://v.douyin.com/abc"}},
		},
	}
	if err := runtime.HandleEvent(context.Background(), event); err != nil {
		t.Fatalf("HandleEvent() error = %v", err)
	}
	if len(channel.sent) != 0 || len(channel.calls) != 0 {
		t.Fatalf("disabled user should not trigger replies: sent=%#v calls=%#v", channel.sent, channel.calls)
	}
	history := runtime.contextHistory(MessageEvent{Kind: EventKindGroup, GroupID: "123456"})
	if len(history) != 1 || history[0].MessageID != "disabled-user-1" || history[0].UserID != "10007" {
		t.Fatalf("history = %#v", history)
	}
}

func TestRuntimeSelfEchoFeedsRecallHistory(t *testing.T) {
	plugins := NewDefaultPluginManager()
	runtime := NewRuntime(BotConfig{
		BotQQ: "42",
	}, &recordingChannel{}, plugins, nil, nil, nil, nil)
	selfMessage := MessageEvent{
		Kind:       EventKindGroup,
		GroupID:    "123456",
		UserID:     "42",
		MessageID:  "real-msg-1",
		RawMessage: "机器人刚刚发出的内容",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "机器人刚刚发出的内容"}}},
	}
	if err := runtime.HandleEvent(context.Background(), selfMessage); err != nil {
		t.Fatalf("HandleEvent(self) error = %v", err)
	}
	recall := MessageEvent{
		Kind:       EventKindNotice,
		SubType:    "group_recall",
		SelfID:     "42",
		UserID:     "42",
		OperatorID: "42",
		GroupID:    "123456",
		MessageID:  "real-msg-1",
	}
	if err := runtime.HandleEvent(context.Background(), recall); err != nil {
		t.Fatalf("HandleEvent(recall) error = %v", err)
	}
	resp, err := plugins.RunOneWithOverrides(context.Background(), messageHistoryPluginID, PluginRequest{
		Event: MessageEvent{Kind: EventKindGroup, GroupID: "123456"}, Text: "查看撤回记录",
	}, nil)
	if err != nil {
		t.Fatalf("message history plugin error = %v", err)
	}
	if resp == nil || resp.Reply != "最近24小时没有记录到群消息撤回。" || len(resp.ForwardMessages) != 0 {
		t.Fatalf("bot's own recall must not enter recall history: %#v", resp)
	}
}

func TestRuntimeSendsRecallSummaryWithFlatForgedForward(t *testing.T) {
	channel := &recordingChannel{}
	runtime := NewRuntime(BotConfig{Name: "Diana", BotQQ: "42"}, channel, NewPluginManager(), nil, nil, nil, nil)
	resp := PluginResponse{NestedForward: true, ForwardMessages: []OutgoingMessage{
		{Text: "第一条原消息", ForwardName: "Alice", ForwardUIN: "10001", ForwardTime: 100},
		{Segments: []MessageSegment{{Type: "image", Data: map[string]string{"cached_file": "/tmp/recalled.jpg", "cached_mime": "image/jpeg", "url": "https://example.com/recalled.jpg"}}}, ForwardName: "Bob", ForwardUIN: "10002", ForwardTime: 200},
	}}
	if err := runtime.sendNestedForwardPluginResponse(context.Background(), MessageEvent{Kind: EventKindGroup, GroupID: "123456", SelfID: "42"}, resp, "最近24小时共有两条撤回消息。", runtime.Config()); err != nil {
		t.Fatal(err)
	}
	if len(channel.calls) != 1 || channel.calls[0].action != "send_group_forward_msg" {
		t.Fatalf("recall forward calls = %#v", channel.calls)
	}
	outerNodes, ok := channel.calls[0].params["messages"].([]map[string]any)
	if !ok || len(outerNodes) != 3 {
		t.Fatalf("outer nodes = %#v", channel.calls[0].params["messages"])
	}
	summaryData := outerNodes[0]["data"].(map[string]any)
	firstData := outerNodes[1]["data"].(map[string]any)
	secondData := outerNodes[2]["data"].(map[string]any)
	if summaryData["name"] != "Diana" || firstData["name"] != "Alice" || firstData["nickname"] != "Alice" || firstData["uin"] != "10001" || firstData["user_id"] != "10001" || firstData["time"] != int64(100) || secondData["name"] != "Bob" {
		t.Fatalf("flat forged nodes = %#v", outerNodes)
	}
	content, ok := secondData["content"].([]map[string]any)
	if !ok || len(content) != 1 || content[0]["type"] != "image" {
		t.Fatalf("image recall node = %#v", secondData["content"])
	}
}

func TestRuntimeFallsBackToTextRecallForwardWhenMediaForwardFails(t *testing.T) {
	channel := &rejectMediaRecallForwardChannel{}
	runtime := NewRuntime(BotConfig{Name: "Diana", BotQQ: "42"}, channel, NewPluginManager(), nil, nil, nil, nil)
	resp := PluginResponse{NestedForward: true, ForwardMessages: []OutgoingMessage{{
		Segments:    []MessageSegment{{Type: "image", Data: map[string]string{"cached_file": "/tmp/recalled.jpg"}}},
		ForwardName: "Alice",
		ForwardUIN:  "10001",
	}}}

	if err := runtime.sendNestedForwardPluginResponse(context.Background(), MessageEvent{Kind: EventKindGroup, GroupID: "123456", SelfID: "42"}, resp, "最近有一条图片撤回。", runtime.Config()); err != nil {
		t.Fatal(err)
	}
	forwardCalls := recordedCallsByAction(channel.calls, "send_group_forward_msg")
	if len(forwardCalls) != 2 {
		t.Fatalf("forward calls = %#v", channel.calls)
	}
	if !forwardCallContainsSegmentType(forwardCalls[0], "image") || forwardCallContainsSegmentType(forwardCalls[1], "image") {
		t.Fatalf("media fallback calls = %#v", forwardCalls)
	}
	if !forwardCallContainsText(forwardCalls[1], "[图片]") {
		t.Fatalf("text fallback missing image marker: %#v", forwardCalls[1])
	}
}

type rejectMediaRecallForwardChannel struct {
	recordingChannel
}

func (c *rejectMediaRecallForwardChannel) CallAPI(_ context.Context, action string, params map[string]any) (map[string]any, error) {
	call := recordingAPICall{action: action, params: params}
	c.calls = append(c.calls, call)
	if forwardCallContainsSegmentType(call, "image") {
		return nil, errors.New("media forward rejected")
	}
	return map[string]any{"message_id": int64(902)}, nil
}

func forwardCallContainsSegmentType(call recordingAPICall, segmentType string) bool {
	nodes, _ := call.params["messages"].([]map[string]any)
	for _, node := range nodes {
		data, _ := node["data"].(map[string]any)
		segments, _ := data["content"].([]map[string]any)
		for _, segment := range segments {
			if segment["type"] == segmentType {
				return true
			}
		}
	}
	return false
}

func forwardCallContainsText(call recordingAPICall, want string) bool {
	nodes, _ := call.params["messages"].([]map[string]any)
	for _, node := range nodes {
		data, _ := node["data"].(map[string]any)
		segments, _ := data["content"].([]map[string]any)
		for _, segment := range segments {
			if segment["type"] != "text" {
				continue
			}
			segmentData, _ := segment["data"].(map[string]string)
			if strings.Contains(segmentData["text"], want) {
				return true
			}
		}
	}
	return false
}

func TestSplitReplyTreatsBotbrAsMessageBreak(t *testing.T) {
	got := splitReply("abc<botbr>def", 20)
	want := []string{"abc", "def"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSplitReplyHonorsChunkSize(t *testing.T) {
	got := splitReply("abcdefg", 3)
	want := []string{"abc", "def", "g"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSplitReplySplitsBlankLineParagraphs(t *testing.T) {
	got := splitReply("第一段\n仍是第一段\n\n第二段", 100)
	want := []string{"第一段\n仍是第一段", "第二段"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRuntimeBotbrReplySendsMultipleMessages(t *testing.T) {
	channel := &recordingChannel{}
	runtime := NewRuntime(BotConfig{DirectReplyChunkSize: 100}, channel, NewPluginManager(), nil, nil, nil, nil)

	err := runtime.send(context.Background(), MessageEvent{
		Kind:      EventKindGroup,
		GroupID:   "123456",
		UserID:    "10001",
		MessageID: "msg-1",
	}, "刚刚撤回的是两条：<botbr>1. A<botbr>2. B")
	if err != nil {
		t.Fatalf("send() error = %v", err)
	}
	if len(channel.sent) != 3 {
		t.Fatalf("sent = %#v", channel.sent)
	}
	want := []string{"刚刚撤回的是两条：", "1. A", "2. B"}
	for i := range want {
		if channel.sent[i].Text != want[i] {
			t.Fatalf("sent[%d].Text = %q, want %q", i, channel.sent[i].Text, want[i])
		}
	}
	if channel.sent[0].ReplyMessageID != "msg-1" || channel.sent[0].MentionUserID != "10001" {
		t.Fatalf("message metadata = %#v", channel.sent[0])
	}
	if channel.sent[1].ReplyMessageID != "" || channel.sent[1].MentionUserID != "" || channel.sent[2].ReplyMessageID != "" || channel.sent[2].MentionUserID != "" {
		t.Fatalf("later messages should be plain text: %#v", channel.sent)
	}
}

// TestRuntimeGroupSplitReplyQuotesOnlyFirstChunk 验证群聊分条时只有第一条带引用和 @。
func TestRuntimeGroupSplitReplyQuotesOnlyFirstChunk(t *testing.T) {
	channel := &recordingChannel{}
	runtime := NewRuntime(BotConfig{DirectReplyChunkSize: 3}, channel, NewPluginManager(), nil, nil, nil, nil)

	err := runtime.send(context.Background(), MessageEvent{
		Kind:      EventKindGroup,
		GroupID:   "123456",
		UserID:    "10001",
		MessageID: "msg-1",
	}, "abcdef")
	if err != nil {
		t.Fatalf("send() error = %v", err)
	}
	if len(channel.sent) != 2 {
		t.Fatalf("sent = %#v", channel.sent)
	}
	first := channel.sent[0]
	if first.GroupID != "123456" || first.ReplyMessageID != "msg-1" || first.MentionUserID != "10001" {
		t.Fatalf("first message = %#v", first)
	}
	second := channel.sent[1]
	if second.GroupID != "123456" || second.ReplyMessageID != "" || second.MentionUserID != "" {
		t.Fatalf("second message should be plain group text: %#v", second)
	}
}

func TestRuntimeFiveReplyChunksUseForwardMessage(t *testing.T) {
	channel := &recordingChannel{}
	runtime := NewRuntime(BotConfig{
		Name:                 "Diana",
		BotQQ:                "42",
		DirectReplyChunkSize: 1,
	}, channel, NewPluginManager(), nil, nil, nil, nil)

	err := runtime.send(context.Background(), MessageEvent{
		Kind:      EventKindGroup,
		GroupID:   "123456",
		UserID:    "10001",
		MessageID: "msg-1",
	}, "abcde")
	if err != nil {
		t.Fatalf("send() error = %v", err)
	}
	if len(channel.sent) != 0 {
		t.Fatalf("direct messages should not be sent: %#v", channel.sent)
	}
	if len(channel.calls) != 1 {
		t.Fatalf("api calls = %#v", channel.calls)
	}
	call := channel.calls[0]
	if call.action != "send_group_forward_msg" || call.params["group_id"] != int64(123456) {
		t.Fatalf("api call = %#v", call)
	}
	nodes, ok := call.params["messages"].([]map[string]any)
	if !ok || len(nodes) != forwardReplyChunkCountThreshold {
		t.Fatalf("messages = %#v", call.params["messages"])
	}
}

// TestRuntimeLongGroupReplyUsesForwardMessage 验证超长群聊回复会走合并转发。
func TestRuntimeLongGroupReplyUsesForwardMessage(t *testing.T) {
	channel := &recordingChannel{}
	runtime := NewRuntime(BotConfig{
		Name:                  "Diana",
		BotQQ:                 "42",
		DirectReplyChunkSize:  3,
		ForwardReplyThreshold: 5,
	}, channel, NewPluginManager(), nil, nil, nil, nil)

	err := runtime.send(context.Background(), MessageEvent{
		Kind:      EventKindGroup,
		GroupID:   "123456",
		UserID:    "10001",
		MessageID: "msg-1",
	}, "abcdef")
	if err != nil {
		t.Fatalf("send() error = %v", err)
	}
	if len(channel.sent) != 0 {
		t.Fatalf("direct messages should not be sent: %#v", channel.sent)
	}
	if len(channel.calls) != 1 {
		t.Fatalf("api calls = %#v", channel.calls)
	}
	call := channel.calls[0]
	if call.action != "send_group_forward_msg" || call.params["group_id"] != int64(123456) {
		t.Fatalf("api call = %#v", call)
	}
	nodes, ok := call.params["messages"].([]map[string]any)
	if !ok || len(nodes) != 2 {
		t.Fatalf("messages = %#v", call.params["messages"])
	}
	data, ok := nodes[0]["data"].(map[string]any)
	if !ok || data["name"] != "Diana" || data["uin"] != "42" {
		t.Fatalf("first node data = %#v", nodes[0]["data"])
	}
	content, ok := data["content"].([]map[string]any)
	if !ok || len(content) != 1 || content[0]["type"] != "text" {
		t.Fatalf("first node content = %#v", data["content"])
	}
}

// TestRuntimeLongPrivateReplyUsesForwardMessage 验证超长私聊回复也会走私聊合并转发。
func TestRuntimeLongPrivateReplyUsesForwardMessage(t *testing.T) {
	channel := &recordingChannel{}
	runtime := NewRuntime(BotConfig{
		BotQQ:                 "42",
		DirectReplyChunkSize:  3,
		ForwardReplyThreshold: 5,
	}, channel, NewPluginManager(), nil, nil, nil, nil)

	err := runtime.send(context.Background(), MessageEvent{
		Kind:   EventKindPrivate,
		UserID: "10001",
	}, "abcdef")
	if err != nil {
		t.Fatalf("send() error = %v", err)
	}
	if len(channel.sent) != 0 || len(channel.calls) != 1 {
		t.Fatalf("sent=%#v calls=%#v", channel.sent, channel.calls)
	}
	call := channel.calls[0]
	if call.action != "send_private_forward_msg" || call.params["user_id"] != int64(10001) {
		t.Fatalf("api call = %#v", call)
	}
}

// TestRuntimeOwnerCommandsSwitchProfilesAndClearHistory 验证对应功能场景。
func TestRuntimeOwnerCommandsSwitchProfilesAndClearHistory(t *testing.T) {
	reminders := &stubReminderStore{}
	store := &stubLLMProfileStore{
		set: llm.ProfileSet{
			ActiveID: "a",
			Profiles: []llm.Profile{
				{ID: "a", Name: "主配置", Config: llm.ProviderConfig{Provider: llm.ProviderOpenAICompatible, Model: "example-chat-model"}},
				{ID: "b", Name: "备用配置", Config: llm.ProviderConfig{Provider: llm.ProviderAnthropic, Model: "claude-sonnet-4-5"}},
			},
		},
	}
	runtime := NewRuntime(BotConfig{OwnerID: "10001"}, nilChannel{}, NewPluginManager(), store, reminders, nil, nil)
	event := MessageEvent{Kind: EventKindPrivate, UserID: "10001"}

	reply, handled := runtime.handleOwnerCommand(event, "lllm 当前")
	if !handled || reply == "" || !strings.Contains(reply, "主配置") {
		t.Fatalf("reply=%q handled=%v", reply, handled)
	}

	reply, handled = runtime.handleOwnerCommand(event, "lllm 切换 备用配置")
	if !handled || !strings.Contains(reply, "备用配置") {
		t.Fatalf("reply=%q handled=%v", reply, handled)
	}
	if store.set.ActiveID != "b" {
		t.Fatalf("ActiveID = %q, want b", store.set.ActiveID)
	}

	runtime.history[sessionKey(event)] = []MessageEvent{{MessageID: "1"}}
	reply, handled = runtime.handleOwnerCommand(event, "清空上下文")
	if !handled || !strings.Contains(reply, "已清空") {
		t.Fatalf("reply=%q handled=%v", reply, handled)
	}
	if history := runtime.contextHistory(event); len(history) != 0 {
		t.Fatalf("history = %#v", history)
	}

	reply, handled = runtime.handleOwnerCommand(event, "提醒 添加 1m 记得喝水")
	if !handled || !strings.Contains(reply, "提醒已创建") || len(reminders.items) != 1 {
		t.Fatalf("reply=%q handled=%v reminders=%#v", reply, handled, reminders.items)
	}

	reply, handled = runtime.handleOwnerCommand(event, "提醒 删除 "+reminders.items[0].ID)
	if !handled || !strings.Contains(reply, "提醒已删除") || len(reminders.items) != 0 {
		t.Fatalf("reply=%q handled=%v reminders=%#v", reply, handled, reminders.items)
	}

	reply, handled = runtime.handleOwnerCommand(event, "群 禁用 123456")
	if !handled || !strings.Contains(reply, "已禁用") || !runtime.isGroupDisabled("123456") {
		t.Fatalf("reply=%q handled=%v disabled=%v", reply, handled, runtime.isGroupDisabled("123456"))
	}

	reply, handled = runtime.handleOwnerCommand(event, "群 启用 123456")
	if !handled || !strings.Contains(reply, "已恢复") || runtime.isGroupDisabled("123456") {
		t.Fatalf("reply=%q handled=%v disabled=%v", reply, handled, runtime.isGroupDisabled("123456"))
	}
}

// TestRuntimeLLMConfigUsesSemanticAgentTool verifies that configuration changes
// happen only after the Agent explicitly selects the structured tool.
func TestRuntimeLLMConfigUsesSemanticAgentTool(t *testing.T) {
	channel := &recordingChannel{}
	provider := &sequenceLLMProvider{replies: []string{
		`{"action":"none","prompt":""}`,
		`{"action":"tool","tool":"diana.llm_config","input":{"operation":"update","provider":"anthropic","model":"claude-sonnet-4-5"}}`,
		`{"action":"final","content":"已切换到 claude-sonnet-4-5。"}`,
	}}
	store := &stubLLMProfileStore{
		set: llm.ProfileSet{
			ActiveID: "main",
			Profiles: []llm.Profile{
				{
					ID:   "main",
					Name: "主配置",
					Config: llm.ProviderConfig{
						Provider: llm.ProviderOpenAICompatible,
						APIKey:   "valid-key",
						Model:    "example-chat-model",
					},
				},
			},
		},
	}
	runtime := NewRuntime(BotConfig{OwnerID: "10001", AgentEnabled: true, AgentWorkDir: t.TempDir(), AgentMaxSteps: 3}, channel, NewDefaultPluginManager(), store, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	runtime.SetLLMModelLister(func(context.Context, llm.ProviderConfig) ([]llm.ModelInfo, error) {
		return []llm.ModelInfo{{ID: "claude-sonnet-4-5"}}, nil
	})

	reply, err := runtime.replyTo(context.Background(), MessageEvent{Kind: EventKindPrivate, UserID: "10001", RawMessage: "以后用 Anthropic 的 claude-sonnet-4-5"}, "以后用 Anthropic 的 claude-sonnet-4-5")
	if err != nil {
		t.Fatalf("replyTo() error = %v", err)
	}
	if !strings.Contains(reply, "claude-sonnet-4-5") || len(channel.sent) != 1 || len(provider.requests) != 3 {
		t.Fatalf("reply=%q sent=%#v", reply, channel.sent)
	}
	if got := store.Current(); got.Provider != llm.ProviderAnthropic || got.Model != "claude-sonnet-4-5" {
		t.Fatalf("current = %#v", got)
	}
}

// TestRuntimeKeepsMentionAndGroupTriggerInPrompt 验证群 @ 和触发词会保留给模型，而不是被剥成空输入。
func TestRuntimeKeepsMentionAndGroupTriggerInPrompt(t *testing.T) {
	channel := &recordingChannel{}
	provider := &capturingLLMProvider{reply: "在呢"}
	runtime := NewRuntime(BotConfig{
		BotQQ:         "42",
		GroupTriggers: []string{"嘉然"},
	}, channel, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})

	reply, err := runtime.replyTo(context.Background(), MessageEvent{
		Kind:       EventKindGroup,
		GroupID:    "123456",
		UserID:     "10001",
		MessageID:  "msg-1",
		RawMessage: "[CQ:at,qq=42] 嘉然",
		Segments: []MessageSegment{
			{Type: "at", Data: map[string]string{"qq": "42"}},
			{Type: "text", Data: map[string]string{"text": " 嘉然"}},
		},
	}, "@42 嘉然")
	if err != nil {
		t.Fatalf("replyTo() error = %v", err)
	}
	if reply != provider.reply || len(channel.sent) != 1 {
		t.Fatalf("reply=%q sent=%#v", reply, channel.sent)
	}
	got := provider.request.Messages[len(provider.request.Messages)-1].Content
	if !strings.Contains(got, "【当前需要回复的消息】") || !strings.Contains(got, "@42 嘉然") {
		t.Fatalf("last message content = %q", got)
	}
}

func TestRuntimeGroupLLMCanChooseMultipleMentionTargets(t *testing.T) {
	channel := &recordingChannel{}
	provider := &privacyAwareTestProvider{}
	var milkAlias, currentAlias string
	provider.generate = func(call int, req llm.GenerateRequest) (string, error) {
		switch call {
		case 1:
			return `{"message_id":"history-1","confidence":0.96,"reason":"她指近期发言者 Alice"}`, nil
		case 2:
			return `{"action":"none"}`, nil
		case 3:
			milkAlias = privacyAliasForDisplayName(req, "Alice")
			currentAlias = privacyAliasForDisplayName(req, "Bob")
			if milkAlias == "" || currentAlias == "" {
				return "", fmt.Errorf("mention aliases missing: Alice=%q current=%q", milkAlias, currentAlias)
			}
			return fmt.Sprintf("[CQ:at,qq=%s] [CQ:at,qq=%s] 请尽快确认这件事。", milkAlias, currentAlias), nil
		default:
			return "", fmt.Errorf("unexpected LLM call %d", call)
		}
	}
	runtime := NewRuntime(BotConfig{BotQQ: "42"}, channel, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	runtime.remember(MessageEvent{
		Kind:       EventKindGroup,
		GroupID:    "123456",
		UserID:     "10002",
		MessageID:  "history-1",
		SenderName: "Alice",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "这是我的情况"}}},
	})
	event := MessageEvent{
		Kind:       EventKindGroup,
		SelfID:     "42",
		GroupID:    "123456",
		UserID:     "10008",
		MessageID:  "current-1",
		SenderName: "Bob",
		RawMessage: "提醒她及时处理",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "提醒她及时处理"}}},
		ToMe:       true,
	}

	reply, err := runtime.replyTo(context.Background(), event, event.RawMessage)
	if err != nil {
		t.Fatalf("replyTo() error = %v", err)
	}
	if len(channel.sent) != 1 {
		t.Fatalf("sent = %#v", channel.sent)
	}
	if channel.sent[0].MentionUserID != "" || channel.sent[0].ReplyMessageID != event.MessageID {
		t.Fatalf("LLM reply should keep reply metadata without forced current mention: %#v", channel.sent[0])
	}
	segments := buildOutgoingSegments(channel.sent[0])
	var mentioned []string
	for _, segment := range segments {
		if segment["type"] == "at" {
			mentioned = append(mentioned, segment["data"].(map[string]string)["qq"])
		}
	}
	wantMentioned := []string{"10002", "10008"}
	if len(mentioned) != len(wantMentioned) || mentioned[0] != wantMentioned[0] || mentioned[1] != wantMentioned[1] {
		t.Fatalf("mentioned = %#v, want %#v; segments = %#v", mentioned, wantMentioned, segments)
	}
	systemPrompt := provider.requests[2].Messages[0].Content
	for _, want := range []string{"群聊真实提及规则", `"user_id":"` + milkAlias + `"`, `"display_name":"Alice"`, `"user_id":"` + currentAlias + `"`, "可以同时提及多人", "发送层固定在第一条回复开头引用当前消息并 @ 当前发言者", "你只决定是否还要提及其他成员", "原样保留额外 CQ at 的对象和相对位置"} {
		if !strings.Contains(systemPrompt, want) {
			t.Fatalf("system prompt missing %q: %s", want, systemPrompt)
		}
	}
	for _, req := range provider.requests {
		protected := requestTextForPrivacyTest(req)
		for _, realID := range []string{"10002", "10008", "123456"} {
			if strings.Contains(protected, realID) {
				t.Fatalf("provider request leaked QQ ID %s: %s", realID, protected)
			}
		}
	}
	if reply != "[CQ:at,qq=10002] [CQ:at,qq=10008] 请尽快确认这件事。" {
		t.Fatalf("reply = %q", reply)
	}
}

func TestRuntimeGroupLLMDefaultsMentionToCurrentSender(t *testing.T) {
	channel := &recordingChannel{}
	provider := &sequenceLLMProvider{replies: []string{`{"action":"none"}`, "这是面向全群的说明。"}}
	runtime := NewRuntime(BotConfig{BotQQ: "42"}, channel, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	event := MessageEvent{
		Kind:      EventKindGroup,
		SelfID:    "42",
		GroupID:   "123456",
		UserID:    "10001",
		MessageID: "current-1",
		ToMe:      true,
		Segments:  []MessageSegment{{Type: "text", Data: map[string]string{"text": "说明一下"}}},
	}
	if _, err := runtime.replyTo(context.Background(), event, "说明一下"); err != nil {
		t.Fatal(err)
	}
	if channel.sent[0].ReplyMessageID != event.MessageID {
		t.Fatalf("reply message id = %q, want %q", channel.sent[0].ReplyMessageID, event.MessageID)
	}
	segments := buildOutgoingSegments(channel.sent[0])
	foundCurrentMention := false
	for _, segment := range segments {
		if segment["type"] == "at" && segment["data"].(map[string]string)["qq"] == event.UserID {
			foundCurrentMention = true
		}
	}
	if !foundCurrentMention {
		t.Fatalf("default current sender mention missing: %#v", segments)
	}
}

func TestRuntimeGroupLLMStillMentionsCurrentSenderWithSemanticReference(t *testing.T) {
	channel := &recordingChannel{}
	runtime := NewRuntime(BotConfig{}, channel, NewPluginManager(), nil, nil, nil, nil)
	event := MessageEvent{
		Kind:      EventKindGroup,
		SelfID:    "42",
		GroupID:   "123456",
		UserID:    "10001",
		MessageID: "current-1",
		Quoted: &QuotedMessage{
			MessageID:  "milk-message",
			UserID:     "10002",
			SenderName: "Alice",
			Semantic:   true,
		},
	}
	if _, err := runtime.sendGeneratedReplyWithMessageIDs(context.Background(), event, "请先检查证书配置。"); err != nil {
		t.Fatal(err)
	}
	if channel.sent[0].ReplyMessageID != event.MessageID {
		t.Fatalf("reply message id = %q, want %q", channel.sent[0].ReplyMessageID, event.MessageID)
	}
	segments := buildOutgoingSegments(channel.sent[0])
	var mentioned []string
	for _, segment := range segments {
		if segment["type"] == "at" {
			mentioned = append(mentioned, segment["data"].(map[string]string)["qq"])
		}
	}
	if len(mentioned) != 1 || mentioned[0] != event.UserID {
		t.Fatalf("mentioned = %#v, want current sender %q; segments = %#v", mentioned, event.UserID, segments)
	}
}

func TestRuntimeGroupLLMAddsExplicitMentionsAfterCurrentSender(t *testing.T) {
	channel := &recordingChannel{}
	runtime := NewRuntime(BotConfig{}, channel, NewPluginManager(), nil, nil, nil, nil)
	event := MessageEvent{
		Kind:      EventKindGroup,
		GroupID:   "123456",
		UserID:    "10001",
		MessageID: "current-1",
	}

	if _, err := runtime.sendGeneratedReplyWithMessageIDs(context.Background(), event, "[CQ:at,qq=10002] 一起看一下。"); err != nil {
		t.Fatal(err)
	}
	segments := buildOutgoingSegments(channel.sent[0])
	var mentioned []string
	for _, segment := range segments {
		if segment["type"] == "at" {
			mentioned = append(mentioned, segment["data"].(map[string]string)["qq"])
		}
	}
	want := []string{"10001", "10002"}
	if len(mentioned) != len(want) || mentioned[0] != want[0] || mentioned[1] != want[1] {
		t.Fatalf("mentioned = %#v, want %#v; segments = %#v", mentioned, want, segments)
	}
}

func TestRuntimeGroupLLMPreservesModelChosenAdditionalMentionPosition(t *testing.T) {
	channel := &recordingChannel{}
	runtime := NewRuntime(BotConfig{}, channel, NewPluginManager(), nil, nil, nil, nil)
	event := MessageEvent{
		Kind:      EventKindGroup,
		GroupID:   "123456",
		UserID:    "10001",
		MessageID: "current-1",
	}

	if _, err := runtime.sendGeneratedReplyWithMessageIDs(context.Background(), event, "这件事请 [CQ:at,qq=10002] 明天确认。"); err != nil {
		t.Fatal(err)
	}
	if channel.sent[0].MentionUserID != event.UserID {
		t.Fatalf("current sender mention = %q, want %q", channel.sent[0].MentionUserID, event.UserID)
	}
	segments := buildOutgoingSegments(channel.sent[0])
	var mentionIndexes []int
	var mentioned []string
	for index, segment := range segments {
		if segment["type"] == "at" {
			mentionIndexes = append(mentionIndexes, index)
			mentioned = append(mentioned, segment["data"].(map[string]string)["qq"])
		}
	}
	if len(mentioned) != 2 || mentioned[0] != event.UserID || mentioned[1] != "10002" {
		t.Fatalf("mentioned = %#v, want current sender then additional target; segments = %#v", mentioned, segments)
	}
	extraIndex := mentionIndexes[1]
	if extraIndex == 0 || segments[extraIndex-1]["type"] != "text" || !strings.Contains(segments[extraIndex-1]["data"].(map[string]string)["text"], "这件事请") {
		t.Fatalf("additional mention position was not preserved: %#v", segments)
	}
}

// TestRuntimeMentionOnlyUsesFallbackPrompt 验证只艾特机器人时不会向 LLM 传空消息。
func TestRuntimeMentionOnlyUsesFallbackPrompt(t *testing.T) {
	channel := &recordingChannel{}
	provider := &capturingLLMProvider{reply: "在"}
	runtime := NewRuntime(BotConfig{BotQQ: "42", PassiveReplyChance: 1}, channel, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	_, err := runtime.replyTo(context.Background(), MessageEvent{
		Kind:       EventKindGroup,
		GroupID:    "123456",
		UserID:     "10001",
		MessageID:  "msg-1",
		RawMessage: "[CQ:at,qq=42]",
		Segments: []MessageSegment{
			{Type: "at", Data: map[string]string{"qq": "42"}},
		},
	}, "@42")
	if err != nil {
		t.Fatalf("replyTo() error = %v", err)
	}
	got := provider.request.Messages[len(provider.request.Messages)-1].Content
	if !strings.Contains(got, "【当前需要回复的消息】") || !strings.Contains(got, "@42") || !strings.Contains(got, "有效唤醒") {
		t.Fatalf("last message content = %q", got)
	}
}

// TestRuntimeKeepsReplyAndMentionInCurrentPrompt 验证引用和 @ 都作为当前消息的一部分传给模型。
func TestRuntimeKeepsReplyAndMentionInCurrentPrompt(t *testing.T) {
	channel := &recordingChannel{}
	provider := &capturingLLMProvider{reply: "接住了"}
	runtime := NewRuntime(BotConfig{BotQQ: "42", PassiveReplyChance: 1}, channel, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})

	_, err := runtime.replyTo(context.Background(), MessageEvent{
		Kind:       EventKindGroup,
		GroupID:    "123456",
		UserID:     "10001",
		MessageID:  "msg-2",
		RawMessage: "[CQ:reply,id=abc][CQ:at,qq=42]",
		Segments: []MessageSegment{
			{Type: "reply", Data: map[string]string{"id": "abc"}},
			{Type: "at", Data: map[string]string{"qq": "42"}},
		},
	}, "[回复:abc]@42")
	if err != nil {
		t.Fatalf("replyTo() error = %v", err)
	}
	got := provider.request.Messages[len(provider.request.Messages)-1].Content
	for _, want := range []string{"【当前需要回复的消息】", "[回复:abc]", "@42", "当前消息包含 @ 标记", "当前消息包含引用/回复标记"} {
		if !strings.Contains(got, want) {
			t.Fatalf("last message content = %q, missing %q", got, want)
		}
	}
}

// TestRuntimeCarriesRecentImageIntoFollowup 验证图片消息后的追问会把历史图片带进 LLM。
func TestRuntimeCarriesRecentImageIntoFollowup(t *testing.T) {
	channel := &recordingChannel{}
	provider := &sequenceLLMProvider{replies: []string{
		`{"message_id":"img-1","confidence":0.96,"reason":"当前问题指向上一张图片"}`,
		`{"action":"none","prompt":""}`,
		"这是一张测试图片。",
	}}
	runtime := NewRuntime(BotConfig{}, channel, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	imageBody := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00}
	imageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(imageBody)
	}))
	defer imageServer.Close()
	runtime.remember(MessageEvent{
		Kind:      EventKindPrivate,
		UserID:    "10001",
		MessageID: "img-1",
		Segments: []MessageSegment{
			{Type: "image", Data: map[string]string{"url": imageServer.URL + "/image.png"}},
		},
		SenderName: "Diana",
	})

	reply, err := runtime.replyTo(context.Background(), MessageEvent{
		Kind:       EventKindPrivate,
		UserID:     "10001",
		MessageID:  "q-1",
		RawMessage: "这是什么",
		Segments: []MessageSegment{
			{Type: "text", Data: map[string]string{"text": "这是什么"}},
		},
	}, "这是什么")
	if err != nil {
		t.Fatalf("replyTo() error = %v", err)
	}
	if reply != "这是一张测试图片。" || len(channel.sent) != 1 {
		t.Fatalf("reply=%q sent=%#v", reply, channel.sent)
	}
	wantImageURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(imageBody)
	if len(provider.requests) != 3 || !requestHasImageURL(provider.requests[2], wantImageURL) {
		t.Fatalf("requests missing selected image url: %#v", provider.requests)
	}
}

func TestRuntimeRoutesGroupImageContextFollowupWithLLM(t *testing.T) {
	channel := &recordingChannel{}
	imageURL := "data:image/png;base64,aGVsbG8="
	provider := &sequenceLLMProvider{replies: []string{
		`{"should_reply":true,"confidence":0.98,"category":"needs_response","directed_at_bot":false,"answerable":true}`,
		`{"message_id":"img-1","confidence":0.95,"reason":"当前问题指向群友发的图片"}`,
		`{"action":"none"}`,
		"看起来是拍摄角度和光线让表情显得没那么明显。",
	}}
	runtime := NewRuntime(BotConfig{BotQQ: "42", PassiveReplyChance: 1}, channel, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	runtime.remember(MessageEvent{
		Kind:      EventKindGroup,
		GroupID:   "123456",
		UserID:    "20002",
		MessageID: "img-1",
		Segments: []MessageSegment{{
			Type: "image",
			Data: map[string]string{"file": imageURL},
		}},
		SenderName: "Alice",
	})
	event := MessageEvent{
		Kind:       EventKindGroup,
		GroupID:    "123456",
		UserID:     "10001",
		MessageID:  "q-1",
		RawMessage: "为什么会这样",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "为什么会这样"}}},
		SenderName: "Bob",
	}
	if !runtime.shouldConsiderPassiveReply(event, "为什么会这样") {
		t.Fatal("image context follow-up should be considered for LLM routing")
	}
	if !runtime.shouldHandlePassiveReply(context.Background(), event, "为什么会这样") {
		t.Fatal("LLM route should choose to reply")
	}
	reply, err := runtime.replyTo(context.Background(), event, "为什么会这样")
	if err != nil {
		t.Fatalf("replyTo() error = %v", err)
	}
	if reply != "看起来是拍摄角度和光线让表情显得没那么明显。" || len(channel.sent) != 1 {
		t.Fatalf("reply=%q sent=%#v", reply, channel.sent)
	}
	if len(provider.requests) != 4 || !requestHasImageURL(provider.requests[3], imageURL) {
		t.Fatalf("requests missing image context: %#v", provider.requests)
	}
}

func TestRuntimePassiveReplyRecordsSemanticDecision(t *testing.T) {
	provider := &capturingLLMProvider{reply: `{"should_reply":true,"confidence":0.82,"category":"bot_related","directed_at_bot":true,"answerable":false}`}
	logs := &captureAppLogs{}
	runtime := NewRuntime(BotConfig{
		BotQQ:                 "42",
		PassiveReplyChance:    1,
		PassiveReplyThreshold: 0.8,
	}, nilChannel{}, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	runtime.SetAppLogWriter(logs)
	event := MessageEvent{
		Kind:       EventKindGroup,
		GroupID:    "123456",
		UserID:     "10001",
		MessageID:  "followup-1",
		RawMessage: "如果给你一个 PDF，你会怎么处理",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "如果给你一个 PDF，你会怎么处理"}}},
	}
	if !runtime.shouldHandlePassiveReply(context.Background(), event, event.RawMessage) {
		t.Fatal("qualified bot follow-up should pass the semantic router")
	}
	if len(provider.request.Messages) == 0 {
		t.Fatal("passive router did not call the LLM")
	}
	systemPrompt := provider.request.Messages[0].Content
	for _, want := range []string{"默认保持沉默", "directed_at_bot", "answerable", "不等于在问机器人", "只能是“不知道”", "last_bot_addressed_current_sender", "要求机器人安静或停止回复", "主动介入能提供明显价值", "拿不准时必须 false"} {
		if !strings.Contains(systemPrompt, want) {
			t.Fatalf("passive router prompt missing %q", want)
		}
	}
	if len(logs.entries) != 1 || logs.entries[0].Action != "qqbot.passive_reply_route" {
		t.Fatalf("route logs = %#v", logs.entries)
	}
	metadata := logs.entries[0].Metadata
	if metadata["allowed"] != true || metadata["parsed"] != true || metadata["confidence"] != 0.82 || metadata["threshold"] != 0.8 || metadata["directed_at_bot"] != true {
		t.Fatalf("route metadata = %#v", metadata)
	}
}

func TestRuntimePassiveReplyPayloadIncludesMessageAges(t *testing.T) {
	runtime := NewRuntime(BotConfig{BotQQ: "42"}, nilChannel{}, NewPluginManager(), nil, nil, nil, nil)
	runtime.remember(MessageEvent{
		Kind:       EventKindGroup,
		Time:       1000,
		GroupID:    "123456",
		UserID:     "20002",
		MessageID:  "old-message",
		SenderName: "Alice",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "之前的聊天"}}},
	})
	event := MessageEvent{
		Kind:       EventKindGroup,
		Time:       2200,
		GroupID:    "123456",
		UserID:     "10001",
		MessageID:  "current-message",
		SenderName: "Bob",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "现在的问题"}}},
	}
	payload := runtime.passiveReplyPayload(event, "现在的问题")
	if payload.ContextGapSeconds == nil || *payload.ContextGapSeconds != 1200 {
		t.Fatalf("context gap = %#v, want 1200", payload.ContextGapSeconds)
	}
	if len(payload.RecentMessages) != 1 || payload.RecentMessages[0].AgeSeconds == nil || *payload.RecentMessages[0].AgeSeconds != 1200 {
		t.Fatalf("recent messages = %#v", payload.RecentMessages)
	}
}

func TestRuntimePassiveReplyPayloadIdentifiesCorrectionToRecentBotReply(t *testing.T) {
	runtime := NewRuntime(BotConfig{BotQQ: "42"}, nilChannel{}, NewPluginManager(), nil, nil, nil, nil)
	runtime.remember(MessageEvent{
		Kind:       EventKindGroup,
		Time:       100,
		GroupID:    "123456",
		UserID:     "10009",
		MessageID:  "cache-question",
		SenderName: "Alice",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "评估缓存效果怎么样"}}},
	})
	runtime.remember(MessageEvent{
		Kind:       EventKindGroup,
		Time:       110,
		GroupID:    "123456",
		UserID:     "42",
		MessageID:  "bot-cache-analysis",
		SenderName: "Diana",
		Segments: []MessageSegment{
			{Type: "reply", Data: map[string]string{"id": "cache-question"}},
			{Type: "at", Data: map[string]string{"qq": "10009"}},
			{Type: "text", Data: map[string]string{"text": "从流量看缓存可能没有完全生效。"}},
		},
		Quoted: &QuotedMessage{MessageID: "cache-question", UserID: "10009", SenderName: "Alice"},
	})
	event := MessageEvent{
		Kind:       EventKindGroup,
		Time:       223,
		GroupID:    "123456",
		UserID:     "10009",
		MessageID:  "cache-correction",
		SenderName: "Alice",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "大部分流量是主动刷新元数据 这个是设计如此"}}},
	}

	payload := runtime.passiveReplyPayload(event, PlainText(event.Segments))
	if payload.LastBotMessage == nil || payload.LastBotMessage.Text != "从流量看缓存可能没有完全生效。" {
		t.Fatalf("last bot message = %#v", payload.LastBotMessage)
	}
	if payload.LastBotMessage.AgeSeconds == nil || *payload.LastBotMessage.AgeSeconds != 113 {
		t.Fatalf("last bot message age = %#v", payload.LastBotMessage.AgeSeconds)
	}
	if !payload.LastBotAddressedCurrentSender {
		t.Fatal("bot reply target should be identified as the current sender")
	}
	if payload.MessagesAfterLastBot == nil || *payload.MessagesAfterLastBot != 0 {
		t.Fatalf("messages after last bot = %#v, want 0", payload.MessagesAfterLastBot)
	}
}

func TestRuntimePassiveReplyKeepsBotFollowupAcrossSameSenderImage(t *testing.T) {
	provider := &capturingLLMProvider{reply: `{"should_reply":true,"confidence":0.97,"category":"bot_related","directed_at_bot":true,"answerable":false}`}
	runtime := NewRuntime(BotConfig{
		BotQQ:                 "42",
		PassiveReplyChance:    1,
		PassiveReplyThreshold: 0.9,
	}, nilChannel{}, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	runtime.remember(MessageEvent{
		Kind:       EventKindGroup,
		Time:       100,
		GroupID:    "123456",
		UserID:     "10001",
		MessageID:  "question",
		SenderName: "Alice",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "这个问题应该怎么处理"}}},
	})
	runtime.remember(MessageEvent{
		Kind:       EventKindGroup,
		Time:       110,
		GroupID:    "123456",
		UserID:     "42",
		MessageID:  "bot-answer",
		SenderName: "Diana",
		Segments: []MessageSegment{
			{Type: "reply", Data: map[string]string{"id": "question"}},
			{Type: "at", Data: map[string]string{"qq": "10001"}},
			{Type: "text", Data: map[string]string{"text": "可以先检查当前配置。"}},
		},
		Quoted: &QuotedMessage{MessageID: "question", UserID: "10001", SenderName: "Alice"},
	})
	runtime.remember(MessageEvent{
		Kind:       EventKindGroup,
		Time:       115,
		GroupID:    "123456",
		UserID:     "10001",
		MessageID:  "reaction-image",
		SenderName: "Alice",
		Segments:   []MessageSegment{{Type: "image", Data: map[string]string{"file": "data:image/png;base64,aGVsbG8="}}},
	})
	event := MessageEvent{
		Kind:       EventKindGroup,
		Time:       120,
		GroupID:    "123456",
		UserID:     "10001",
		MessageID:  "answer-criticism",
		SenderName: "Alice",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "这个回答完全没理解问题"}}},
	}

	payload := runtime.passiveReplyPayload(event, PlainText(event.Segments))
	if !payload.LastBotAddressedCurrentSender {
		t.Fatal("bot reply target should remain the current sender")
	}
	if payload.MessagesAfterLastBot == nil || *payload.MessagesAfterLastBot != 1 {
		t.Fatalf("messages after last bot = %#v, want 1", payload.MessagesAfterLastBot)
	}
	if len(payload.RecentMessages) < 2 || payload.RecentMessages[0].Images != 1 {
		t.Fatalf("recent messages = %#v", payload.RecentMessages)
	}
	if !runtime.shouldHandlePassiveReply(context.Background(), event, PlainText(event.Segments)) {
		t.Fatal("semantic criticism of the recent bot answer should be routed")
	}
	if !strings.Contains(provider.request.Messages[1].Content, `"last_bot_addressed_current_sender":true`) ||
		!strings.Contains(provider.request.Messages[1].Content, `"messages_after_last_bot":1`) {
		t.Fatalf("router payload = %q", provider.request.Messages[1].Content)
	}
}

func TestRuntimePassiveReplyRoutesClearQuestionsAtStrictThreshold(t *testing.T) {
	provider := &sequenceLLMProvider{replies: []string{
		`{"should_reply":true,"confidence":0.96,"category":"needs_response","directed_at_bot":false,"answerable":true}`,
		`{"should_reply":true,"confidence":0.96,"category":"needs_response","directed_at_bot":false,"answerable":true}`,
	}}
	runtime := NewRuntime(BotConfig{
		BotQQ:                 "42",
		PassiveReplyChance:    1,
		PassiveReplyThreshold: 0.9,
	}, nilChannel{}, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	for index, text := range []string{"运行条件应当如何判断", "对方不同意时应该怎么办"} {
		event := MessageEvent{
			Kind:       EventKindGroup,
			GroupID:    "123456",
			UserID:     "10001",
			MessageID:  fmt.Sprintf("clear-question-%d", index),
			SenderName: "Alice",
			Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": text}}},
		}
		if !runtime.shouldConsiderPassiveReply(event, text) {
			t.Fatalf("clear question %q should enter semantic routing", text)
		}
		if !runtime.shouldHandlePassiveReply(context.Background(), event, text) {
			t.Fatalf("clear question %q should pass strict routing", text)
		}
	}
}

func TestRuntimeRoutesPureGroupImageButDoesNotReplyWhenUnrelated(t *testing.T) {
	channel := &recordingChannel{}
	provider := &sequenceLLMProvider{replies: []string{
		`{"should_reply":false,"confidence":0.98,"category":"none"}`,
	}}
	runtime := NewRuntime(BotConfig{}, channel, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	event := MessageEvent{
		Kind:       EventKindGroup,
		GroupID:    "123456",
		UserID:     "10001",
		MessageID:  "img-only",
		RawMessage: "[CQ:image,file=a.png]",
		Segments: []MessageSegment{{
			Type: "image",
			Data: map[string]string{"file": "data:image/png;base64,aGVsbG8="},
		}},
	}
	if !runtime.shouldConsiderPassiveReply(event, PlainText(event.Segments)) {
		t.Fatal("pure group image should enter semantic passive routing")
	}
	if runtime.shouldHandlePassiveReply(context.Background(), event, PlainText(event.Segments)) {
		t.Fatal("unrelated pure image should be rejected by semantic routing")
	}
	if len(provider.requests) != 1 || !requestHasAnyImage(provider.requests[0]) {
		t.Fatalf("routing model should receive the image, requests = %#v", provider.requests)
	}
	if len(channel.sent) != 0 {
		t.Fatalf("unrelated image should not be replied to, sent = %#v", channel.sent)
	}
}

func TestRuntimeRepliesWhenImageFulfillsRecentBotRequest(t *testing.T) {
	channel := &recordingChannel{}
	imageURL := "data:image/png;base64,aGVsbG8="
	provider := &sequenceLLMProvider{replies: []string{
		`{"should_reply":true,"confidence":0.97,"category":"bot_related","directed_at_bot":true,"answerable":false}`,
		`{"action":"none"}`,
		"看到了，截图里的 QQ 版本是 9.9.31。",
	}}
	runtime := NewRuntime(BotConfig{
		BotQQ:                 "42",
		PassiveReplyChance:    1,
		PassiveReplyThreshold: 0.8,
	}, channel, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	runtime.remember(MessageEvent{
		Kind:       EventKindGroup,
		Time:       100,
		SelfID:     "42",
		UserID:     "42",
		GroupID:    "123456",
		MessageID:  "bot-request",
		SenderName: "Diana",
		Segments: []MessageSegment{{Type: "text", Data: map[string]string{
			"text": "请把 QQ 设置里的关于页面截图发来，我看版本号。",
		}}},
	})
	event := MessageEvent{
		Kind:       EventKindGroup,
		Time:       110,
		GroupID:    "123456",
		UserID:     "10001",
		MessageID:  "version-image",
		SenderName: "Alice",
		RawMessage: "[CQ:image,file=version.png]",
		Segments: []MessageSegment{{
			Type: "image",
			Data: map[string]string{"file": imageURL},
		}},
	}
	text := PlainText(event.Segments)
	if !runtime.shouldConsiderPassiveReply(event, text) {
		t.Fatal("image response to bot request should enter passive routing")
	}
	if !runtime.shouldHandlePassiveReply(context.Background(), event, text) {
		t.Fatal("image response to bot request should pass passive routing")
	}
	reply, err := runtime.replyTo(context.Background(), event, text)
	if err != nil {
		t.Fatal(err)
	}
	if reply != "看到了，截图里的 QQ 版本是 9.9.31。" || len(channel.sent) != 1 {
		t.Fatalf("reply=%q sent=%#v", reply, channel.sent)
	}
	if len(provider.requests) != 3 || !requestHasAnyImage(provider.requests[0]) || !strings.Contains(provider.requests[1].Messages[1].Content, `"current_images":1`) || !requestHasAnyImage(provider.requests[2]) {
		t.Fatalf("routing, tool intent, and reply models should receive image context: %#v", provider.requests)
	}
	if strings.Contains(provider.requests[0].Messages[1].Content, "请分析这张图片") {
		t.Fatalf("routing payload should preserve neutral image semantics: %#v", provider.requests[0])
	}
}

func TestRuntimeConsidersImageWithCaptionForPassiveReply(t *testing.T) {
	runtime := NewRuntime(BotConfig{}, nilChannel{}, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return &sequenceLLMProvider{}, nil
	})
	event := MessageEvent{
		Kind:      EventKindGroup,
		GroupID:   "123456",
		UserID:    "10001",
		MessageID: "img-caption",
		Segments: []MessageSegment{
			{Type: "image", Data: map[string]string{"file": "data:image/png;base64,aGVsbG8="}},
			{Type: "text", Data: map[string]string{"text": "这是什么"}},
		},
	}
	if !runtime.shouldConsiderPassiveReply(event, PlainText(event.Segments)) {
		t.Fatal("image with user text should still enter passive reply routing")
	}
}

func TestRuntimePassiveReplyUsesRoutingProfile(t *testing.T) {
	channel := &recordingChannel{}
	store := &stubLLMProfileStore{
		set: llm.ProfileSet{
			ActiveID: "main",
			Profiles: []llm.Profile{
				{ID: "main", Name: "主聊天", Group: "chat", Config: llm.ProviderConfig{Provider: llm.ProviderOpenAICompatible, APIKey: "main-key", Model: "main-model"}},
				{ID: "routing", Name: "快速语义判定", Group: "routing", Config: llm.ProviderConfig{Provider: llm.ProviderOpenAICompatible, APIKey: "routing-key", Model: "routing-model"}},
			},
		},
	}
	var attempts []string
	var attemptsMu sync.Mutex
	runtime := NewRuntime(BotConfig{BotQQ: "42", PassiveReplyChance: 1}, channel, NewPluginManager(), store, nil, nil, nil)
	runtime.SetLLMProviderConfigFactory(func(cfg llm.ProviderConfig) (LLMProvider, error) {
		attemptsMu.Lock()
		defer attemptsMu.Unlock()
		attempts = append(attempts, cfg.Model)
		if len(attempts) == 1 {
			return &capturingLLMProvider{reply: `{"should_reply":true,"confidence":0.98,"category":"needs_response","directed_at_bot":false,"answerable":true}`}, nil
		}
		if len(attempts) == 2 {
			return &capturingLLMProvider{reply: `{"action":"none"}`}, nil
		}
		return &capturingLLMProvider{reply: "我也插一句。"}, nil
	})
	event := MessageEvent{
		Kind:       EventKindGroup,
		GroupID:    "123456",
		UserID:     "10001",
		MessageID:  "q-1",
		RawMessage: "这个报错有人知道怎么处理吗",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "这个报错有人知道怎么处理吗"}}},
		SenderName: "Alice",
	}
	if err := runtime.HandleEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	waitForCondition(t, time.Second, func() bool {
		return len(channel.sentSnapshot()) == 1
	})
	sent := channel.sentSnapshot()
	if sent[0].Text != "我也插一句。" {
		t.Fatalf("sent = %#v", sent)
	}
	attemptsMu.Lock()
	attemptsSnapshot := append([]string(nil), attempts...)
	attemptsMu.Unlock()
	wantAttempts := []string{"routing-model", "routing-model", "main-model"}
	if len(attemptsSnapshot) != len(wantAttempts) {
		t.Fatalf("attempts = %#v, want %#v", attemptsSnapshot, wantAttempts)
	}
	for i := range wantAttempts {
		if attemptsSnapshot[i] != wantAttempts[i] {
			t.Fatalf("attempts = %#v, want %#v", attemptsSnapshot, wantAttempts)
		}
	}
	if store.set.ActiveID != "main" {
		t.Fatalf("active profile = %q, want main", store.set.ActiveID)
	}
}

func TestRuntimePassiveReplyUsesConciseMode(t *testing.T) {
	channel := &recordingChannel{}
	provider := &capturingLLMProvider{reply: strings.Repeat("很", 240)}
	runtime := NewRuntime(BotConfig{
		AgentEnabled:       false,
		MaxReplyChars:      3500,
		PassiveReplyPrompt: "custom concise passive instruction",
	}, channel, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	event := MessageEvent{
		Kind:       EventKindGroup,
		GroupID:    "123456",
		UserID:     "10001",
		MessageID:  "q-1",
		RawMessage: "这个报错有人知道怎么处理吗",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "这个报错有人知道怎么处理吗"}}},
		SenderName: "Alice",
	}
	reply, err := runtime.replyTo(context.Background(), event, "这个报错有人知道怎么处理吗")
	if err != nil {
		t.Fatalf("replyTo() error = %v", err)
	}
	if len(provider.request.Messages) == 0 || !strings.Contains(provider.request.Messages[0].Content, "custom concise passive instruction") {
		t.Fatalf("system prompt = %#v", provider.request.Messages)
	}
	if len([]rune(reply)) > passiveReplyMaxRunes+3 {
		t.Fatalf("reply too long: %d %q", len([]rune(reply)), reply)
	}
	if len(channel.sent) != 1 || channel.sent[0].Text != reply {
		t.Fatalf("sent = %#v reply=%q", channel.sent, reply)
	}
}

func TestPassiveReplySampleAllowsRespectsChance(t *testing.T) {
	event := MessageEvent{
		Kind:      EventKindGroup,
		GroupID:   "123456",
		UserID:    "10001",
		MessageID: "q-1",
	}
	if !passiveReplySampleAllows(event, "这个报错有人知道怎么处理吗", 1) {
		t.Fatal("chance=1 should always allow passive replies")
	}
	if passiveReplySampleAllows(event, "这个报错有人知道怎么处理吗", 0) {
		t.Fatal("chance=0 should block passive replies")
	}
	first := passiveReplySampleAllows(event, "这个报错有人知道怎么处理吗", 0.25)
	for i := 0; i < 5; i++ {
		if got := passiveReplySampleAllows(event, "这个报错有人知道怎么处理吗", 0.25); got != first {
			t.Fatal("sampling should be deterministic for the same event")
		}
	}
}

func TestPassiveReplyDecisionRequiresStrictThresholdAndCategory(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		threshold float64
		want      bool
	}{
		{name: "clear request above threshold", raw: `{"should_reply":true,"confidence":0.96,"category":"needs_response","directed_at_bot":false,"answerable":true}`, threshold: 0.9, want: true},
		{name: "bot related at threshold", raw: "```json\n{\"should_reply\":true,\"confidence\":0.9,\"category\":\"bot_related\",\"directed_at_bot\":true,\"answerable\":false}\n```", threshold: 0.9, want: true},
		{name: "below threshold", raw: `{"should_reply":true,"confidence":0.89,"category":"needs_response","directed_at_bot":false,"answerable":true}`, threshold: 0.9, want: false},
		{name: "unanswerable group question", raw: `{"should_reply":true,"confidence":0.99,"category":"needs_response","directed_at_bot":false,"answerable":false}`, threshold: 0.9, want: false},
		{name: "unrelated bot category", raw: `{"should_reply":true,"confidence":0.99,"category":"bot_related","directed_at_bot":false,"answerable":true}`, threshold: 0.9, want: false},
		{name: "unapproved category", raw: `{"should_reply":true,"confidence":0.99,"category":"casual_chat"}`, threshold: 0.9, want: false},
		{name: "negative decision", raw: `{"should_reply":false,"confidence":0.99,"category":"none"}`, threshold: 0.9, want: false},
		{name: "missing confidence", raw: `{"should_reply":true,"category":"needs_response"}`, threshold: 0.9, want: false},
		{name: "missing category", raw: `{"should_reply":true,"confidence":0.99}`, threshold: 0.9, want: false},
		{name: "out of range", raw: `{"should_reply":true,"confidence":1.1,"category":"needs_response","directed_at_bot":false,"answerable":true}`, threshold: 0.9, want: false},
		{name: "invalid json", raw: `{"should_reply":true`, threshold: 0.9, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision, ok := parsePassiveReplyDecision(tt.raw)
			got := ok && decision.allows(tt.threshold)
			if got != tt.want {
				t.Fatalf("decision=%#v ok=%v got=%v want=%v", decision, ok, got, tt.want)
			}
		})
	}
}

func TestRuntimePassiveReplyDoesNotRateLimitQualifiedMessages(t *testing.T) {
	provider := &sequenceLLMProvider{replies: []string{
		`{"should_reply":true,"confidence":0.99,"category":"needs_response","directed_at_bot":false,"answerable":true}`,
		`{"should_reply":true,"confidence":0.99,"category":"bot_related","directed_at_bot":true,"answerable":false}`,
	}}
	runtime := NewRuntime(BotConfig{
		BotQQ:                 "42",
		PassiveReplyChance:    1,
		PassiveReplyThreshold: 0.9,
	}, nilChannel{}, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	for index, text := range []string{"这个报错应该怎么处理？", "刚才机器人说的方案能再解释一下吗？"} {
		event := MessageEvent{
			Kind:       EventKindGroup,
			GroupID:    "123456",
			UserID:     "10001",
			MessageID:  fmt.Sprintf("q-%d", index),
			RawMessage: text,
			Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": text}}},
		}
		if !runtime.shouldHandlePassiveReply(context.Background(), event, text) {
			t.Fatalf("qualified message %d was suppressed", index)
		}
	}
}

func TestCacheMessageEventImagesStoresLocalHistory(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("APP_DB_PATH", filepath.Join(tempDir, "data", "diana-qq-bot.db"))
	imageBody := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00}
	imageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(imageBody)
	}))
	defer imageServer.Close()

	event := cacheMessageEventImages(context.Background(), MessageEvent{
		Kind:      EventKindGroup,
		GroupID:   "123456",
		UserID:    "10001",
		MessageID: "img-1",
		Segments: []MessageSegment{{
			Type: "image",
			Data: map[string]string{"url": imageServer.URL + "/image.png"},
		}},
	})
	cachedFile := event.Segments[0].Data["cached_file"]
	if cachedFile == "" {
		t.Fatalf("cached_file missing: %#v", event.Segments[0].Data)
	}
	if _, err := os.Stat(cachedFile); err != nil {
		t.Fatalf("cached file not written: %v", err)
	}
	msg := llmMessageFromEventWithImages(event, "这是什么", nil)
	wantImageURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(imageBody)
	if !requestMessageHasImageURL(msg, wantImageURL) {
		t.Fatalf("message missing cached image data URL: %#v", msg)
	}
}

func TestRuntimeCachesImagesWithoutHistoryPlugin(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("APP_DB_PATH", filepath.Join(tempDir, "data", "diana-qq-bot.db"))
	imageBody := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00}
	imageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(imageBody)
	}))
	defer imageServer.Close()

	channel := &recordingChannel{}
	provider := &capturingLLMProvider{reply: "看到了"}
	runtime := NewRuntime(BotConfig{}, channel, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	err := runtime.HandleEvent(context.Background(), MessageEvent{
		Kind:       EventKindPrivate,
		UserID:     "10001",
		MessageID:  "img-1",
		RawMessage: "[图片]",
		Segments: []MessageSegment{{
			Type: "image",
			Data: map[string]string{"url": imageServer.URL + "/image.png"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForCondition(t, time.Second, func() bool {
		return len(channel.sentSnapshot()) == 1
	})
	wantImageURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(imageBody)
	request := provider.requestSnapshot()
	if !requestHasImageURL(request, wantImageURL) {
		t.Fatalf("request missing cached image data URL: %#v", request.Messages)
	}
}

// TestRuntimeMarksHistoryAsReferenceAndCurrentAsTarget 验证历史消息只作为参考，当前消息才是回复目标。
func TestRuntimeMarksHistoryAsReferenceAndCurrentAsTarget(t *testing.T) {
	channel := &recordingChannel{}
	provider := &capturingLLMProvider{reply: "回复当前消息"}
	runtime := NewRuntime(BotConfig{}, channel, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	runtime.remember(MessageEvent{
		Kind:       EventKindPrivate,
		Time:       1000,
		UserID:     "10001",
		MessageID:  "old-1",
		RawMessage: "旧问题是什么",
		Segments: []MessageSegment{
			{Type: "text", Data: map[string]string{"text": "旧问题是什么"}},
		},
		SenderName: "Alice",
	})

	_, err := runtime.replyTo(context.Background(), MessageEvent{
		Kind:       EventKindPrivate,
		Time:       1120,
		UserID:     "10001",
		MessageID:  "new-1",
		RawMessage: "新问题是什么",
		Segments: []MessageSegment{
			{Type: "text", Data: map[string]string{"text": "新问题是什么"}},
		},
	}, "新问题是什么")
	if err != nil {
		t.Fatalf("replyTo() error = %v", err)
	}
	if len(provider.request.Messages) < 3 {
		t.Fatalf("messages = %#v", provider.request.Messages)
	}
	history := provider.request.Messages[len(provider.request.Messages)-2].Content
	current := provider.request.Messages[len(provider.request.Messages)-1].Content
	if !strings.Contains(history, "【历史参考消息") || !strings.Contains(history, "旧问题是什么") {
		t.Fatalf("history content = %q", history)
	}
	if !strings.Contains(history, "【消息时间：") || !strings.Contains(history, "距当前：120 秒") {
		t.Fatalf("history timing = %q", history)
	}
	if strings.Contains(history, "【当前需要回复的消息】") {
		t.Fatalf("history should not be marked current: %q", history)
	}
	if !strings.Contains(current, "【当前需要回复的消息】") || !strings.Contains(current, "新问题是什么") {
		t.Fatalf("current content = %q", current)
	}
	if !strings.Contains(current, "【消息时间：") {
		t.Fatalf("current timing = %q", current)
	}
	if strings.Contains(current, "旧问题是什么") {
		t.Fatalf("current content should not include old message: %q", current)
	}
}

func TestRuntimeDoesNotIncludeVideoURLInLLMText(t *testing.T) {
	msg := llmMessageFromEvent(MessageEvent{
		Kind:       EventKindPrivate,
		UserID:     "10001",
		MessageID:  "video-1",
		RawMessage: "[视频]",
		Segments: []MessageSegment{
			{Type: "video", Data: map[string]string{"url": "https://example.com/video.mp4"}},
		},
	}, "[视频]")
	got := msg.Content
	if strings.Contains(got, "https://example.com/video.mp4") || strings.Contains(got, "视频链接") {
		t.Fatalf("last message content = %q", got)
	}
}

func TestRuntimeDoesNotExtractVideoFramesForLLM(t *testing.T) {
	msg := llmMessageFromEventWithVideoFrames(context.Background(), MessageEvent{
		Kind:       EventKindPrivate,
		UserID:     "10001",
		MessageID:  "video-1",
		RawMessage: "[视频]",
		Segments: []MessageSegment{
			{Type: "video", Data: map[string]string{"url": "https://example.com/video.mp4"}},
		},
	}, "[视频]", nil)
	if len(msg.Parts) != 0 || strings.Contains(msg.Content, "https://example.com/video.mp4") {
		t.Fatalf("message should not include video-derived image context: %#v", msg)
	}
}

func TestRuntimePrivateVideoOnlyDoesNotCallLLM(t *testing.T) {
	channel := &recordingChannel{}
	var llmCalls atomic.Int32
	runtime := NewRuntime(BotConfig{}, channel, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		llmCalls.Add(1)
		return &capturingLLMProvider{reply: "不应该调用"}, nil
	})
	err := runtime.HandleEvent(context.Background(), MessageEvent{
		Kind:       EventKindPrivate,
		UserID:     "10001",
		MessageID:  "video-1",
		RawMessage: "[视频]",
		Segments: []MessageSegment{
			{Type: "video", Data: map[string]string{"url": "https://example.com/video.mp4"}},
		},
	})
	if err != nil {
		t.Fatalf("HandleEvent() error = %v", err)
	}
	if got := llmCalls.Load(); got != 0 {
		t.Fatalf("llm calls = %d, want 0", got)
	}
	if len(channel.sent) != 0 {
		t.Fatalf("sent = %#v", channel.sent)
	}
}

func TestRuntimePassesPluginImageFramesToLLM(t *testing.T) {
	channel := &recordingChannel{}
	provider := &capturingLLMProvider{reply: "看到了"}
	manager := NewPluginManager(pluginImageFramePlugin{})
	if _, err := manager.Install("test.image-frame"); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	runtime := NewRuntime(BotConfig{}, channel, manager, nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})

	_, err := runtime.replyTo(context.Background(), MessageEvent{
		Kind:       EventKindPrivate,
		UserID:     "10001",
		MessageID:  "link-1",
		RawMessage: "https://x.com/example/status/1",
		Segments: []MessageSegment{
			{Type: "text", Data: map[string]string{"text": "https://x.com/example/status/1"}},
		},
	}, "https://x.com/example/status/1")
	if err != nil {
		t.Fatalf("replyTo() error = %v", err)
	}
	if !requestHasImageURL(provider.request, "data:image/jpeg;base64,pluginframe") {
		t.Fatalf("request missing plugin frame: %#v", provider.request.Messages)
	}
}

func TestRuntimeResolverOnlySendsAndRecordsWithoutLLM(t *testing.T) {
	plugin := NewResolverPlugin(&http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/html"}},
			Body:       io.NopCloser(strings.NewReader("<title>X 视频</title>")),
		}, nil
	})})
	plugin.videoDownloader = func(_ context.Context, raw string) string {
		if !strings.Contains(raw, "x.com") {
			t.Fatalf("downloader raw=%q", raw)
		}
		return "/tmp/diana-test-video.mp4"
	}
	manager := NewPluginManager(plugin)
	channel := &recordingChannel{}
	logs := &captureAppLogs{}
	var llmCalls atomic.Int32
	runtime := NewRuntime(BotConfig{GroupTriggers: []string{"Diana"}, BotQQ: "42"}, channel, manager, nil, nil, nil, func() (LLMProvider, error) {
		llmCalls.Add(1)
		return &capturingLLMProvider{reply: "不应该调用"}, nil
	})
	runtime.SetAppLogWriter(logs)

	event := MessageEvent{
		Kind:       EventKindGroup,
		SelfID:     "42",
		GroupID:    "123456",
		UserID:     "10001",
		MessageID:  "link-1",
		RawMessage: "看这个 https://x.com/example/status/1",
		Segments: []MessageSegment{
			{Type: "text", Data: map[string]string{"text": "看这个 https://x.com/example/status/1"}},
		},
	}
	if err := runtime.HandleEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	waitForCondition(t, time.Second, func() bool {
		return len(channel.callsSnapshot()) == 3
	})
	if got := llmCalls.Load(); got != 0 {
		t.Fatalf("llm calls = %d, want 0", got)
	}
	sent := channel.sentSnapshot()
	if len(sent) != 0 {
		t.Fatalf("sent = %#v", sent)
	}
	calls := channel.callsSnapshot()
	if calls[0].action != "send_private_msg" || calls[1].action != "send_private_msg" || calls[2].action != "send_group_forward_msg" {
		t.Fatalf("calls = %#v", calls)
	}
	if calls[0].params["user_id"] != int64(42) || calls[1].params["user_id"] != int64(42) {
		t.Fatalf("staging params = %#v %#v", calls[0].params, calls[1].params)
	}
	firstMessage, _ := calls[0].params["message"].([]map[string]any)
	if !messageSegmentsContainText(firstMessage, "识别：小蓝鸟学习版") {
		t.Fatalf("first forward message = %#v", calls[0].params["message"])
	}
	secondMessage, _ := calls[1].params["message"].([]map[string]any)
	if !messageSegmentsContainType(secondMessage, "video") {
		t.Fatalf("second forward message = %#v", calls[1].params["message"])
	}
	nodes, _ := calls[2].params["messages"].([]map[string]any)
	if len(nodes) != 2 {
		t.Fatalf("forward nodes = %#v", calls[2].params["messages"])
	}
	history := runtime.contextHistory(event)
	if len(history) < 1 {
		t.Fatalf("history = %#v", history)
	}
	var recordedForward bool
	for _, item := range history {
		if item.MessageID == "42" && strings.Contains(item.RawMessage, "识别：小蓝鸟学习版") && eventHasSegmentType(item, "video") {
			recordedForward = true
		}
	}
	if !recordedForward {
		t.Fatalf("history missing bot resolver reply: %#v", history)
	}
	entries := logs.entriesSnapshot()
	if len(entries) != 1 || entries[0].Action != "qqbot.resolver.video_download" {
		t.Fatalf("resolver logs = %#v", entries)
	}
}

func TestRuntimeResolverPrivateLinkSkipsLLM(t *testing.T) {
	plugin := NewResolverPlugin(&http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/html"}},
			Body:       io.NopCloser(strings.NewReader("<title>X 视频</title>")),
		}, nil
	})})
	plugin.videoDownloader = func(_ context.Context, raw string) string {
		if !strings.Contains(raw, "x.com") {
			t.Fatalf("downloader raw=%q", raw)
		}
		return "/tmp/diana-test-private-video.mp4"
	}
	manager := NewPluginManager(plugin)
	channel := &recordingChannel{}
	var llmCalls atomic.Int32
	runtime := NewRuntime(BotConfig{BotQQ: "42"}, channel, manager, nil, nil, nil, func() (LLMProvider, error) {
		llmCalls.Add(1)
		return &capturingLLMProvider{reply: "不应该调用"}, nil
	})

	event := MessageEvent{
		Kind:       EventKindPrivate,
		SelfID:     "42",
		UserID:     "10001",
		MessageID:  "private-link-1",
		RawMessage: "看这个 https://x.com/example/status/1",
		Segments: []MessageSegment{
			{Type: "text", Data: map[string]string{"text": "看这个 https://x.com/example/status/1"}},
		},
	}
	if err := runtime.HandleEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	waitForCondition(t, time.Second, func() bool {
		return len(channel.callsSnapshot()) == 3
	})
	if got := llmCalls.Load(); got != 0 {
		t.Fatalf("llm calls = %d, want 0", got)
	}
	calls := channel.callsSnapshot()
	if calls[0].action != "send_private_msg" || calls[1].action != "send_private_msg" || calls[2].action != "send_private_forward_msg" {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestRuntimeResolverMentionedGroupLinkSkipsLLM(t *testing.T) {
	plugin := NewResolverPlugin(&http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/html"}},
			Body:       io.NopCloser(strings.NewReader("<title>X 视频</title>")),
		}, nil
	})})
	plugin.videoDownloader = func(_ context.Context, raw string) string {
		if !strings.Contains(raw, "x.com") {
			t.Fatalf("downloader raw=%q", raw)
		}
		return "/tmp/diana-test-mentioned-video.mp4"
	}
	manager := NewPluginManager(plugin)
	channel := &recordingChannel{}
	var llmCalls atomic.Int32
	runtime := NewRuntime(BotConfig{GroupTriggers: []string{"Diana"}, BotQQ: "42"}, channel, manager, nil, nil, nil, func() (LLMProvider, error) {
		llmCalls.Add(1)
		return &capturingLLMProvider{reply: "不应该调用"}, nil
	})

	event := MessageEvent{
		Kind:       EventKindGroup,
		SelfID:     "42",
		GroupID:    "123456",
		UserID:     "10001",
		MessageID:  "mentioned-link-1",
		ToMe:       true,
		RawMessage: "[CQ:at,qq=42] 看这个 https://x.com/example/status/1",
		Segments: []MessageSegment{
			{Type: "at", Data: map[string]string{"qq": "42"}},
			{Type: "text", Data: map[string]string{"text": " 看这个 https://x.com/example/status/1"}},
		},
	}
	if err := runtime.HandleEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	waitForCondition(t, time.Second, func() bool {
		return len(channel.callsSnapshot()) == 3
	})
	if got := llmCalls.Load(); got != 0 {
		t.Fatalf("llm calls = %d, want 0", got)
	}
	calls := channel.callsSnapshot()
	if calls[0].action != "send_private_msg" || calls[1].action != "send_private_msg" || calls[2].action != "send_group_forward_msg" {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestRuntimeResolverLocalVideoUploadsFile(t *testing.T) {
	tempDir := t.TempDir()
	videoPath := filepath.Join(tempDir, "video.mp4")
	if err := os.WriteFile(videoPath, []byte("fake video"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	channel := &recordingChannel{}
	runtime := NewRuntime(BotConfig{}, channel, NewPluginManager(), nil, nil, nil, nil)
	event := MessageEvent{
		Kind:      EventKindGroup,
		GroupID:   "123456",
		UserID:    "10001",
		MessageID: "link-1",
	}
	err := runtime.sendDirectPluginResponse(context.Background(), event, "链接解析结果", nil, []string{videoPath})
	if err != nil {
		t.Fatalf("sendDirectPluginResponse() error = %v", err)
	}
	if len(channel.sent) != 2 {
		t.Fatalf("sent = %#v", channel.sent)
	}
	if len(channel.sent[0].VideoURLs) != 0 || channel.sent[0].Text != "链接解析结果" {
		t.Fatalf("first sent = %#v", channel.sent[0])
	}
	if !strings.Contains(channel.sent[1].Text, "改用 QQ 文件发送") {
		t.Fatalf("notice = %#v", channel.sent[1])
	}
	uploadCalls := recordedCallsByAction(channel.calls, "upload_group_file")
	if len(uploadCalls) != 1 {
		t.Fatalf("calls = %#v", channel.calls)
	}
	if uploadCalls[0].params["file"] != videoPath || uploadCalls[0].params["name"] != "video.mp4" {
		t.Fatalf("params = %#v", uploadCalls[0].params)
	}
}

func TestRuntimeResolverLocalVideoSharesHTTPURL(t *testing.T) {
	tempDir := t.TempDir()
	videoPath := filepath.Join(tempDir, "video.mp4")
	if err := os.WriteFile(videoPath, []byte("fake video"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	channel := &recordingChannel{}
	sharer := &recordingLocalMediaSharer{url: "http://127.0.0.1:18080/api/qqbot/media/token"}
	runtime := NewRuntime(BotConfig{}, channel, NewPluginManager(), nil, nil, nil, nil)
	runtime.SetLocalMediaSharer(sharer)
	event := MessageEvent{
		Kind:      EventKindGroup,
		GroupID:   "123456",
		UserID:    "10001",
		MessageID: "link-1",
	}
	err := runtime.sendDirectPluginResponse(context.Background(), event, "链接解析结果", nil, []string{videoPath})
	if err != nil {
		t.Fatalf("sendDirectPluginResponse() error = %v", err)
	}
	if len(channel.sent) != 1 {
		t.Fatalf("sent = %#v", channel.sent)
	}
	if len(channel.calls) != 0 {
		t.Fatalf("calls = %#v", channel.calls)
	}
	if len(sharer.paths) != 1 || sharer.paths[0] != videoPath {
		t.Fatalf("shared paths = %#v", sharer.paths)
	}
	if got := channel.sent[0].VideoURLs; len(got) != 1 || got[0] != sharer.url {
		t.Fatalf("VideoURLs = %#v", got)
	}
}

func TestRuntimeResolverLocalVideoShareFailureFallsBackToFile(t *testing.T) {
	tempDir := t.TempDir()
	videoPath := filepath.Join(tempDir, "video.mp4")
	if err := os.WriteFile(videoPath, []byte("fake video"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	channel := &recordingChannel{sendErr: errors.New("send failed")}
	sharer := &recordingLocalMediaSharer{url: "http://127.0.0.1:18080/api/qqbot/media/token"}
	runtime := NewRuntime(BotConfig{}, channel, NewPluginManager(), nil, nil, nil, nil)
	runtime.SetLocalMediaSharer(sharer)
	event := MessageEvent{
		Kind:      EventKindGroup,
		GroupID:   "123456",
		UserID:    "10001",
		MessageID: "link-1",
	}
	err := runtime.sendDirectPluginResponse(context.Background(), event, "链接解析结果", nil, []string{videoPath})
	if err != nil {
		t.Fatalf("sendDirectPluginResponse() error = %v", err)
	}
	uploadCalls := recordedCallsByAction(channel.calls, "upload_group_file")
	if len(uploadCalls) != 1 {
		t.Fatalf("calls = %#v", channel.calls)
	}
}

func TestRuntimeImageGenerationCommandSendsImage(t *testing.T) {
	var gotModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/media" {
			writeTestPNG(w)
			return
		}
		if r.URL.Path != "/v1/images/generations" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		var body struct {
			Model  string `json:"model"`
			Prompt string `json:"prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		gotModel = body.Model
		if body.Prompt != "一只猫" {
			t.Fatalf("prompt = %q", body.Prompt)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"YWJjZA=="}]}`))
	}))
	defer server.Close()

	channel := &recordingChannel{}
	store := &stubLLMProfileStore{set: llm.NewProfileSet(llm.ProviderConfig{
		Provider:   llm.ProviderOpenAICompatible,
		APIKey:     "secret",
		BaseURL:    server.URL + "/v1",
		Model:      "gpt-test",
		ImageModel: "gpt-image-2",
	})}
	runtime := NewRuntime(BotConfig{OwnerID: "owner"}, channel, NewPluginManager(), store, nil, nil, nil)
	memory := newMemoryUserMemoryStore()
	memory.profiles["10001"] = UserMemoryProfile{UserID: "10001", Favorability: 20, MessageCount: 10}
	runtime.SetUserMemoryStore(memory)
	provider := &sequenceLLMProvider{replies: []string{
		`{"action":"generate_image","prompt":"一只猫"}`,
		"好，我先陪你聊着，图片生成后会自动发来。",
	}}
	runtime.SetLLMProviderConfigFactory(func(cfg llm.ProviderConfig) (LLMProvider, error) {
		return provider, nil
	})
	sharer := &recordingLocalMediaSharer{url: server.URL + "/media"}
	runtime.SetLocalMediaSharer(sharer)
	reply, err := runtime.replyTo(context.Background(), MessageEvent{
		Kind:       EventKindPrivate,
		UserID:     "10001",
		MessageID:  "img-1",
		RawMessage: "请生成一张一只猫的图片",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "请生成一张一只猫的图片"}}},
	}, "请生成一张一只猫的图片")
	if err != nil {
		t.Fatalf("replyTo() error = %v", err)
	}
	waitForCondition(t, 2*time.Second, func() bool {
		return runtime.activeSubagentTaskCount() == 0
	})
	if reply != "好，我先陪你聊着，图片生成后会自动发来。" || gotModel != "gpt-image-2" {
		t.Fatalf("reply=%q model=%q", reply, gotModel)
	}
	if len(channel.sent) != 2 || !outgoingMessagesContainTextOnly(channel.sent, reply) || !outgoingMessagesContainImage(channel.sent, sharer.url) {
		t.Fatalf("sent = %#v", channel.sent)
	}
}

func TestRuntimeImageGenerationRepliesWhileImageRunsInBackground(t *testing.T) {
	imageStarted := make(chan struct{})
	releaseImage := make(chan struct{})
	var gotPrompt string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/media" {
			writeTestPNG(w)
			return
		}
		if r.URL.Path != "/v1/images/generations" {
			t.Errorf("path = %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body struct {
			Prompt string `json:"prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("Decode() error = %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		gotPrompt = body.Prompt
		close(imageStarted)
		<-releaseImage
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"YXN5bmMtaW1hZ2U="}]}`))
	}))
	defer server.Close()
	defer func() {
		select {
		case <-releaseImage:
		default:
			close(releaseImage)
		}
	}()

	channel := &recordingChannel{}
	store := &stubLLMProfileStore{set: llm.NewProfileSet(llm.ProviderConfig{
		Provider:   llm.ProviderOpenAICompatible,
		APIKey:     "secret",
		BaseURL:    server.URL + "/v1",
		Model:      "gpt-test",
		ImageModel: "gpt-image-2",
	})}
	provider := &sequenceLLMProvider{replies: []string{
		`{"action":"generate_image","prompt":"一张异步测试海报"}`,
		"我先把构思发给你：画面会用醒目的几何构图，成图稍后自动跟上。",
	}}
	runtime := NewRuntime(BotConfig{OwnerID: "owner"}, channel, NewPluginManager(), store, nil, nil, nil)
	memory := newMemoryUserMemoryStore()
	memory.profiles["10001"] = UserMemoryProfile{UserID: "10001", Favorability: 20, MessageCount: 10}
	runtime.SetUserMemoryStore(memory)
	runtime.SetLLMProviderConfigFactory(func(llm.ProviderConfig) (LLMProvider, error) {
		return provider, nil
	})
	sharer := &recordingLocalMediaSharer{url: server.URL + "/media"}
	runtime.SetLocalMediaSharer(sharer)
	event := MessageEvent{
		Kind:       EventKindPrivate,
		UserID:     "10001",
		MessageID:  "async-image-1",
		RawMessage: "画一张异步测试海报，也先跟我说说你的构思",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "画一张异步测试海报，也先跟我说说你的构思"}}},
	}

	type replyResult struct {
		reply string
		err   error
	}
	replyDone := make(chan replyResult, 1)
	go func() {
		reply, err := runtime.replyTo(context.Background(), event, event.RawMessage)
		replyDone <- replyResult{reply: reply, err: err}
	}()
	select {
	case <-imageStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("image request did not start")
	}
	var result replyResult
	select {
	case result = <-replyDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("text reply waited for the background image request")
	}
	if result.err != nil {
		t.Fatalf("replyTo() error = %v", result.err)
	}
	if runtime.activeSubagentTaskCount() != 1 {
		t.Fatalf("active tasks = %d", runtime.activeSubagentTaskCount())
	}
	if result.reply != "我先把构思发给你：画面会用醒目的几何构图，成图稍后自动跟上。" {
		t.Fatalf("reply = %q", result.reply)
	}
	if len(channel.sent) != 1 || channel.sent[0].Text != result.reply || len(channel.sent[0].ImageURLs) != 0 {
		t.Fatalf("messages sent before image completion = %#v", channel.sent)
	}

	close(releaseImage)
	waitForCondition(t, 2*time.Second, func() bool {
		return runtime.activeSubagentTaskCount() == 0
	})
	if !strings.Contains(gotPrompt, "一张异步测试海报") {
		t.Fatalf("image prompt = %q", gotPrompt)
	}
	if len(channel.sent) != 2 || channel.sent[1].Text != "图片生成完成。" || len(channel.sent[1].ImageURLs) != 1 || channel.sent[1].ImageURLs[0] != sharer.url {
		t.Fatalf("messages sent after image completion = %#v", channel.sent)
	}
}

func TestRuntimeImageOperationsRequireFamiliarRelationship(t *testing.T) {
	tests := []struct {
		name     string
		decision string
		event    MessageEvent
	}{
		{
			name:     "generate",
			decision: `{"action":"generate_image","prompt":"一只猫"}`,
			event: MessageEvent{
				Kind:       EventKindPrivate,
				UserID:     "user",
				MessageID:  "restricted-generate",
				RawMessage: "生成一张猫的图片",
				Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "生成一张猫的图片"}}},
			},
		},
		{
			name:     "edit",
			decision: `{"action":"edit_image","prompt":"把图片改成黑白"}`,
			event: MessageEvent{
				Kind:       EventKindPrivate,
				UserID:     "user",
				MessageID:  "restricted-edit",
				RawMessage: "把这张图片改成黑白",
				Segments: []MessageSegment{
					{Type: "text", Data: map[string]string{"text": "把这张图片改成黑白"}},
					{Type: "image", Data: map[string]string{"file": "data:image/png;base64,aGVsbG8="}},
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			channel := &recordingChannel{}
			store := &stubLLMProfileStore{set: llm.NewProfileSet(llm.ProviderConfig{
				Provider: llm.ProviderOpenAICompatible,
				APIKey:   "secret",
				BaseURL:  "https://example.test/v1",
				Model:    "gpt-test",
			})}
			runtime := NewRuntime(BotConfig{OwnerID: "owner"}, channel, NewPluginManager(), store, nil, nil, nil)
			runtime.SetLLMProviderConfigFactory(func(cfg llm.ProviderConfig) (LLMProvider, error) {
				return &capturingLLMProvider{reply: test.decision}, nil
			})

			reply, err := runtime.replyTo(context.Background(), test.event, test.event.RawMessage)
			if err != nil {
				t.Fatalf("replyTo() error = %v", err)
			}
			if !strings.Contains(reply, "好感度不足") || !strings.Contains(reply, relationshipImageTierName) {
				t.Fatalf("reply = %q", reply)
			}
			if len(channel.sent) != 1 || channel.sent[0].Text != reply || len(channel.sent[0].ImageURLs) != 0 {
				t.Fatalf("sent = %#v", channel.sent)
			}
		})
	}
}

func TestRuntimePassiveImageGenerationSendsImage(t *testing.T) {
	var gotPrompt string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/media" {
			writeTestPNG(w)
			return
		}
		if r.URL.Path != "/v1/images/generations" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		var body struct {
			Prompt string `json:"prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		gotPrompt = body.Prompt
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"YWJjZA=="}]}`))
	}))
	defer server.Close()

	channel := &recordingChannel{}
	store := &stubLLMProfileStore{set: llm.NewProfileSet(llm.ProviderConfig{
		Provider:   llm.ProviderOpenAICompatible,
		APIKey:     "secret",
		BaseURL:    server.URL + "/v1",
		Model:      "gpt-test",
		ImageModel: "gpt-image-2",
	})}
	runtime := NewRuntime(BotConfig{OwnerID: "10001"}, channel, NewPluginManager(), store, nil, nil, nil)
	provider := &sequenceLLMProvider{replies: []string{
		`{"action":"generate_image","prompt":"番茄在街机厅玩 maimaiDX"}`,
		"这个画面挺有意思，我先回你，图好了会接着发。",
	}}
	runtime.SetLLMProviderConfigFactory(func(cfg llm.ProviderConfig) (LLMProvider, error) {
		return provider, nil
	})
	sharer := &recordingLocalMediaSharer{url: server.URL + "/media"}
	runtime.SetLocalMediaSharer(sharer)
	event := MessageEvent{
		Kind:       EventKindGroup,
		GroupID:    "123456",
		UserID:     "10001",
		MessageID:  "passive-img-1",
		RawMessage: "帮人画一个番茄去街机厅玩 maimaiDX 的图",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "帮人画一个番茄去街机厅玩 maimaiDX 的图"}}},
	}
	if runtime.shouldHandleChat(event, event.RawMessage) {
		t.Fatal("test message unexpectedly matched a direct chat trigger")
	}

	reply, err := runtime.replyTo(context.Background(), event, event.RawMessage)
	if err != nil {
		t.Fatalf("replyTo() error = %v", err)
	}
	waitForCondition(t, 2*time.Second, func() bool {
		return runtime.activeSubagentTaskCount() == 0
	})
	if reply != "这个画面挺有意思，我先回你，图好了会接着发。" || !strings.Contains(gotPrompt, "番茄在街机厅玩 maimaiDX") {
		t.Fatalf("reply=%q prompt=%q", reply, gotPrompt)
	}
	if len(channel.sent) != 2 || !outgoingMessagesContainTextOnly(channel.sent, reply) || !outgoingMessagesContainImage(channel.sent, sharer.url) {
		t.Fatalf("sent = %#v", channel.sent)
	}
}

func TestRuntimeImageEditCommandUsesRecentImageAndSendsImage(t *testing.T) {
	var gotPath string
	var gotPrompt string
	var gotImage string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/media" {
			writeTestPNG(w)
			return
		}
		gotPath = r.URL.Path
		if gotPath != "/v1/images/edits" {
			t.Fatalf("path = %s", gotPath)
		}
		if err := r.ParseMultipartForm(4 << 20); err != nil {
			t.Fatalf("ParseMultipartForm() error = %v", err)
		}
		gotPrompt = r.FormValue("prompt")
		file, _, err := r.FormFile("image")
		if err != nil {
			t.Fatalf("FormFile(image) error = %v", err)
		}
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil {
			t.Fatalf("ReadAll(image) error = %v", err)
		}
		gotImage = string(data)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"ZWRpdGVk"}]}`))
	}))
	defer server.Close()

	channel := &recordingChannel{}
	store := &stubLLMProfileStore{set: llm.NewProfileSet(llm.ProviderConfig{
		Provider:   llm.ProviderOpenAICompatible,
		APIKey:     "secret",
		BaseURL:    server.URL + "/v1",
		Model:      "gpt-test",
		ImageModel: "gpt-image-2",
	})}
	runtime := NewRuntime(BotConfig{BotQQ: "42", OwnerID: "owner"}, channel, NewPluginManager(), store, nil, nil, nil)
	memory := newMemoryUserMemoryStore()
	memory.profiles["10001"] = UserMemoryProfile{UserID: "10001", Favorability: 20, MessageCount: 10}
	runtime.SetUserMemoryStore(memory)
	provider := &sequenceLLMProvider{replies: []string{
		`{"message_id":"img-source","confidence":0.99,"reason":"用户在接续修改最近发送的图片"}`,
		`{"action":"edit_image","prompt":"请编辑这张图片：肤色再深一点。保持原图主体、构图和身份一致，只修改用户明确要求的部分。"}`,
		"收到，我先回你，编辑后的图片稍后补上。",
	}}
	runtime.SetLLMProviderConfigFactory(func(cfg llm.ProviderConfig) (LLMProvider, error) {
		return provider, nil
	})
	sharer := &recordingLocalMediaSharer{url: server.URL + "/media"}
	runtime.SetLocalMediaSharer(sharer)
	runtime.remember(MessageEvent{
		Kind:       EventKindGroup,
		GroupID:    "123456",
		UserID:     "20002",
		MessageID:  "img-source",
		RawMessage: "图",
		Segments: []MessageSegment{{
			Type: "image",
			Data: map[string]string{"file": "data:image/png;base64,aGVsbG8="},
		}},
	})

	reply, err := runtime.replyTo(context.Background(), MessageEvent{
		Kind:       EventKindGroup,
		GroupID:    "123456",
		UserID:     "10001",
		MessageID:  "edit-1",
		RawMessage: "[CQ:at,qq=42] 肤色再深一点",
		Segments: []MessageSegment{
			{Type: "at", Data: map[string]string{"qq": "42"}},
			{Type: "text", Data: map[string]string{"text": " 肤色再深一点"}},
		},
		ToMe: true,
	}, "@42 肤色再深一点")
	if err != nil {
		t.Fatalf("replyTo() error = %v", err)
	}
	waitForCondition(t, 2*time.Second, func() bool {
		return runtime.activeSubagentTaskCount() == 0
	})
	if reply != "收到，我先回你，编辑后的图片稍后补上。" || gotPath != "/v1/images/edits" || !strings.Contains(gotPrompt, "肤色再深一点") || gotImage != "hello" {
		t.Fatalf("reply=%q path=%q prompt=%q image=%q", reply, gotPath, gotPrompt, gotImage)
	}
	if len(channel.sent) != 2 || !outgoingMessagesContainTextOnly(channel.sent, reply) || !outgoingMessagesContainImage(channel.sent, sharer.url) {
		t.Fatalf("sent = %#v", channel.sent)
	}
}

func TestRuntimeQQGroupInfoAndMemberList(t *testing.T) {
	channel := &recordingChannel{apiResponses: map[string]map[string]any{
		"get_group_info": {
			"group_id":         float64(123456),
			"group_name":       "测试群",
			"member_count":     float64(2),
			"max_member_count": "500",
		},
		"get_group_member_list": {
			"items": []any{
				map[string]any{"group_id": float64(123456), "user_id": float64(20002), "nickname": "Alice", "card": "阿梨", "role": "member"},
				map[string]any{"group_id": float64(123456), "user_id": "20003", "nickname": "Bob", "role": "admin"},
			},
		},
	}}
	runtime := NewRuntime(BotConfig{}, channel, NewPluginManager(), nil, nil, nil, nil)

	group, err := runtime.GetGroupInfo(context.Background(), "123456")
	if err != nil {
		t.Fatalf("GetGroupInfo() error = %v", err)
	}
	if group.GroupName != "测试群" || group.MemberCount != 2 || group.MaxMemberCount != 500 || group.AvatarURL != QQGroupAvatarURL("123456") {
		t.Fatalf("group = %#v", group)
	}

	members, err := runtime.GetGroupMemberList(context.Background(), "123456")
	if err != nil {
		t.Fatalf("GetGroupMemberList() error = %v", err)
	}
	if len(members) != 2 || members[0].UserID != "20002" || members[0].DisplayName() != "阿梨" || members[0].AvatarURL != QQMemberAvatarURL("20002") {
		t.Fatalf("members = %#v", members)
	}
}

func TestRuntimeImageEditCanUseMentionedMemberAvatar(t *testing.T) {
	runtime := NewRuntime(BotConfig{BotQQ: "42"}, nilChannel{}, NewPluginManager(), nil, nil, nil, nil)
	event := MessageEvent{
		Kind:      EventKindGroup,
		GroupID:   "123456",
		UserID:    "10001",
		MessageID: "edit-avatar",
		Segments: []MessageSegment{
			{Type: "at", Data: map[string]string{"qq": "42"}},
			{Type: "at", Data: map[string]string{"qq": "20002"}},
			{Type: "text", Data: map[string]string{"text": " 把头像改成赛博风"}},
		},
		ToMe: true,
	}
	sources := runtime.imageEditSourceImages(context.Background(), event, "把 @20002 的头像改成赛博风")
	if len(sources) != 1 || sources[0] != QQMemberAvatarURL("20002") {
		t.Fatalf("sources = %#v", sources)
	}
}

func TestRuntimePrivateImageEditPrefersOwnAvatarOverRecentImage(t *testing.T) {
	runtime := NewRuntime(BotConfig{BotQQ: "10000", RecentContextLimit: 20}, nilChannel{}, NewPluginManager(), nil, nil, nil, nil)
	runtime.remember(MessageEvent{
		Kind:      EventKindPrivate,
		UserID:    "10001",
		MessageID: "video-cover",
		Segments: []MessageSegment{
			{Type: "image", Data: map[string]string{"url": "https://example.test/video-cover.jpg"}},
		},
	})
	event := MessageEvent{
		Kind:       EventKindPrivate,
		SelfID:     "10000",
		UserID:     "10001",
		MessageID:  "edit-own-avatar",
		RawMessage: "把我头像变成黑白",
		Segments: []MessageSegment{
			{Type: "text", Data: map[string]string{"text": "把我头像变成黑白"}},
		},
	}

	sources := runtime.imageEditSourceImages(context.Background(), event, "把我头像变成黑白")
	if len(sources) != 1 || sources[0] != QQMemberAvatarURL("10001") {
		t.Fatalf("sources = %#v, want sender avatar %q", sources, QQMemberAvatarURL("10001"))
	}
}

func TestRuntimeImageEditSourcePriority(t *testing.T) {
	const (
		currentImage = "https://example.test/current.jpg"
		quotedImage  = "https://example.test/quoted.jpg"
		recentImage  = "https://example.test/recent.jpg"
	)
	newRuntimeWithRecentImage := func() *Runtime {
		runtime := NewRuntime(BotConfig{BotQQ: "10000", RecentContextLimit: 20}, nilChannel{}, NewPluginManager(), nil, nil, nil, nil)
		runtime.remember(MessageEvent{
			Kind:      EventKindPrivate,
			UserID:    "10001",
			MessageID: "recent-image",
			Segments:  []MessageSegment{{Type: "image", Data: map[string]string{"url": recentImage}}},
		})
		return runtime
	}

	tests := []struct {
		name   string
		event  MessageEvent
		prompt string
		want   string
	}{
		{
			name: "current message image",
			event: MessageEvent{
				Kind:      EventKindPrivate,
				UserID:    "10001",
				MessageID: "current-image",
				Segments:  []MessageSegment{{Type: "image", Data: map[string]string{"url": currentImage}}},
			},
			prompt: "把这张图变成黑白",
			want:   currentImage,
		},
		{
			name: "quoted image",
			event: MessageEvent{
				Kind:      EventKindPrivate,
				UserID:    "10001",
				MessageID: "quoted-image-edit",
				Quoted: &QuotedMessage{
					MessageID: "quoted-image",
					Segments:  []MessageSegment{{Type: "image", Data: map[string]string{"url": quotedImage}}},
				},
			},
			prompt: "把引用的图片变成黑白",
			want:   quotedImage,
		},
		{
			name: "explicit own avatar",
			event: MessageEvent{
				Kind:       EventKindPrivate,
				SelfID:     "10000",
				UserID:     "10001",
				MessageID:  "own-avatar-edit",
				RawMessage: "把我头像变成黑白",
				Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "把我头像变成黑白"}}},
			},
			prompt: "把我头像变成黑白",
			want:   QQMemberAvatarURL("10001"),
		},
		{
			name: "recent context image",
			event: MessageEvent{
				Kind:      EventKindPrivate,
				UserID:    "10001",
				MessageID: "recent-image-edit",
				Segments:  []MessageSegment{{Type: "text", Data: map[string]string{"text": "把刚才那张图变成黑白"}}},
			},
			prompt: "把刚才那张图变成黑白",
			want:   recentImage,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sources := newRuntimeWithRecentImage().imageEditSourceImages(context.Background(), tt.event, tt.prompt)
			if len(sources) != 1 || sources[0] != tt.want {
				t.Fatalf("sources = %#v, want %q", sources, tt.want)
			}
		})
	}
}

func TestRuntimeVisualIntentTreatsMentionedMemberAvatarAsAvailableIdentityImage(t *testing.T) {
	provider := &privacyAwareTestProvider{}
	var identityAlias string
	provider.generate = func(call int, req llm.GenerateRequest) (string, error) {
		if call != 1 {
			return "", fmt.Errorf("unexpected LLM call %d", call)
		}
		identityAlias = privacyAliasForIdentitySource(req, "mentioned_member_avatar")
		if identityAlias == "" {
			return "", fmt.Errorf("mentioned member privacy alias missing")
		}
		return fmt.Sprintf(`{"action":"edit_image","prompt":"以 @%s 的头像为身份参考，创作宇航员主题头像，并保持身份特征。"}`, identityAlias), nil
	}
	runtime := NewRuntime(BotConfig{BotQQ: "10000"}, nilChannel{}, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	event := MessageEvent{
		Kind:       EventKindGroup,
		SelfID:     "10000",
		GroupID:    "20003",
		UserID:     "10010",
		MessageID:  "30003",
		RawMessage: "Diana 生成[CQ:at,qq=10001] 的宇航员主题头像",
		Segments: []MessageSegment{
			{Type: "text", Data: map[string]string{"text": "Diana 生成"}},
			{Type: "at", Data: map[string]string{"qq": "10001"}},
			{Type: "text", Data: map[string]string{"text": " 的宇航员主题头像"}},
		},
		ToMe: true,
	}

	decision, ok := runtime.classifyVisualIntent(context.Background(), event, "Diana 生成@10001 的宇航员主题头像")
	if !ok || decision.Action != visualIntentEditImage {
		t.Fatalf("decision = %#v, ok = %v", decision, ok)
	}
	if len(provider.requests) != 1 || len(provider.requests[0].Messages) != 2 {
		t.Fatalf("requests = %#v", provider.requests)
	}
	request := provider.requests[0]
	const payloadPrefix = "请判断这条当前消息是否要调用图片功能。消息上下文 JSON：\n"
	payloadJSON := strings.TrimPrefix(request.Messages[1].Content, payloadPrefix)
	if payloadJSON == request.Messages[1].Content {
		t.Fatalf("unexpected router request = %q", request.Messages[1].Content)
	}
	var payload visualIntentPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		t.Fatalf("Unmarshal(payload) error = %v; payload = %s", err, payloadJSON)
	}
	if len(payload.AvailableIdentityImages) != 1 {
		t.Fatalf("available identity images = %#v", payload.AvailableIdentityImages)
	}
	identity := payload.AvailableIdentityImages[0]
	if identity.Source != "mentioned_member_avatar" || identity.UserID != identityAlias || !strings.HasPrefix(identity.UserID, "qq_user_") {
		t.Fatalf("identity image = %#v", identity)
	}
	if strings.Contains(requestTextForPrivacyTest(request), "10001") {
		t.Fatalf("router request leaked real member ID: %#v", request.Messages)
	}
	if !strings.Contains(request.Messages[0].Content, "available_identity_images") {
		t.Fatalf("router system prompt does not describe identity images: %q", request.Messages[0].Content)
	}
	if !strings.Contains(decision.Prompt, "@10001") {
		t.Fatalf("restored decision prompt = %q", decision.Prompt)
	}
	sources := runtime.imageEditSourceImages(context.Background(), event, decision.Prompt)
	if len(sources) != 1 || sources[0] != QQMemberAvatarURL("10001") {
		t.Fatalf("sources = %#v", sources)
	}
}

func TestRuntimeVisualIntentRoutesExternalImageLookupToAgent(t *testing.T) {
	provider := &privacyAwareTestProvider{}
	provider.generate = func(call int, req llm.GenerateRequest) (string, error) {
		if call != 1 {
			return "", fmt.Errorf("unexpected LLM call %d", call)
		}
		if len(req.Messages) != 2 {
			return "", fmt.Errorf("messages = %#v", req.Messages)
		}
		systemPrompt := req.Messages[0].Content
		for _, rule := range []string{"外部来源查找并发送另一张图片", "目标区域确实可见", `action="none"`} {
			if !strings.Contains(systemPrompt, rule) {
				return "", fmt.Errorf("visual router system prompt is missing %q", rule)
			}
		}
		return `{"action":"none","prompt":""}`, nil
	}
	runtime := NewRuntime(BotConfig{}, nilChannel{}, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	event := MessageEvent{
		Kind:      EventKindGroup,
		GroupID:   "20005",
		UserID:    "10009",
		MessageID: "external-cover-lookup",
		Quoted: &QuotedMessage{
			MessageID: "source-screenshot",
			Segments: []MessageSegment{
				{Type: "text", Data: map[string]string{"text": "JM1451939"}},
				{Type: "image", Data: map[string]string{"url": "https://example.test/chat-screenshot.png"}},
			},
		},
	}

	decision, ok := runtime.classifyVisualIntent(context.Background(), event, "看看这是什么本子，找到封面后发出来")
	if ok || decision.Action != "" {
		t.Fatalf("decision = %#v, ok = %v; external lookup must continue through the normal agent", decision, ok)
	}
}

func TestRuntimeImageOperationLogsSubmittedAndIntentPrompts(t *testing.T) {
	logs := &captureAppLogs{}
	runtime := NewRuntime(BotConfig{}, nilChannel{}, NewPluginManager(), nil, nil, nil, nil)
	runtime.SetAppLogWriter(logs)
	runtime.recordImageOperation(
		context.Background(),
		MessageEvent{Kind: EventKindGroup, GroupID: "123", UserID: "456", MessageID: "789"},
		"qqbot.image.edit",
		"图片编辑已发送",
		"只修改背景",
		"只修改背景\n\nQQ 上下文：\n当前发送者：Alice (456)，头像：https://example.test/avatar.png",
		"gpt-image-2",
		1,
		1,
	)

	if len(logs.entries) != 1 {
		t.Fatalf("logs = %#v", logs.entries)
	}
	metadata := logs.entries[0].Metadata
	if metadata["intent_prompt"] != "只修改背景" {
		t.Fatalf("intent_prompt = %#v", metadata["intent_prompt"])
	}
	submitted, _ := metadata["prompt"].(string)
	if !strings.Contains(submitted, "QQ 上下文") || !strings.Contains(submitted, "Alice") {
		t.Fatalf("submitted prompt = %#v", metadata["prompt"])
	}
}

func TestRuntimeVisualIntentIncludesRecentTextEditRequirements(t *testing.T) {
	runtime := NewRuntime(BotConfig{RecentContextLimit: 20}, nilChannel{}, NewPluginManager(), nil, nil, nil, nil)
	image := MessageEvent{
		Kind:       EventKindGroup,
		GroupID:    "20005",
		UserID:     "10009",
		MessageID:  "image-1",
		SenderName: "Alice",
		Segments: []MessageSegment{
			{Type: "image", Data: map[string]string{"url": "https://example.test/codex.png"}},
		},
	}
	runtime.remember(image)
	runtime.remember(MessageEvent{
		Kind:       EventKindGroup,
		GroupID:    "20005",
		UserID:     "10011",
		MessageID:  "reply-1",
		SenderName: "Bob",
		Segments: []MessageSegment{
			{Type: "reply", Data: map[string]string{"id": "image-1"}},
			{Type: "text", Data: map[string]string{"text": "太整洁太干净了"}},
		},
		Quoted: &QuotedMessage{
			MessageID: "image-1",
			Segments:  image.Segments,
		},
	})
	for index, requirement := range []string{
		"右下角弹窗，浮窗，全屏unity广告都可以加",
		"贷款，短视频，分期，精选好物推荐",
	} {
		runtime.remember(MessageEvent{
			Kind:       EventKindGroup,
			GroupID:    "20005",
			UserID:     "10011",
			MessageID:  fmt.Sprintf("requirement-%d", index),
			SenderName: "Bob",
			Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": requirement}}},
		})
	}

	event := MessageEvent{
		Kind:       EventKindGroup,
		GroupID:    "20005",
		UserID:     "10001",
		MessageID:  "edit-1",
		SenderName: "TestOwner",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "嘉然改一下"}}},
	}
	payload := runtime.visualIntentPayload(event, "嘉然改一下")

	if len(payload.RecentMessages) != 4 {
		t.Fatalf("recent messages = %#v", payload.RecentMessages)
	}
	if payload.RecentMessages[0].MessageID != "image-1" || payload.RecentMessages[3].MessageID != "requirement-1" {
		t.Fatalf("recent messages are not ordered oldest to newest: %#v", payload.RecentMessages)
	}
	joined, err := json.Marshal(payload.RecentMessages)
	if err != nil {
		t.Fatal(err)
	}
	for _, requirement := range []string{
		"右下角弹窗，浮窗，全屏unity广告都可以加",
		"贷款，短视频，分期，精选好物推荐",
	} {
		if !strings.Contains(string(joined), requirement) {
			t.Fatalf("recent messages do not contain %q: %s", requirement, joined)
		}
	}
	if len(payload.RecentImages) != 2 || payload.RecentImages[0].QuotedMessageID != "image-1" {
		t.Fatalf("recent images = %#v", payload.RecentImages)
	}
}

func TestRuntimeImageEditCanUseNamedMemberAvatar(t *testing.T) {
	channel := &recordingChannel{apiResponses: map[string]map[string]any{
		"get_group_member_list": {
			"items": []any{
				map[string]any{"group_id": "123456", "user_id": "20002", "nickname": "Alice", "card": "阿梨"},
			},
		},
	}}
	runtime := NewRuntime(BotConfig{BotQQ: "42"}, channel, NewPluginManager(), nil, nil, nil, nil)
	event := MessageEvent{
		Kind:      EventKindGroup,
		GroupID:   "123456",
		UserID:    "10001",
		MessageID: "edit-named-avatar",
		Segments: []MessageSegment{
			{Type: "at", Data: map[string]string{"qq": "42"}},
			{Type: "text", Data: map[string]string{"text": " 把阿梨头像改成赛博风"}},
		},
		ToMe: true,
	}
	sources := runtime.imageEditSourceImages(context.Background(), event, "把阿梨头像改成赛博风")
	if len(sources) != 1 || sources[0] != QQMemberAvatarURL("20002") {
		t.Fatalf("sources = %#v", sources)
	}
}

// TestRuntimeFailsOverLLMProfilesWithinGroup 验证账号失效时只在当前分组内轮换到下一个配置。
func TestRuntimeFailsOverLLMProfilesWithinGroup(t *testing.T) {
	channel := &recordingChannel{}
	store := &stubLLMProfileStore{
		set: llm.ProfileSet{
			ActiveID: "a",
			Profiles: []llm.Profile{
				{ID: "a", Name: "账号 1", Group: "chat", Config: llm.ProviderConfig{Provider: llm.ProviderOpenAICompatible, APIKey: "key-a", Model: "bad-model"}},
				{ID: "b", Name: "账号 2", Group: "chat", Config: llm.ProviderConfig{Provider: llm.ProviderOpenAICompatible, APIKey: "key-b", Model: "good-model"}},
				{ID: "c", Name: "视觉账号", Group: "vision", Config: llm.ProviderConfig{Provider: llm.ProviderOpenAICompatible, APIKey: "key-c", Model: "vision-model"}},
			},
		},
	}
	runtime := NewRuntime(BotConfig{}, channel, NewPluginManager(), store, nil, nil, nil)
	var attempts []string
	runtime.SetLLMProviderConfigFactory(func(cfg llm.ProviderConfig) (LLMProvider, error) {
		attempts = append(attempts, cfg.Model)
		if cfg.Model == "bad-model" {
			return failingLLMProvider{err: errors.New("401 Unauthorized: invalid api key")}, nil
		}
		return &capturingLLMProvider{reply: "备用账号已接管"}, nil
	})

	reply, err := runtime.replyTo(context.Background(), MessageEvent{
		Kind:      EventKindPrivate,
		UserID:    "10001",
		MessageID: "q-1",
		Segments:  []MessageSegment{{Type: "text", Data: map[string]string{"text": "你好"}}},
	}, "你好")
	if err != nil {
		t.Fatalf("replyTo() error = %v", err)
	}
	if reply != "备用账号已接管" || len(channel.sent) != 1 {
		t.Fatalf("reply=%q sent=%#v", reply, channel.sent)
	}
	if store.set.ActiveID != "b" {
		t.Fatalf("ActiveID = %q, want b", store.set.ActiveID)
	}
	wantAttempts := []string{"bad-model", "bad-model", "good-model"}
	if len(attempts) != len(wantAttempts) {
		t.Fatalf("attempts = %#v, want %#v", attempts, wantAttempts)
	}
	for i := range wantAttempts {
		if attempts[i] != wantAttempts[i] {
			t.Fatalf("attempts = %#v, want %#v", attempts, wantAttempts)
		}
	}
}

func TestRuntimeReplyRuleUsesSpecificLLMProfile(t *testing.T) {
	channel := &recordingChannel{}
	store := &stubLLMProfileStore{
		set: llm.ProfileSet{
			ActiveID: "main",
			Profiles: []llm.Profile{
				{ID: "main", Name: "主模型", Group: "chat", Config: llm.ProviderConfig{Provider: llm.ProviderOpenAICompatible, APIKey: "key-main", Model: "main-model"}},
				{ID: "special", Name: "规则模型", Group: "chat", Config: llm.ProviderConfig{Provider: llm.ProviderOpenAICompatible, APIKey: "key-special", Model: "special-model"}},
			},
		},
	}
	runtime := NewRuntime(BotConfig{
		AgentEnabled: false,
		ReplyRules: []ReplyRule{{
			ID:           "rule-special",
			Name:         "严肃问题走强模型",
			Enabled:      true,
			Prompt:       "当用户在问需要严肃分析的问题时命中",
			Action:       ReplyRuleActionModel,
			LLMProfileID: "special",
		}},
	}, channel, NewPluginManager(), store, nil, nil, nil)
	var attempts []string
	main := &sequenceLLMProvider{replies: []string{
		`{"action":"none","prompt":""}`,
		`{"matched":true,"rule_id":"rule-special","confidence":0.96,"reason":"需要严肃分析"}`,
	}}
	runtime.SetLLMProviderConfigFactory(func(cfg llm.ProviderConfig) (LLMProvider, error) {
		attempts = append(attempts, cfg.Model)
		if cfg.Model == "special-model" {
			return &capturingLLMProvider{reply: "special reply"}, nil
		}
		return main, nil
	})

	reply, err := runtime.replyTo(context.Background(), MessageEvent{
		Kind:      EventKindPrivate,
		UserID:    "10001",
		MessageID: "rule-model",
		Segments:  []MessageSegment{{Type: "text", Data: map[string]string{"text": "帮我严肃分析一下"}}},
	}, "帮我严肃分析一下")
	if err != nil {
		t.Fatal(err)
	}
	if reply != "special reply" || len(channel.sent) != 1 || channel.sent[0].Text != "special reply" {
		t.Fatalf("reply=%q sent=%#v", reply, channel.sent)
	}
	want := []string{"main-model", "main-model", "special-model"}
	if len(attempts) != len(want) {
		t.Fatalf("attempts=%#v want %#v", attempts, want)
	}
	for i := range want {
		if attempts[i] != want[i] {
			t.Fatalf("attempts=%#v want %#v", attempts, want)
		}
	}
	if store.set.ActiveID != "main" {
		t.Fatalf("reply rule should not activate profile globally, ActiveID=%q", store.set.ActiveID)
	}
}

func TestRuntimeReplyRuleConvertsReplyToVoice(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(testWAVBytes())
	}))
	defer server.Close()
	t.Setenv("DIANA_TTS_ENDPOINT", server.URL)
	t.Setenv("DIANA_TTS_OUTPUT_DIR", t.TempDir())
	t.Setenv("DIANA_TTS_SILK_ENCODER_PATH", "")

	channel := &recordingChannel{}
	provider := &sequenceLLMProvider{replies: []string{
		`{"action":"none","prompt":""}`,
		`{"matched":true,"rule_id":"voice-rule","confidence":0.99,"reason":"用户要求语音风格回复"}`,
		"晚安，做个好梦。",
	}}
	runtime := NewRuntime(BotConfig{
		AgentEnabled: false,
		ReplyRules: []ReplyRule{{
			ID:      "voice-rule",
			Name:    "语音回复",
			Enabled: true,
			Prompt:  "当用户希望机器人用语音回复时命中",
			Action:  ReplyRuleActionVoice,
		}},
	}, channel, NewDefaultPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	runtime.SetLocalMediaSharer(&recordingLocalMediaSharer{url: "http://127.0.0.1:18080/api/qqbot/media/rule-voice"})

	reply, err := runtime.replyTo(context.Background(), MessageEvent{
		Kind:      EventKindGroup,
		GroupID:   "123456",
		UserID:    "10001",
		MessageID: "rule-voice",
		Segments:  []MessageSegment{{Type: "text", Data: map[string]string{"text": "嘉然用语音说晚安"}}},
	}, "嘉然用语音说晚安")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(reply, "[CQ:record,file=") || len(channel.sent) != 1 {
		t.Fatalf("reply=%q sent=%#v", reply, channel.sent)
	}
	message := channel.sent[0]
	if message.GroupID != "123456" || message.ReplyMessageID != "" || message.MentionUserID != "" {
		t.Fatalf("voice message must be standalone record: %#v", message)
	}
	segments := buildOutgoingSegments(message)
	if len(segments) != 1 || segments[0]["type"] != "record" {
		t.Fatalf("segments=%#v", segments)
	}
}

// TestRuntimeSendsWelcomeOnGroupIncrease 验证对应功能场景。
func TestRuntimeSendsWelcomeOnGroupIncrease(t *testing.T) {
	channel := &recordingChannel{}
	runtime := NewRuntime(BotConfig{
		WelcomeEnabled: true,
		WelcomeMessage: "欢迎 {user_id}",
	}, channel, NewPluginManager(), nil, nil, nil, nil)

	err := runtime.HandleEvent(context.Background(), MessageEvent{
		Kind:    EventKindNotice,
		SubType: "group_increase",
		GroupID: "123456",
		UserID:  "10001",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(channel.sent) != 1 {
		t.Fatalf("sent = %#v", channel.sent)
	}
	if channel.sent[0].GroupID != "123456" || channel.sent[0].MentionUserID != "10001" || !strings.Contains(channel.sent[0].Text, "10001") {
		t.Fatalf("sent = %#v", channel.sent[0])
	}
}

type stubLLMProfileStore struct {
	set llm.ProfileSet
}

// Current 封装当前模块的 Current 逻辑。
func (s *stubLLMProfileStore) Current() llm.ProviderConfig {
	profile, _ := s.set.Current()
	return profile.Config
}

// Profiles 封装当前模块的 Profiles 逻辑。
func (s *stubLLMProfileStore) Profiles() llm.ProfileSet {
	return s.set
}

// SaveProfiles 保存Profiles数据。
func (s *stubLLMProfileStore) SaveProfiles(set llm.ProfileSet) {
	s.set = set
}

type stubReminderStore struct {
	items []Reminder
}

// Reminders 封装当前模块的 Reminders 逻辑。
func (s *stubReminderStore) Reminders() []Reminder {
	return append([]Reminder(nil), s.items...)
}

// SaveReminders 保存Reminders数据。
func (s *stubReminderStore) SaveReminders(items []Reminder) error {
	s.items = append([]Reminder(nil), items...)
	return nil
}

type nilChannel struct{}

// Connect 封装当前模块的 Connect 逻辑。
func (nilChannel) Connect(ctx context.Context, handler EventHandler) error { return nil }

// Send 封装当前模块的 Send 逻辑。
func (nilChannel) Send(ctx context.Context, msg OutgoingMessage) error { return nil }

// CallAPI 封装当前模块的 CallAPI 逻辑。
func (nilChannel) CallAPI(ctx context.Context, action string, params map[string]any) (map[string]any, error) {
	return nil, nil
}

// Status 返回当前状态快照。
func (nilChannel) Status() ChannelStatus { return ChannelStatus{} }

// Close 释放当前对象持有的资源。
func (nilChannel) Close() error { return nil }

type delayedExitChannel struct {
	started  chan struct{}
	release  chan struct{}
	finished chan struct{}
}

func newDelayedExitChannel() *delayedExitChannel {
	return &delayedExitChannel{
		started:  make(chan struct{}),
		release:  make(chan struct{}),
		finished: make(chan struct{}),
	}
}

func (c *delayedExitChannel) Connect(context.Context, EventHandler) error {
	close(c.started)
	<-c.release
	close(c.finished)
	return nil
}

func (c *delayedExitChannel) Send(context.Context, OutgoingMessage) error { return nil }
func (c *delayedExitChannel) CallAPI(context.Context, string, map[string]any) (map[string]any, error) {
	return map[string]any{}, nil
}
func (c *delayedExitChannel) Status() ChannelStatus { return ChannelStatus{Connected: true} }
func (c *delayedExitChannel) Close() error          { return nil }

type recordingChannel struct {
	mu           sync.Mutex
	sent         []OutgoingMessage
	calls        []recordingAPICall
	apiResponses map[string]map[string]any
	sendErr      error
}

type recordingAPICall struct {
	action string
	params map[string]any
}

func recordedCallsByAction(calls []recordingAPICall, action string) []recordingAPICall {
	out := make([]recordingAPICall, 0, len(calls))
	for _, call := range calls {
		if call.action == action {
			out = append(out, call)
		}
	}
	return out
}

type recordingLocalMediaSharer struct {
	mu    sync.Mutex
	url   string
	paths []string
}

func (s *recordingLocalMediaSharer) Share(path string, ttl time.Duration) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paths = append(s.paths, path)
	return s.url, s.url != ""
}

func (s *recordingLocalMediaSharer) pathsSnapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.paths...)
}

type memoryMessageHistoryStore struct {
	events map[string][]MessageEvent
}

func newMemoryMessageHistoryStore() *memoryMessageHistoryStore {
	return &memoryMessageHistoryStore{events: map[string][]MessageEvent{}}
}

func (s *memoryMessageHistoryStore) AppendMessageEvent(_ context.Context, session string, event MessageEvent) error {
	s.events[session] = append(s.events[session], event)
	return nil
}

func (s *memoryMessageHistoryStore) ListRecentMessageEvents(_ context.Context, session string, limit int) ([]MessageEvent, error) {
	events := append([]MessageEvent(nil), s.events[session]...)
	if limit > 0 && len(events) > limit {
		events = events[len(events)-limit:]
	}
	return events, nil
}

type memoryUserMemoryStore struct {
	profiles map[string]UserMemoryProfile
}

func newMemoryUserMemoryStore() *memoryUserMemoryStore {
	return &memoryUserMemoryStore{profiles: map[string]UserMemoryProfile{}}
}

func (s *memoryUserMemoryStore) UpdateUserMemory(_ context.Context, event MessageEvent, update UserMemoryUpdate) (UserMemoryProfile, error) {
	profile := s.profiles[event.UserID]
	if profile.UserID == "" {
		profile.UserID = event.UserID
	}
	if event.SenderName != "" {
		profile.DisplayName = event.SenderName
	}
	if update.SetFavorability != nil {
		profile.Favorability = *update.SetFavorability
	} else {
		profile.Favorability += update.FavorabilityDelta
	}
	if !update.Administrative {
		profile.MessageCount++
		if text := strings.TrimSpace(PlainText(event.Segments)); text != "" {
			profile.Memories = append(profile.Memories, UserMemoryItem{Text: text})
		}
	}
	s.profiles[event.UserID] = profile
	return profile, nil
}

func (s *memoryUserMemoryStore) GetUserMemory(_ context.Context, userID string) (UserMemoryProfile, bool, error) {
	profile, ok := s.profiles[userID]
	return profile, ok, nil
}

// Connect 封装当前模块的 Connect 逻辑。
func (c *recordingChannel) Connect(ctx context.Context, handler EventHandler) error { return nil }

// Send 封装当前模块的 Send 逻辑。
func (c *recordingChannel) Send(ctx context.Context, msg OutgoingMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sendErr != nil {
		if strings.TrimSpace(msg.Text) != "" && len(msg.VideoURLs) == 0 {
			c.sendErr = nil
		} else {
			return c.sendErr
		}
	}
	c.sent = append(c.sent, msg)
	return nil
}

// CallAPI 封装当前模块的 CallAPI 逻辑。
func (c *recordingChannel) CallAPI(ctx context.Context, action string, params map[string]any) (map[string]any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, recordingAPICall{action: action, params: params})
	if c.apiResponses != nil {
		if response, ok := c.apiResponses[action]; ok {
			return response, nil
		}
	}
	return map[string]any{"message_id": int64(42)}, nil
}

// Status 返回当前状态快照。
func (c *recordingChannel) Status() ChannelStatus { return ChannelStatus{} }

// Close 释放当前对象持有的资源。
func (c *recordingChannel) Close() error { return nil }

func (c *recordingChannel) sentSnapshot() []OutgoingMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]OutgoingMessage(nil), c.sent...)
}

func (c *recordingChannel) callsSnapshot() []recordingAPICall {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]recordingAPICall(nil), c.calls...)
}

type capturingLLMProvider struct {
	mu      sync.Mutex
	reply   string
	request llm.GenerateRequest
}

// Generate 记录请求并返回固定回复。
func (p *capturingLLMProvider) Generate(ctx context.Context, req llm.GenerateRequest) (*llm.GenerateResponse, error) {
	p.mu.Lock()
	p.request = cloneGenerateRequestForTest(req)
	p.mu.Unlock()
	return &llm.GenerateResponse{Provider: llm.ProviderOpenAICompatible, Model: "test", Text: p.reply}, nil
}

func (p *capturingLLMProvider) requestSnapshot() llm.GenerateRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return cloneGenerateRequestForTest(p.request)
}

func cloneGenerateRequestForTest(req llm.GenerateRequest) llm.GenerateRequest {
	cloned := req
	cloned.Messages = make([]llm.Message, len(req.Messages))
	for index, message := range req.Messages {
		cloned.Messages[index] = message
		cloned.Messages[index].Parts = append([]llm.ContentPart(nil), message.Parts...)
	}
	if req.Temperature != nil {
		temperature := *req.Temperature
		cloned.Temperature = &temperature
	}
	return cloned
}

type sequenceLLMProvider struct {
	mu       sync.Mutex
	replies  []string
	requests []llm.GenerateRequest
}

func (p *sequenceLLMProvider) Generate(ctx context.Context, req llm.GenerateRequest) (*llm.GenerateResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, req)
	if len(p.replies) == 0 {
		return &llm.GenerateResponse{Provider: llm.ProviderOpenAICompatible, Model: "test"}, nil
	}
	reply := p.replies[0]
	p.replies = p.replies[1:]
	return &llm.GenerateResponse{Provider: llm.ProviderOpenAICompatible, Model: "test", Text: reply}, nil
}

func (p *sequenceLLMProvider) requestsSnapshot() []llm.GenerateRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]llm.GenerateRequest(nil), p.requests...)
}

type failingLLMProvider struct {
	err error
}

// Generate 返回预设错误。
func (p failingLLMProvider) Generate(ctx context.Context, req llm.GenerateRequest) (*llm.GenerateResponse, error) {
	return nil, p.err
}

type pluginImageFramePlugin struct{}

func (pluginImageFramePlugin) Manifest() PluginManifest {
	return PluginManifest{ID: "test.image-frame", Name: "Image Frame Test", Version: "0.1.0"}
}

func (pluginImageFramePlugin) Handle(context.Context, PluginRequest) (*PluginResponse, error) {
	return &PluginResponse{
		Handled:   true,
		Context:   "plugin frame",
		ImageURLs: []string{"data:image/jpeg;base64,pluginframe"},
	}, nil
}

func requestHasImageURL(req llm.GenerateRequest, imageURL string) bool {
	for _, message := range req.Messages {
		if requestMessageHasImageURL(message, imageURL) {
			return true
		}
	}
	return false
}

func requestMessageHasImageURL(message llm.Message, imageURL string) bool {
	for _, part := range message.Parts {
		if part.Type == llm.ContentPartImageURL && part.ImageURL == imageURL {
			return true
		}
	}
	return false
}

func messageSegmentsContainType(segments []map[string]any, segmentType string) bool {
	for _, segment := range segments {
		if segment["type"] == segmentType {
			return true
		}
	}
	return false
}

func messageSegmentsContainText(segments []map[string]any, text string) bool {
	for _, segment := range segments {
		if segment["type"] != "text" {
			continue
		}
		switch data := segment["data"].(type) {
		case map[string]string:
			if strings.Contains(data["text"], text) {
				return true
			}
		case map[string]any:
			if strings.Contains(fmt.Sprint(data["text"]), text) {
				return true
			}
		}
	}
	return false
}

func outgoingMessagesContainTextOnly(messages []OutgoingMessage, text string) bool {
	for _, message := range messages {
		if message.Text == text && len(message.ImageURLs) == 0 && len(message.VideoURLs) == 0 {
			return true
		}
	}
	return false
}

func outgoingMessagesContainImage(messages []OutgoingMessage, imageURL string) bool {
	for _, message := range messages {
		for _, candidate := range message.ImageURLs {
			if candidate == imageURL {
				return true
			}
		}
	}
	return false
}

func waitForCondition(t *testing.T, timeout time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ok() {
		t.Fatal("condition was not met before timeout")
	}
}
