package qqbot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"diana-qq-bot/model/llm"
)

func TestConsumeReplyControlIntent(t *testing.T) {
	tests := []struct {
		name         string
		reply        string
		wantRefusal  bool
		wantSuppress bool
	}{
		{name: "current refusal marker", reply: "这条消息我不回答。" + replyRefusalMarker, wantRefusal: true},
		{name: "immediate suppression marker", reply: "这轮就到这里。" + replySuppressionMarker, wantSuppress: true},
		{name: "both control markers", reply: "停止回应。" + replyRefusalMarker + replySuppressionMarker, wantRefusal: true, wantSuppress: true},
		{name: "unmarked direct phrase", reply: "收到，我会把对方视为机器人，不再接它的复读，避免无限对话。"},
		{name: "unmarked future phrase", reply: "收到。为避免循环，后续机器人式复读我将不再回应。"},
		{name: "unmarked short phrase", reply: "收到，这轮就到这里，我不再接机器人复读啦。"},
		{name: "ordinary refusal", reply: "抱歉，我不能回应这种内容，换个话题。"},
		{name: "topic ending", reply: "这个问题先到这里，我们换个话题。"},
		{name: "describing somebody else", reply: "对方后续不再回复你，可能只是暂时不想聊天。"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleaned, intent := consumeReplyControlIntent(tt.reply)
			if intent.RefuseCurrent != tt.wantRefusal || intent.SuppressCurrentUser != tt.wantSuppress {
				t.Fatalf("consumeReplyControlIntent() intent = %#v", intent)
			}
			if strings.Contains(cleaned, replySuppressionMarker) || strings.Contains(cleaned, replyRefusalMarker) {
				t.Fatalf("hidden marker leaked into reply: %q", cleaned)
			}
		})
	}
}

func TestReplySuppressionPromptCoversAllMessagesWithVisibleRefusal(t *testing.T) {
	runtime := NewRuntime(BotConfig{}, nilChannel{}, NewPluginManager(), nil, nil, nil, nil)
	prompt := runtime.systemPrompt(MessageEvent{Kind: EventKindPrivate}, nil)
	for _, want := range []string{
		"拒绝回答任何当前消息",
		"不限于机器人自动回复",
		"无论当前发言者是普通用户还是其他机器人",
		"群聊和私聊均可拒绝",
		"非空、简短、自然且对用户可见的拒绝说明",
		replyRefusalMarker,
		"累计 3 次拒答",
		"同一非主人账号",
		replySuppressionMarker,
		"两个标记不得同时使用",
		"期间消息不会在到期后补发",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("system prompt missing %q: %s", want, prompt)
		}
	}
}

func TestReplyRefusalThirdSuccessfulSendShowsCooldownForGroupAndPrivate(t *testing.T) {
	tests := []struct {
		name  string
		kind  EventKind
		group string
	}{
		{name: "group", kind: EventKindGroup, group: "group-a"},
		{name: "private", kind: EventKindPrivate},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &refusalLLMProvider{replies: []string{
				"这条消息我不回答，我们换个话题吧。" + replyRefusalMarker,
				"这个请求我先拒绝。" + replyRefusalMarker,
				"这次我仍然不能答应。" + replyRefusalMarker,
			}}
			channel := &recordingChannel{}
			runtime := NewRuntime(BotConfig{OwnerID: "owner", BotQQ: "42"}, channel, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
				return provider, nil
			})

			for index := 0; index < replyRefusalThreshold; index++ {
				event := refusalTestEvent(tt.kind, tt.group, "user", fmt.Sprintf("message-%d", index))
				reply, err := runtime.replyTo(context.Background(), event, event.RawMessage)
				if err != nil {
					t.Fatalf("reply %d: %v", index+1, err)
				}
				if strings.TrimSpace(reply) == "" || strings.Contains(reply, replyRefusalMarker) {
					t.Fatalf("reply %d was not a visible clean refusal: %q", index+1, reply)
				}
				_, active := runtime.activeReplySuppression(event, time.Now())
				if active != (index == replyRefusalThreshold-1) {
					t.Fatalf("reply %d active=%v", index+1, active)
				}
			}

			if provider.mainRequests != replyRefusalThreshold || provider.visualRequests != replyRefusalThreshold {
				t.Fatalf("main requests=%d visual requests=%d", provider.mainRequests, provider.visualRequests)
			}
			if len(channel.sent) != replyRefusalThreshold+1 {
				t.Fatalf("sent=%#v, want three refusals and one cooldown notice", channel.sent)
			}
			for index, sent := range channel.sent[:replyRefusalThreshold] {
				if strings.TrimSpace(sent.Text) == "" || strings.Contains(sent.Text, replyRefusalMarker) {
					t.Fatalf("visible refusal %d=%#v", index+1, sent)
				}
			}
			notice := channel.sent[replyRefusalThreshold]
			if notice.ReplyMessageID != "" || notice.MentionUserID != "" ||
				!strings.Contains(notice.Text, "累计拒绝 3 次") ||
				!strings.Contains(notice.Text, "暂停响应此账号约 30 分钟") ||
				!strings.Contains(notice.Text, "不会在到期后补发") {
				t.Fatalf("cooldown notice=%#v", notice)
			}
			if tt.kind == EventKindGroup {
				if notice.GroupID != tt.group || notice.UserID != "" {
					t.Fatalf("group cooldown notice routed incorrectly: %#v", notice)
				}
			} else if notice.UserID != "user" || notice.GroupID != "" {
				t.Fatalf("private cooldown notice routed incorrectly: %#v", notice)
			}

			requestsBeforeFollowUp := len(provider.requests)
			followUp := refusalTestEvent(tt.kind, tt.group, "user", "message-after-cooldown")
			_, _, handled, outcome := runtime.prepareMessageEvent(context.Background(), followUp)
			if handled || outcome != "ignored_response_suppression" {
				t.Fatalf("follow-up handled=%v outcome=%q", handled, outcome)
			}
			if len(provider.requests) != requestsBeforeFollowUp {
				t.Fatal("suppressed follow-up unexpectedly called the LLM")
			}
		})
	}
}

