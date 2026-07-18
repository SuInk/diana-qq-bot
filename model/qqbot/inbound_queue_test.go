package qqbot

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"diana-qq-bot/model/llm"
)

func TestRuntimeDurableInboxSurvivesRestartAndDeduplicates(t *testing.T) {
	store := newMemoryInboundEventStore()
	event := queuedDirectTestEvent("incoming-1", time.Now().Unix())
	ingestRuntime := NewRuntime(BotConfig{BotQQ: "42", GroupTriggers: []string{"Diana"}}, nilChannel{}, NewPluginManager(), nil, nil, nil, nil)
	ingestRuntime.SetInboundEventStore(store)
	for i := 0; i < 2; i++ {
		if err := ingestRuntime.HandleEvent(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	if count, _ := store.PendingInboundCount(context.Background()); count != 1 {
		t.Fatalf("pending after duplicate ingest = %d, want 1", count)
	}

	channel := newQueueTestChannel()
	provider := &sequenceLLMProvider{replies: []string{`{"action":"none","prompt":""}`, "恢复成功"}}
	runtime := newQueuedTestRuntime(channel, store, provider)
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForCondition(t, 3*time.Second, func() bool { return channel.sentCount() == 1 })
	if err := runtime.Stop(); err != nil {
		t.Fatal(err)
	}
	if count, _ := store.PendingInboundCount(context.Background()); count != 0 {
		t.Fatalf("pending after reply = %d, want 0", count)
	}

	restarted := newQueuedTestRuntime(channel, store, nil)
	if err := restarted.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(700 * time.Millisecond)
	if err := restarted.Stop(); err != nil {
		t.Fatal(err)
	}
	if got := channel.sentCount(); got != 1 {
		t.Fatalf("completed event replayed after restart: sent=%d", got)
	}
}

func TestRuntimeBackfillsMissedHistoryIntoDurableQueue(t *testing.T) {
	store := newMemoryInboundEventStore()
	watermark := time.Now().Add(-10 * time.Minute).Unix()
	store.sessions = []HistorySession{{Kind: EventKindGroup, ID: "123", LastEventTime: watermark}}
	channel := newQueueTestChannel()
	channel.responses["get_group_list"] = map[string]any{"items": []any{map[string]any{"group_id": int64(123)}}}
	channel.responses["get_group_msg_history"] = map[string]any{"messages": []any{
		historyTestMessage(900, watermark-1, "Diana 已处理的旧水位"),
		historyTestMessage(901, watermark+1, "Diana 重启时漏掉的消息"),
	}}
	provider := &sequenceLLMProvider{replies: []string{`{"action":"none","prompt":""}`, "补回成功"}}
	runtime := newQueuedTestRuntime(channel, store, provider)
	if err := runtime.backfillInboundHistory(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	if store.hasEvent("group:123:900") {
		t.Fatal("event older than the persisted watermark was queued")
	}
	if !store.hasEvent("group:123:901") {
		t.Fatal("missed history was not added to the durable queue")
	}
	if channel.sentCount() != 0 {
		t.Fatal("backfill should enqueue before workers process the message")
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForCondition(t, 3*time.Second, func() bool {
		return channel.sentCount() == 1 && store.isDone("group:123:901")
	})
	if err := runtime.Stop(); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeDrainsPendingWhileHistoryBackfillIsSlow(t *testing.T) {
	store := newMemoryInboundEventStore()
	store.sessions = []HistorySession{{Kind: EventKindGroup, ID: "123", LastEventTime: 100}}
	event := queuedDirectTestEvent("pending-before-restart", time.Now().Unix())
	if _, inserted, err := store.EnqueueInboundEvent(context.Background(), sessionKey(event), event); err != nil || !inserted {
		t.Fatalf("enqueue inserted=%v err=%v", inserted, err)
	}
	channel := newBlockingHistoryChannel()
	provider := &sequenceLLMProvider{replies: []string{`{"action":"none","prompt":""}`, "队列先恢复"}}
	runtime := newQueuedTestRuntime(channel, store, provider)
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	go func() { _ = runtime.backfillInboundHistory(context.Background(), store) }()
	waitForSignal(t, channel.historyStarted)
	waitForCondition(t, 2*time.Second, func() bool { return channel.sentCount() == 1 })
	close(channel.releaseHistory)
	if err := runtime.Stop(); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeRecoversProcessingMessageWithinReplayWindowAfterRestart(t *testing.T) {
	store := newMemoryInboundEventStore()
	event := queuedDirectTestEvent("old-processing", time.Now().Add(-90*time.Minute).Unix())
	if _, inserted, err := store.EnqueueInboundEvent(context.Background(), sessionKey(event), event); err != nil || !inserted {
		t.Fatalf("enqueue inserted=%v err=%v", inserted, err)
	}
	if _, ok, err := store.ClaimNextInboundEvent(context.Background(), "dead-runtime", time.Now().Add(time.Hour)); err != nil || !ok {
		t.Fatalf("pre-restart claim ok=%v err=%v", ok, err)
	}
	channel := newQueueTestChannel()
	runtime := newQueuedTestRuntime(channel, store, &sequenceLLMProvider{replies: []string{`{"action":"none","prompt":""}`, "旧消息恢复成功"}})
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForCondition(t, 3*time.Second, func() bool {
		return channel.sentCount() == 1 && store.isDone("group:123:old-processing")
	})
	if err := runtime.Stop(); err != nil {
		t.Fatal(err)
	}
	if count, _ := store.PendingInboundCount(context.Background()); count != 0 {
		t.Fatalf("pending after restart recovery=%d, want 0", count)
	}
}

func TestRuntimeDoesNotReplyBeyondInboundReplayWindow(t *testing.T) {
	channel := newQueueTestChannel()
	runtime := newQueuedTestRuntime(channel, newMemoryInboundEventStore(), &sequenceLLMProvider{replies: []string{"不应调用"}})
	event := queuedDirectTestEvent("expired", time.Now().Add(-InboundReplayWindow-time.Minute).Unix())
	outcome, err := runtime.processInboundQueueItem(context.Background(), InboundQueueItem{Event: event})
	if err != nil {
		t.Fatal(err)
	}
	if outcome != "ignored_stale" {
		t.Fatalf("outcome=%q, want ignored_stale", outcome)
	}
	if channel.sentCount() != 0 {
		t.Fatal("message older than the replay window triggered a reply")
	}
}

func TestReplyToBotRemainsAnUnconditionalTrigger(t *testing.T) {
	runtime := NewRuntime(BotConfig{BotQQ: "42"}, nilChannel{}, NewPluginManager(), nil, nil, nil, nil)
	event := MessageEvent{
		Kind:      EventKindGroup,
		GroupID:   "123",
		UserID:    "10001",
		MessageID: "reply-1",
		Quoted:    &QuotedMessage{MessageID: "bot-1", UserID: "42"},
		Segments:  []MessageSegment{{Type: "text", Data: map[string]string{"text": "再说一下"}}},
	}
	event = runtime.enrichReplyReference(context.Background(), event)
	if !event.ToMe || !runtime.shouldHandleChat(event, "再说一下") {
		t.Fatal("replying to the bot must bypass the passive router")
	}
}

func TestPassiveReplyRouterUsesStrictSemanticTimeout(t *testing.T) {
	if got := passiveReplyRouteTimeout(BotConfig{RequestTimeout: 5 * time.Minute}); got != semanticRouteTimeout {
		t.Fatalf("route timeout = %s, want %s", got, semanticRouteTimeout)
	}
	if got := passiveReplyRouteTimeout(BotConfig{RequestTimeout: 3 * time.Minute}); got != semanticRouteTimeout {
		t.Fatalf("route timeout = %s, want %s", got, semanticRouteTimeout)
	}
	if got := passiveReplyRouteTimeout(BotConfig{RequestTimeout: 8 * time.Second}); got != 8*time.Second {
		t.Fatalf("short configured timeout = %s, want 8s", got)
	}
}

func TestRuntimeAssignsInboundPriorities(t *testing.T) {
	runtime := NewRuntime(BotConfig{BotQQ: "42"}, nilChannel{}, NewDefaultPluginManager(), nil, nil, nil, nil)
	tests := []struct {
		name  string
		event MessageEvent
		want  int
	}{
		{
			name: "direct trigger",
			event: MessageEvent{Kind: EventKindGroup, GroupID: "123", UserID: "1", ToMe: true,
				Segments: []MessageSegment{{Type: "text", Data: map[string]string{"text": "帮我看看"}}}},
			want: InboundPriorityTriggered,
		},
		{
			name: "quoted reply",
			event: MessageEvent{Kind: EventKindGroup, GroupID: "123", UserID: "1", Quoted: &QuotedMessage{MessageID: "9", UserID: "2"},
				Segments: []MessageSegment{{Type: "text", Data: map[string]string{"text": "接着说"}}}},
			want: InboundPriorityReply,
		},
		{
			name: "resolver",
			event: MessageEvent{Kind: EventKindGroup, GroupID: "123", UserID: "1",
				Segments: []MessageSegment{{Type: "text", Data: map[string]string{"text": "https://www.bilibili.com/video/BV1Gc7K6UEgz/"}}}},
			want: InboundPriorityResolver,
		},
		{
			name: "ordinary chat",
			event: MessageEvent{Kind: EventKindGroup, GroupID: "123", UserID: "1",
				Segments: []MessageSegment{{Type: "text", Data: map[string]string{"text": "今天天气还行"}}}},
			want: InboundPriorityNormal,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := runtime.inboundPriority(tt.event); got != tt.want {
				t.Fatalf("priority=%d, want %d", got, tt.want)
			}
		})
	}
}

func TestPassiveReplyRouterRetriesTransientErrorOnce(t *testing.T) {
	store := &stubLLMProfileStore{set: llm.ProfileSet{
		ActiveID: "cheap-primary",
		Profiles: []llm.Profile{
			{ID: "cheap-primary", Group: "cheap", Config: llm.ProviderConfig{Provider: llm.ProviderOpenAICompatible, Model: "cheap-primary"}},
		},
	}}
	provider := &countingErrorLLMProvider{err: errors.New("502 Bad Gateway")}
	runtime := NewRuntime(BotConfig{BotQQ: "42", PassiveReplyChance: 1}, nilChannel{}, NewPluginManager(), store, nil, nil, nil)
	var configuredModels []string
	runtime.SetLLMProviderConfigFactory(func(cfg llm.ProviderConfig) (LLMProvider, error) {
		configuredModels = append(configuredModels, cfg.Model)
		return provider, nil
	})
	event := MessageEvent{
		Kind:       EventKindGroup,
		GroupID:    "123",
		UserID:     "1",
		MessageID:  "passive-1",
		RawMessage: "这个问题该怎么处理？",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "这个问题该怎么处理？"}}},
	}
	if runtime.shouldHandlePassiveReply(context.Background(), event, event.RawMessage) {
		t.Fatal("failed passive route unexpectedly allowed a reply")
	}
	if provider.calls != 2 {
		t.Fatalf("provider calls=%d, want initial request plus one retry", provider.calls)
	}
	if len(configuredModels) != 1 || configuredModels[0] != "cheap-primary" {
		t.Fatalf("configured models=%#v, want only cheap-primary", configuredModels)
	}
}

func TestMainLLMProviderRetriesTimeoutOnce(t *testing.T) {
	store := &stubLLMProfileStore{set: llm.ProfileSet{
		ActiveID: "main",
		Profiles: []llm.Profile{
			{ID: "main", Group: "default", Config: llm.ProviderConfig{Provider: llm.ProviderOpenAICompatible, Model: "main"}},
		},
	}}
	provider := &countingErrorLLMProvider{err: fmt.Errorf("request failed: %w", context.DeadlineExceeded)}
	runtime := NewRuntime(BotConfig{}, nilChannel{}, NewPluginManager(), store, nil, nil, nil)
	runtime.SetLLMProviderConfigFactory(func(llm.ProviderConfig) (LLMProvider, error) {
		return provider, nil
	})
	_, err := runtime.runLLMProvider(context.Background(), func(client LLMProvider) (string, error) {
		_, runErr := client.Generate(context.Background(), llm.GenerateRequest{})
		return "", runErr
	})
	if err == nil {
		t.Fatal("timeout retry unexpectedly succeeded")
	}
	if provider.calls != 2 {
		t.Fatalf("provider calls=%d, want initial request plus one retry", provider.calls)
	}
}

func TestDefaultBotConcurrencyIsEight(t *testing.T) {
	if got := (BotConfig{}).WithDefaults().MaxBotConcurrency; got != 8 {
		t.Fatalf("MaxBotConcurrency=%d, want 8", got)
	}
}

func newQueuedTestRuntime(channel Channel, store InboundEventStore, provider LLMProvider) *Runtime {
	var factory LLMProviderFactory
	if provider != nil {
		factory = func() (LLMProvider, error) { return provider, nil }
	}
	runtime := NewRuntime(BotConfig{Enabled: true, BotQQ: "42", GroupTriggers: []string{"Diana"}}, channel, NewPluginManager(), nil, nil, nil, factory)
	runtime.SetInboundEventStore(store)
	return runtime
}

func queuedDirectTestEvent(messageID string, eventTime int64) MessageEvent {
	return MessageEvent{
		Kind:       EventKindGroup,
		Time:       eventTime,
		SelfID:     "42",
		GroupID:    "123",
		UserID:     "10001",
		MessageID:  messageID,
		RawMessage: "Diana 帮我看看",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "Diana 帮我看看"}}},
	}
}

