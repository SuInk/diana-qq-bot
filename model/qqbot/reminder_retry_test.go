package qqbot

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestScheduledQueryFailureNotifiesAndSchedulesRetry(t *testing.T) {
	now := time.Now()
	store := &stubReminderStore{items: []Reminder{{
		ID:              "query-fail",
		Kind:            ReminderKindQuery,
		OwnerID:         "10001",
		GroupID:         "123456",
		UserID:          "10001",
		Message:         "查询最新公告",
		TriggerAt:       now.Add(-time.Minute),
		IntervalSeconds: int64((6 * time.Hour) / time.Second),
		CreatedAt:       now.Add(-7 * time.Hour),
	}}}
	channel := &recordingChannel{}
	runtime := NewRuntime(BotConfig{OwnerID: "10001", AgentEnabled: false}, channel, NewPluginManager(), nil, store, nil, nil)

	runtime.fireDueReminders(context.Background())

	item := store.items[0]
	if item.LastRunAt.IsZero() || item.ConsecutiveFailures != 1 || !strings.Contains(item.LastError, "Agent 已禁用") {
		t.Fatalf("failure state = %#v", item)
	}
	if retryIn := time.Until(item.TriggerAt); retryIn < 5*time.Minute-2*time.Second || retryIn > 6*time.Minute+2*time.Second {
		t.Fatalf("retry scheduled in %s", retryIn)
	}
	if item.PendingDelivery != "" || scheduleStatus(item) != "retrying" {
		t.Fatalf("schedule state = %#v status=%q", item, scheduleStatus(item))
	}
	if len(channel.sent) != 1 || channel.sent[0].GroupID != "123456" {
		t.Fatalf("failure notices = %#v", channel.sent)
	}
	for _, want := range []string{"query-fail", "执行失败", "自动重试"} {
		if !strings.Contains(channel.sent[0].Text, want) {
			t.Fatalf("failure notice missing %q: %q", want, channel.sent[0].Text)
		}
	}
}

