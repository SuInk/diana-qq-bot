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
	minimumScheduleInterval   = 1 * time.Minute
	maximumScheduleInterval   = 365 * 24 * time.Hour
	maximumScheduleQueryRunes = 2000
)

type dianaScheduleTool struct {
	runtime *Runtime
	event   MessageEvent
}

type dianaScheduleResult struct {
	OK       bool            `json:"ok"`
	Action   string          `json:"action"`
	Message  string          `json:"message,omitempty"`
	Schedule *dianaSchedule  `json:"schedule,omitempty"`
	Items    []dianaSchedule `json:"items,omitempty"`
}

type dianaSchedule struct {
	ID                  string    `json:"id"`
	Query               string    `json:"query"`
	Interval            string    `json:"interval"`
	NextRunAt           time.Time `json:"next_run_at"`
	LastRunAt           time.Time `json:"last_run_at,omitempty"`
	Status              string    `json:"status"`
	CancelledAt         time.Time `json:"cancelled_at,omitempty"`
	LastError           string    `json:"last_error,omitempty"`
	ConsecutiveFailures int       `json:"consecutive_failures,omitempty"`
	PendingDelivery     bool      `json:"pending_delivery,omitempty"`
	PendingSince        time.Time `json:"pending_since,omitempty"`
	GroupID             string    `json:"group_id,omitempty"`
	UserID              string    `json:"user_id,omitempty"`
}

func newDianaScheduleTool(runtime *Runtime, event MessageEvent) *dianaScheduleTool {
	return &dianaScheduleTool{runtime: runtime, event: event}
}

func (t *dianaScheduleTool) Name() string {
	return "diana.schedule"
}

func (t *dianaScheduleTool) Description() string {
	return `创建和管理持久化周期查询/订阅。用户要求“每 N 分钟/小时自动查询、定期搜索并通知”时必须使用此工具；只执行一次的提醒使用 diana.reminder。禁止使用 run_command、sleep 或后台进程代替。初识及以上可用。单项创建兼容 input: {"operation":"create","interval":"2h","query":"查询要求"}；一次创建多项使用 items，最多 5 项。update 可修改有效订阅的 interval 和/或 query。剩余额度不足时按 items 顺序创建到额度上限；有效周期订阅在用户取消或删除前始终占用额度。cancel 只停止并保留记录，delete 才彻底删除。主人可在任意操作中提供 target_user_id 代其他用户管理，创建仍占目标用户额度。管理示例：{"operation":"list|update|cancel|delete","id":"update/cancel/delete 必填","target_user_id":"仅主人可选"}`
}

