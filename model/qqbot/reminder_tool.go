package qqbot

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	maximumReminderDelay        = 365 * 24 * time.Hour
	maximumReminderMessageRunes = 2000
	maximumTasksPerToolCall     = 5
)

type dianaReminderTool struct {
	runtime *Runtime
	event   MessageEvent
}

type dianaReminderResult struct {
	OK       bool            `json:"ok"`
	Action   string          `json:"action"`
	Message  string          `json:"message,omitempty"`
	Reminder *dianaReminder  `json:"reminder,omitempty"`
	Items    []dianaReminder `json:"items,omitempty"`
}

type dianaReminder struct {
	ID                  string    `json:"id"`
	Message             string    `json:"message"`
	TriggerAt           time.Time `json:"trigger_at"`
	Status              string    `json:"status"`
	Used                bool      `json:"used"`
	UsedAt              time.Time `json:"used_at,omitempty"`
	CancelledAt         time.Time `json:"cancelled_at,omitempty"`
	LastError           string    `json:"last_error,omitempty"`
	ConsecutiveFailures int       `json:"consecutive_failures,omitempty"`
	GroupID             string    `json:"group_id,omitempty"`
	UserID              string    `json:"user_id,omitempty"`
}

func newDianaReminderTool(runtime *Runtime, event MessageEvent) *dianaReminderTool {
	return &dianaReminderTool{runtime: runtime, event: event}
}

func (t *dianaReminderTool) Name() string {
	return "diana.reminder"
}

func (t *dianaReminderTool) Description() string {
	return `创建和管理持久化一次性提醒。用户要求“N 分钟/小时后提醒我”时必须使用此工具；周期查询或定期订阅使用 diana.schedule。禁止使用 run_command、sleep 或后台进程代替。初识及以上可用。单项创建兼容 input: {"operation":"create","delay":"1m","message":"提醒内容"}；一次创建多项使用 items，最多 5 项。update 可修改未执行提醒的 delay 和/或 message。剩余额度不足时按 items 顺序创建到额度上限；已执行或已取消的提醒不占额度。cancel 只停止并保留记录，delete 才彻底删除。主人可在任意操作中提供 target_user_id 代其他用户管理，创建仍占目标用户额度。管理示例：{"operation":"list|update|cancel|delete","id":"update/cancel/delete 必填","target_user_id":"仅主人可选"}`
}

