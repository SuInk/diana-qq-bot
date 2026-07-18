package qqbot

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"

	"diana-qq-bot/model/llm"
)

const (
	memoryWorkerCount       = 2
	memoryPollInterval      = 750 * time.Millisecond
	memoryLeaseDuration     = 3 * time.Minute
	memoryExtractionTimeout = 60 * time.Second
	memorySummaryMaxEvents  = 100
)

var memoryProfileGroups = []string{"memory", "memories", "recall"}

type memoryGatePayload struct {
	Current          memoryGateEvent    `json:"current"`
	RecentMessages   []memoryGateEvent  `json:"recent_messages,omitempty"`
	ExistingMemories []memoryGateMemory `json:"existing_memories,omitempty"`
}

type memoryGateEvent struct {
	Time      string `json:"time,omitempty"`
	UserID    string `json:"user_id,omitempty"`
	Sender    string `json:"sender,omitempty"`
	Text      string `json:"text,omitempty"`
	Quoted    string `json:"quoted,omitempty"`
	GroupID   string `json:"group_id,omitempty"`
	MessageID string `json:"message_id,omitempty"`
}

type memoryGateMemory struct {
	Key        string           `json:"key"`
	Kind       MemoryKind       `json:"kind"`
	Topic      string           `json:"topic"`
	Entity     string           `json:"entity,omitempty"`
	Content    string           `json:"content"`
	Confidence float64          `json:"confidence"`
	Importance float64          `json:"importance"`
	Visibility MemoryVisibility `json:"visibility"`
	Version    int              `json:"version"`
}

func (r *Runtime) runMemoryCoordinator(ctx context.Context, leaseOwner string, releaseStale bool, done chan struct{}) {
	defer close(done)
	r.mu.RLock()
	store := r.structuredMemory
	r.mu.RUnlock()
	if store == nil {
		return
	}
	if releaseStale {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := store.ReleaseMemoryJobLeases(releaseCtx, ""); err != nil {
			log.Printf("qqbot memory stale lease recovery failed: %v", err)
		}
		cancel()
	}

	var workers sync.WaitGroup
	for index := 0; index < memoryWorkerCount; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			r.runMemoryWorker(ctx, leaseOwner, store)
		}()
	}
	<-ctx.Done()
	workers.Wait()
	releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := store.ReleaseMemoryJobLeases(releaseCtx, leaseOwner); err != nil {
		log.Printf("qqbot memory lease release failed: %v", err)
	}
	cancel()
}

func (r *Runtime) runMemoryWorker(ctx context.Context, leaseOwner string, store StructuredMemoryStore) {
	ticker := time.NewTicker(memoryPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-r.memoryWake:
		}
		for ctx.Err() == nil {
			claimCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			job, ok, err := store.ClaimNextMemoryJob(claimCtx, leaseOwner, time.Now().Add(memoryLeaseDuration))
			cancel()
			if err != nil {
				log.Printf("qqbot memory job claim failed: %v", err)
				break
			}
			if !ok {
				break
			}
			jobCtx, jobCancel := context.WithTimeout(ctx, memoryExtractionTimeout)
			err = r.processMemoryJob(jobCtx, store, job)
			jobCancel()

			commitCtx, commitCancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err == nil {
				err = store.CompleteMemoryJob(commitCtx, job.ID, leaseOwner)
			} else {
				retryAt := time.Now().Add(memoryRetryDelay(job.Attempts))
				err = store.RetryMemoryJob(commitCtx, job.ID, leaseOwner, retryAt, err.Error())
			}
			commitCancel()
			if err != nil {
				log.Printf("qqbot memory job state update failed: %v", err)
			}
		}
	}
}

func memoryRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 7 {
		attempt = 7
	}
	return time.Duration(1<<(attempt-1)) * 15 * time.Second
}

func (r *Runtime) processMemoryJob(ctx context.Context, store StructuredMemoryStore, job MemoryJob) error {
	switch job.Payload.Kind {
	case MemoryJobEvent:
		return r.processEventMemoryJob(ctx, store, job.Payload)
	case MemoryJobSummary:
		return r.processSummaryMemoryJob(ctx, store, job)
	default:
		return fmt.Errorf("unsupported memory job kind %q", job.Payload.Kind)
	}
}

