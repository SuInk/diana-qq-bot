package qqbot

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

type recallPersistenceStore struct {
	events map[string][]MessageEvent
}

func newRecallPersistenceStore() *recallPersistenceStore {
	return &recallPersistenceStore{events: map[string][]MessageEvent{}}
}

func (s *recallPersistenceStore) AppendMessageEvent(_ context.Context, session string, event MessageEvent) error {
	s.events[session] = append(s.events[session], event)
	return nil
}

func (s *recallPersistenceStore) ListRecentMessageEvents(_ context.Context, session string, limit int) ([]MessageEvent, error) {
	events := append([]MessageEvent(nil), s.events[session]...)
	if limit > 0 && len(events) > limit {
		events = events[len(events)-limit:]
	}
	return events, nil
}

func (s *recallPersistenceStore) FindMessageEvent(_ context.Context, session string, messageID string) (MessageEvent, bool, error) {
	events := s.events[session]
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Kind != EventKindNotice && events[i].MessageID == messageID {
			return events[i], true, nil
		}
	}
	return MessageEvent{}, false, nil
}

func (s *recallPersistenceStore) ListGroupRecallEvents(_ context.Context, groupID string) ([]MessageEvent, error) {
	var out []MessageEvent
	for _, event := range s.events["group:"+groupID] {
		if isRecallNotice(event) && event.GroupID == groupID {
			out = append(out, event)
		}
	}
	return out, nil
}

func TestRuntimePersistsRecallIndependentlyAndPluginRestoresIt(t *testing.T) {
	store := newRecallPersistenceStore()
	runtime := NewRuntime(BotConfig{DisabledGroups: []string{"123"}}, nilChannel{}, NewPluginManager(), nil, nil, nil, nil)
	runtime.SetMessageHistoryStore(store)

	original := MessageEvent{
		Kind:       EventKindGroup,
		Time:       100,
		GroupID:    "123",
		UserID:     "20002",
		MessageID:  "old-1",
		RawMessage: "重启后仍要看到的撤回内容",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "重启后仍要看到的撤回内容"}}},
		SenderName: "Alice",
	}
	if err := runtime.HandleEvent(context.Background(), original); err != nil {
		t.Fatal(err)
	}
	if err := runtime.HandleEvent(context.Background(), MessageEvent{
		Kind:      EventKindNotice,
		SubType:   "group_recall",
		Time:      101,
		GroupID:   "123",
		UserID:    "20002",
		MessageID: "old-1",
	}); err != nil {
		t.Fatal(err)
	}

	events := store.events["group:123"]
	if len(events) != 2 {
		t.Fatalf("persisted events = %#v", events)
	}
	recall := events[1]
	if !isRecallNotice(recall) || recall.RawMessage != original.RawMessage || recall.SenderName != "Alice" {
		t.Fatalf("persisted recall = %#v", recall)
	}

	restored, err := store.ListGroupRecallEvents(context.Background(), "123")
	if err != nil {
		t.Fatal(err)
	}
	plugin := NewMessageHistoryPlugin()
	resp, err := plugin.Handle(context.Background(), PluginRequest{
		Event:        MessageEvent{Kind: EventKindGroup, GroupID: "123"},
		RecallEvents: restored,
		Text:         "撤回了什么",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil || !strings.Contains(resp.Context, original.RawMessage) || !strings.Contains(resp.Context, "Alice") {
		t.Fatalf("restored response = %#v", resp)
	}
}

func TestRuntimeRecoversMissingRecallContentFromNapCat(t *testing.T) {
	store := newRecallPersistenceStore()
	channel := &recallGetMsgChannel{}
	runtime := NewRuntime(BotConfig{DisabledGroups: []string{"123"}}, channel, NewPluginManager(), nil, nil, nil, nil)
	runtime.SetMessageHistoryStore(store)

	if err := runtime.HandleEvent(context.Background(), MessageEvent{
		Kind:       EventKindNotice,
		SubType:    "group_recall",
		Time:       200,
		SelfID:     "bot",
		GroupID:    "123",
		UserID:     "20002",
		OperatorID: "admin",
		MessageID:  "remote-1",
	}); err != nil {
		t.Fatal(err)
	}

	events := store.events["group:123"]
	if channel.calls != 1 || len(events) != 2 {
		t.Fatalf("calls=%d events=%#v", channel.calls, events)
	}
	if events[0].Kind != EventKindGroup || events[0].RawMessage != "NapCat补回的正文" {
		t.Fatalf("recovered original = %#v", events[0])
	}
	recall := events[1]
	if recall.Kind != EventKindNotice || recall.RawMessage != "NapCat补回的正文" || recall.OriginalTime != 150 || recall.Time != 200 || recall.OperatorID != "admin" {
		t.Fatalf("recovered recall = %#v", recall)
	}
}

func TestRuntimeDoesNotPersistBotOwnRecall(t *testing.T) {
	store := newRecallPersistenceStore()
	runtime := NewRuntime(BotConfig{BotQQ: "bot"}, nilChannel{}, NewPluginManager(), nil, nil, nil, nil)
	runtime.SetMessageHistoryStore(store)
	if err := runtime.HandleEvent(context.Background(), MessageEvent{
		Kind:       EventKindNotice,
		SubType:    "group_recall",
		Time:       200,
		SelfID:     "bot",
		GroupID:    "123",
		UserID:     "bot",
		OperatorID: "bot",
		MessageID:  "bot-message",
	}); err != nil {
		t.Fatal(err)
	}
	if events := store.events["group:123"]; len(events) != 0 {
		t.Fatalf("bot's own recall should not be recorded: %#v", events)
	}
}

func TestRuntimePersistsBotMessageRecalledByAdministrator(t *testing.T) {
	store := newRecallPersistenceStore()
	store.events["group:123"] = []MessageEvent{{
		Kind:       EventKindGroup,
		Time:       150,
		SelfID:     "bot",
		GroupID:    "123",
		UserID:     "bot",
		MessageID:  "bot-message",
		RawMessage: "机器人原消息",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "机器人原消息"}}},
		SenderName: "Diana",
	}}
	runtime := NewRuntime(BotConfig{BotQQ: "bot"}, nilChannel{}, NewPluginManager(), nil, nil, nil, nil)
	runtime.SetMessageHistoryStore(store)
	if err := runtime.HandleEvent(context.Background(), MessageEvent{
		Kind:       EventKindNotice,
		SubType:    "group_recall",
		Time:       200,
		SelfID:     "bot",
		GroupID:    "123",
		UserID:     "bot",
		OperatorID: "admin",
		MessageID:  "bot-message",
	}); err != nil {
		t.Fatal(err)
	}
	events := store.events["group:123"]
	if len(events) != 2 || !isRecallNotice(events[1]) || events[1].OperatorID != "admin" {
		t.Fatalf("administrator recall was not recorded: %#v", events)
	}
}