func (t *dianaReminderTool) Run(ctx context.Context, input map[string]any) (string, error) {
	if t == nil || t.runtime == nil {
		return "", fmt.Errorf("diana reminder: runtime is not configured")
	}
	policy := t.runtime.relationshipPolicy(ctx, t.event)
	if !policy.AllowPersonalSchedule {
		return "", fmt.Errorf("好感度不足：当前关系等级为“%s”，尚未解锁个人提醒", policy.Name)
	}
	targetID, err := taskTargetUserID(ctx, t.runtime, t.event, input)
	if err != nil {
		return "", err
	}
	targetEvent := t.event
	targetEvent.UserID = targetID
	operation := strings.ToLower(strings.TrimSpace(configToolString(input, "operation")))
	switch operation {
	case "create", "add":
		requests, err := parseReminderCreateRequests(input)
		if err != nil {
			return "", err
		}
		items, err := t.runtime.addOneTimeReminders(targetEvent, requests)
		if err != nil {
			return "", err
		}
		message := fmt.Sprintf("已创建并持久化 %d 个一次性提醒。", len(items))
		if len(items) < len(requests) {
			message = fmt.Sprintf("本次请求 %d 个一次性提醒，按剩余额度创建了 %d 个。", len(requests), len(items))
		}
		result := dianaReminderResult{
			OK:      true,
			Action:  "created",
			Message: message,
			Items:   make([]dianaReminder, 0, len(items)),
		}
		for _, item := range items {
			result.Items = append(result.Items, *reminderForTool(item))
		}
		if len(items) == 1 {
			result.Reminder = reminderForTool(items[0])
		}
		return marshalDianaReminderResult(result)
	case "list":
		items := t.runtime.oneTimeReminders(targetID)
		result := make([]dianaReminder, 0, len(items))
		for _, item := range items {
			result = append(result, *reminderForTool(item))
		}
		return marshalDianaReminderResult(dianaReminderResult{
			OK:      true,
			Action:  "listed",
			Message: fmt.Sprintf("当前共有 %d 个一次性提醒。", len(result)),
			Items:   result,
		})
	case "update", "edit":
		id := strings.TrimSpace(configToolString(input, "id"))
		if id == "" {
			return "", fmt.Errorf("修改提醒时必须提供 id")
		}
		item, err := t.runtime.updateOneTimeReminder(targetID, id, input)
		if err != nil {
			return "", err
		}
		return marshalDianaReminderResult(dianaReminderResult{
			OK:       true,
			Action:   "updated",
			Message:  "一次性提醒已更新。",
			Reminder: reminderForTool(item),
		})
	case "cancel":
		id := strings.TrimSpace(configToolString(input, "id"))
		if id == "" {
			return "", fmt.Errorf("取消提醒时必须提供 id")
		}
		item, err := t.runtime.cancelOneTimeReminder(targetID, id)
		if err != nil {
			return "", err
		}
		return marshalDianaReminderResult(dianaReminderResult{
			OK:       true,
			Action:   "cancelled",
			Message:  "一次性提醒已取消并释放额度，记录仍保留。",
			Reminder: reminderForTool(item),
		})
	case "delete", "remove":
		id := strings.TrimSpace(configToolString(input, "id"))
		if id == "" {
			return "", fmt.Errorf("删除提醒时必须提供 id")
		}
		removed, err := t.runtime.deleteOneTimeReminder(targetID, id)
		if err != nil {
			return "", err
		}
		if !removed {
			return "", fmt.Errorf("没有找到属于当前用户的一次性提醒 %s", id)
		}
		return marshalDianaReminderResult(dianaReminderResult{
			OK:      true,
			Action:  "deleted",
			Message: "一次性提醒已删除。",
		})
	default:
		return "", fmt.Errorf("operation 必须是 create、list、update、cancel 或 delete")
	}
}

type reminderCreateRequest struct {
	Delay   time.Duration
	Message string
}

func parseReminderCreateRequests(input map[string]any) ([]reminderCreateRequest, error) {
	batch, batched, err := toolBatchItems(input)
	if err != nil {
		return nil, err
	}
	if !batched {
		batch = []map[string]any{input}
	}
	requests := make([]reminderCreateRequest, 0, len(batch))
	for index, item := range batch {
		delay, err := parseReminderDelay(configToolString(item, "delay"))
		if err != nil {
			return nil, fmt.Errorf("第 %d 个提醒: %w", index+1, err)
		}
		message := strings.TrimSpace(configToolString(item, "message"))
		if message == "" {
			return nil, fmt.Errorf("第 %d 个提醒内容不能为空", index+1)
		}
		if len([]rune(message)) > maximumReminderMessageRunes {
			return nil, fmt.Errorf("第 %d 个提醒内容不能超过 %d 个字符", index+1, maximumReminderMessageRunes)
		}
		requests = append(requests, reminderCreateRequest{Delay: delay, Message: message})
	}
	return requests, nil
}

func toolBatchItems(input map[string]any) ([]map[string]any, bool, error) {
	value, exists := input["items"]
	if !exists {
		return nil, false, nil
	}
	var items []map[string]any
	switch batch := value.(type) {
	case []any:
		for index, raw := range batch {
			item, ok := raw.(map[string]any)
			if !ok {
				return nil, true, fmt.Errorf("items[%d] 必须是对象", index)
			}
			items = append(items, item)
		}
	case []map[string]any:
		items = append(items, batch...)
	default:
		return nil, true, fmt.Errorf("items 必须是数组")
	}
	if len(items) == 0 {
		return nil, true, fmt.Errorf("items 不能为空")
	}
	if len(items) > maximumTasksPerToolCall {
		return nil, true, fmt.Errorf("一次最多创建 %d 个任务", maximumTasksPerToolCall)
	}
	return items, true, nil
}