func TestScheduledQuerySendFailurePersistsResultAndRetriesWithoutLLM(t *testing.T) {
	now := time.Now()
	store := &stubReminderStore{items: []Reminder{{
		ID:              "delivery-fail",
		Kind:            ReminderKindQuery,
		OwnerID:         "10001",
		GroupID:         "123456",
		UserID:          "10001",
		Message:         "查询最新公告并总结变化",
		TriggerAt:       now.Add(-time.Minute),
		IntervalSeconds: int64((6 * time.Hour) / time.Second),
		CreatedAt:       now.Add(-7 * time.Hour),
	}}}
	channel := newScriptedBackoffChannel("123456")
	channel.alwaysFail["123456"] = true
	provider := &sequenceLLMProvider{replies: []string{`{"action":"final","content":"最新公告没有变化。"}`}}
	runtime := NewRuntime(BotConfig{
		OwnerID:        "10001",
		AgentEnabled:   true,
		AgentWorkDir:   t.TempDir(),
		AgentMaxSteps:  3,
		RequestTimeout: 5 * time.Second,
	}, channel, NewPluginManager(), nil, store, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	ctx := withOutboundDeliveryPolicy(context.Background(), fastOutboundDeliveryPolicy())

	runtime.fireDueReminders(ctx)

	failed := store.items[0]
	if failed.ConsecutiveFailures != 1 || strings.TrimSpace(failed.PendingDelivery) == "" || failed.PendingSince.IsZero() {
		t.Fatalf("pending delivery state = %#v", failed)
	}
	if !strings.Contains(failed.PendingDelivery, "最新公告没有变化") || scheduleStatus(failed) != "retrying" {
		t.Fatalf("pending result = %#v", failed)
	}
	if retryIn := time.Until(failed.TriggerAt); retryIn < 30*time.Minute-2*time.Second || retryIn > 36*time.Minute+2*time.Second {
		t.Fatalf("delivery retry scheduled in %s", retryIn)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("initial LLM requests = %d", len(provider.requests))
	}
	privateNotices := channel.attemptTexts("")
	if len(privateNotices) != 1 || !strings.Contains(privateNotices[0], "结果发送失败") || !strings.Contains(privateNotices[0], "结果已保留") {
		t.Fatalf("private failure notices = %#v", privateNotices)
	}

	channel.mu.Lock()
	channel.alwaysFail["123456"] = false
	channel.mu.Unlock()
	time.Sleep(fastOutboundDeliveryPolicy().DropCooldown + 10*time.Millisecond)
	store.items[0].TriggerAt = time.Now().Add(-time.Second)
	pendingMessage := store.items[0].PendingDelivery

	runtime.fireDueReminders(ctx)

	recovered := store.items[0]
	if recovered.PendingDelivery != "" || !recovered.PendingSince.IsZero() || recovered.ConsecutiveFailures != 0 || recovered.LastError != "" {
		t.Fatalf("recovered state = %#v", recovered)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("delivery retry reran LLM: requests=%d", len(provider.requests))
	}
	groupAttempts := channel.attemptTexts("123456")
	if len(groupAttempts) < 2 || groupAttempts[len(groupAttempts)-1] != pendingMessage {
		t.Fatalf("group delivery attempts = %#v, pending=%q", groupAttempts, pendingMessage)
	}
	if nextIn := time.Until(recovered.TriggerAt); nextIn < 5*time.Hour+59*time.Minute || nextIn > 6*time.Hour+time.Minute {
		t.Fatalf("normal schedule did not resume: next in %s", nextIn)
	}
}

func TestOneTimeReminderSendFailureRetriesAndNotifies(t *testing.T) {
	now := time.Now()
	store := &stubReminderStore{items: []Reminder{{
		ID:        "reminder-fail",
		Kind:      ReminderKindMessage,
		OwnerID:   "10001",
		GroupID:   "123456",
		UserID:    "10001",
		Message:   "去听课",
		TriggerAt: now.Add(-time.Minute),
		CreatedAt: now.Add(-time.Hour),
	}}}
	channel := newScriptedBackoffChannel("123456")
	channel.alwaysFail["123456"] = true
	runtime := NewRuntime(BotConfig{}, channel, NewPluginManager(), nil, store, nil, nil)
	ctx := withOutboundDeliveryPolicy(context.Background(), fastOutboundDeliveryPolicy())

	runtime.fireDueReminders(ctx)

	failed := store.items[0]
	if !failed.LastRunAt.IsZero() || failed.ConsecutiveFailures != 1 || failed.LastError == "" || reminderStatus(failed) != "retrying" {
		t.Fatalf("failed reminder state = %#v status=%q", failed, reminderStatus(failed))
	}
	if retryIn := time.Until(failed.TriggerAt); retryIn < 30*time.Minute-2*time.Second || retryIn > 36*time.Minute+2*time.Second {
		t.Fatalf("reminder retry scheduled in %s", retryIn)
	}
	privateNotices := channel.attemptTexts("")
	if len(privateNotices) != 1 || !strings.Contains(privateNotices[0], "提醒 reminder-fail") || !strings.Contains(privateNotices[0], "自动重试") {
		t.Fatalf("private notices = %#v", privateNotices)
	}

	channel.mu.Lock()
	channel.alwaysFail["123456"] = false
	channel.mu.Unlock()
	time.Sleep(fastOutboundDeliveryPolicy().DropCooldown + 10*time.Millisecond)
	store.items[0].TriggerAt = time.Now().Add(-time.Second)
	runtime.fireDueReminders(ctx)

	recovered := store.items[0]
	if recovered.LastRunAt.IsZero() || recovered.ConsecutiveFailures != 0 || recovered.LastError != "" || reminderStatus(recovered) != "used" {
		t.Fatalf("recovered reminder = %#v status=%q", recovered, reminderStatus(recovered))
	}
}

func TestReminderFailureNoticeDoesNotRecursivelyNotify(t *testing.T) {
	store := &stubReminderStore{items: []Reminder{{
		ID:        "notice-fail",
		Kind:      ReminderKindMessage,
		OwnerID:   "10001",
		UserID:    "10001",
		Message:   "去听课",
		TriggerAt: time.Now().Add(-time.Minute),
		CreatedAt: time.Now().Add(-time.Hour),
	}}}
	channel := newScriptedBackoffChannel()
	channel.alwaysFail[""] = true
	runtime := NewRuntime(BotConfig{}, channel, NewPluginManager(), nil, store, nil, nil)

	runtime.fireDueReminders(context.Background())

	attempts := channel.attemptTexts("")
	if len(attempts) != 2 {
		t.Fatalf("send attempts = %#v, want reminder plus one failure notice", attempts)
	}
	if !strings.Contains(attempts[0], "提醒你") || !strings.Contains(attempts[1], "自动重试") {
		t.Fatalf("send attempts = %#v", attempts)
	}
	if store.items[0].ConsecutiveFailures != 1 || !store.items[0].LastRunAt.IsZero() {
		t.Fatalf("retry state = %#v", store.items[0])
	}
}

func TestDurableReminderRetryDelayUsesSeparateQueryAndDeliveryBackoff(t *testing.T) {
	item := Reminder{ID: "retry-policy"}
	queryErr := errors.New("query failed")
	deliveryErr := &outboundSendError{Cause: errors.New("send failed")}
	queryDelay := durableReminderRetryDelay(item, queryErr, 1)
	deliveryDelay := durableReminderRetryDelay(item, deliveryErr, 1)
	if queryDelay < 5*time.Minute || queryDelay > 6*time.Minute {
		t.Fatalf("query delay = %s", queryDelay)
	}
	if deliveryDelay < 30*time.Minute || deliveryDelay > 36*time.Minute {
		t.Fatalf("delivery delay = %s", deliveryDelay)
	}
	if capped := durableReminderRetryDelay(item, deliveryErr, 20); capped != 6*time.Hour {
		t.Fatalf("maximum delay = %s", capped)
	}
}