func historyTestMessage(messageID int64, eventTime int64, text string) map[string]any {
	return map[string]any{
		"time":         eventTime,
		"self_id":      int64(42),
		"message_type": "group",
		"group_id":     int64(123),
		"user_id":      int64(10001),
		"message_id":   messageID,
		"message_seq":  messageID,
		"raw_message":  text,
		"message":      []any{map[string]any{"type": "text", "data": map[string]any{"text": text}}},
	}
}

type memoryInboundRecord struct {
	item       InboundQueueItem
	state      string
	leaseOwner string
}

type countingErrorLLMProvider struct {
	calls int
	err   error
}

func (p *countingErrorLLMProvider) Generate(context.Context, llm.GenerateRequest) (*llm.GenerateResponse, error) {
	p.calls++
	return nil, p.err
}

type memoryInboundEventStore struct {
	mu       sync.Mutex
	records  map[string]*memoryInboundRecord
	order    []string
	sessions []HistorySession
}

func newMemoryInboundEventStore() *memoryInboundEventStore {
	return &memoryInboundEventStore{records: map[string]*memoryInboundRecord{}}
}

func (s *memoryInboundEventStore) EnqueueInboundEvent(_ context.Context, session string, event MessageEvent, priorities ...int) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := session + ":" + event.MessageID
	if _, ok := s.records[id]; ok {
		return id, false, nil
	}
	priority := InboundPriorityNormal
	if len(priorities) > 0 {
		priority = priorities[0]
	}
	s.records[id] = &memoryInboundRecord{item: InboundQueueItem{ID: id, Session: session, Event: event, Priority: priority}, state: "pending"}
	s.order = append(s.order, id)
	return id, true, nil
}