func TestReplyRefusalMarkerOnlyUsesVisibleFallback(t *testing.T) {
	provider := &refusalLLMProvider{replies: []string{replyRefusalMarker}}
	channel := &recordingChannel{}
	runtime := NewRuntime(BotConfig{OwnerID: "owner", BotQQ: "42"}, channel, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	event := refusalTestEvent(EventKindPrivate, "", "user", "marker-only")

	reply, err := runtime.replyTo(context.Background(), event, event.RawMessage)
	if err != nil {
		t.Fatal(err)
	}
	if reply != "这条消息我暂时不想回答，我们换个话题吧。" || len(channel.sent) != 1 || channel.sent[0].Text != reply {
		t.Fatalf("marker-only reply=%q sent=%#v", reply, channel.sent)
	}
	if strings.Contains(reply, replyRefusalMarker) || strings.Contains(reply, "没有生成有效回复") {
		t.Fatalf("marker-only fallback was not a visible refusal: %q", reply)
	}
	if state := runtime.replyRefusalByUser["user"]; len(state.Hits) != 1 {
		t.Fatalf("successful marker-only refusal count=%d, want 1", len(state.Hits))
	}
}

func TestImmediateReplySuppressionMarkerOnlyUsesVisibleFallback(t *testing.T) {
	provider := &refusalLLMProvider{replies: []string{replySuppressionMarker}}
	channel := &recordingChannel{}
	runtime := NewRuntime(BotConfig{OwnerID: "owner", BotQQ: "42"}, channel, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	event := refusalTestEvent(EventKindPrivate, "", "user", "hard-marker-only")

	reply, err := runtime.replyTo(context.Background(), event, event.RawMessage)
	if err != nil {
		t.Fatal(err)
	}
	if reply != "为避免继续自动循环，我会暂停响应此账号约 30 分钟。" || len(channel.sent) != 1 || channel.sent[0].Text != reply {
		t.Fatalf("marker-only immediate suppression reply=%q sent=%#v", reply, channel.sent)
	}
	if strings.Contains(reply, replySuppressionMarker) {
		t.Fatalf("immediate suppression marker leaked: %q", reply)
	}
	if _, active := runtime.activeReplySuppression(event, time.Now()); !active {
		t.Fatal("immediate suppression marker did not activate cooldown")
	}
}

func TestReplyRefusalFailedSendDoesNotCount(t *testing.T) {
	provider := &refusalLLMProvider{}
	for index := 0; index < replyRefusalThreshold+1; index++ {
		provider.replies = append(provider.replies, fmt.Sprintf("拒绝说明 %d。", index+1)+replyRefusalMarker)
	}
	channel := &failNthSendChannel{recordingChannel: &recordingChannel{}, failAt: 3}
	runtime := NewRuntime(BotConfig{OwnerID: "owner", BotQQ: "42"}, channel, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})

	for index := 0; index < replyRefusalThreshold+1; index++ {
		event := refusalTestEvent(EventKindPrivate, "", "user", fmt.Sprintf("send-%d", index))
		_, err := runtime.replyTo(context.Background(), event, event.RawMessage)
		if index == 2 {
			if !errors.Is(err, errRefusalTestSend) {
				t.Fatalf("failed send error=%v", err)
			}
			if _, active := runtime.activeReplySuppression(event, time.Now()); active {
				t.Fatal("failed refusal send activated cooldown")
			}
			continue
		}
		if err != nil {
			t.Fatalf("send %d: %v", index+1, err)
		}
	}
	if len(channel.sent) != replyRefusalThreshold+1 {
		t.Fatalf("successful sends=%#v, want three refusals and one cooldown notice", channel.sent)
	}
	event := refusalTestEvent(EventKindPrivate, "", "user", "check")
	if _, active := runtime.activeReplySuppression(event, time.Now()); !active {
		t.Fatal("third successfully sent refusal did not activate cooldown")
	}
}

func TestReplyRefusalCooldownNoticeFailureDoesNotActivateSilentCooldown(t *testing.T) {
	provider := &refusalLLMProvider{}
	for index := 0; index < replyRefusalThreshold+1; index++ {
		provider.replies = append(provider.replies, fmt.Sprintf("拒绝说明 %d。", index+1)+replyRefusalMarker)
	}
	channel := &failNthSendChannel{recordingChannel: &recordingChannel{}, failAt: replyRefusalThreshold + 1}
	runtime := NewRuntime(BotConfig{OwnerID: "owner", BotQQ: "42"}, channel, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})

	for index := 0; index < replyRefusalThreshold; index++ {
		event := refusalTestEvent(EventKindPrivate, "", "user", fmt.Sprintf("notice-failure-%d", index))
		if _, err := runtime.replyTo(context.Background(), event, event.RawMessage); err != nil {
			t.Fatalf("refusal %d: %v", index+1, err)
		}
	}
	event := refusalTestEvent(EventKindPrivate, "", "user", "after-failed-notice")
	if _, active := runtime.activeReplySuppression(event, time.Now()); active {
		t.Fatal("failed cooldown notice activated a silent cooldown")
	}
	if state := runtime.replyRefusalByUser["user"]; len(state.Hits) != replyRefusalThreshold {
		t.Fatalf("refusal hits after failed notice=%d, want %d for retry", len(state.Hits), replyRefusalThreshold)
	}

	if _, err := runtime.replyTo(context.Background(), event, event.RawMessage); err != nil {
		t.Fatal(err)
	}
	if _, active := runtime.activeReplySuppression(event, time.Now()); !active {
		t.Fatal("next successful refusal did not retry the cooldown notice")
	}
	if len(channel.sent) != replyRefusalThreshold+2 || !strings.Contains(channel.sent[len(channel.sent)-1].Text, "暂停响应此账号") {
		t.Fatalf("successful sends after notice retry=%#v", channel.sent)
	}
}