func parseReminderDelay(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	delay, err := time.ParseDuration(raw)
	if err != nil || delay <= 0 {
		return 0, fmt.Errorf("提醒时长格式不正确，请使用 30s、1m、2h 这类格式")
	}
	if delay > maximumReminderDelay {
		return 0, fmt.Errorf("提醒时长不能超过 %s", maximumReminderDelay)
	}
	return delay, nil
}

func (r *Runtime) addOneTimeReminder(event MessageEvent, delay time.Duration, message string) (Reminder, error) {
	items, err := r.addOneTimeReminders(event, []reminderCreateRequest{{Delay: delay, Message: message}})
	if err != nil {
		return Reminder{}, err
	}
	return items[0], nil
}

func (r *Runtime) addOneTimeReminders(event MessageEvent, requests []reminderCreateRequest) ([]Reminder, error) {
	if r.reminders == nil {
		return nil, fmt.Errorf("当前未启用提醒功能")
	}
	if len(requests) == 0 {
		return nil, fmt.Errorf("至少需要一个提醒")
	}
	if len(requests) > maximumTasksPerToolCall {
		return nil, fmt.Errorf("一次最多创建 %d 个任务", maximumTasksPerToolCall)
	}
	policy := r.relationshipPolicy(context.Background(), event)
	limit := policy.personalScheduleLimit()
	if limit == 0 {
		return nil, fmt.Errorf("当前关系等级为“%s”，没有个人提醒权限", policy.Name)
	}

	r.reminderMu.Lock()
	defer r.reminderMu.Unlock()
	items := r.reminders.Reminders()
	count := 0
	for _, existing := range items {
		if existing.Kind == ReminderKindMessage && existing.OwnerID == event.UserID && existing.LastRunAt.IsZero() && existing.CancelledAt.IsZero() {
			count++
		}
	}
	remaining := limit - count
	if remaining <= 0 {
		return nil, fmt.Errorf("当前关系等级最多创建 %d 个一次性提醒，额度已满", limit)
	}
	if len(requests) > remaining {
		requests = requests[:remaining]
	}
	now := time.Now()
	requestedAt := now
	if event.Time > 0 {
		eventTime := time.Unix(event.Time, 0)
		if !eventTime.After(now.Add(time.Minute)) {
			requestedAt = eventTime
		}
	}
	created := make([]Reminder, 0, len(requests))
	for index, request := range requests {
		message := strings.TrimSpace(request.Message)
		if request.Delay <= 0 || request.Delay > maximumReminderDelay {
			return nil, fmt.Errorf("第 %d 个提醒时长无效", index+1)
		}
		if message == "" || len([]rune(message)) > maximumReminderMessageRunes {
			return nil, fmt.Errorf("第 %d 个提醒内容无效", index+1)
		}
		triggerAt := requestedAt.Add(request.Delay)
		if triggerAt.Before(now) {
			triggerAt = now
		}
		created = append(created, Reminder{
			ID:        uuid.NewString()[:8],
			Kind:      ReminderKindMessage,
			OwnerID:   event.UserID,
			GroupID:   event.GroupID,
			UserID:    event.UserID,
			Message:   message,
			TriggerAt: triggerAt,
			CreatedAt: now,
		})
	}
	if err := r.reminders.SaveReminders(append(items, created...)); err != nil {
		return nil, fmt.Errorf("保存提醒失败: %w", err)
	}
	return created, nil
}

func (r *Runtime) oneTimeReminders(ownerID string) []Reminder {
	if r.reminders == nil {
		return nil
	}
	r.reminderMu.Lock()
	defer r.reminderMu.Unlock()
	items := r.reminders.Reminders()
	out := make([]Reminder, 0, len(items))
	for _, item := range items {
		if item.Kind == ReminderKindMessage && item.OwnerID == ownerID {
			out = append(out, item)
		}
	}
	return out
}

func (r *Runtime) deleteOneTimeReminder(ownerID string, id string) (bool, error) {
	if r.reminders == nil {
		return false, fmt.Errorf("当前未启用提醒功能")
	}
	r.reminderMu.Lock()
	defer r.reminderMu.Unlock()
	items := r.reminders.Reminders()
	next := make([]Reminder, 0, len(items))
	removed := false
	for _, item := range items {
		if item.Kind == ReminderKindMessage && item.OwnerID == ownerID && item.ID == id {
			removed = true
			continue
		}
		next = append(next, item)
	}
	if !removed {
		return false, nil
	}
	if err := r.reminders.SaveReminders(next); err != nil {
		return false, fmt.Errorf("删除提醒失败: %w", err)
	}
	return true, nil
}