func (t *dianaScheduleTool) Run(_ context.Context, input map[string]any) (string, error) {
	if t == nil || t.runtime == nil {
		return "", fmt.Errorf("diana schedule: runtime is not configured")
	}
	policy := t.runtime.relationshipPolicy(context.Background(), t.event)
	if !policy.AllowPersonalSchedule {
		return "", fmt.Errorf("好感度不足：当前关系等级为“%s”，尚未解锁个人定时订阅", policy.Name)
	}
	targetID, err := taskTargetUserID(context.Background(), t.runtime, t.event, input)
	if err != nil {
		return "", err
	}
	targetEvent := t.event
	targetEvent.UserID = targetID
	operation := strings.ToLower(strings.TrimSpace(configToolString(input, "operation")))
	switch operation {
	case "create", "add":
		requests, err := parseScheduleCreateRequests(input)
		if err != nil {
			return "", err
		}
		items, err := t.runtime.addScheduledQueries(targetEvent, requests)
		if err != nil {
			return "", err
		}
		message := fmt.Sprintf("已创建并持久化 %d 个周期查询，将在到期后自动执行并发送到当前会话。", len(items))
		if len(items) < len(requests) {
			message = fmt.Sprintf("本次请求 %d 个周期查询，按剩余额度创建了 %d 个。", len(requests), len(items))
		}
		result := dianaScheduleResult{
			OK:      true,
			Action:  "created",
			Message: message,
			Items:   make([]dianaSchedule, 0, len(items)),
		}
		for _, item := range items {
			result.Items = append(result.Items, *scheduleForTool(item))
		}
		if len(items) == 1 {
			result.Schedule = scheduleForTool(items[0])
		}
		return marshalDianaScheduleResult(result)
	case "list":
		items := t.runtime.scheduledQueries(targetID)
		result := make([]dianaSchedule, 0, len(items))
		for _, item := range items {
			result = append(result, *scheduleForTool(item))
		}
		return marshalDianaScheduleResult(dianaScheduleResult{
			OK:      true,
			Action:  "listed",
			Message: fmt.Sprintf("当前共有 %d 个周期查询。", len(result)),
			Items:   result,
		})
	case "update", "edit":
		id := strings.TrimSpace(configToolString(input, "id"))
		if id == "" {
			return "", fmt.Errorf("修改定时订阅时必须提供 id")
		}
		item, err := t.runtime.updateScheduledQuery(targetID, id, input)
		if err != nil {
			return "", err
		}
		return marshalDianaScheduleResult(dianaScheduleResult{
			OK:       true,
			Action:   "updated",
			Message:  "定时订阅已更新。",
			Schedule: scheduleForTool(item),
		})
	case "cancel":
		id := strings.TrimSpace(configToolString(input, "id"))
		if id == "" {
			return "", fmt.Errorf("取消定时订阅时必须提供 id")
		}
		item, err := t.runtime.cancelScheduledQuery(targetID, id)
		if err != nil {
			return "", err
		}
		return marshalDianaScheduleResult(dianaScheduleResult{
			OK:       true,
			Action:   "cancelled",
			Message:  "定时订阅已取消并释放额度，记录仍保留。",
			Schedule: scheduleForTool(item),
		})
	case "delete", "remove":
		id := strings.TrimSpace(configToolString(input, "id"))
		if id == "" {
			return "", fmt.Errorf("删除定时订阅时必须提供 id")
		}
		removed, err := t.runtime.deleteScheduledQuery(targetID, id)
		if err != nil {
			return "", err
		}
		if !removed {
			return "", fmt.Errorf("没有找到属于目标用户的定时订阅 %s", id)
		}
		return marshalDianaScheduleResult(dianaScheduleResult{
			OK:      true,
			Action:  "deleted",
			Message: "定时订阅已删除。",
		})
	default:
		return "", fmt.Errorf("operation 必须是 create、list、update、cancel 或 delete")
	}
}

type scheduleCreateRequest struct {
	Interval time.Duration
	Query    string
}

func parseScheduleCreateRequests(input map[string]any) ([]scheduleCreateRequest, error) {
	batch, batched, err := toolBatchItems(input)
	if err != nil {
		return nil, err
	}
	if !batched {
		batch = []map[string]any{input}
	}
	requests := make([]scheduleCreateRequest, 0, len(batch))
	for index, item := range batch {
		interval, err := parseScheduleInterval(configToolString(item, "interval"))
		if err != nil {
			return nil, fmt.Errorf("第 %d 个周期任务: %w", index+1, err)
		}
		query := strings.TrimSpace(configToolString(item, "query"))
		if query == "" {
			return nil, fmt.Errorf("第 %d 个周期任务 query 不能为空", index+1)
		}
		if len([]rune(query)) > maximumScheduleQueryRunes {
			return nil, fmt.Errorf("第 %d 个周期任务 query 不能超过 %d 个字符", index+1, maximumScheduleQueryRunes)
		}
		requests = append(requests, scheduleCreateRequest{Interval: interval, Query: query})
	}
	return requests, nil
}

func parseScheduleInterval(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	interval, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("周期格式不正确，请使用 1m、2h、24h 这类格式")
	}
	if interval < minimumScheduleInterval {
		return 0, fmt.Errorf("周期不能短于 %s", minimumScheduleInterval)
	}
	if interval > maximumScheduleInterval {
		return 0, fmt.Errorf("周期不能超过 %s", maximumScheduleInterval)
	}
	return interval, nil
}

