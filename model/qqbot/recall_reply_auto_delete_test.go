package qqbot

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRecallReplyAutoDeleteConfigDefaultsAndCanBeDisabled(t *testing.T) {
	defaults := (BotConfig{}).WithDefaults()
	if defaults.RecallReplyMode != RecallReplyModeLLMSummary {
		t.Fatalf("default recall reply mode = %q", defaults.RecallReplyMode)
	}
	if defaults.RecallReplyAutoDeleteEnabled == nil || !*defaults.RecallReplyAutoDeleteEnabled {
		t.Fatalf("default config = %#v", defaults.RecallReplyAutoDeleteEnabled)
	}

	disabled := false
	payload := PayloadFromConfig(BotConfig{
		RecallReplyMode:              RecallReplyModeOriginalForward,
		RecallReplyAutoDeleteEnabled: &disabled,
	})
	got := ConfigFromPayload(payload, BotConfig{})
	if got.RecallReplyMode != RecallReplyModeOriginalForward {
		t.Fatalf("recall reply mode did not round-trip: %q", got.RecallReplyMode)
	}
	if got.RecallReplyAutoDeleteEnabled == nil || *got.RecallReplyAutoDeleteEnabled {
		t.Fatalf("disabled config did not round-trip: %#v", got.RecallReplyAutoDeleteEnabled)
	}
}

func TestMessageHistoryPluginMarksOnlyRecallQueriesForAutoDelete(t *testing.T) {
	plugin := NewMessageHistoryPlugin()
	plugin.Observe(context.Background(), MessageEvent{
		Kind:       EventKindGroup,
		GroupID:    "123",
		UserID:     "20002",
		MessageID:  "old-1",
		RawMessage: "撤回前的内容",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "撤回前的内容"}}},
	})
	plugin.Observe(context.Background(), MessageEvent{
		Kind:      EventKindNotice,
		SubType:   "group_recall",
		GroupID:   "123",
		UserID:    "20002",
		MessageID: "old-1",
	})

	query, err := plugin.Handle(context.Background(), PluginRequest{Event: MessageEvent{Kind: EventKindGroup, GroupID: "123"}, Text: "查看刚才撤回的消息"})
	if err != nil {
		t.Fatal(err)
	}
	normal, err := plugin.Handle(context.Background(), PluginRequest{Event: MessageEvent{Kind: EventKindGroup, GroupID: "123"}, Text: "今天吃什么"})
	if err != nil {
		t.Fatal(err)
	}
	if query == nil || !query.RecallDisclosure {
		t.Fatalf("recall query response = %#v", query)
	}
	if normal != nil {
		t.Fatalf("normal response = %#v", normal)
	}
	if !recallReplyShouldAutoDelete(BotConfig{}, []PluginResponse{*query}) {
		t.Fatal("enabled recall disclosure should auto-delete")
	}
	disabled := false
	if recallReplyShouldAutoDelete(BotConfig{RecallReplyAutoDeleteEnabled: &disabled}, []PluginResponse{*query}) {
		t.Fatal("disabled recall disclosure should not auto-delete")
	}
}

func TestRuntimeCollectsSentMessageIDAndDeletesItAfterDelay(t *testing.T) {
	channel := newRecallDeleteChannel()
	runtime := NewRuntime(BotConfig{DirectReplyChunkSize: 900, ForwardReplyThreshold: 900}, channel, NewPluginManager(), nil, nil, nil, nil)
	event := MessageEvent{Kind: EventKindGroup, GroupID: "123", UserID: "456", MessageID: "source-1"}

	messageIDs, err := runtime.sendWithMessageIDs(context.Background(), event, "撤回记录内容")
	if err != nil {
		t.Fatal(err)
	}
	if len(messageIDs) != 1 || messageIDs[0] != "101" {
		t.Fatalf("message ids = %#v", messageIDs)
	}
	history := runtime.contextHistory(event)
	if len(history) != 1 || history[0].MessageID != "101" || history[0].RawMessage != "撤回记录内容" {
		t.Fatalf("outgoing history = %#v", history)
	}
	runtime.scheduleMessageDeletes(event, messageIDs, 5*time.Millisecond)

	select {
	case deleted := <-channel.deleted:
		if deleted != int64(101) {
			t.Fatalf("deleted message id = %#v", deleted)
		}
	case <-time.After(time.Second):
		t.Fatal("delete_msg was not called")
	}
}

func TestRuntimeCollectsForwardMessageID(t *testing.T) {
	channel := newRecallDeleteChannel()
	runtime := NewRuntime(BotConfig{BotQQ: "42", DirectReplyChunkSize: 10, ForwardReplyThreshold: 5}, channel, NewPluginManager(), nil, nil, nil, nil)
	event := MessageEvent{Kind: EventKindGroup, GroupID: "123", UserID: "456", MessageID: "source-1", SelfID: "42"}

	messageIDs, err := runtime.sendWithMessageIDs(context.Background(), event, "这是一条超过合并转发阈值的撤回记录")
	if err != nil {
		t.Fatal(err)
	}
	if len(messageIDs) != 1 || messageIDs[0] != "901" {
		t.Fatalf("forward message ids = %#v", messageIDs)
	}
	history := runtime.contextHistory(event)
	if len(history) != 1 || history[0].MessageID != "901" || !strings.Contains(strings.ReplaceAll(history[0].RawMessage, "\n", ""), "超过合并转发阈值") {
		t.Fatalf("forward outgoing history = %#v", history)
	}
}

type recallDeleteChannel struct {
	mu      sync.Mutex
	nextID  int64
	deleted chan any
}

func newRecallDeleteChannel() *recallDeleteChannel {
	return &recallDeleteChannel{nextID: 100, deleted: make(chan any, 4)}
}

func (c *recallDeleteChannel) Connect(context.Context, EventHandler) error { return nil }
func (c *recallDeleteChannel) Send(ctx context.Context, msg OutgoingMessage) error {
	_, err := c.SendWithResult(ctx, msg)
	return err
}
func (c *recallDeleteChannel) SendWithResult(context.Context, OutgoingMessage) (map[string]any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	return map[string]any{"message_id": c.nextID}, nil
}
func (c *recallDeleteChannel) CallAPI(_ context.Context, action string, params map[string]any) (map[string]any, error) {
	if action == "delete_msg" {
		c.deleted <- params["message_id"]
		return map[string]any{}, nil
	}
	if action == "send_group_forward_msg" || action == "send_private_forward_msg" {
		return map[string]any{"message_id": int64(901)}, nil
	}
	return map[string]any{}, nil
}
func (c *recallDeleteChannel) Status() ChannelStatus {
	return ChannelStatus{Connected: true, SelfID: "42"}
}
func (c *recallDeleteChannel) Close() error { return nil }
