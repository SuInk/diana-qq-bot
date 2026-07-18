package qqbot

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDianaTasksToolListsAllKindsForCurrentUser(t *testing.T) {
	now := time.Now()
	store := &stubReminderStore{items: []Reminder{
		{ID: "used", Kind: ReminderKindMessage, OwnerID: "user", UserID: "user", Message: "A", LastRunAt: now, CreatedAt: now.Add(-3 * time.Minute)},
		{ID: "active", Kind: ReminderKindQuery, OwnerID: "user", UserID: "user", Message: "B", IntervalSeconds: 3600, CreatedAt: now.Add(-2 * time.Minute)},
		{ID: "other", Kind: ReminderKindMessage, OwnerID: "other", UserID: "other", Message: "C", CreatedAt: now.Add(-time.Minute)},
	}}
	runtime := NewRuntime(BotConfig{OwnerID: "owner"}, nilChannel{}, NewPluginManager(), nil, store, nil, nil)
	raw, err := newDianaTasksTool(runtime, MessageEvent{UserID: "user"}).Run(context.Background(), map[string]any{"operation": "list"})
	if err != nil {
		t.Fatal(err)
	}
	var result dianaTasksResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if result.Scope != "mine" || len(result.Items) != 2 {
		t.Fatalf("result=%#v", result)
	}
	if result.Items[0].Kind != "reminder" || result.Items[0].Status != "used" || result.Items[0].ConsumesQuota {
		t.Fatalf("used item=%#v", result.Items[0])
	}
	if result.Items[1].Kind != "schedule" || result.Items[1].Status != "active" || !result.Items[1].ConsumesQuota {
		t.Fatalf("schedule item=%#v", result.Items[1])
	}
}

func TestDianaTasksToolAllScopeRequiresOwner(t *testing.T) {
	store := &stubReminderStore{items: []Reminder{{ID: "a", OwnerID: "user"}, {ID: "b", OwnerID: "other"}}}
	runtime := NewRuntime(BotConfig{OwnerID: "owner"}, nilChannel{}, NewPluginManager(), nil, store, nil, nil)
	if _, err := newDianaTasksTool(runtime, MessageEvent{UserID: "user"}).Run(context.Background(), map[string]any{"scope": "all"}); err == nil || !strings.Contains(err.Error(), "只有主人") {
		t.Fatalf("non-owner error=%v", err)
	}
	raw, err := newDianaTasksTool(runtime, MessageEvent{UserID: "owner"}).Run(context.Background(), map[string]any{"scope": "all"})
	if err != nil {
		t.Fatal(err)
	}
	var result dianaTasksResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if result.Scope != "all" || len(result.Items) != 2 {
		t.Fatalf("result=%#v", result)
	}
}

func TestRuntimeAgentCanQueryAllPersonalTasks(t *testing.T) {
	store := &stubReminderStore{items: []Reminder{
		{ID: "reminder", Kind: ReminderKindMessage, OwnerID: "user", UserID: "user", Message: "喝水"},
		{ID: "schedule", Kind: ReminderKindQuery, OwnerID: "user", UserID: "user", Message: "查询公告", IntervalSeconds: 3600},
	}}
	provider := &sequenceLLMProvider{replies: []string{
		`{"action":"none","prompt":""}`,
		`{"action":"tool","tool":"diana.tasks","input":{"operation":"list","scope":"mine"}}`,
		`{"action":"final","content":"你有一个喝水提醒和一个查询公告的周期订阅。"}`,
	}}
	runtime := NewRuntime(BotConfig{
		OwnerID: "owner", AgentEnabled: true, AgentWorkDir: t.TempDir(), AgentMaxSteps: 3,
	}, &recordingChannel{}, NewPluginManager(), nil, store, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	event := MessageEvent{Kind: EventKindPrivate, UserID: "user", MessageID: "tasks", Segments: []MessageSegment{{Type: "text", Data: map[string]string{"text": "我有哪些提醒和订阅"}}}}
	reply, err := runtime.replyTo(context.Background(), event, "我有哪些提醒和订阅")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reply, "喝水提醒") || !strings.Contains(reply, "周期订阅") || len(provider.requests) != 3 {
		t.Fatalf("reply=%q requests=%d", reply, len(provider.requests))
	}
	if !requestMessagesContain(provider.requests[2].Messages, `"kind": "reminder"`) || !requestMessagesContain(provider.requests[2].Messages, `"kind": "schedule"`) {
		t.Fatalf("tool result missing: %#v", provider.requests[2].Messages)
	}
}