func (s *memoryInboundEventStore) ClaimNextInboundEvent(_ context.Context, leaseOwner string, _ time.Time, groupConcurrency ...int) (InboundQueueItem, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	groupLimit := 1
	if len(groupConcurrency) > 0 && groupConcurrency[0] > 0 {
		groupLimit = groupConcurrency[0]
	}
	selectedID := ""
	for _, id := range s.order {
		record := s.records[id]
		if record.state != "pending" {
			continue
		}
		limit := 1
		if record.item.Event.Kind == EventKindGroup {
			limit = groupLimit
		}
		active := 0
		for _, candidate := range s.records {
			if candidate.state == "processing" && candidate.item.Session == record.item.Session {
				active++
			}
		}
		if active >= limit {
			continue
		}
		if selectedID == "" || record.item.Priority > s.records[selectedID].item.Priority {
			selectedID = id
		}
	}
	if selectedID == "" {
		return InboundQueueItem{}, false, nil
	}
	record := s.records[selectedID]
	record.state = "processing"
	record.leaseOwner = leaseOwner
	record.item.Attempts++
	return record.item, true, nil
}

func (s *memoryInboundEventStore) CompleteInboundEvent(_ context.Context, id string, leaseOwner string, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if record := s.records[id]; record != nil && record.state == "processing" && record.leaseOwner == leaseOwner {
		record.state = "done"
		record.leaseOwner = ""
	}
	return nil
}

