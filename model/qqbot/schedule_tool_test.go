package qqbot

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestDianaScheduleToolCreatesListsAndDeletesQuery(t *testing.T) {
	store := &stubReminderStore{}
	runtime := NewRuntime(BotConfig{OwnerID: "10001"}, nilChannel{}, NewPluginManager(), nil, store, nil, nil)
	event := MessageEvent{Kind: EventKindGroup, GroupID: "123456", UserID: "10001"}
	tool := newDianaScheduleTool(runtime, event)

	createdRaw, err := tool.Run(context.Background(), map[string]any{
		"operation": "create",
		"interval":  "6h",
		"query":     "查询目标项目的最新公告并总结变化",
	})
	if err != nil {
		t.Fatal(err)
	}
	var created dianaScheduleResult
	if err := json.Unmarshal([]byte(createdRaw), &created); err != nil {
		t.Fatal(err)
	}
	if !created.OK || created.Schedule == nil || created.Schedule.Interval != "6h0m0s" {
		t.Fatalf("created = %#v", created)
	}
	if len(store.items) != 1 {
		t.Fatalf("items = %#v", store.items)
	}
	item := store.items[0]
	if item.Kind != ReminderKindQuery || item.IntervalSeconds != int64((6*time.Hour)/time.Second) {
		t.Fatalf("item = %#v", item)
	}
	if item.GroupID != "123456" || item.UserID != "10001" || item.OwnerID != "10001" {
		t.Fatalf("target = %#v", item)
	}
	if remaining := time.Until(item.TriggerAt); remaining < 5*time.Hour+59*time.Minute || remaining > 6*time.Hour+time.Minute {
		t.Fatalf("next run in %s", remaining)
	}

	listedRaw, err := tool.Run(context.Background(), map[string]any{"operation": "list"})
	if err != nil || !strings.Contains(listedRaw, item.ID) {
		t.Fatalf("listed=%q err=%v", listedRaw, err)
	}
	deletedRaw, err := tool.Run(context.Background(), map[string]any{"operation": "delete", "id": item.ID})
	if err != nil || !strings.Contains(deletedRaw, "deleted") || len(store.items) != 0 {
		t.Fatalf("deleted=%q err=%v items=%#v", deletedRaw, err, store.items)
	}
}

func TestDianaScheduleToolCreatesAtMostFivePerCall(t *testing.T) {
	store := &stubReminderStore{}
	runtime := NewRuntime(BotConfig{OwnerID: "10001"}, nilChannel{}, NewPluginManager(), nil, store, nil, nil)
	tool := newDianaScheduleTool(runtime, MessageEvent{Kind: EventKindPrivate, UserID: "10001"})
	items := make([]any, 0, maximumTasksPerToolCall)
	for index := 0; index < maximumTasksPerToolCall; index++ {
		items = append(items, map[string]any{"interval": fmt.Sprintf("%dh", index+1), "query": fmt.Sprintf("查询 %d", index+1)})
	}
	raw, err := tool.Run(context.Background(), map[string]any{"operation": "create", "items": items})
	if err != nil {
		t.Fatal(err)
	}
	var result dianaScheduleResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != maximumTasksPerToolCall || result.Schedule != nil || len(store.items) != maximumTasksPerToolCall {
		t.Fatalf("result=%#v stored=%#v", result, store.items)
	}

	tooMany := append(append([]any(nil), items...), map[string]any{"interval": "6h", "query": "第六个"})
	_, err = tool.Run(context.Background(), map[string]any{"operation": "create", "items": tooMany})
	if err == nil || !strings.Contains(err.Error(), "一次最多创建 5 个") || len(store.items) != maximumTasksPerToolCall {
		t.Fatalf("err=%v stored=%#v", err, store.items)
	}
}