func TestOwnerCanManageOtherUsersReminderAndSchedule(t *testing.T) {
	store := &stubReminderStore{}
	runtime := NewRuntime(BotConfig{OwnerID: "10001"}, nilChannel{}, NewPluginManager(), nil, store, nil, nil)
	ownerEvent := MessageEvent{UserID: "10001"}

	reminderRaw, err := newDianaReminderTool(runtime, ownerEvent).Run(context.Background(), map[string]any{
		"operation": "create", "target_user_id": "20002", "delay": "1h", "message": "喝水",
	})
	if err != nil {
		t.Fatal(err)
	}
	var reminder dianaReminderResult
	if err := json.Unmarshal([]byte(reminderRaw), &reminder); err != nil {
		t.Fatal(err)
	}
	scheduleRaw, err := newDianaScheduleTool(runtime, ownerEvent).Run(context.Background(), map[string]any{
		"operation": "create", "target_user_id": "20002", "interval": "2h", "query": "查询公告",
	})
	if err != nil {
		t.Fatal(err)
	}
	var schedule dianaScheduleResult
	if err := json.Unmarshal([]byte(scheduleRaw), &schedule); err != nil {
		t.Fatal(err)
	}
	if len(store.items) != 2 || store.items[0].OwnerID != "20002" || store.items[1].OwnerID != "20002" || reminder.Reminder.UserID != "20002" || schedule.Schedule.UserID != "20002" {
		t.Fatalf("reminder=%#v schedule=%#v stored=%#v", reminder, schedule, store.items)
	}
	if _, err := newDianaReminderTool(runtime, ownerEvent).Run(context.Background(), map[string]any{
		"operation": "update", "target_user_id": "20002", "id": reminder.Reminder.ID, "delay": "3h", "message": "按时喝水",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := newDianaScheduleTool(runtime, ownerEvent).Run(context.Background(), map[string]any{
		"operation": "update", "target_user_id": "20002", "id": schedule.Schedule.ID, "interval": "4h", "query": "查询新公告",
	}); err != nil {
		t.Fatal(err)
	}
	if store.items[0].Message != "按时喝水" || store.items[1].Message != "查询新公告" || store.items[1].IntervalSeconds != 4*3600 {
		t.Fatalf("tasks not updated: %#v", store.items)
	}

	if _, err := newDianaScheduleTool(runtime, ownerEvent).Run(context.Background(), map[string]any{
		"operation": "cancel", "target_user_id": "20002", "id": schedule.Schedule.ID,
	}); err != nil {
		t.Fatal(err)
	}
	if store.items[1].CancelledAt.IsZero() {
		t.Fatalf("schedule not cancelled: %#v", store.items)
	}
	raw, err := newDianaTasksTool(runtime, ownerEvent).Run(context.Background(), map[string]any{"target_user_id": "20002"})
	if err != nil {
		t.Fatal(err)
	}
	var tasks dianaTasksResult
	if err := json.Unmarshal([]byte(raw), &tasks); err != nil {
		t.Fatal(err)
	}
	if tasks.Scope != "target:20002" || len(tasks.Items) != 2 {
		t.Fatalf("tasks=%#v", tasks)
	}
}

func TestNonOwnerCannotManageAnotherUsersTasks(t *testing.T) {
	runtime := NewRuntime(BotConfig{OwnerID: "10001"}, nilChannel{}, NewPluginManager(), nil, &stubReminderStore{}, nil, nil)
	_, err := newDianaScheduleTool(runtime, MessageEvent{UserID: "20002"}).Run(context.Background(), map[string]any{
		"operation": "create", "target_user_id": "20003", "interval": "1h", "query": "越权",
	})
	if err == nil || !strings.Contains(err.Error(), "只有主人") {
		t.Fatalf("schedule error=%v", err)
	}
	_, err = newDianaReminderTool(runtime, MessageEvent{UserID: "20002"}).Run(context.Background(), map[string]any{
		"operation": "list", "target_user_id": "20003",
	})
	if err == nil || !strings.Contains(err.Error(), "只有主人") {
		t.Fatalf("reminder error=%v", err)
	}
}