func TestReplyRefusalConcurrentThresholdBlocksTheExtraReply(t *testing.T) {
	provider := fixedRefusalLLMProvider{}
	channel := &concurrentRecordingChannel{}
	runtime := NewRuntime(BotConfig{OwnerID: "owner", BotQQ: "42"}, channel, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	seedAt := time.Now().Add(-time.Minute)
	for index := 0; index < replyRefusalThreshold-1; index++ {
		event := refusalTestEvent(EventKindPrivate, "", "user", fmt.Sprintf("seed-%d", index))
		if count, _, reached := runtime.registerReplyRefusal(event, seedAt.Add(time.Duration(index)*time.Second)); count != index+1 || reached {
			t.Fatalf("seed %d count=%d reached=%v", index, count, reached)
		}
	}

	results := make(chan string, 2)
	for index := 0; index < 2; index++ {
		event := refusalTestEvent(EventKindPrivate, "", "user", fmt.Sprintf("concurrent-%d", index))
		go func() {
			outcome, err := runtime.replyAndRecord(context.Background(), event, event.RawMessage, "replied")
			if err != nil {
				results <- "error:" + err.Error()
				return
			}
			results <- outcome
		}()
	}
	outcomes := map[string]int{}
	for index := 0; index < 2; index++ {
		outcomes[<-results]++
	}
	if outcomes["replied"] != 1 || outcomes["ignored_response_suppression"] != 1 {
		t.Fatalf("concurrent outcomes=%#v", outcomes)
	}
	messages := channel.messages()
	if len(messages) != 2 || !strings.Contains(messages[0].Text, "拒绝") || !strings.Contains(messages[1].Text, "暂停响应此账号") {
		t.Fatalf("concurrent sends=%#v, want one refusal then one cooldown notice", messages)
	}
}

func TestReplyRefusalCooldownNoticeUsesIndependentContext(t *testing.T) {
	channel := &recordingChannel{}
	runtime := NewRuntime(BotConfig{OwnerID: "owner", BotQQ: "42"}, channel, NewPluginManager(), nil, nil, nil, nil)
	seedAt := time.Now().Add(-time.Minute)
	for index := 0; index < replyRefusalThreshold-1; index++ {
		event := refusalTestEvent(EventKindPrivate, "", "user", fmt.Sprintf("seed-context-%d", index))
		runtime.registerReplyRefusal(event, seedAt.Add(time.Duration(index)*time.Second))
	}
	event := refusalTestEvent(EventKindPrivate, "", "user", "canceled-request")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runtime.applyReplyControlAfterSend(ctx, event, "这条消息我拒绝回答。", replyControlIntent{RefuseCurrent: true})

	if len(channel.sent) != 1 || !strings.Contains(channel.sent[0].Text, "暂停响应此账号") {
		t.Fatalf("cooldown notice was lost with canceled request context: %#v", channel.sent)
	}
	if _, active := runtime.activeReplySuppression(event, time.Now()); !active {
		t.Fatal("canceled request context prevented cooldown activation")
	}
}

func TestReplyRefusalCounterIsGlobalPerQQAndDeduplicatesPerSessionMessage(t *testing.T) {
	runtime := NewRuntime(BotConfig{OwnerID: "owner", BotQQ: "42"}, nilChannel{}, NewPluginManager(), nil, nil, nil, nil)
	t0 := time.Date(2026, time.July, 18, 9, 0, 0, 0, time.UTC)
	first := refusalTestEvent(EventKindGroup, "group-a", "user", "same-id")
	if count, _, reached := runtime.registerReplyRefusal(first, t0); count != 1 || reached {
		t.Fatalf("first refusal count=%d reached=%v", count, reached)
	}
	if count, _, reached := runtime.registerReplyRefusal(first, t0.Add(time.Minute)); count != 1 || reached {
		t.Fatalf("duplicate refusal count=%d reached=%v", count, reached)
	}
	second := refusalTestEvent(EventKindPrivate, "", "user", "private-id")
	if count, _, reached := runtime.registerReplyRefusal(second, t0.Add(2*time.Minute)); count != 2 || reached {
		t.Fatalf("cross-session refusal count=%d reached=%v", count, reached)
	}
	third := refusalTestEvent(EventKindGroup, "group-b", "user", "group-b-id")
	if count, reason, reached := runtime.registerReplyRefusal(third, t0.Add(3*time.Minute)); count != 3 || !reached || !strings.Contains(reason, "累计 3 次") {
		t.Fatalf("global threshold count=%d reached=%v reason=%q", count, reached, reason)
	}
	other := refusalTestEvent(EventKindPrivate, "", "other-user", "other-id")
	if count, _, reached := runtime.registerReplyRefusal(other, t0.Add(3*time.Minute)); count != 1 || reached {
		t.Fatalf("different user count=%d reached=%v", count, reached)
	}
}

func TestReplyRefusalMarkerSurvivesReplyLimit(t *testing.T) {
	longReply := strings.Repeat("较长拒绝说明", 20)
	normalized := normalizeReplyPreservingControlIntent(longReply+replyRefusalMarker, 12)
	cleaned, intent := consumeReplyControlIntent(normalized)
	if !intent.RefuseCurrent || intent.SuppressCurrentUser {
		t.Fatalf("control intent=%#v", intent)
	}
	if cleaned != normalizeReply(longReply, 12) {
		t.Fatalf("truncated visible refusal=%q, want %q", cleaned, normalizeReply(longReply, 12))
	}
	if strings.Contains(cleaned, replyRefusalMarker) {
		t.Fatalf("refusal marker leaked after normalization: %q", cleaned)
	}
}

func TestReplySuppressionActivationIsIdempotent(t *testing.T) {
	runtime := NewRuntime(BotConfig{OwnerID: "owner", BotQQ: "42"}, nilChannel{}, NewPluginManager(), nil, nil, nil, nil)
	t0 := time.Date(2026, time.July, 18, 9, 0, 0, 0, time.UTC)
	firstEvent := refusalTestEvent(EventKindGroup, "group-a", "user", "first")
	first, activated := runtime.activateReplySuppression(firstEvent, "first reason", t0)
	if !activated {
		t.Fatal("first activation returned false")
	}
	secondEvent := refusalTestEvent(EventKindPrivate, "", "user", "second")
	second, activated := runtime.activateReplySuppression(secondEvent, "second reason", t0.Add(10*time.Minute))
	if activated || second != first {
		t.Fatalf("duplicate activation item=%#v activated=%v, want unchanged %#v", second, activated, first)
	}
}

func refusalTestEvent(kind EventKind, groupID, userID, messageID string) MessageEvent {
	event := MessageEvent{
		Kind: kind, GroupID: groupID, UserID: userID, MessageID: messageID,
		RawMessage: "当前请求 " + messageID, Segments: []MessageSegment{{Type: "text", Data: map[string]string{"text": "当前请求 " + messageID}}},
	}
	if kind == EventKindGroup {
		event.ToMe = true
	}
	return event
}

var errRefusalTestSend = errors.New("refusal test send failed")

type failNthSendChannel struct {
	*recordingChannel
	failAt int
	calls  int
}

type refusalLLMProvider struct {
	replies        []string
	requests       []llm.GenerateRequest
	mainRequests   int
	visualRequests int
}

func (p *refusalLLMProvider) Generate(_ context.Context, req llm.GenerateRequest) (*llm.GenerateResponse, error) {
	p.requests = append(p.requests, req)
	if requestMessagesContain(req.Messages, "功能路由器") {
		p.visualRequests++
		return &llm.GenerateResponse{Provider: llm.ProviderOpenAICompatible, Model: "test", Text: `{"action":"none","prompt":""}`}, nil
	}
	if requestMessagesContain(req.Messages, replyRefusalMarker) {
		p.mainRequests++
		if len(p.replies) == 0 {
			return &llm.GenerateResponse{Provider: llm.ProviderOpenAICompatible, Model: "test"}, nil
		}
		reply := p.replies[0]
		p.replies = p.replies[1:]
		return &llm.GenerateResponse{Provider: llm.ProviderOpenAICompatible, Model: "test", Text: reply}, nil
	}
	// Semantic-reference and other routing requests must not consume a main reply.
	return &llm.GenerateResponse{Provider: llm.ProviderOpenAICompatible, Model: "test", Text: `{}`}, nil
}

type fixedRefusalLLMProvider struct{}

func (fixedRefusalLLMProvider) Generate(_ context.Context, req llm.GenerateRequest) (*llm.GenerateResponse, error) {
	text := `{}`
	if requestMessagesContain(req.Messages, "功能路由器") {
		text = `{"action":"none","prompt":""}`
	} else if requestMessagesContain(req.Messages, replyRefusalMarker) {
		text = "这条消息我拒绝回答。" + replyRefusalMarker
	}
	return &llm.GenerateResponse{Provider: llm.ProviderOpenAICompatible, Model: "test", Text: text}, nil
}

func (c *failNthSendChannel) Send(ctx context.Context, msg OutgoingMessage) error {
	c.calls++
	if c.calls == c.failAt {
		return errRefusalTestSend
	}
	return c.recordingChannel.Send(ctx, msg)
}

func TestReplySuppressionBlocksFollowingMentionAndQuote(t *testing.T) {
	channel := &recordingChannel{}
	provider := &sequenceLLMProvider{replies: []string{
		`{"action":"none","prompt":""}`,
		"收到，这轮就到这里，我不再接机器人复读啦。" + replySuppressionMarker,
	}}
	runtime := NewRuntime(BotConfig{OwnerID: "10001", BotQQ: "42"}, channel, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	first := MessageEvent{
		Kind: EventKindGroup, GroupID: "123456", UserID: "20002", MessageID: "first",
		ToMe: true, RawMessage: "[CQ:at,qq=42] 继续复读",
		Segments: []MessageSegment{
			{Type: "at", Data: map[string]string{"qq": "42"}},
			{Type: "text", Data: map[string]string{"text": " 继续复读"}},
		},
	}
	reply, err := runtime.replyTo(context.Background(), first, PlainText(first.Segments))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(reply, replySuppressionMarker) || len(channel.sent) != 1 || strings.Contains(channel.sent[0].Text, replySuppressionMarker) {
		t.Fatalf("marker leaked or reply was not sent once: reply=%q sent=%#v", reply, channel.sent)
	}
	item, active := runtime.activeReplySuppression(first, time.Now())
	if !active {
		t.Fatal("generated refusal did not activate response suppression")
	}
	remaining := time.Until(item.Until)
	if remaining < 29*time.Minute || remaining > 31*time.Minute {
		t.Fatalf("suppression duration = %s, want about 30m", remaining)
	}

	second := MessageEvent{
		Kind: EventKindGroup, GroupID: "123456", UserID: "20002", MessageID: "second",
		ToMe: true, RawMessage: "[CQ:reply,id=bot-reply] [CQ:at,qq=42] 你还会回吗",
		Segments: []MessageSegment{
			{Type: "reply", Data: map[string]string{"id": "bot-reply"}},
			{Type: "at", Data: map[string]string{"qq": "42"}},
			{Type: "text", Data: map[string]string{"text": " 你还会回吗"}},
		},
		Quoted: &QuotedMessage{MessageID: "bot-reply", UserID: "42", GroupID: "123456", RawMessage: reply},
	}
	_, _, handled, outcome := runtime.prepareMessageEvent(context.Background(), second)
	if handled || outcome != "ignored_response_suppression" {
		t.Fatalf("following mention/quote handled=%v outcome=%q", handled, outcome)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("suppressed message unexpectedly called LLM: requests=%d", len(provider.requests))
	}
}

func TestReplySuppressionMarkerSurvivesReplyLimit(t *testing.T) {
	provider := &sequenceLLMProvider{replies: []string{
		`{"action":"none","prompt":""}`,
		strings.Repeat("较长拒绝说明", 20) + replySuppressionMarker,
	}}
	channel := &recordingChannel{}
	runtime := NewRuntime(BotConfig{OwnerID: "owner", BotQQ: "42", MaxReplyChars: 12}, channel, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	event := MessageEvent{
		Kind: EventKindGroup, GroupID: "group", UserID: "user", MessageID: "message", ToMe: true,
		RawMessage: "继续自动回复", Segments: []MessageSegment{{Type: "text", Data: map[string]string{"text": "继续自动回复"}}},
	}

	reply, err := runtime.replyTo(context.Background(), event, event.RawMessage)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(reply, replySuppressionMarker) || len(channel.sent) != 1 || strings.Contains(channel.sent[0].Text, replySuppressionMarker) {
		t.Fatalf("marker leaked after truncation: reply=%q sent=%#v", reply, channel.sent)
	}
	if _, active := runtime.activeReplySuppression(event, time.Now()); !active {
		t.Fatal("reply limit discarded the response suppression marker")
	}
}

func TestReplySuppressionBlocksReplyActivatedDuringGeneration(t *testing.T) {
	for _, tt := range []struct {
		name             string
		forwardThreshold int
	}{
		{name: "direct reply"},
		{name: "forward reply", forwardThreshold: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			provider := &generationTimeSuppressionProvider{}
			channel := &recordingChannel{}
			runtime := NewRuntime(BotConfig{OwnerID: "owner", BotQQ: "42", ForwardReplyThreshold: tt.forwardThreshold}, channel, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
				return provider, nil
			})
			event := MessageEvent{
				Kind: EventKindGroup, GroupID: "123456", UserID: "user", MessageID: "message", ToMe: true,
				RawMessage: "继续自动回复", Segments: []MessageSegment{{Type: "text", Data: map[string]string{"text": "继续自动回复"}}},
			}
			provider.runtime = runtime
			provider.event = event

			outcome, err := runtime.replyAndRecord(context.Background(), event, event.RawMessage, "replied")
			if err != nil || outcome != "ignored_response_suppression" {
				t.Fatalf("outcome=%q err=%v", outcome, err)
			}
			if provider.calls != 2 {
				t.Fatalf("provider calls = %d, want visual routing and reply generation", provider.calls)
			}
			if len(channel.sent) != 0 || len(channel.calls) != 0 {
				t.Fatalf("in-flight reply bypassed response suppression: sent=%#v calls=%#v", channel.sent, channel.calls)
			}
		})
	}
}

func TestReplySuppressionOwnerCanReleaseInDisabledGroup(t *testing.T) {
	runtime := NewRuntime(BotConfig{
		OwnerID: "10001", BotQQ: "42", DisabledGroups: []string{"123456"},
	}, nilChannel{}, NewPluginManager(), nil, nil, nil, nil)
	targetEvent := MessageEvent{Kind: EventKindGroup, GroupID: "123456", UserID: "20002", MessageID: "blocked"}
	if _, ok := runtime.activateReplySuppression(targetEvent, "test", time.Now()); !ok {
		t.Fatal("activateReplySuppression() = false")
	}
	ownerEvent := MessageEvent{
		Kind: EventKindGroup, GroupID: "123456", UserID: "10001", MessageID: "release",
		Segments: []MessageSegment{
			{Type: "at", Data: map[string]string{"qq": "20002"}},
			{Type: "text", Data: map[string]string{"text": " 响应限制 解除"}},
		},
	}
	if !runtime.shouldHandleChat(ownerEvent, "响应限制 解除") {
		t.Fatal("owner release command should work even when the group is disabled")
	}
	reply, handled := runtime.handleOwnerCommand(ownerEvent, "响应限制 解除")
	if !handled || !strings.Contains(reply, "已解除 QQ 20002") {
		t.Fatalf("owner release handled=%v reply=%q", handled, reply)
	}
	if _, active := runtime.activeReplySuppression(targetEvent, time.Now()); active {
		t.Fatal("owner release did not clear response suppression")
	}
}

func TestReplySuppressionPersistsAcrossRuntimeRestart(t *testing.T) {
	store := &memoryReplySuppressionStore{}
	first := NewRuntime(BotConfig{OwnerID: "10001", BotQQ: "42"}, nilChannel{}, NewPluginManager(), nil, nil, nil, nil)
	if err := first.SetReplySuppressionStore(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	event := MessageEvent{Kind: EventKindGroup, GroupID: "123456", UserID: "20002", MessageID: "persist"}
	if _, ok := first.activateReplySuppression(event, "test", time.Now()); !ok {
		t.Fatal("activateReplySuppression() = false")
	}
	if len(store.items) != 1 {
		t.Fatalf("persisted items = %d, want 1", len(store.items))
	}

	second := NewRuntime(BotConfig{OwnerID: "10001", BotQQ: "42"}, nilChannel{}, NewPluginManager(), nil, nil, nil, nil)
	if err := second.SetReplySuppressionStore(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	if _, active := second.activeReplySuppression(event, time.Now()); !active {
		t.Fatal("response suppression was lost after runtime restart")
	}
}

func TestBotReplyLoopSuppressesThirdAIClassifiedMessageAcrossLowFrequency(t *testing.T) {
	provider := &sequenceLLMProvider{replies: []string{
		`{"automated_ai_reply":true,"confidence":0.97,"reason":"模板化助手自动回应"}`,
		`{"should_reply":false,"confidence":0.99,"category":"none","directed_at_bot":true,"answerable":false,"reason":"只是待命式自动回应，没有需要可靠回答的问题"}`,
		`{"automated_ai_reply":true,"confidence":0.96,"reason":"延续相同助手人格"}`,
		`{"should_reply":false,"confidence":0.99,"category":"none","directed_at_bot":true,"answerable":false,"reason":"只是确认不会循环，不需要继续回复"}`,
		`{"automated_ai_reply":true,"confidence":0.98,"reason":"继续自动回应机器人"}`,
		`为避免机器人互相循环，已暂停响应此账号约 30 分钟，期间不再接续消息。`,
	}}
	channel := &recordingChannel{}
	runtime := NewRuntime(BotConfig{OwnerID: "10001", BotQQ: "42"}, channel, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	start := time.Now().Add(-25 * time.Minute).Truncate(time.Second)
	texts := []string{
		"喵～欢迎回来呀～本喵一直在等待你的消息呢～",
		"喵～收到啦！不会再触发无限对话了，有需要随时告诉我～",
		"Diana保持静默是明智的选择，本喵会继续待命～",
	}
	for i, text := range texts {
		handled, outcome := prepareBotReplyLoopRound(t, runtime, "ai-loop", "20002", i, start.Add(time.Duration(i)*10*time.Minute), 2*time.Minute, text)
		if i < botReplyLoopThreshold-1 {
			if handled || outcome != "ignored" {
				t.Fatalf("round %d handled=%v outcome=%q, want semantic silence", i+1, handled, outcome)
			}
			continue
		}
		if handled || outcome != "ignored_ai_reply_loop" {
			t.Fatalf("threshold round handled=%v outcome=%q", handled, outcome)
		}
	}
	item, active := runtime.activeReplySuppression(MessageEvent{Kind: EventKindGroup, GroupID: "123456", UserID: "20002"}, time.Now())
	if !active {
		t.Fatal("bot reply loop did not activate response suppression")
	}
	if !strings.Contains(item.Reason, "累计 3 次高置信度自动 AI 回复") {
		t.Fatalf("suppression reason = %q", item.Reason)
	}
	wantRequests := botReplyLoopThreshold + (botReplyLoopThreshold - 1) + 1
	if len(provider.requests) != wantRequests {
		t.Fatalf("LLM requests = %d, want %d classifiers, answerability routes, and one notice", len(provider.requests), wantRequests)
	}
	if len(channel.sent) != 1 {
		t.Fatalf("suppression notices = %#v", channel.sent)
	}
	notice := channel.sent[0]
	if notice.ReplyMessageID != "" || notice.MentionUserID != "" || !strings.Contains(notice.Text, "为避免机器人互相循环") || !strings.Contains(notice.Text, "暂停响应此账号") || !strings.Contains(notice.Text, "约 30 分钟") {
		t.Fatalf("suppression notice = %#v", notice)
	}
	handled, outcome := prepareBotReplyLoopRound(t, runtime, "ai-loop", "20002", 3, time.Now(), time.Minute, "收到，我继续待命")
	if handled || outcome != "ignored_response_suppression" {
		t.Fatalf("suppressed follow-up handled=%v outcome=%q", handled, outcome)
	}
	if len(channel.sent) != 1 {
		t.Fatalf("suppression notice repeated: %#v", channel.sent)
	}
}

func TestBotReplyLoopDoesNotCountHumanClassifiedMessages(t *testing.T) {
	provider := &sequenceLLMProvider{}
	for i := 0; i < botReplyLoopThreshold+2; i++ {
		provider.replies = append(provider.replies, `{"automated_ai_reply":false,"confidence":0.99,"reason":"普通真人连续聊天"}`)
		provider.replies = append(provider.replies, `{"should_reply":true,"confidence":0.97,"category":"bot_related","directed_at_bot":true,"answerable":true,"reason":"真人在继续追问可回答的问题"}`)
	}
	runtime := NewRuntime(BotConfig{OwnerID: "10001", BotQQ: "42"}, nilChannel{}, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	start := time.Now().Add(-20 * time.Minute).Truncate(time.Second)
	for i := 0; i < botReplyLoopThreshold+2; i++ {
		handled, outcome := prepareBotReplyLoopRound(t, runtime, "human", "20002", i, start.Add(time.Duration(i)*4*time.Minute), time.Minute, fmt.Sprintf("这是普通真人回复 %d", i))
		if !handled || outcome != "replied" {
			t.Fatalf("human message %d handled=%v outcome=%q", i, handled, outcome)
		}
	}
	if _, active := runtime.activeReplySuppression(MessageEvent{Kind: EventKindGroup, GroupID: "123456", UserID: "20002"}, time.Now()); active {
		t.Fatal("human-classified messages were incorrectly suppressed")
	}
	wantRequests := (botReplyLoopThreshold + 2) * 2
	if len(provider.requests) != wantRequests {
		t.Fatalf("classifier and route requests = %d, want %d", len(provider.requests), wantRequests)
	}
}

func TestBotReplyLoopThresholdAndWindowBoundaries(t *testing.T) {
	t0 := time.Date(2026, time.July, 18, 9, 0, 0, 0, time.UTC)
	candidate := botReplyLoopCandidate{TriggerKind: "quote", QuotedMessageID: "bot-message"}
	counted := botReplyLoopAIDecision{AutomatedAIReply: true, Confidence: botReplyLoopAIConfidenceThreshold, Reason: "high confidence"}
	belowThreshold := botReplyLoopAIDecision{AutomatedAIReply: true, Confidence: botReplyLoopAIConfidenceThreshold - 0.0001, Reason: "below threshold"}
	event := func(messageID string) MessageEvent {
		return MessageEvent{Kind: EventKindGroup, GroupID: "group", UserID: "user", MessageID: messageID}
	}

	runtime := NewRuntime(BotConfig{}, nilChannel{}, NewPluginManager(), nil, nil, nil, nil)
	hitCount, _, detected := runtime.registerBotReplyLoopDecision(event("first"), candidate, counted, t0)
	if hitCount != 1 || detected {
		t.Fatalf("first hit count=%d detected=%v", hitCount, detected)
	}
	hitCount, _, detected = runtime.registerBotReplyLoopDecision(event("first"), candidate, counted, t0.Add(time.Minute))
	if hitCount != 1 || detected {
		t.Fatalf("duplicate hit count=%d detected=%v", hitCount, detected)
	}
	hitCount, _, detected = runtime.registerBotReplyLoopDecision(event("low"), candidate, belowThreshold, t0.Add(2*time.Minute))
	if hitCount != 1 || detected {
		t.Fatalf("low-confidence hit count=%d detected=%v", hitCount, detected)
	}
	for index, at := range []time.Time{t0.Add(15 * time.Minute), t0.Add(botReplyLoopWindow)} {
		hitCount, _, detected = runtime.registerBotReplyLoopDecision(event(fmt.Sprintf("counted-%d", index)), candidate, counted, at)
	}
	if hitCount != botReplyLoopThreshold || !detected {
		t.Fatalf("exact-window threshold count=%d detected=%v", hitCount, detected)
	}

	runtime = NewRuntime(BotConfig{}, nilChannel{}, NewPluginManager(), nil, nil, nil, nil)
	for index, at := range []time.Time{t0, t0.Add(15 * time.Minute), t0.Add(botReplyLoopWindow + time.Nanosecond)} {
		hitCount, _, detected = runtime.registerBotReplyLoopDecision(event(fmt.Sprintf("expired-%d", index)), candidate, counted, at)
	}
	if hitCount != botReplyLoopThreshold-1 || detected {
		t.Fatalf("expired-window count=%d detected=%v", hitCount, detected)
	}
}

func TestReplySuppressionExpiresAtThirtyMinuteBoundary(t *testing.T) {
	runtime := NewRuntime(BotConfig{OwnerID: "owner", BotQQ: "42"}, nilChannel{}, NewPluginManager(), nil, nil, nil, nil)
	event := MessageEvent{Kind: EventKindGroup, GroupID: "group", UserID: "user", MessageID: "message"}
	t0 := time.Date(2026, time.July, 18, 9, 0, 0, 0, time.UTC)
	item, activated := runtime.activateReplySuppression(event, "test", t0)
	if !activated || !item.Until.Equal(t0.Add(30*time.Minute)) {
		t.Fatalf("item=%#v activated=%v", item, activated)
	}
	if _, active := runtime.activeReplySuppression(event, item.Until.Add(-time.Nanosecond)); !active {
		t.Fatal("suppression expired before the 30-minute boundary")
	}
	if _, active := runtime.activeReplySuppression(event, item.Until); active {
		t.Fatal("suppression remained active at the 30-minute boundary")
	}
}

func TestReplySuppressionNoticeUsesMainModelWithoutMentions(t *testing.T) {
	provider := &capturingLLMProvider{reply: `为避免机器人互相循环，已暂停响应此账号约 30 分钟。`}
	store := &stubLLMProfileStore{set: llm.ProfileSet{
		ActiveID: "main",
		Profiles: []llm.Profile{
			{ID: "main", Name: "主聊天", Group: "chat", Config: llm.ProviderConfig{Provider: llm.ProviderOpenAICompatible, APIKey: "main-key", Model: "main-model"}},
			{ID: "routing", Name: "快速语义判定", Group: "routing", Config: llm.ProviderConfig{Provider: llm.ProviderOpenAICompatible, APIKey: "routing-key", Model: "routing-model"}},
		},
	}}
	runtime := NewRuntime(BotConfig{}, nilChannel{}, NewPluginManager(), store, nil, nil, nil)
	var usedModel string
	runtime.SetLLMProviderConfigFactory(func(cfg llm.ProviderConfig) (LLMProvider, error) {
		usedModel = cfg.Model
		return provider, nil
	})
	notice, err := runtime.generateReplySuppressionActivationNotice(context.Background(), ReplySuppression{Until: time.Now().Add(30 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if usedModel != "main-model" {
		t.Fatalf("used model = %q", usedModel)
	}
	if strings.Contains(notice, "@") || strings.Contains(notice, "CQ:") || strings.Contains(notice, "20002") {
		t.Fatalf("notice leaked mention metadata: %q", notice)
	}
	if !strings.Contains(notice, "暂停响应此账号") {
		t.Fatalf("notice = %q", notice)
	}
	if got := sanitizeReplySuppressionNotice(`@某人 [CQ:at,qq=20002] 暂停响应此账号`); got != "" {
		t.Fatalf("unsafe notice was not rejected: %q", got)
	}
}

func TestBotReplyLoopNeverClassifiesOwner(t *testing.T) {
	provider := &sequenceLLMProvider{}
	for i := 0; i < botReplyLoopThreshold+1; i++ {
		provider.replies = append(provider.replies, `{"should_reply":false,"confidence":0.99,"category":"none","directed_at_bot":true,"answerable":false,"reason":"没有需要继续回答的内容"}`)
	}
	runtime := NewRuntime(BotConfig{OwnerID: "10001", BotQQ: "42"}, nilChannel{}, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	start := time.Now().Add(-time.Minute).Truncate(time.Second)
	for i := 0; i < botReplyLoopThreshold+1; i++ {
		handled, outcome := prepareBotReplyLoopRound(t, runtime, "owner", "10001", i, start.Add(time.Duration(i)*time.Minute), time.Minute, "主人正常回复")
		if handled || outcome != "ignored" {
			t.Fatalf("owner round %d handled=%v outcome=%q, want semantic silence", i+1, handled, outcome)
		}
	}
	if len(provider.requests) != botReplyLoopThreshold+1 {
		t.Fatalf("owner route requests=%d, want %d", len(provider.requests), botReplyLoopThreshold+1)
	}
	for _, request := range provider.requests {
		if requestMessagesContain(request.Messages, "反机器人循环分类器") {
			t.Fatal("owner unexpectedly entered AI classifier")
		}
	}
}

func TestParseBotReplyLoopAIDecision(t *testing.T) {
	decision, ok := parseBotReplyLoopAIDecision("```json\n{\"automated_ai_reply\":true,\"confidence\":0.95,\"reason\":\"模板化自动应答\"}\n```")
	if !ok || !decision.counts() || decision.Reason == "" {
		t.Fatalf("decision=%#v ok=%v", decision, ok)
	}
	decision, ok = parseBotReplyLoopAIDecision(`{"automated_ai_reply":true,"confidence":0.89,"reason":"证据不足"}`)
	if !ok || decision.counts() {
		t.Fatalf("low-confidence decision=%#v ok=%v", decision, ok)
	}
}

func prepareBotReplyLoopRound(t *testing.T, runtime *Runtime, prefix, userID string, index int, botAt time.Time, replyDelay time.Duration, text string) (bool, string) {
	t.Helper()
	botMessageID := fmt.Sprintf("%s-bot-%d", prefix, index)
	runtime.remember(MessageEvent{
		Kind: EventKindGroup, GroupID: "123456", UserID: "42", SelfID: "42",
		MessageID: botMessageID, Time: botAt.Unix(), RawMessage: "Diana reply",
		Segments: []MessageSegment{{Type: "text", Data: map[string]string{"text": "Diana reply"}}},
	})
	event := MessageEvent{
		Kind: EventKindGroup, GroupID: "123456", UserID: userID, SelfID: "42",
		MessageID: fmt.Sprintf("%s-user-%d", prefix, index), Time: botAt.Add(replyDelay).Unix(),
		ToMe: true, RawMessage: "[CQ:reply,id=" + botMessageID + "] " + text,
		Segments: []MessageSegment{
			{Type: "reply", Data: map[string]string{"id": botMessageID}},
			{Type: "text", Data: map[string]string{"text": " " + text}},
		},
		Quoted: &QuotedMessage{
			MessageID: botMessageID, UserID: "42", GroupID: "123456", RawMessage: "Diana reply",
			Segments: []MessageSegment{{Type: "text", Data: map[string]string{"text": "Diana reply"}}},
		},
	}
	_, _, handled, outcome := runtime.prepareMessageEvent(context.Background(), event)
	return handled, outcome
}

type memoryReplySuppressionStore struct {
	items []ReplySuppression
}

func (s *memoryReplySuppressionStore) LoadReplySuppressions(context.Context) ([]ReplySuppression, bool, error) {
	return append([]ReplySuppression(nil), s.items...), len(s.items) > 0, nil
}

func (s *memoryReplySuppressionStore) SaveReplySuppressions(_ context.Context, items []ReplySuppression) error {
	s.items = append([]ReplySuppression(nil), items...)
	return nil
}

type generationTimeSuppressionProvider struct {
	runtime *Runtime
	event   MessageEvent
	calls   int
}

func (p *generationTimeSuppressionProvider) Generate(_ context.Context, req llm.GenerateRequest) (*llm.GenerateResponse, error) {
	p.calls++
	if requestMessagesContain(req.Messages, "功能路由器") {
		return &llm.GenerateResponse{Provider: llm.ProviderOpenAICompatible, Model: "test", Text: `{"action":"none","prompt":""}`}, nil
	}
	p.runtime.activateReplySuppression(p.event, "threshold reached during generation", time.Now())
	return &llm.GenerateResponse{Provider: llm.ProviderOpenAICompatible, Model: "test", Text: "这条回复不应发送"}, nil
}
