package qqbot

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"strings"
	"time"

	"diana-qq-bot/model/applog"
)

const (
	reminderQueryRetryInitialDelay    = 5 * time.Minute
	reminderDeliveryRetryInitialDelay = 30 * time.Minute
	reminderRetryMaximumDelay         = 6 * time.Hour
)

func durableReminderRetryDelay(item Reminder, cause error, failures int) time.Duration {
	delay := reminderQueryRetryInitialDelay
	if errors.Is(cause, errOutboundSend) {
		delay = reminderDeliveryRetryInitialDelay
	}
	if failures < 1 {
		failures = 1
	}
	for attempt := 1; attempt < failures && delay < reminderRetryMaximumDelay; attempt++ {
		delay *= 2
		if delay > reminderRetryMaximumDelay {
			delay = reminderRetryMaximumDelay
		}
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(item.ID))
	_, _ = hash.Write([]byte(fmt.Sprintf("\x00%d", failures)))
	permille := int64(1000 + hash.Sum32()%201)
	jittered := time.Duration(int64(delay) * permille / 1000)
	if jittered > reminderRetryMaximumDelay {
		return reminderRetryMaximumDelay
	}
	return jittered
}

func (r *Runtime) storeScheduledQueryPending(id, message string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		return fmt.Errorf("定时订阅 %s 没有可持久化的发送结果", id)
	}
	r.reminderMu.Lock()
	defer r.reminderMu.Unlock()
	items := r.reminders.Reminders()
	for index := range items {
		item := &items[index]
		if item.ID != id || !reminderIsScheduledQuery(*item) {
			continue
		}
		if !item.CancelledAt.IsZero() {
			return fmt.Errorf("定时订阅 %s 已取消", id)
		}
		item.PendingDelivery = message
		item.PendingSince = time.Now()
		if err := r.reminders.SaveReminders(items); err != nil {
			return fmt.Errorf("保存定时订阅待发送结果: %w", err)
		}
		return nil
	}
	return fmt.Errorf("没有找到定时订阅 %s", id)
}

func (r *Runtime) rescheduleOneTimeReminder(id string, cause error) (Reminder, error) {
	r.reminderMu.Lock()
	items := r.reminders.Reminders()
	var updated Reminder
	found := false
	for index := range items {
		item := &items[index]
		if item.ID != id || reminderIsScheduledQuery(*item) {
			continue
		}
		found = true
		item.LastError = truncateRunesFromStart(cause.Error(), 500)
		item.ConsecutiveFailures++
		item.TriggerAt = time.Now().Add(durableReminderRetryDelay(*item, cause, item.ConsecutiveFailures))
		updated = *item
		break
	}
	var saveErr error
	if found {
		saveErr = r.reminders.SaveReminders(items)
	}
	r.reminderMu.Unlock()
	if !found {
		return Reminder{}, fmt.Errorf("没有找到一次性提醒 %s", id)
	}
	if saveErr != nil {
		return updated, fmt.Errorf("保存提醒重试状态: %w", saveErr)
	}
	return updated, nil
}

func (r *Runtime) notifyReminderFailure(ctx context.Context, item Reminder, cause error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	notice := reminderFailureNotice(item, cause)
	target := reminderSourceEvent(item)
	if target.Kind == EventKindGroup && errors.Is(cause, errOutboundSend) {
		target = MessageEvent{Kind: EventKindPrivate, UserID: item.UserID}
	}
	if strings.TrimSpace(target.UserID) == "" && target.Kind == EventKindPrivate {
		return fmt.Errorf("提醒失败通知缺少订阅者 QQ 号")
	}
	if err := r.send(ctx, target, notice); err == nil {
		return nil
	} else if target.Kind != EventKindGroup || strings.TrimSpace(item.UserID) == "" {
		return err
	} else {
		privateTarget := MessageEvent{Kind: EventKindPrivate, UserID: item.UserID}
		if privateErr := r.send(ctx, privateTarget, notice); privateErr != nil {
			return errors.Join(err, privateErr)
		}
		return nil
	}
}

func reminderFailureNotice(item Reminder, cause error) string {
	nextAttempt := item.TriggerAt.Format("2006-01-02 15:04:05")
	if reminderIsScheduledQuery(item) {
		if strings.TrimSpace(item.PendingDelivery) != "" {
			return fmt.Sprintf("定时订阅 %s 的结果发送失败，结果已保留。将在 %s 自动重试发送（连续失败 %d 次）。", item.ID, nextAttempt, item.ConsecutiveFailures)
		}
		return fmt.Sprintf("定时订阅 %s 本次执行失败：%s 将在 %s 自动重试（连续失败 %d 次）。", item.ID, publicQQErrorMessage(cause), nextAttempt, item.ConsecutiveFailures)
	}
	return fmt.Sprintf("提醒 %s 本次发送失败，将在 %s 自动重试（连续失败 %d 次）。", item.ID, nextAttempt, item.ConsecutiveFailures)
}

func (r *Runtime) recordReminderRetry(item Reminder, cause error, noticeErr error) {
	detail := cause.Error()
	if noticeErr != nil {
		detail += "\n失败通知发送失败：" + noticeErr.Error()
		log.Printf("qqbot reminder failure notice could not be delivered: id=%s: %v", item.ID, noticeErr)
	}
	writer := r.appLogWriter()
	if writer == nil {
		return
	}
	target := firstNonEmpty(strings.TrimSpace(item.GroupID), strings.TrimSpace(item.UserID))
	logCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = writer.AppendLog(logCtx, applog.Entry{
		Kind:    applog.KindError,
		Level:   applog.LevelError,
		Action:  "qqbot.reminder.retry_scheduled",
		Message: "提醒或订阅执行失败，已安排自动重试",
		Detail:  detail,
		Actor:   item.OwnerID,
		Target:  target,
		Metadata: map[string]any{
			"reminder_id":          item.ID,
			"reminder_kind":        item.Kind,
			"group_id":             item.GroupID,
			"user_id":              item.UserID,
			"next_retry_at":        item.TriggerAt,
			"consecutive_failures": item.ConsecutiveFailures,
			"pending_delivery":     strings.TrimSpace(item.PendingDelivery) != "",
			"notice_delivered":     noticeErr == nil,
		},
	})
}