func (r *Runtime) cancelOneTimeReminder(ownerID string, id string) (Reminder, error) {
	if r.reminders == nil {
		return Reminder{}, fmt.Errorf("当前未启用提醒功能")
	}
	r.reminderMu.Lock()
	defer r.reminderMu.Unlock()
	items := r.reminders.Reminders()
	for index := range items {
		item := &items[index]
		if item.Kind != ReminderKindMessage || item.OwnerID != ownerID || item.ID != id {
			continue
		}
		if !item.LastRunAt.IsZero() {
			return Reminder{}, fmt.Errorf("一次性提醒 %s 已执行，不能再取消；可以删除记录", id)
		}
		if !item.CancelledAt.IsZero() {
			return Reminder{}, fmt.Errorf("一次性提醒 %s 已经取消", id)
		}
		item.CancelledAt = time.Now()
		if err := r.reminders.SaveReminders(items); err != nil {
			return Reminder{}, fmt.Errorf("取消提醒失败: %w", err)
		}
		return *item, nil
	}
	return Reminder{}, fmt.Errorf("没有找到属于当前用户的一次性提醒 %s", id)
}

func (r *Runtime) updateOneTimeReminder(ownerID string, id string, input map[string]any) (Reminder, error) {
	if r.reminders == nil {
		return Reminder{}, fmt.Errorf("当前未启用提醒功能")
	}
	rawDelay := strings.TrimSpace(configToolString(input, "delay"))
	message := strings.TrimSpace(configToolString(input, "message"))
	if rawDelay == "" && message == "" {
		return Reminder{}, fmt.Errorf("修改提醒时至少提供 delay 或 message")
	}
	var delay time.Duration
	var err error
	if rawDelay != "" {
		delay, err = parseReminderDelay(rawDelay)
		if err != nil {
			return Reminder{}, err
		}
	}
	if len([]rune(message)) > maximumReminderMessageRunes {
		return Reminder{}, fmt.Errorf("提醒内容不能超过 %d 个字符", maximumReminderMessageRunes)
	}
	r.reminderMu.Lock()
	defer r.reminderMu.Unlock()
	items := r.reminders.Reminders()
	for index := range items {
		item := &items[index]
		if item.Kind != ReminderKindMessage || item.OwnerID != ownerID || item.ID != id {
			continue
		}
		if !item.LastRunAt.IsZero() || !item.CancelledAt.IsZero() {
			return Reminder{}, fmt.Errorf("提醒 %s 已执行或已取消，不能修改；请新建提醒", id)
		}
		if rawDelay != "" {
			item.TriggerAt = time.Now().Add(delay)
		}
		if message != "" {
			item.Message = message
		}
		if err := r.reminders.SaveReminders(items); err != nil {
			return Reminder{}, fmt.Errorf("修改提醒失败: %w", err)
		}
		return *item, nil
	}
	return Reminder{}, fmt.Errorf("没有找到属于目标用户的一次性提醒 %s", id)
}

func reminderForTool(item Reminder) *dianaReminder {
	return &dianaReminder{
		ID:                  item.ID,
		Message:             item.Message,
		TriggerAt:           item.TriggerAt,
		Status:              reminderStatus(item),
		Used:                !item.LastRunAt.IsZero(),
		UsedAt:              item.LastRunAt,
		CancelledAt:         item.CancelledAt,
		LastError:           item.LastError,
		ConsecutiveFailures: item.ConsecutiveFailures,
		GroupID:             item.GroupID,
		UserID:              item.UserID,
	}
}

func reminderStatus(item Reminder) string {
	if !item.CancelledAt.IsZero() {
		return "cancelled"
	}
	if !item.LastRunAt.IsZero() {
		return "used"
	}
	if item.ConsecutiveFailures > 0 {
		return "retrying"
	}
	return "active"
}

func marshalDianaReminderResult(result dianaReminderResult) (string, error) {
	body, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}
	return string(body), nil
}
