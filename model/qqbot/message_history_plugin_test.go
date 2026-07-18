package qqbot

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestMessageHistoryPluginAddsEveryCachedRecallToContext(t *testing.T) {
	plugin := NewMessageHistoryPlugin()
	for i := 1; i <= 5; i++ {
		messageID := fmt.Sprintf("old-%d", i)
		content := fmt.Sprintf("撤回内容-%d", i)
		plugin.Observe(context.Background(), MessageEvent{
			Kind:       EventKindGroup,
			GroupID:    "123",
			UserID:     "20002",
			MessageID:  messageID,
			RawMessage: content,
			Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": content}}},
			SenderName: "Alice",
		})
		plugin.Observe(context.Background(), messageEventFromEnvelope(oneBotEnvelope{
			PostType:   "notice",
			NoticeType: "group_recall",
			GroupID:    "123",
			UserID:     "20002",
			MessageID:  messageID,
		}))
	}

	resp, err := plugin.Handle(context.Background(), PluginRequest{
		Event: MessageEvent{Kind: EventKindGroup, GroupID: "123"},
		Text:  "查看所有撤回内容",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil {
		t.Fatal("expected recall context")
	}
	for i := 1; i <= 5; i++ {
		content := fmt.Sprintf("撤回内容-%d", i)
		if !strings.Contains(resp.Context, content) {
			t.Fatalf("context missing %q: %q", content, resp.Context)
		}
	}
}

func TestMessageHistoryPluginUsesRecent24HoursOldestFirst(t *testing.T) {
	referenceTime := int64(1_800_000_000)
	plugin := NewMessageHistoryPlugin()
	resp, err := plugin.Handle(context.Background(), PluginRequest{
		Event: MessageEvent{Kind: EventKindGroup, GroupID: "123", Time: referenceTime},
		Text:  "查看撤回记录",
		RecallEvents: []MessageEvent{
			{Kind: EventKindNotice, SubType: "group_recall", GroupID: "123", MessageID: "old", Time: referenceTime - int64(25*time.Hour/time.Second), RawMessage: "超过24小时"},
			{Kind: EventKindNotice, SubType: "group_recall", GroupID: "123", MessageID: "newer", Time: referenceTime - 60, OriginalTime: referenceTime - 120, RawMessage: "较新内容", UserID: "user-1", OperatorID: "user-1"},
			{Kind: EventKindNotice, SubType: "group_recall", GroupID: "123", MessageID: "older", Time: referenceTime - 120, OriginalTime: referenceTime - 180, RawMessage: "较旧内容", UserID: "user-2", OperatorID: "admin"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil {
		t.Fatal("expected recall context")
	}
	if strings.Contains(resp.Context, "超过24小时") {
		t.Fatalf("context includes expired recall: %s", resp.Context)
	}
	newerIndex := strings.Index(resp.Context, "|newer|")
	olderIndex := strings.Index(resp.Context, "|older|")
	if newerIndex < 0 || olderIndex < 0 || olderIndex >= newerIndex {
		t.Fatalf("recalls are not oldest first: %s", resp.Context)
	}
	for _, want := range []string{"最近24小时群撤回消息时间线", "记录总数=2", "字段顺序=序号|撤回时间|原消息发送时间|原消息ID|原消息发送者|被@对象|执行撤回者|执行者身份|结论", "|newer|", "|older|", "原消息完整内容=较新内容", "直接生成最终QQ回复"} {
		if !strings.Contains(resp.Context, want) {
			t.Fatalf("context missing %q: %s", want, resp.Context)
		}
	}
	if !resp.Forward || !resp.NestedForward || len(resp.ForwardMessages) != 2 {
		t.Fatalf("nested recall forwarding is not configured: %#v", resp)
	}
	if resp.ForwardMessages[0].ForwardUIN != "user-2" || resp.ForwardMessages[1].ForwardUIN != "user-1" {
		t.Fatalf("forged nodes are not oldest first: %#v", resp.ForwardMessages)
	}
}

func TestRecallReplyModeDefaultsToLLMSummary(t *testing.T) {
	responses := applyRecallReplyMode([]PluginResponse{{
		Handled:          true,
		Context:          "完整撤回时间线",
		Forward:          true,
		NestedForward:    true,
		ForwardMessages:  []OutgoingMessage{{Text: "原消息"}},
		RecallDisclosure: true,
	}}, "")
	if len(responses) != 1 {
		t.Fatalf("responses = %#v", responses)
	}
	got := responses[0]
	if got.Context != "完整撤回时间线" || got.Reply != "" || got.Forward || got.NestedForward || len(got.ForwardMessages) != 0 {
		t.Fatalf("default recall response still forwards originals: %#v", got)
	}
}

func TestRecallReplyModeRoutesNoRecordsThroughLLM(t *testing.T) {
	responses := applyRecallReplyMode([]PluginResponse{{
		Handled:          true,
		Reply:            "最近24小时没有记录到群消息撤回。",
		RecallDisclosure: true,
	}}, RecallReplyModeLLMSummary)
	got := responses[0]
	if got.Reply != "" || got.Context != "最近24小时没有记录到群消息撤回。" {
		t.Fatalf("no-record response bypassed LLM: %#v", got)
	}
}

func TestRecallReplyModeCanKeepOriginalForward(t *testing.T) {
	want := []PluginResponse{{
		Handled:          true,
		Context:          "完整撤回时间线",
		Forward:          true,
		NestedForward:    true,
		ForwardMessages:  []OutgoingMessage{{Text: "原消息"}},
		RecallDisclosure: true,
	}}
	got := applyRecallReplyMode(want, RecallReplyModeOriginalForward)
	if !got[0].Forward || !got[0].NestedForward || len(got[0].ForwardMessages) != 1 {
		t.Fatalf("original-forward mode changed response: %#v", got[0])
	}
}

func TestMessageHistoryPluginKeepsRecallOperator(t *testing.T) {
	plugin := NewMessageHistoryPlugin()
	plugin.Observe(context.Background(), MessageEvent{
		Kind:       EventKindGroup,
		SelfID:     "bot",
		GroupID:    "123",
		UserID:     "member",
		MessageID:  "old-member",
		RawMessage: "成员测试消息",
	})
	plugin.Observe(context.Background(), messageEventFromEnvelope(oneBotEnvelope{
		PostType:   "notice",
		NoticeType: "group_recall",
		SelfID:     "bot",
		GroupID:    "123",
		UserID:     "member",
		OperatorID: "bot",
		MessageID:  "old-member",
	}))
	plugin.Observe(context.Background(), MessageEvent{
		Kind:       EventKindGroup,
		SelfID:     "bot",
		GroupID:    "123",
		UserID:     "bot",
		MessageID:  "old-bot",
		RawMessage: "机器人测试消息",
	})
	plugin.Observe(context.Background(), messageEventFromEnvelope(oneBotEnvelope{
		PostType:   "notice",
		NoticeType: "group_recall",
		SelfID:     "bot",
		GroupID:    "123",
		UserID:     "bot",
		OperatorID: "bot",
		MessageID:  "old-bot",
	}))

	resp, err := plugin.Handle(context.Background(), PluginRequest{Event: MessageEvent{Kind: EventKindGroup, GroupID: "123"}, Text: "查看撤回记录"})
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil || !strings.Contains(resp.Context, "机器人以管理员身份撤回，绝不是发送者自行撤回") || !strings.Contains(resp.Context, "机器人自行撤回") {
		t.Fatalf("resp = %#v", resp)
	}
}

func TestMessageHistoryPluginNamesAdministratorSeparatelyFromOriginalSender(t *testing.T) {
	plugin := NewMessageHistoryPlugin()
	plugin.Observe(context.Background(), MessageEvent{
		Kind:       EventKindGroup,
		SelfID:     "10000",
		GroupID:    "20001",
		UserID:     "10003",
		MessageID:  "30005",
		RawMessage: "[表情:1]",
		SenderName: "Carol",
	})
	plugin.Observe(context.Background(), messageEventFromEnvelope(oneBotEnvelope{
		PostType:   "notice",
		NoticeType: "group_recall",
		SelfID:     "10000",
		GroupID:    "20001",
		UserID:     "10003",
		OperatorID: "10001",
		MessageID:  "30005",
	}))
	channel := &recallIdentityChannel{}
	req := PluginRequest{
		Event: MessageEvent{
			Kind:       EventKindGroup,
			GroupID:    "20001",
			UserID:     "10001",
			SenderName: "TestOwner",
		},
		Channel: channel,
	}

	req.Text = "查看撤回记录"
	resp, err := plugin.Handle(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil {
		t.Fatal("expected recall context")
	}
	for _, want := range []string{
		"Carol(10003)",
		"TestOwner(10001)",
		"群主",
		"管理员撤回，绝不是发送者自行撤回",
	} {
		if !strings.Contains(resp.Context, want) {
			t.Fatalf("context missing %q: %s", want, resp.Context)
		}
	}
	if channel.calls != 1 {
		t.Fatalf("OneBot calls = %d, want 1", channel.calls)
	}
	if _, err := plugin.Handle(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if channel.calls != 1 {
		t.Fatalf("cached OneBot calls = %d, want 1", channel.calls)
	}
}

func TestMessageHistoryPluginReusesRecentParticipantNamesForRecalledMentions(t *testing.T) {
	plugin := NewMessageHistoryPlugin()
	recentParticipant := MessageEvent{
		Kind:       EventKindGroup,
		SelfID:     "10000",
		GroupID:    "20001",
		UserID:     "10002",
		MessageID:  "participant",
		SenderName: "Alice",
		RawMessage: "在场",
	}
	plugin.Observe(context.Background(), MessageEvent{
		Kind:       EventKindGroup,
		SelfID:     "10000",
		GroupID:    "20001",
		UserID:     "10004",
		MessageID:  "recalled",
		SenderName: "Carol",
		RawMessage: "[CQ:at,qq=10002] [CQ:at,qq=10000] 测试",
		Segments: []MessageSegment{
			{Type: "at", Data: map[string]string{"qq": "10002"}},
			{Type: "text", Data: map[string]string{"text": " "}},
			{Type: "at", Data: map[string]string{"qq": "10000"}},
			{Type: "text", Data: map[string]string{"text": " 测试"}},
		},
	})
	plugin.Observe(context.Background(), messageEventFromEnvelope(oneBotEnvelope{
		PostType:   "notice",
		NoticeType: "group_recall",
		SelfID:     "10000",
		GroupID:    "20001",
		UserID:     "10004",
		OperatorID: "10004",
		MessageID:  "recalled",
	}))

	resp, err := plugin.Handle(context.Background(), PluginRequest{
		Event:        MessageEvent{Kind: EventKindGroup, SelfID: "10000", GroupID: "20001"},
		RecentEvents: []MessageEvent{recentParticipant},
		Text:         "查看撤回记录",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil {
		t.Fatal("expected recall context")
	}
	if want := "|Alice(10002)、机器人(10000)|"; !strings.Contains(resp.Context, want) {
		t.Fatalf("context missing %q: %s", want, resp.Context)
	}
}

func TestMessageHistoryPluginLooksUpAndCachesRecalledMentionIdentity(t *testing.T) {
	plugin := NewMessageHistoryPlugin()
	channel := &recallMentionIdentityChannel{}
	recall := MessageEvent{
		Kind:         EventKindNotice,
		SubType:      "group_recall",
		SelfID:       "10000",
		GroupID:      "20001",
		UserID:       "10003",
		SenderName:   "Carol",
		OperatorID:   "10003",
		OperatorName: "Carol",
		OperatorRole: "member",
		MessageID:    "recalled",
		Time:         1_800_000_000,
		Segments:     []MessageSegment{{Type: "at", Data: map[string]string{"qq": "10002"}}},
	}
	req := PluginRequest{
		Event:        MessageEvent{Kind: EventKindGroup, SelfID: "10000", GroupID: "20001", Time: recall.Time},
		Text:         "查看撤回记录",
		RecallEvents: []MessageEvent{recall},
		Channel:      channel,
	}

	for attempt := 0; attempt < 2; attempt++ {
		resp, err := plugin.Handle(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if resp == nil || !strings.Contains(resp.Context, "|Alice(10002)|") {
			t.Fatalf("resp = %#v", resp)
		}
	}
	if channel.calls != 1 {
		t.Fatalf("OneBot calls = %d, want 1", channel.calls)
	}
}

type recallIdentityChannel struct {
	nilChannel
	calls int
}

type recallMentionIdentityChannel struct {
	nilChannel
	calls int
}

func (c *recallMentionIdentityChannel) CallAPI(_ context.Context, action string, params map[string]any) (map[string]any, error) {
	c.calls++
	if action != "get_group_member_info" || stringFromAny(params["group_id"]) != "20001" || stringFromAny(params["user_id"]) != "10002" {
		return nil, fmt.Errorf("unexpected call %q %#v", action, params)
	}
	return map[string]any{
		"group_id": int64(20001),
		"user_id":  int64(10002),
		"card":     "Alice",
		"role":     "member",
	}, nil
}

func (c *recallIdentityChannel) CallAPI(_ context.Context, action string, params map[string]any) (map[string]any, error) {
	c.calls++
	if action != "get_group_member_info" {
		return nil, fmt.Errorf("unexpected action %q", action)
	}
	if stringFromAny(params["group_id"]) != "20001" || stringFromAny(params["user_id"]) != "10001" {
		return nil, fmt.Errorf("unexpected params %#v", params)
	}
	return map[string]any{
		"group_id": int64(20001),
		"user_id":  int64(10001),
		"nickname": "TestOwner",
		"role":     "owner",
	}, nil
}