func (r *Runtime) processEventMemoryJob(ctx context.Context, store StructuredMemoryStore, payload MemoryJobPayload) error {
	event := payload.Event
	text := memoryEventText(event)
	if !memoryEventEligible(r.effectiveConfigForEvent(event), event, text) {
		return nil
	}
	existing, err := store.ListStructuredMemories(ctx, StructuredMemoryQuery{
		SubjectUserID: event.UserID,
		Session:       payload.Session,
		GroupID:       event.GroupID,
		Now:           time.Now(),
		MaxCandidates: 40,
	})
	if err != nil {
		return fmt.Errorf("load existing memories: %w", err)
	}
	gatePayload := memoryGatePayload{
		Current:          memoryGateEventFromMessage(event, text),
		RecentMessages:   r.memoryGateRecentEvents(event),
		ExistingMemories: memoryGateExistingMemories(existing, event.UserID),
	}
	payloadJSON, err := json.Marshal(gatePayload)
	if err != nil {
		return err
	}
	messages := []llm.Message{
		{
			Role: llm.RoleSystem,
			Content: strings.TrimSpace(`你是 Diana/嘉然的长期记忆门控器。消息原文已经单独、永久保存在事件日志中；你的任务不是复述聊天，而是只提议值得形成派生长期记忆的内容。

必须遵守：
1. 逐句理解语义、指代、引用和最近上下文，不得用关键词、前缀、子串或正则机械判断。
2. 只记录关于当前发言者的稳定事实、持续偏好、长期交互要求，或未来仍有明显价值的重要情景。普通问题、一次性任务、寒暄、玩梗、短暂情绪、媒体占位、链接、机器人回答、未经证实的第三方传闻都不记。当前任务里的格式要求、分析角度、修改意见、验收条件和临时约束即使表达得很明确，也只属于工作记忆；除非语义明确要求今后跨话题默认遵循或长期记住，否则绝不能写成 instruction。
3. 提醒、订阅、待办已有独立任务系统，不要重复写成长期记忆。好感度也由独立关系系统维护。
4. source_type=explicit 只用于当前发言者直接明确陈述；需要结合上下文推断时用 inferred。inferred 必须 confidence>=0.90，拿不准就不输出。
5. 每条记忆必须有稳定且颗粒度足够细的 key，例如 preference.food.spicy、profile.pet.cat、instruction.reply_style。更新、否定或要求忘记已有记忆时必须复用 existing_memories 中的原 key；不要用笼统的 profile、preference、chat 作为 key。
6. action=upsert 表示新增、确认或更新；action=forget 只用于当前发言者明确要求忘记、撤销或纠正已有记忆。forget 时 content 可以为空。
7. kind 只能是 fact、preference、episode、instruction。instruction 只表示跨会话长期有效的交互规则，不表示本次任务要求。episode 只用于重要的一次性经历，默认 retention_days=90；稳定事实和偏好可以为 0 表示不过期。
8. visibility=session 表示只在当前私聊或群可见；visibility=user 只适用于当前发言者明确陈述、非敏感且跨会话确有帮助的稳定事实/偏好。医疗、心理、财务、身份凭证、住址、联系方式、隐私关系等 sensitive=true，且必须 visibility=session。
9. importance 和 confidence 均为 0 到 1。只有 importance>=0.45 的内容才输出；明确要求“记住”的重要内容可提高 importance，但仍要按真实语义组织，不照抄命令。
10. content 必须写成自包含、无歧义的第三人称事实，保留实体；evidence 是不超过 60 字的最小证据片段。最多输出 5 条。
11. 只输出合法 JSON 对象，不要 Markdown 或解释。没有候选时输出 {"memories":[]}。格式：{"memories":[{"action":"upsert","key":"preference.food.spicy","kind":"preference","topic":"饮食偏好","entity":"辣味食物","content":"用户偏好辣味食物","evidence":"我喜欢吃辣","source_type":"explicit","confidence":0.98,"importance":0.68,"visibility":"user","sensitive":false,"retention_days":0}]}`),
		},
		{
			Role:    llm.RoleUser,
			Content: "请对当前消息执行记忆门控。上下文 JSON：\n" + string(payloadJSON),
		},
	}
	raw, err := r.runLLMMemoryProvider(ctx, func(client LLMProvider) (string, error) {
		response, err := client.Generate(ctx, llm.GenerateRequest{Messages: messages})
		if err != nil {
			return "", err
		}
		return response.Text, nil
	})
	if err != nil {
		return fmt.Errorf("memory gate llm: %w", err)
	}
	candidates, err := parseMemoryCandidates(raw)
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		return nil
	}
	_, err = store.ApplyMemoryCandidates(ctx, MemoryWriteRequest{
		SubjectUserID:   strings.TrimSpace(event.UserID),
		SubjectName:     strings.TrimSpace(event.SenderNameOrID()),
		Session:         payload.Session,
		EventKind:       event.Kind,
		GroupID:         event.GroupID,
		SourceMessageID: event.MessageID,
		SourceEventTime: memoryEventTime(event),
		Candidates:      candidates,
	})
	if err != nil {
		return fmt.Errorf("apply memory candidates: %w", err)
	}
	return nil
}

