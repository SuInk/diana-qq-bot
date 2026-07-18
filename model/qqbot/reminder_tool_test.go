package qqbot

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestDianaReminderToolCreatesListsAndDeletesReminder(t *testing.T) {
	store := &stubReminderStore{}
	runtime := NewRuntime(BotConfig{OwnerID: "10001"}, nilChannel{}, NewPluginManager(), nil, store, nil, nil)
	event := MessageEvent{Kind: EventKindPrivate, UserID: "10001", Time: time.Now().Unix()}
	tool := newDianaReminderTool(runtime, event)

	createdRaw, err := tool.Run(context.Background(), map[string]any{
		"operation": "create",
		"delay":     "1m",
		"message":   "睡觉",
	})
	if err != nil {
		t.Fatal(err)
	}
	var created dianaReminderResult
	if err := json.Unmarshal([]byte(createdRaw), &created); err != nil {
		t.Fatal(err)
	}
	if !created.OK || created.Reminder == nil || created.Reminder.Message != "睡觉" {
		t.Fatalf("created = %#v", created)
	}
	if len(store.items) != 1 || store.items[0].Kind != ReminderKindMessage {
		t.Fatalf("items = %#v", store.items)
	}
	remaining := time.Until(store.items[0].TriggerAt)
	if remaining < 58*time.Second || remaining > 61*time.Second {
		t.Fatalf("trigger remaining = %s", remaining)
	}

	listedRaw, err := tool.Run(context.Background(), map[string]any{"operation": "list"})
	if err != nil || !strings.Contains(listedRaw, store.items[0].ID) {
		t.Fatalf("listed=%q err=%v", listedRaw, err)
	}
	deletedRaw, err := tool.Run(context.Background(), map[string]any{"operation": "delete", "id": store.items[0].ID})
	if err != nil || !strings.Contains(deletedRaw, "deleted") || len(store.items) != 0 {
		t.Fatalf("deleted=%q err=%v items=%#v", deletedRaw, err, store.items)
	}
}

func TestDianaReminderToolCreatesAtMostFivePerCall(t *testing.T) {
	store := &stubReminderStore{}
	runtime := NewRuntime(BotConfig{OwnerID: "10001"}, nilChannel{}, NewPluginManager(), nil, store, nil, nil)
	tool := newDianaReminderTool(runtime, MessageEvent{Kind: EventKindPrivate, UserID: "10001", Time: time.Now().Unix()})
	items := make([]any, 0, maximumTasksPerToolCall)
	for index := 0; index < maximumTasksPerToolCall; index++ {
		items = append(items, map[string]any{"delay": fmt.Sprintf("%dm", index+1), "message": fmt.Sprintf("提醒 %d", index+1)})
	}
	raw, err := tool.Run(context.Background(), map[string]any{"operation": "create", "items": items})
	if err != nil {
		t.Fatal(err)
	}
	var result dianaReminderResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != maximumTasksPerToolCall || result.Reminder != nil || len(store.items) != maximumTasksPerToolCall {
		t.Fatalf("result=%#v stored=%#v", result, store.items)
	}

	tooMany := append(append([]any(nil), items...), map[string]any{"delay": "6m", "message": "第六个"})
	_, err = tool.Run(context.Background(), map[string]any{"operation": "create", "items": tooMany})
	if err == nil || !strings.Contains(err.Error(), "一次最多创建 5 个") || len(store.items) != maximumTasksPerToolCall {
		t.Fatalf("err=%v stored=%#v", err, store.items)
	}
}