func (s *memoryInboundEventStore) RetryInboundEvent(_ context.Context, id string, leaseOwner string, _ time.Time, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if record := s.records[id]; record != nil && record.state == "processing" && record.leaseOwner == leaseOwner {
		record.state = "pending"
		record.leaseOwner = ""
	}
	return nil
}

func (s *memoryInboundEventStore) ReleaseInboundLeases(_ context.Context, leaseOwner string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, record := range s.records {
		if record.state == "processing" && (leaseOwner == "" || record.leaseOwner == leaseOwner) {
			record.state = "pending"
			record.leaseOwner = ""
		}
	}
	return nil
}

func (s *memoryInboundEventStore) PendingInboundCount(context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, record := range s.records {
		if record.state != "done" {
			count++
		}
	}
	return count, nil
}

func (s *memoryInboundEventStore) GroupHistoryWatermark(context.Context, string) (int64, bool, error) {
	return 0, false, nil
}

func (s *memoryInboundEventStore) ListHistorySessions(context.Context) ([]HistorySession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]HistorySession(nil), s.sessions...), nil
}

func (s *memoryInboundEventStore) hasEvent(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.records[id]
	return ok
}

func (s *memoryInboundEventStore) isDone(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.records[id]
	return record != nil && record.state == "done"
}