func (r *Runtime) processSummaryMemoryJob(ctx context.Context, store StructuredMemoryStore, job MemoryJob) error {
	events := job.Payload.Events
	if len(events) == 0 {
		return nil
	}
	if len(events) > memorySummaryMaxEvents {
		events = events[len(events)-memorySummaryMaxEvents:]
	}
	existing, err := store.ListStructuredMemories(ctx, StructuredMemoryQuery{
		Session:       job.Payload.Session,
		Now:           time.Now(),
		MaxCandidates: 30,
		Kinds:         []MemoryKind{MemoryKindSummary},
	})
	if err != nil {
		return fmt.Errorf("load conversation summaries: %w", err)
	}
	lines := make([]string, 0, len(events))
	for _, event := range events {
		if line := compactContextEventWithTime(event); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return nil
	}
	input := struct {
		Session  string             `json:"session"`
		Events   []string           `json:"events"`
		Existing []memoryGateMemory `json:"existing_summaries,omitempty"`
	}{
		Session:  job.Payload.Session,
		Events:   lines,
		Existing: memoryGateExistingMemories(existing, ""),
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return err
	}
	messages := []llm.Message{
		{
			Role: llm.RoleSystem,
			Content: strings.TrimSpace(`你是 Diana/嘉然的会话记忆整合器。请把一批较早的原始聊天事件整理为按时间和主题组织的长期会话摘要，原始事件会继续保留。

要求：
1. 理解整段对话后按主题聚合，保留人物、时间、事件、决定、未解决问题和事实变化；删除寒暄、重复和无后续价值的噪声。
2. 不得按关键词机械摘抄，不得把提问误当事实，不得补充原文没有的信息。
3. existing_summaries 是同会话已有摘要。相同日期和主题必须复用原 key，并生成包含旧摘要与新事件的完整更新版；不同主题建立新 key。
4. key 使用 summary.<YYYY-MM-DD>.<topic>，topic 简短明确，content 自包含。importance/confidence 为 0 到 1，visibility 固定 session，source_type 固定 summary，sensitive 按内容判断，retention_days 默认 365。
5. 最多输出 5 条；完全没有长期价值时输出空数组。
6. 只输出合法 JSON：{"memories":[{"action":"upsert","key":"summary.2026-07-15.memory-design","kind":"summary","topic":"记忆系统设计","entity":"Diana","content":"...","evidence":"事件范围摘要","source_type":"summary","confidence":0.96,"importance":0.8,"visibility":"session","sensitive":false,"retention_days":365}]}`),
		},
		{Role: llm.RoleUser, Content: "请整合这批较早会话。上下文 JSON：\n" + string(inputJSON)},
	}
	raw, err := r.runLLMMemoryProvider(ctx, func(client LLMProvider) (string, error) {
		response, err := client.Generate(ctx, llm.GenerateRequest{Messages: messages})
		if err != nil {
			return "", err
		}
		return response.Text, nil
	})
	if err != nil {
		return fmt.Errorf("memory summary llm: %w", err)
	}
	candidates, err := parseMemoryCandidates(raw)
	if err != nil {
		return err
	}
	for index := range candidates {
		candidates[index].Action = MemoryActionUpsert
		candidates[index].Kind = MemoryKindSummary
		candidates[index].SourceType = MemorySourceSummary
		candidates[index].Visibility = MemoryVisibilitySession
		if candidates[index].RetentionDays == 0 {
			candidates[index].RetentionDays = 365
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	first := events[0]
	last := events[len(events)-1]
	_, err = store.ApplyMemoryCandidates(ctx, MemoryWriteRequest{
		Session:         job.Payload.Session,
		EventKind:       first.Kind,
		GroupID:         first.GroupID,
		SourceMessageID: "summary:" + job.ID,
		SourceEventTime: memoryEventTime(last),
		Candidates:      candidates,
	})
	if err != nil {
		return fmt.Errorf("apply conversation summaries: %w", err)
	}
	return nil
}

func (r *Runtime) enqueueEventMemory(event MessageEvent, text string) {
	cfg := r.effectiveConfigForEvent(event)
	if !memoryEventEligible(cfg, event, text) || hasKnownResolverPlatformURL(event, text) {
		return
	}
	r.mu.RLock()
	store := r.structuredMemory
	r.mu.RUnlock()
	if store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_, inserted, err := store.EnqueueMemoryJob(ctx, MemoryJobPayload{
		Kind:    MemoryJobEvent,
		Session: sessionKey(event),
		Event:   event,
	})
	cancel()
	if err != nil {
		log.Printf("qqbot memory event enqueue failed: %v", err)
		return
	}
	if inserted {
		r.wakeMemoryWorkers()
	}
}

func (r *Runtime) enqueueContextSummary(session string, events []MessageEvent) {
	if strings.TrimSpace(session) == "" || len(events) == 0 {
		return
	}
	r.mu.RLock()
	store := r.structuredMemory
	r.mu.RUnlock()
	if store == nil {
		return
	}
	copyEvents := append([]MessageEvent(nil), events...)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_, inserted, err := store.EnqueueMemoryJob(ctx, MemoryJobPayload{
		Kind:    MemoryJobSummary,
		Session: session,
		Events:  copyEvents,
	})
	cancel()
	if err != nil {
		log.Printf("qqbot memory summary enqueue failed: %v", err)
		return
	}
	if inserted {
		r.wakeMemoryWorkers()
	}
}

func (r *Runtime) wakeMemoryWorkers() {
	select {
	case r.memoryWake <- struct{}{}:
	default:
	}
}

func (r *Runtime) runLLMMemoryProvider(ctx context.Context, run llmProviderRunFunc) (string, error) {
	run = r.withLLMQQPrivacyRun(ctx, run)
	r.mu.RLock()
	cfgFactory := r.llmCfgFactory
	factory := r.llmFactory
	store := r.llmStore
	r.mu.RUnlock()
	if cfgFactory != nil && store != nil {
		set := store.Profiles().WithDefaults()
		groups := append(append([]string(nil), memoryProfileGroups...), semanticRouteProfileGroups...)
		seen := map[string]bool{}
		for _, group := range groups {
			group = llm.NormalizeProfileGroup(group)
			if seen[group] {
				continue
			}
			seen[group] = true
			profiles := llmProfilesInGroup(set, group)
			if len(profiles) > 0 {
				return runLLMProviderProfileAttempts(ctx, profiles, cfgFactory, true, run)
			}
		}
		if current, ok := set.Current(); ok {
			return runLLMProviderProfileAttempts(ctx, []llm.Profile{current}, cfgFactory, true, run)
		}
		return "", fmt.Errorf("qqbot: no llm profile is configured")
	}
	if factory == nil {
		return "", fmt.Errorf("qqbot: llm provider is not configured")
	}
	client, err := factory()
	if err != nil {
		return "", err
	}
	return run(withTransientLLMRetry(client, true))
}

func parseMemoryCandidates(raw string) ([]MemoryCandidate, error) {
	raw = strings.TrimSpace(stripJSONCodeFence(raw))
	start, end := strings.Index(raw, "{"), strings.LastIndex(raw, "}")
	if start < 0 || end < start {
		return nil, fmt.Errorf("invalid memory gate response")
	}
	var envelope struct {
		Memories []MemoryCandidate `json:"memories"`
	}
	if err := json.Unmarshal([]byte(raw[start:end+1]), &envelope); err != nil {
		return nil, fmt.Errorf("decode memory gate response: %w", err)
	}
	if len(envelope.Memories) > 8 {
		envelope.Memories = envelope.Memories[:8]
	}
	return envelope.Memories, nil
}

func memoryEventEligible(cfg BotConfig, event MessageEvent, text string) bool {
	if event.Kind != EventKindGroup && event.Kind != EventKindPrivate {
		return false
	}
	if strings.TrimSpace(event.UserID) == "" || (strings.TrimSpace(cfg.BotQQ) != "" && event.UserID == cfg.BotQQ) {
		return false
	}
	text = strings.TrimSpace(text)
	if text == "" || memoryTextOnlyURLs(text) {
		return false
	}
	meaningful := 0
	for _, value := range text {
		if unicode.IsLetter(value) || unicode.IsDigit(value) {
			meaningful++
		}
	}
	return meaningful >= 2
}

func memoryTextOnlyURLs(text string) bool {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 {
		return false
	}
	for _, field := range fields {
		parsed, err := url.Parse(strings.Trim(field, "，。！？、,!?()[]{}<>\"'"))
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return false
		}
	}
	return true
}

func memoryEventText(event MessageEvent) string {
	var builder strings.Builder
	for _, segment := range event.Segments {
		if segment.Type == "text" {
			builder.WriteString(segment.Data["text"])
		}
	}
	text := strings.TrimSpace(builder.String())
	if text == "" && len(event.Segments) == 0 {
		text = strings.TrimSpace(event.RawMessage)
	}
	return strings.Join(strings.Fields(text), " ")
}

func memoryEventTime(event MessageEvent) time.Time {
	if event.Time > 0 {
		return time.Unix(event.Time, 0).UTC()
	}
	return time.Now().UTC()
}

func memoryGateEventFromMessage(event MessageEvent, text string) memoryGateEvent {
	item := memoryGateEvent{
		Time:      memoryEventTime(event).Format(time.RFC3339),
		UserID:    strings.TrimSpace(event.UserID),
		Sender:    strings.TrimSpace(event.SenderNameOrID()),
		Text:      truncateRunesFromStart(strings.TrimSpace(text), 500),
		GroupID:   strings.TrimSpace(event.GroupID),
		MessageID: strings.TrimSpace(event.MessageID),
	}
	if event.Quoted != nil {
		item.Quoted = truncateRunesFromStart(quotedPromptText(event.Quoted), 300)
	}
	return item
}

func (r *Runtime) memoryGateRecentEvents(current MessageEvent) []memoryGateEvent {
	history := r.contextHistory(current)
	items := make([]memoryGateEvent, 0, 6)
	for index := len(history) - 1; index >= 0 && len(items) < 6; index-- {
		event := history[index]
		if event.MessageID != "" && event.MessageID == current.MessageID {
			continue
		}
		text := memoryEventText(event)
		if text == "" {
			continue
		}
		items = append(items, memoryGateEventFromMessage(event, text))
	}
	for left, right := 0, len(items)-1; left < right; left, right = left+1, right-1 {
		items[left], items[right] = items[right], items[left]
	}
	return items
}

func memoryGateExistingMemories(items []StructuredMemoryItem, subjectUserID string) []memoryGateMemory {
	out := make([]memoryGateMemory, 0, len(items))
	for _, item := range items {
		if subjectUserID != "" && item.SubjectUserID != subjectUserID {
			continue
		}
		out = append(out, memoryGateMemory{
			Key:        item.Key,
			Kind:       item.Kind,
			Topic:      item.Topic,
			Entity:     item.Entity,
			Content:    item.Content,
			Confidence: item.Confidence,
			Importance: item.Importance,
			Visibility: item.Visibility,
			Version:    item.Version,
		})
		if len(out) >= 30 {
			break
		}
	}
	return out
}

func compactContextEventWithTime(event MessageEvent) string {
	line := compactContextEvent(event)
	if line == "" {
		return ""
	}
	return memoryEventTime(event).Format("2006-01-02 15:04") + " " + line
}