func TestDianaReminderBatchUsesRemainingQuota(t *testing.T) {
	store := &stubReminderStore{}
	for index := 0; index < 4; index++ {
		store.items = append(store.items, Reminder{ID: fmt.Sprintf("existing-%d", index), Kind: ReminderKindMessage, OwnerID: "20002", UserID: "20002"})
	}
	runtime := NewRuntime(BotConfig{OwnerID: "10001"}, nilChannel{}, NewPluginManager(), nil, store, nil, nil)
	memory := newMemoryUserMemoryStore()
	memory.profiles["20002"] = UserMemoryProfile{UserID: "20002", Favorability: 60, MessageCount: 30}
	runtime.SetUserMemoryStore(memory)
	tool := newDianaReminderTool(runtime, MessageEvent{Kind: EventKindPrivate, UserID: "20002"})
	raw, err := tool.Run(context.Background(), map[string]any{
		"operation": "create",
		"items": []any{
			map[string]any{"delay": "1m", "message": "A"},
			map[string]any{"delay": "2m", "message": "B"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var result dianaReminderResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].Message != "A" || !strings.Contains(result.Message, "按剩余额度创建了 1 个") || len(store.items) != 5 || store.items[4].Message != "A" {
		t.Fatalf("result=%#v stored=%#v", result, store.items)
	}
}

func TestDianaReminderUsedItemsDoNotConsumeQuota(t *testing.T) {
	store := &stubReminderStore{items: []Reminder{{
		ID: "used", Kind: ReminderKindMessage, OwnerID: "20002", UserID: "20002",
		LastRunAt: time.Now().Add(-time.Minute),
	}}}
	runtime := NewRuntime(BotConfig{OwnerID: "10001"}, nilChannel{}, NewPluginManager(), nil, store, nil, nil)
	memory := newMemoryUserMemoryStore()
	memory.profiles["20002"] = UserMemoryProfile{UserID: "20002", Favorability: 60, MessageCount: 30}
	runtime.SetUserMemoryStore(memory)
	raw, err := newDianaReminderTool(runtime, MessageEvent{UserID: "20002"}).Run(context.Background(), map[string]any{
		"operation": "create",
		"items": []any{
			map[string]any{"delay": "1m", "message": "A"},
			map[string]any{"delay": "2m", "message": "B"},
			map[string]any{"delay": "3m", "message": "C"},
			map[string]any{"delay": "4m", "message": "D"},
			map[string]any{"delay": "5m", "message": "E"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var result dianaReminderResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 5 || len(store.items) != 6 || store.items[0].LastRunAt.IsZero() {
		t.Fatalf("result=%#v stored=%#v", result, store.items)
	}
}

func TestDianaReminderCancelReleasesQuotaAndDeleteRemovesRecord(t *testing.T) {
	store := &stubReminderStore{}
	runtime := NewRuntime(BotConfig{OwnerID: "owner"}, nilChannel{}, NewPluginManager(), nil, store, nil, nil)
	tool := newDianaReminderTool(runtime, MessageEvent{UserID: "user"})
	createdRaw, err := tool.Run(context.Background(), map[string]any{"operation": "create", "delay": "1m", "message": "A"})
	if err != nil {
		t.Fatal(err)
	}
	var created dianaReminderResult
	if err := json.Unmarshal([]byte(createdRaw), &created); err != nil {
		t.Fatal(err)
	}
	id := created.Reminder.ID
	cancelledRaw, err := tool.Run(context.Background(), map[string]any{"operation": "cancel", "id": id})
	if err != nil {
		t.Fatal(err)
	}
	var cancelled dianaReminderResult
	if err := json.Unmarshal([]byte(cancelledRaw), &cancelled); err != nil {
		t.Fatal(err)
	}
	if cancelled.Reminder == nil || cancelled.Reminder.Status != "cancelled" || store.items[0].CancelledAt.IsZero() {
		t.Fatalf("cancelled=%#v stored=%#v", cancelled, store.items)
	}
	if _, err := tool.Run(context.Background(), map[string]any{"operation": "create", "delay": "2m", "message": "B"}); err != nil {
		t.Fatalf("cancelled reminder still consumed quota: %v", err)
	}
	if _, err := tool.Run(context.Background(), map[string]any{"operation": "delete", "id": id}); err != nil {
		t.Fatal(err)
	}
	if len(store.items) != 1 || store.items[0].Message != "B" {
		t.Fatalf("stored=%#v", store.items)
	}
}

func TestCancelledReminderAndScheduleDoNotRun(t *testing.T) {
	now := time.Now()
	store := &stubReminderStore{items: []Reminder{
		{ID: "reminder", Kind: ReminderKindMessage, OwnerID: "user", UserID: "user", Message: "A", TriggerAt: now.Add(-time.Minute), CancelledAt: now.Add(-time.Second)},
		{ID: "schedule", Kind: ReminderKindQuery, OwnerID: "user", UserID: "user", Message: "B", TriggerAt: now.Add(-time.Minute), IntervalSeconds: 3600, CancelledAt: now.Add(-time.Second)},
	}}
	channel := &recordingChannel{}
	runtime := NewRuntime(BotConfig{OwnerID: "owner", AgentEnabled: true}, channel, NewPluginManager(), nil, store, nil, nil)
	runtime.fireDueReminders(context.Background())
	if len(channel.sent) != 0 || !store.items[0].LastRunAt.IsZero() || !store.items[1].LastRunAt.IsZero() {
		t.Fatalf("cancelled tasks ran: sent=%#v stored=%#v", channel.sent, store.items)
	}
}

func TestDelayedInboundReminderFiresImmediatelyFromOriginalMessageTime(t *testing.T) {
	store := &stubReminderStore{}
	channel := &recordingChannel{}
	runtime := NewRuntime(BotConfig{OwnerID: "10001"}, channel, NewPluginManager(), nil, store, nil, nil)
	event := MessageEvent{
		Kind:   EventKindPrivate,
		UserID: "10001",
		Time:   time.Now().Add(-90 * time.Second).Unix(),
	}
	item, err := runtime.addOneTimeReminder(event, time.Minute, "睡觉")
	if err != nil {
		t.Fatal(err)
	}
	if item.TriggerAt.After(time.Now().Add(time.Second)) {
		t.Fatalf("delayed trigger = %s", item.TriggerAt)
	}

	runtime.fireDueReminders(context.Background())
	if len(store.items) != 1 || store.items[0].LastRunAt.IsZero() {
		t.Fatalf("used reminders = %#v", store.items)
	}
	if len(channel.sent) != 1 || channel.sent[0].UserID != "10001" || !strings.Contains(channel.sent[0].Text, "睡觉") {
		t.Fatalf("sent = %#v", channel.sent)
	}
	runtime.fireDueReminders(context.Background())
	if len(channel.sent) != 1 {
		t.Fatalf("used reminder fired again: %#v", channel.sent)
	}
}

func TestRuntimeAgentCanCreateNaturalLanguageReminder(t *testing.T) {
	store := &stubReminderStore{}
	channel := &recordingChannel{}
	provider := &sequenceLLMProvider{replies: []string{
		`{"action":"none","prompt":""}`,
		`{"action":"tool","tool":"diana.reminder","input":{"operation":"create","delay":"1m","message":"睡觉"}}`,
		`{"action":"final","content":"好，一分钟后提醒你睡觉。"}`,
	}}
	runtime := NewRuntime(BotConfig{
		OwnerID:        "10001",
		AgentEnabled:   true,
		AgentWorkDir:   t.TempDir(),
		AgentMaxSteps:  3,
		RequestTimeout: 5 * time.Second,
	}, channel, NewPluginManager(), nil, store, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	event := MessageEvent{
		Kind:      EventKindPrivate,
		UserID:    "10001",
		MessageID: "reminder-1",
		Time:      time.Now().Unix(),
		Segments:  []MessageSegment{{Type: "text", Data: map[string]string{"text": "1min后提醒我睡觉"}}},
	}
	reply, err := runtime.replyTo(context.Background(), event, "1min后提醒我睡觉")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reply, "一分钟后") || len(store.items) != 1 {
		t.Fatalf("reply=%q items=%#v", reply, store.items)
	}
	if len(channel.sent) != 1 || channel.sent[0].UserID != "10001" {
		t.Fatalf("sent = %#v", channel.sent)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("requests = %d", len(provider.requests))
	}
	foundReminderTool := false
	foundNoCommandTimerRule := false
	for _, msg := range provider.requests[1].Messages {
		if strings.Contains(msg.Content, "diana.reminder") {
			foundReminderTool = true
		}
		if strings.Contains(msg.Content, "禁止使用 run_command") {
			foundNoCommandTimerRule = true
		}
	}
	if !foundReminderTool || !foundNoCommandTimerRule {
		t.Fatalf("agent prompt missing reminder guidance: tool=%v rule=%v", foundReminderTool, foundNoCommandTimerRule)
	}
}

func TestScheduleSupportsOneMinutePolling(t *testing.T) {
	interval, err := parseScheduleInterval("1m")
	if err != nil || interval != time.Minute {
		t.Fatalf("interval=%s err=%v", interval, err)
	}
	if _, err := parseScheduleInterval("59s"); err == nil {
		t.Fatal("59s interval should be rejected")
	}
}

func TestNextScheduledTriggerSkipsMissedMinuteSlots(t *testing.T) {
	previous := time.Date(2026, 7, 12, 12, 0, 0, 0, time.Local)
	now := previous.Add(2*time.Minute + 10*time.Second)
	next := nextScheduledTrigger(previous, time.Minute, now)
	want := previous.Add(3 * time.Minute)
	if !next.Equal(want) {
		t.Fatalf("next = %s, want %s", next, want)
	}
}

func TestReminderDispatcherDoesNotBlockBehindSlowTask(t *testing.T) {
	now := time.Now()
	store := &stubReminderStore{items: []Reminder{
		{ID: "slow", Kind: ReminderKindMessage, OwnerID: "10001", UserID: "10001", Message: "slow", TriggerAt: now.Add(-time.Second)},
		{ID: "fast", Kind: ReminderKindMessage, OwnerID: "10001", UserID: "10001", Message: "fast", TriggerAt: now.Add(-time.Second)},
	}}
	channel := &blockingReminderChannel{
		slowStarted: make(chan struct{}, 2),
		releaseSlow: make(chan struct{}),
		fastSent:    make(chan struct{}, 1),
	}
	runtime := NewRuntime(BotConfig{OwnerID: "10001"}, channel, NewPluginManager(), nil, store, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runtime.dispatchDueReminders(ctx)
	select {
	case <-channel.slowStarted:
	case <-time.After(time.Second):
		t.Fatal("slow reminder did not start")
	}
	select {
	case <-channel.fastSent:
	case <-time.After(time.Second):
		t.Fatal("fast reminder was blocked behind the slow reminder")
	}

	runtime.dispatchDueReminders(ctx)
	select {
	case <-channel.slowStarted:
		t.Fatal("active reminder was dispatched twice")
	case <-time.After(100 * time.Millisecond):
	}
	close(channel.releaseSlow)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		items := runtime.oneTimeReminders("10001")
		if len(items) == 2 && !items[0].LastRunAt.IsZero() && !items[1].LastRunAt.IsZero() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("remaining reminders = %#v", runtime.oneTimeReminders("10001"))
}

type blockingReminderChannel struct {
	slowStarted chan struct{}
	releaseSlow chan struct{}
	fastSent    chan struct{}
}

func (c *blockingReminderChannel) Connect(context.Context, EventHandler) error { return nil }

func (c *blockingReminderChannel) Send(ctx context.Context, msg OutgoingMessage) error {
	switch {
	case strings.Contains(msg.Text, "slow"):
		select {
		case c.slowStarted <- struct{}{}:
		default:
		}
		select {
		case <-c.releaseSlow:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	case strings.Contains(msg.Text, "fast"):
		select {
		case c.fastSent <- struct{}{}:
		default:
		}
	}
	return nil
}

func (c *blockingReminderChannel) CallAPI(context.Context, string, map[string]any) (map[string]any, error) {
	return map[string]any{}, nil
}

func (c *blockingReminderChannel) Status() ChannelStatus { return ChannelStatus{Connected: true} }
func (c *blockingReminderChannel) Close() error          { return nil }