func TestRuntimePersistsSentMessageWithOneBotIDForAdministratorRecall(t *testing.T) {
	store := newRecallPersistenceStore()
	channel := newRecallDeleteChannel()
	runtime := NewRuntime(BotConfig{BotQQ: "42", Name: "Diana"}, channel, NewPluginManager(), nil, nil, nil, nil)
	runtime.SetMessageHistoryStore(store)
	source := MessageEvent{
		Kind:      EventKindGroup,
		Time:      100,
		SelfID:    "42",
		GroupID:   "123",
		UserID:    "456",
		MessageID: "source-1",
	}

	messageIDs, err := runtime.sendWithMessageIDs(context.Background(), source, "机器人发出的原消息")
	if err != nil {
		t.Fatal(err)
	}
	if len(messageIDs) != 1 || messageIDs[0] != "101" {
		t.Fatalf("sent message ids = %#v", messageIDs)
	}
	events := store.events["group:123"]
	if len(events) != 1 || events[0].MessageID != "101" || events[0].RawMessage != "机器人发出的原消息" {
		t.Fatalf("persisted outgoing event = %#v", events)
	}

	if err := runtime.HandleEvent(context.Background(), MessageEvent{
		Kind:       EventKindNotice,
		SubType:    "group_recall",
		Time:       200,
		SelfID:     "42",
		GroupID:    "123",
		UserID:     "42",
		OperatorID: "admin",
		MessageID:  "101",
	}); err != nil {
		t.Fatal(err)
	}
	events = store.events["group:123"]
	if len(events) != 2 {
		t.Fatalf("persisted events = %#v", events)
	}
	recall := events[1]
	if !isRecallNotice(recall) || recall.OperatorID != "admin" || recall.RawMessage != "机器人发出的原消息" {
		t.Fatalf("administrator recall = %#v", recall)
	}
}

type recallGetMsgChannel struct {
	nilChannel
	calls int
}

func (c *recallGetMsgChannel) CallAPI(_ context.Context, action string, params map[string]any) (map[string]any, error) {
	c.calls++
	if action != "get_msg" || stringFromAny(params["message_id"]) != "remote-1" {
		return nil, fmt.Errorf("unexpected call %s %#v", action, params)
	}
	return map[string]any{
		"time":         int64(150),
		"message_type": "group",
		"group_id":     "123",
		"user_id":      "20002",
		"message_id":   "remote-1",
		"raw_message":  "NapCat补回的正文",
		"message": []any{
			map[string]any{"type": "text", "data": map[string]any{"text": "NapCat补回的正文"}},
		},
		"sender": map[string]any{"user_id": "20002", "card": "Alice"},
	}, nil
}