func TestDianaScheduleBatchUsesRemainingQuota(t *testing.T) {
	store := &stubReminderStore{}
	for index := 0; index < 4; index++ {
		store.items = append(store.items, Reminder{ID: fmt.Sprintf("existing-%d", index), Kind: ReminderKindQuery, OwnerID: "20002", UserID: "20002", IntervalSeconds: 3600})
	}
	runtime := NewRuntime(BotConfig{OwnerID: "10001"}, nilChannel{}, NewPluginManager(), nil, store, nil, nil)
	memory := newMemoryUserMemoryStore()
	memory.profiles["20002"] = UserMemoryProfile{UserID: "20002", Favorability: 60, MessageCount: 30}
	runtime.SetUserMemoryStore(memory)
	tool := newDianaScheduleTool(runtime, MessageEvent{Kind: EventKindPrivate, UserID: "20002"})
	raw, err := tool.Run(context.Background(), map[string]any{
		"operation": "create",
		"items": []any{
			map[string]any{"interval": "1h", "query": "A"},
			map[string]any{"interval": "2h", "query": "B"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var result dianaScheduleResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].Query != "A" || !strings.Contains(result.Message, "按剩余额度创建了 1 个") || len(store.items) != 5 || store.items[4].Message != "A" {
		t.Fatalf("result=%#v stored=%#v", result, store.items)
	}
}

func TestDianaScheduleExecutedItemsStillConsumeQuota(t *testing.T) {
	store := &stubReminderStore{}
	for index := 0; index < 5; index++ {
		store.items = append(store.items, Reminder{ID: fmt.Sprintf("running-%d", index), Kind: ReminderKindQuery, OwnerID: "20002", UserID: "20002", IntervalSeconds: 3600})
	}
	store.items[0].LastRunAt = time.Now().Add(-time.Minute)
	runtime := NewRuntime(BotConfig{OwnerID: "10001"}, nilChannel{}, NewPluginManager(), nil, store, nil, nil)
	memory := newMemoryUserMemoryStore()
	memory.profiles["20002"] = UserMemoryProfile{UserID: "20002", Favorability: 60, MessageCount: 30}
	runtime.SetUserMemoryStore(memory)
	_, err := newDianaScheduleTool(runtime, MessageEvent{UserID: "20002"}).Run(context.Background(), map[string]any{
		"operation": "create",
		"interval":  "1h",
		"query":     "A",
	})
	if err == nil || !strings.Contains(err.Error(), "额度已满") || len(store.items) != 5 {
		t.Fatalf("err=%v stored=%#v", err, store.items)
	}
}

func TestDianaScheduleToolAllowsOneDefaultTaskAndRejectsShortIntervals(t *testing.T) {
	store := &stubReminderStore{}
	runtime := NewRuntime(BotConfig{OwnerID: "10001"}, nilChannel{}, NewPluginManager(), nil, store, nil, nil)

	tool := newDianaScheduleTool(runtime, MessageEvent{UserID: "20002"})
	if _, err := tool.Run(context.Background(), map[string]any{
		"operation": "create",
		"interval":  "6h",
		"query":     "查询最新消息",
	}); err != nil {
		t.Fatalf("default create: %v", err)
	}
	if _, err := tool.Run(context.Background(), map[string]any{"operation": "create", "interval": "6h", "query": "第二项"}); err == nil || !strings.Contains(err.Error(), "最多创建 1 个") {
		t.Fatalf("default quota error = %v", err)
	}
	_, err := newDianaScheduleTool(runtime, MessageEvent{UserID: "10001"}).Run(context.Background(), map[string]any{
		"operation": "create",
		"interval":  "1s",
		"query":     "查询最新消息",
	})
	if err == nil || !strings.Contains(err.Error(), "不能短于") {
		t.Fatalf("short interval error = %v", err)
	}
}

func TestDianaScheduleToolAllowsFriendWithPersonalQuota(t *testing.T) {
	store := &stubReminderStore{}
	runtime := NewRuntime(BotConfig{OwnerID: "10001"}, nilChannel{}, NewPluginManager(), nil, store, nil, nil)
	memory := newMemoryUserMemoryStore()
	memory.profiles["20002"] = UserMemoryProfile{UserID: "20002", Favorability: 60, MessageCount: 30}
	runtime.SetUserMemoryStore(memory)
	tool := newDianaScheduleTool(runtime, MessageEvent{Kind: EventKindPrivate, UserID: "20002"})

	for i := 0; i < 5; i++ {
		if _, err := tool.Run(context.Background(), map[string]any{
			"operation": "create",
			"interval":  "6h",
			"query":     fmt.Sprintf("查询第 %d 项", i+1),
		}); err != nil {
			t.Fatalf("friend schedule %d: %v", i, err)
		}
	}
	_, err := tool.Run(context.Background(), map[string]any{
		"operation": "create",
		"interval":  "6h",
		"query":     "超过额度",
	})
	if err == nil || !strings.Contains(err.Error(), "最多创建 5 个") {
		t.Fatalf("quota error = %v", err)
	}
}

func TestDianaScheduleCancelReleasesQuotaAndDeleteRemovesRecord(t *testing.T) {
	store := &stubReminderStore{}
	runtime := NewRuntime(BotConfig{OwnerID: "owner"}, nilChannel{}, NewPluginManager(), nil, store, nil, nil)
	tool := newDianaScheduleTool(runtime, MessageEvent{UserID: "user"})
	createdRaw, err := tool.Run(context.Background(), map[string]any{"operation": "create", "interval": "1h", "query": "A"})
	if err != nil {
		t.Fatal(err)
	}
	var created dianaScheduleResult
	if err := json.Unmarshal([]byte(createdRaw), &created); err != nil {
		t.Fatal(err)
	}
	id := created.Schedule.ID
	cancelledRaw, err := tool.Run(context.Background(), map[string]any{"operation": "cancel", "id": id})
	if err != nil {
		t.Fatal(err)
	}
	var cancelled dianaScheduleResult
	if err := json.Unmarshal([]byte(cancelledRaw), &cancelled); err != nil {
		t.Fatal(err)
	}
	if cancelled.Schedule == nil || cancelled.Schedule.Status != "cancelled" || store.items[0].CancelledAt.IsZero() {
		t.Fatalf("cancelled=%#v stored=%#v", cancelled, store.items)
	}
	if _, err := tool.Run(context.Background(), map[string]any{"operation": "create", "interval": "2h", "query": "B"}); err != nil {
		t.Fatalf("cancelled schedule still consumed quota: %v", err)
	}
	if _, err := tool.Run(context.Background(), map[string]any{"operation": "delete", "id": id}); err != nil {
		t.Fatal(err)
	}
	if len(store.items) != 1 || store.items[0].Message != "B" {
		t.Fatalf("stored=%#v", store.items)
	}
}

func TestRuntimeAgentCanCreateScheduledQuery(t *testing.T) {
	store := &stubReminderStore{}
	channel := &recordingChannel{}
	provider := &sequenceLLMProvider{replies: []string{
		`{"action":"none","prompt":""}`,
		`{"action":"tool","tool":"diana.schedule","input":{"operation":"create","interval":"6h","query":"查询最新公告并总结变化"}}`,
		`{"action":"final","content":"已建立每 6 小时执行一次的订阅。"}`,
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
		MessageID: "schedule-1",
		Segments:  []MessageSegment{{Type: "text", Data: map[string]string{"text": "每 6 小时自动查询最新公告并通知我"}}},
	}
	reply, err := runtime.replyTo(context.Background(), event, "每 6 小时自动查询最新公告并通知我")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reply, "每 6 小时") || len(store.items) != 1 {
		t.Fatalf("reply=%q items=%#v", reply, store.items)
	}
	if len(channel.sent) != 1 || channel.sent[0].UserID != "10001" {
		t.Fatalf("sent = %#v", channel.sent)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("requests = %d", len(provider.requests))
	}
	foundTool := false
	for _, msg := range provider.requests[1].Messages {
		if strings.Contains(msg.Content, "diana.schedule") {
			foundTool = true
			break
		}
	}
	if !foundTool {
		t.Fatal("agent prompt did not expose diana.schedule")
	}
}

func TestRuntimeDueScheduledQueryRunsAgentAndReschedules(t *testing.T) {
	store := &stubReminderStore{items: []Reminder{{
		ID:              "task1234",
		Kind:            ReminderKindQuery,
		OwnerID:         "10001",
		UserID:          "10001",
		Message:         "查询最新公告并总结变化",
		TriggerAt:       time.Now().Add(-time.Minute),
		IntervalSeconds: int64((6 * time.Hour) / time.Second),
		CreatedAt:       time.Now().Add(-7 * time.Hour),
	}}}
	channel := &recordingChannel{}
	provider := &sequenceLLMProvider{replies: []string{
		`{"action":"final","content":"最新公告没有变化。"}`,
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

	runtime.fireDueReminders(context.Background())

	if len(channel.sent) != 1 || !strings.Contains(channel.sent[0].Text, "最新公告没有变化") {
		t.Fatalf("sent = %#v", channel.sent)
	}
	if len(store.items) != 1 {
		t.Fatalf("items = %#v", store.items)
	}
	next := store.items[0]
	if next.TriggerAt.Before(time.Now().Add(5*time.Hour+59*time.Minute)) || next.LastRunAt.IsZero() {
		t.Fatalf("rescheduled item = %#v", next)
	}
	if next.LastError != "" || next.ConsecutiveFailures != 0 {
		t.Fatalf("failure state = %#v", next)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("requests = %d", len(provider.requests))
	}
	foundQuery := false
	for _, msg := range provider.requests[0].Messages {
		if strings.Contains(msg.Content, "查询最新公告并总结变化") {
			foundQuery = true
			break
		}
	}
	if !foundQuery {
		t.Fatalf("scheduled query missing from request: %#v", provider.requests[0].Messages)
	}
}