type queueTestChannel struct {
	mu        sync.Mutex
	connected bool
	sent      []OutgoingMessage
	responses map[string]map[string]any
}

func newQueueTestChannel() *queueTestChannel {
	return &queueTestChannel{
		connected: true,
		responses: map[string]map[string]any{
			"get_group_list":         {"items": []any{}},
			"get_recent_contact":     {"items": []any{}},
			"get_group_msg_history":  {"messages": []any{}},
			"get_friend_msg_history": {"messages": []any{}},
		},
	}
}

func (c *queueTestChannel) Connect(ctx context.Context, _ EventHandler) error {
	<-ctx.Done()
	return ctx.Err()
}

func (c *queueTestChannel) Send(_ context.Context, msg OutgoingMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, msg)
	return nil
}

func (c *queueTestChannel) CallAPI(_ context.Context, action string, _ map[string]any) (map[string]any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if response, ok := c.responses[action]; ok {
		return response, nil
	}
	return map[string]any{}, nil
}

func (c *queueTestChannel) Status() ChannelStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return ChannelStatus{Connected: c.connected, SelfID: "42"}
}

func (c *queueTestChannel) Close() error { return nil }

func (c *queueTestChannel) sentCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.sent)
}

type blockingHistoryChannel struct {
	*queueTestChannel
	historyStarted chan struct{}
	releaseHistory chan struct{}
	startOnce      sync.Once
}

func newBlockingHistoryChannel() *blockingHistoryChannel {
	return &blockingHistoryChannel{
		queueTestChannel: newQueueTestChannel(),
		historyStarted:   make(chan struct{}),
		releaseHistory:   make(chan struct{}),
	}
}

func (c *blockingHistoryChannel) CallAPI(ctx context.Context, action string, params map[string]any) (map[string]any, error) {
	if action == "get_group_msg_history" {
		c.startOnce.Do(func() { close(c.historyStarted) })
		select {
		case <-c.releaseHistory:
			return map[string]any{"messages": []any{}}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return c.queueTestChannel.CallAPI(ctx, action, params)
}
