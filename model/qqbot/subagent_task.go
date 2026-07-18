package qqbot

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"diana-qq-bot/model/applog"
	"diana-qq-bot/model/llm"

	"github.com/google/uuid"
)

const (
	defaultSubagentTaskConcurrency = 2
	defaultSubagentTaskTimeout     = 20 * time.Minute
	maxSubagentLLMConcurrency      = 6
	subagentProgressMinRuntime     = 5 * time.Second
	subagentProgressMinInterval    = 15 * time.Second
)

type SubagentTaskStatus struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Name      string    `json:"name"`
	Phase     string    `json:"phase"`
	Completed int       `json:"completed,omitempty"`
	Total     int       `json:"total,omitempty"`
	StartedAt time.Time `json:"started_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type activeSubagentTask struct {
	status           SubagentTaskStatus
	lastNotification time.Time
	lastMessage      string
}

type reservedSubagentTask struct {
	id    string
	key   string
	task  PluginTask
	event MessageEvent
}

type pluginTaskReservation struct {
	reserved   []reservedSubagentTask
	duplicates []SubagentTaskStatus
	ack        string
	handled    bool
}

func subagentLLMConcurrency(maxBotConcurrency int) int {
	if maxBotConcurrency <= 0 {
		return 1
	}
	if maxBotConcurrency > maxSubagentLLMConcurrency {
		return maxSubagentLLMConcurrency
	}
	return maxBotConcurrency
}

func (r *Runtime) launchPluginTasks(ctx context.Context, event MessageEvent, tasks []PluginTask) (string, bool, error) {
	reservation := r.reservePluginTasks(event, tasks)
	if !reservation.handled {
		return "", false, nil
	}
	if err := r.send(ctx, event, reservation.ack); err != nil {
		r.cancelPluginTaskReservation(reservation)
		return "", true, err
	}
	r.startPluginTaskReservation(reservation)
	return reservation.ack, true, nil
}

func (r *Runtime) reservePluginTasks(event MessageEvent, tasks []PluginTask) pluginTaskReservation {
	reserved := make([]reservedSubagentTask, 0, len(tasks))
	duplicates := make([]SubagentTaskStatus, 0, len(tasks))
	now := time.Now()

	r.subagentMu.Lock()
	for _, task := range tasks {
		if task.Run == nil {
			continue
		}
		task.Kind = strings.TrimSpace(task.Kind)
		if task.Kind == "" {
			task.Kind = "plugin"
		}
		task.Name = strings.TrimSpace(task.Name)
		if task.Name == "" {
			task.Name = "后台任务"
		}
		key := strings.TrimSpace(task.Key)
		if key == "" {
			key = task.Kind + ":" + uuid.NewString()
		}
		if active, ok := r.subagentTasks[key]; ok {
			duplicates = append(duplicates, active.status)
			continue
		}
		id := shortSubagentTaskID(task.Kind)
		status := SubagentTaskStatus{
			ID:        id,
			Kind:      task.Kind,
			Name:      task.Name,
			Phase:     "queued",
			StartedAt: now,
			UpdatedAt: now,
		}
		r.subagentTasks[key] = activeSubagentTask{status: status}
		reserved = append(reserved, reservedSubagentTask{id: id, key: key, task: task, event: event})
	}
	r.subagentMu.Unlock()

	if len(reserved) == 0 && len(duplicates) == 0 {
		return pluginTaskReservation{}
	}
	return pluginTaskReservation{
		reserved:   reserved,
		duplicates: duplicates,
		ack:        subagentStartedMessage(reserved, duplicates),
		handled:    true,
	}
}

func (r *Runtime) startPluginTaskReservation(reservation pluginTaskReservation) {
	rootCtx := r.subagentRootContext()
	for _, item := range reservation.reserved {
		go r.runPluginTask(rootCtx, item)
	}
}

func (r *Runtime) cancelPluginTaskReservation(reservation pluginTaskReservation) {
	r.subagentMu.Lock()
	defer r.subagentMu.Unlock()
	for _, item := range reservation.reserved {
		if active, ok := r.subagentTasks[item.key]; ok && active.status.ID == item.id {
			delete(r.subagentTasks, item.key)
		}
	}
}

func shortSubagentTaskID(kind string) string {
	prefix := "task"
	kind = strings.ToLower(kind)
	if strings.Contains(kind, "ocr") {
		prefix = "ocr"
	} else if strings.Contains(kind, "image") {
		prefix = "img"
	}
	return prefix + "-" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
}

func subagentStartedMessage(reserved []reservedSubagentTask, duplicates []SubagentTaskStatus) string {
	if len(reserved) == 0 {
		if len(duplicates) == 1 {
			return fmt.Sprintf("同一任务正在后台处理中（任务编号：%s），完成后我会继续回复。", duplicates[0].ID)
		}
		return "这些任务已经在后台处理中，完成后我会继续回复。"
	}
	if len(reserved) == 1 {
		item := reserved[0]
		message := strings.TrimSpace(item.task.StartedMessage)
		if message == "" {
			message = fmt.Sprintf("已启动后台任务：%s。", item.task.Name)
		}
		return fmt.Sprintf("%s\n任务编号：%s。完成后我会继续回复。", message, item.id)
	}
	return fmt.Sprintf("已启动 %d 个后台任务。完成后我会依次回复。", len(reserved))
}

func (r *Runtime) subagentRootContext() context.Context {
	r.mu.RLock()
	ctx := r.runCtx
	r.mu.RUnlock()
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func (r *Runtime) runPluginTask(rootCtx context.Context, item reservedSubagentTask) {
	sem := r.subagentSem
	select {
	case sem <- struct{}{}:
		defer func() { <-sem }()
	case <-rootCtx.Done():
		r.removeSubagentTask(item.key, item.id)
		return
	}

	timeout := item.task.Timeout
	if timeout <= 0 {
		timeout = defaultSubagentTaskTimeout
	}
	ctx, cancel := context.WithTimeout(rootCtx, timeout)
	defer cancel()
	r.updateSubagentTask(item.key, item.id, PluginTaskProgress{Phase: "running"})
	r.recordSubagentTaskLog(ctx, item, applog.KindOperation, applog.LevelInfo, "后台任务已开始", "")

	services := PluginTaskServices{
		Generate: r.generateForPluginTask,
		Report: func(progress PluginTaskProgress) {
			r.reportSubagentProgress(ctx, item, progress)
		},
	}
	result, err := runPluginTaskSafely(ctx, item.task, services)
	if err != nil {
		if ctx.Err() == nil || rootCtx.Err() == nil {
			message := fmt.Sprintf("任务 %s 执行失败：%s", item.id, publicQQErrorMessage(err))
			_ = r.sendSubagentFollowup(rootCtx, item.event, message)
			r.recordSubagentTaskLog(context.Background(), item, applog.KindError, applog.LevelError, "后台任务执行失败", err.Error())
		}
		r.removeSubagentTask(item.key, item.id)
		return
	}

	sent := false
	for _, message := range result.Messages {
		message = routeOutgoingToEvent(item.event, message)
		if err := r.sendOutgoing(rootCtx, item.event, message); err != nil {
			r.setError(err.Error())
			r.recordSubagentTaskLog(context.Background(), item, applog.KindError, applog.LevelError, "后台任务结果发送失败", err.Error())
			r.removeSubagentTask(item.key, item.id)
			return
		}
		sent = true
	}
	reply := strings.TrimSpace(result.Reply)
	if reply == "" && !sent {
		reply = fmt.Sprintf("任务 %s 已完成，但没有生成可发送的结果。", item.id)
	}
	if reply != "" {
		if err := r.sendSubagentFollowup(rootCtx, item.event, reply); err != nil {
			r.setError(err.Error())
			r.recordSubagentTaskLog(context.Background(), item, applog.KindError, applog.LevelError, "后台任务结果发送失败", err.Error())
			r.removeSubagentTask(item.key, item.id)
			return
		}
	}
	r.recordSubagentTaskLog(context.Background(), item, applog.KindOperation, applog.LevelInfo, "后台任务已完成", "")
	r.removeSubagentTask(item.key, item.id)
}

func runPluginTaskSafely(ctx context.Context, task PluginTask, services PluginTaskServices) (result PluginTaskResult, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("plugin task panic: %v", recovered)
		}
	}()
	return task.Run(ctx, services)
}

// RunPluginTask executes one task synchronously for diagnostics such as the
// WebUI file test endpoint. Normal chat should use launchPluginTasks instead.
func (r *Runtime) RunPluginTask(ctx context.Context, task PluginTask) (PluginTaskResult, error) {
	if task.Run == nil {
		return PluginTaskResult{}, fmt.Errorf("plugin task is not runnable")
	}
	timeout := task.Timeout
	if timeout <= 0 {
		timeout = defaultSubagentTaskTimeout
	}
	taskCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return runPluginTaskSafely(taskCtx, task, PluginTaskServices{
		Generate: r.generateForPluginTask,
		Report:   func(PluginTaskProgress) {},
	})
}

func (r *Runtime) generateForPluginTask(ctx context.Context, req llm.GenerateRequest) (string, error) {
	sem := r.subagentLLMSem
	select {
	case sem <- struct{}{}:
		defer func() { <-sem }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	return r.runLLMProvider(ctx, func(client LLMProvider) (string, error) {
		resp, err := client.Generate(ctx, req)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(resp.Text), nil
	})
}

func (r *Runtime) reportSubagentProgress(ctx context.Context, item reservedSubagentTask, progress PluginTaskProgress) {
	shouldNotify, message := r.updateSubagentTask(item.key, item.id, progress)
	if !shouldNotify || strings.TrimSpace(message) == "" {
		return
	}
	notifyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_ = r.sendSubagentFollowup(notifyCtx, item.event, fmt.Sprintf("任务 %s：%s", item.id, message))
}

func (r *Runtime) updateSubagentTask(key string, id string, progress PluginTaskProgress) (bool, string) {
	now := time.Now()
	r.subagentMu.Lock()
	defer r.subagentMu.Unlock()
	active, ok := r.subagentTasks[key]
	if !ok || active.status.ID != id {
		return false, ""
	}
	if phase := strings.TrimSpace(progress.Phase); phase != "" {
		active.status.Phase = phase
	}
	if progress.Completed >= 0 {
		active.status.Completed = progress.Completed
	}
	if progress.Total >= 0 {
		active.status.Total = progress.Total
	}
	active.status.UpdatedAt = now
	message := strings.TrimSpace(progress.Message)
	shouldNotify := message != "" && message != active.lastMessage && now.Sub(active.status.StartedAt) >= subagentProgressMinRuntime
	if !active.lastNotification.IsZero() && now.Sub(active.lastNotification) < subagentProgressMinInterval {
		shouldNotify = false
	}
	if progress.Total > 0 && progress.Completed >= progress.Total {
		// The final task result follows immediately, so avoid a redundant completion notice.
		shouldNotify = false
	}
	if shouldNotify {
		active.lastNotification = now
		active.lastMessage = message
	}
	r.subagentTasks[key] = active
	return shouldNotify, message
}

func (r *Runtime) removeSubagentTask(key string, id string) {
	r.subagentMu.Lock()
	defer r.subagentMu.Unlock()
	if active, ok := r.subagentTasks[key]; ok && active.status.ID == id {
		delete(r.subagentTasks, key)
	}
}

func (r *Runtime) activeSubagentTaskCount() int {
	r.subagentMu.Lock()
	defer r.subagentMu.Unlock()
	return len(r.subagentTasks)
}

func (r *Runtime) subagentTaskStatuses() []SubagentTaskStatus {
	r.subagentMu.Lock()
	defer r.subagentMu.Unlock()
	statuses := make([]SubagentTaskStatus, 0, len(r.subagentTasks))
	for _, active := range r.subagentTasks {
		statuses = append(statuses, active.status)
	}
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].StartedAt.Before(statuses[j].StartedAt) })
	return statuses
}

func (r *Runtime) sendSubagentFollowup(ctx context.Context, event MessageEvent, reply string) error {
	cfg := r.effectiveConfigForEvent(event)
	reply = normalizeReply(reply, cfg.MaxReplyChars)
	chunks := splitReply(reply, cfg.DirectReplyChunkSize)
	if shouldUseForwardReply(reply, chunks, cfg.ForwardReplyThreshold) {
		return r.sendForwardReply(ctx, event, reply, cfg)
	}
	for _, chunk := range chunks {
		if strings.TrimSpace(chunk) == "" {
			continue
		}
		if err := r.sendOutgoing(ctx, event, routeOutgoingToEvent(event, OutgoingMessage{Text: chunk})); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) recordSubagentTaskLog(ctx context.Context, item reservedSubagentTask, kind applog.Kind, level applog.Level, message string, detail string) {
	writer := r.appLogWriter()
	if writer == nil {
		return
	}
	logCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_ = writer.AppendLog(logCtx, applog.Entry{
		Kind:    kind,
		Level:   level,
		Action:  "qqbot.subagent_task",
		Message: message,
		Detail:  detail,
		Actor:   qqEventActor(item.event),
		Target:  item.id,
		Metadata: map[string]any{
			"kind":     item.task.Kind,
			"name":     item.task.Name,
			"group_id": item.event.GroupID,
			"user_id":  item.event.UserID,
		},
	})
}