func (r *Runtime) addScheduledQuery(event MessageEvent, interval time.Duration, query string) (Reminder, error) {
	items, err := r.addScheduledQueries(event, []scheduleCreateRequest{{Interval: interval, Query: query}})
	if err != nil {
		return Reminder{}, err
	}
	return items[0], nil
}

func (r *Runtime) addScheduledQueries(event MessageEvent, requests []scheduleCreateRequest) ([]Reminder, error) {
	if r.reminders == nil {
		return nil, fmt.Errorf("当前未启用定时任务存储")
	}
	if len(requests) == 0 {
		return nil, fmt.Errorf("至少需要一个周期任务")
	}
	if len(requests) > maximumTasksPerToolCall {
		return nil, fmt.Errorf("一次最多创建 %d 个任务", maximumTasksPerToolCall)
	}
	policy := r.relationshipPolicy(context.Background(), event)
	limit := policy.personalScheduleLimit()
	if limit == 0 {
		return nil, fmt.Errorf("当前关系等级为“%s”，没有个人定时订阅权限", policy.Name)
	}
	r.reminderMu.Lock()
	defer r.reminderMu.Unlock()
	items := r.reminders.Reminders()
	count := 0
	for _, item := range items {
		if reminderIsScheduledQuery(item) && item.OwnerID == event.UserID && item.CancelledAt.IsZero() {
			count++
		}
	}
	remaining := limit - count
	if remaining <= 0 {
		return nil, fmt.Errorf("当前关系等级最多创建 %d 个定时订阅，额度已满", limit)
	}
	if len(requests) > remaining {
		requests = requests[:remaining]
	}
	now := time.Now()
	created := make([]Reminder, 0, len(requests))
	for index, request := range requests {
		query := strings.TrimSpace(request.Query)
		if request.Interval < minimumScheduleInterval || request.Interval > maximumScheduleInterval {
			return nil, fmt.Errorf("第 %d 个周期任务间隔无效", index+1)
		}
		if query == "" || len([]rune(query)) > maximumScheduleQueryRunes {
			return nil, fmt.Errorf("第 %d 个周期任务 query 无效", index+1)
		}
		created = append(created, Reminder{
			ID:              uuid.NewString()[:8],
			Kind:            ReminderKindQuery,
			OwnerID:         event.UserID,
			GroupID:         event.GroupID,
			UserID:          event.UserID,
			Message:         query,
			TriggerAt:       now.Add(request.Interval),
			IntervalSeconds: int64(request.Interval / time.Second),
			CreatedAt:       now,
		})
	}
	if err := r.reminders.SaveReminders(append(items, created...)); err != nil {
		return nil, fmt.Errorf("保存定时订阅失败: %w", err)
	}
	return created, nil
}

func (r *Runtime) scheduledQueries(ownerID string) []Reminder {
	if r.reminders == nil {
		return nil
	}
	r.reminderMu.Lock()
	defer r.reminderMu.Unlock()
	items := r.reminders.Reminders()
	out := make([]Reminder, 0, len(items))
	for _, item := range items {
		if reminderIsScheduledQuery(item) && item.OwnerID == ownerID {
			out = append(out, item)
		}
	}
	return out
}

func (r *Runtime) deleteScheduledQuery(ownerID string, id string) (bool, error) {
	if r.reminders == nil {
		return false, fmt.Errorf("当前未启用定时任务存储")
	}
	r.reminderMu.Lock()
	defer r.reminderMu.Unlock()
	items := r.reminders.Reminders()
	next := make([]Reminder, 0, len(items))
	removed := false
	for _, item := range items {
		if reminderIsScheduledQuery(item) && item.OwnerID == ownerID && item.ID == id {
			removed = true
			continue
		}
		next = append(next, item)
	}
	if !removed {
		return false, nil
	}
	if err := r.reminders.SaveReminders(next); err != nil {
		return false, fmt.Errorf("删除定时订阅失败: %w", err)
	}
	return true, nil
}

func (r *Runtime) cancelScheduledQuery(ownerID string, id string) (Reminder, error) {
	if r.reminders == nil {
		return Reminder{}, fmt.Errorf("当前未启用定时任务存储")
	}
	r.reminderMu.Lock()
	defer r.reminderMu.Unlock()
	items := r.reminders.Reminders()
	for index := range items {
		item := &items[index]
		if !reminderIsScheduledQuery(*item) || item.OwnerID != ownerID || item.ID != id {
			continue
		}
		if !item.CancelledAt.IsZero() {
			return Reminder{}, fmt.Errorf("定时订阅 %s 已经取消", id)
		}
		item.CancelledAt = time.Now()
		item.PendingDelivery = ""
		item.PendingSince = time.Time{}
		if err := r.reminders.SaveReminders(items); err != nil {
			return Reminder{}, fmt.Errorf("取消定时订阅失败: %w", err)
		}
		return *item, nil
	}
	return Reminder{}, fmt.Errorf("没有找到属于当前用户的定时订阅 %s", id)
}

func (r *Runtime) updateScheduledQuery(ownerID string, id string, input map[string]any) (Reminder, error) {
	if r.reminders == nil {
		return Reminder{}, fmt.Errorf("当前未启用定时任务存储")
	}
	rawInterval := strings.TrimSpace(configToolString(input, "interval"))
	query := strings.TrimSpace(configToolString(input, "query"))
	if rawInterval == "" && query == "" {
		return Reminder{}, fmt.Errorf("修改定时订阅时至少提供 interval 或 query")
	}
	var interval time.Duration
	var err error
	if rawInterval != "" {
		interval, err = parseScheduleInterval(rawInterval)
		if err != nil {
			return Reminder{}, err
		}
	}
	if len([]rune(query)) > maximumScheduleQueryRunes {
		return Reminder{}, fmt.Errorf("定时订阅查询不能超过 %d 个字符", maximumScheduleQueryRunes)
	}
	r.reminderMu.Lock()
	defer r.reminderMu.Unlock()
	items := r.reminders.Reminders()
	for index := range items {
		item := &items[index]
		if !reminderIsScheduledQuery(*item) || item.OwnerID != ownerID || item.ID != id {
			continue
		}
		if !item.CancelledAt.IsZero() {
			return Reminder{}, fmt.Errorf("定时订阅 %s 已取消，不能修改；请新建订阅", id)
		}
		if rawInterval != "" {
			item.IntervalSeconds = int64(interval / time.Second)
			item.TriggerAt = time.Now().Add(interval)
		}
		if query != "" {
			item.Message = query
			item.PendingDelivery = ""
			item.PendingSince = time.Time{}
			item.LastError = ""
			item.ConsecutiveFailures = 0
			item.TriggerAt = time.Now().Add(time.Duration(item.IntervalSeconds) * time.Second)
		}
		if err := r.reminders.SaveReminders(items); err != nil {
			return Reminder{}, fmt.Errorf("修改定时订阅失败: %w", err)
		}
		return *item, nil
	}
	return Reminder{}, fmt.Errorf("没有找到属于目标用户的定时订阅 %s", id)
}

func reminderIsScheduledQuery(item Reminder) bool {
	return item.Kind == ReminderKindQuery && item.IntervalSeconds > 0
}

func scheduleForTool(item Reminder) *dianaSchedule {
	return &dianaSchedule{
		ID:                  item.ID,
		Query:               item.Message,
		Interval:            (time.Duration(item.IntervalSeconds) * time.Second).String(),
		NextRunAt:           item.TriggerAt,
		LastRunAt:           item.LastRunAt,
		Status:              scheduleStatus(item),
		CancelledAt:         item.CancelledAt,
		LastError:           item.LastError,
		ConsecutiveFailures: item.ConsecutiveFailures,
		PendingDelivery:     strings.TrimSpace(item.PendingDelivery) != "",
		PendingSince:        item.PendingSince,
		GroupID:             item.GroupID,
		UserID:              item.UserID,
	}
}

func scheduleStatus(item Reminder) string {
	if !item.CancelledAt.IsZero() {
		return "cancelled"
	}
	if item.ConsecutiveFailures > 0 {
		return "retrying"
	}
	return "active"
}

func marshalDianaScheduleResult(result dianaScheduleResult) (string, error) {
	body, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}
	return string(body), nil
}
