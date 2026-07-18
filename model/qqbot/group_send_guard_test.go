package qqbot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestReplyAndRecordStopsAfterTerminalGroupSendFailure(t *testing.T) {
	channel := &failingOutboundChannel{
		err:      errors.New("opaque NapCat send failure"),
		groupIDs: []string{"123456", "20005"},
	}
	runtime := NewRuntime(BotConfig{OwnerID: "10001"}, channel, NewPluginManager(), nil, nil, nil, nil)
	event := MessageEvent{
		Kind:      EventKindGroup,
		GroupID:   "20006",
		UserID:    "10001",
		MessageID: "message-1",
		Time:      time.Now().Add(-time.Minute).Unix(),
	}

	outcome, err := runtime.replyAndRecord(context.Background(), event, "帮助", "replied")
	if err != nil {
		t.Fatalf("replyAndRecord() error = %v", err)
	}
	if outcome != "ignored_unavailable_group" {
		t.Fatalf("outcome = %q", outcome)
	}
	if got := channel.sendAttempts(); got != 1 {
		t.Fatalf("send attempts = %d, want 1", got)
	}

	if err := runtime.send(context.Background(), event, "不要再发错误提示"); !errors.Is(err, errGroupSendUnavailable) {
		t.Fatalf("blocked send error = %v", err)
	}
	if got := channel.sendAttempts(); got != 1 {
		t.Fatalf("blocked send reached OneBot; attempts = %d", got)
	}
	if err := runtime.blockedGroupSendError(MessageEvent{Kind: EventKindGroup, GroupID: "20005"}); err != nil {
		t.Fatalf("unrelated group was blocked: %v", err)
	}
	if got := channel.groupListAttempts(); got != 1 {
		t.Fatalf("group list checks = %d, want 1", got)
	}

	_, _, handled, ignoredOutcome := runtime.prepareMessageEvent(context.Background(), event)
	if handled || ignoredOutcome != "ignored_unavailable_group" {
		t.Fatalf("prepare handled=%v outcome=%q", handled, ignoredOutcome)
	}
}

func TestReplyAndRecordDoesNotSendFallbackAfterTransientSendFailure(t *testing.T) {
	channel := &failingOutboundChannel{
		err:      errors.New("发送失败，你已被移出该群，请重新加群。"),
		groupIDs: []string{"123456"},
	}
	runtime := NewRuntime(BotConfig{OwnerID: "10001"}, channel, NewPluginManager(), nil, nil, nil, nil)
	event := MessageEvent{Kind: EventKindGroup, GroupID: "123456", UserID: "10001", MessageID: "message-2"}

	outcome, err := runtime.replyAndRecord(context.Background(), event, "帮助", "replied")
	if outcome != "" || !errors.Is(err, errOutboundSend) {
		t.Fatalf("outcome=%q error=%v", outcome, err)
	}
	if errors.Is(err, errGroupSendUnavailable) {
		t.Fatalf("transient error marked group unavailable: %v", err)
	}
	if got := channel.sendAttempts(); got != 1 {
		t.Fatalf("send attempts = %d, want 1 (no immediate error fallback)", got)
	}
}

func TestUnavailableGroupClearsOnlyForNewerLiveEvent(t *testing.T) {
	channel := &failingOutboundChannel{err: errors.New("opaque send failure"), groupIDs: []string{"123456"}}
	runtime := NewRuntime(BotConfig{}, channel, NewPluginManager(), nil, nil, nil, nil)
	event := MessageEvent{Kind: EventKindGroup, GroupID: "20006", UserID: "10001", Time: time.Now().Add(-time.Minute).Unix()}
	if err := runtime.send(context.Background(), event, "first"); !errors.Is(err, errGroupSendUnavailable) {
		t.Fatalf("first send error = %v", err)
	}
	if !runtime.ignoreUnavailableGroupEvent(event) {
		t.Fatal("older queued event unexpectedly cleared unavailable group")
	}

	live := event
	live.Time = time.Now().Add(2 * time.Second).Unix()
	if runtime.ignoreUnavailableGroupEvent(live) {
		t.Fatal("newer live event did not clear unavailable group")
	}
	if err := runtime.blockedGroupSendError(live); err != nil {
		t.Fatalf("group remained blocked: %v", err)
	}
}

func TestGroupSendFailureRequiresVerifiedGroupList(t *testing.T) {
	channel := &failingOutboundChannel{
		err:          errors.New("opaque send failure"),
		groupListErr: errors.New("group list temporarily unavailable"),
	}
	runtime := NewRuntime(BotConfig{}, channel, NewPluginManager(), nil, nil, nil, nil)
	event := MessageEvent{Kind: EventKindGroup, GroupID: "20006", UserID: "10001"}
	err := runtime.send(context.Background(), event, "test")
	if !errors.Is(err, errOutboundSend) || errors.Is(err, errGroupSendUnavailable) {
		t.Fatalf("unverified send error = %v", err)
	}
	if blockedErr := runtime.blockedGroupSendError(event); blockedErr != nil {
		t.Fatalf("failed membership check blocked group: %v", blockedErr)
	}
}

func TestDefaultOutboundBackoffUsesLongIntervals(t *testing.T) {
	policy := defaultOutboundDeliveryPolicy()
	if policy.InitialDelay != time.Minute || policy.MaximumDelay != 15*time.Minute {
		t.Fatalf("retry delays = %s..%s", policy.InitialDelay, policy.MaximumDelay)
	}
	if policy.FailureWindow != 30*time.Minute || policy.DropCooldown != 30*time.Minute {
		t.Fatalf("failure window = %s, cooldown = %s", policy.FailureWindow, policy.DropCooldown)
	}
	event := MessageEvent{Kind: EventKindGroup, GroupID: "123456"}
	for failures, minimum := range map[int]time.Duration{
		1: time.Minute,
		2: 2 * time.Minute,
		3: 4 * time.Minute,
		4: 8 * time.Minute,
		5: 15 * time.Minute,
	} {
		delay := outboundBackoffDelay(event, "send_group_msg", failures, policy)
		if delay < minimum || delay > policy.MaximumDelay {
			t.Fatalf("failure %d delay = %s, want %s..%s", failures, delay, minimum, policy.MaximumDelay)
		}
	}
}

func TestGroupOutboundBackoffRetriesExactSendAndRecovers(t *testing.T) {
	channel := newScriptedBackoffChannel("123456", "20005")
	channel.failuresRemaining["123456"] = 2
	runtime := NewRuntime(BotConfig{}, channel, NewPluginManager(), nil, nil, nil, nil)
	ctx := withOutboundDeliveryPolicy(context.Background(), fastOutboundDeliveryPolicy())
	event := MessageEvent{Kind: EventKindGroup, GroupID: "123456", UserID: "10001", MessageID: "retry-1"}

	if err := runtime.send(ctx, event, "same payload"); err != nil {
		t.Fatalf("send() error = %v", err)
	}
	if got := channel.attemptTexts("123456"); len(got) != 3 || got[0] != "same payload" || got[1] != "same payload" || got[2] != "same payload" {
		t.Fatalf("attempted payloads = %#v", got)
	}
	if got := channel.groupListAttempts(); got != 2 {
		t.Fatalf("group list checks = %d, want 2", got)
	}

	if err := runtime.send(ctx, event, "next payload"); err != nil {
		t.Fatalf("next send() error = %v", err)
	}
	if got := channel.attemptTexts("123456"); len(got) != 4 || got[3] != "next payload" {
		t.Fatalf("recovered attempts = %#v", got)
	}
}

func TestGroupOutboundBackoffContinuesRemainingChunksAfterFirstSuccess(t *testing.T) {
	channel := newScriptedBackoffChannel("123456")
	channel.attemptErrors = []error{nil, errors.New("second chunk failed once"), nil}
	runtime := NewRuntime(BotConfig{ForwardReplyThreshold: 5000}, channel, NewPluginManager(), nil, nil, nil, nil)
	ctx := withOutboundDeliveryPolicy(context.Background(), fastOutboundDeliveryPolicy())
	event := MessageEvent{Kind: EventKindGroup, GroupID: "123456", UserID: "10001", MessageID: "chunks-1"}

	if _, err := runtime.sendWithMessageIDs(ctx, event, "first<botbr>second"); err != nil {
		t.Fatalf("sendWithMessageIDs() error = %v", err)
	}
	if got := channel.attemptTexts("123456"); len(got) != 3 || got[0] != "first" || got[1] != "second" || got[2] != "second" {
		t.Fatalf("chunk attempts = %#v", got)
	}
}

func TestGroupOutboundBackoffDropsAfterFailureWindow(t *testing.T) {
	channel := newScriptedBackoffChannel("123456", "20005")
	channel.alwaysFail["123456"] = true
	runtime := NewRuntime(BotConfig{}, channel, NewPluginManager(), nil, nil, nil, nil)
	ctx := withOutboundDeliveryPolicy(context.Background(), fastOutboundDeliveryPolicy())
	failedEvent := MessageEvent{Kind: EventKindGroup, GroupID: "123456", UserID: "10001", MessageID: "drop-1"}

	err := runtime.send(ctx, failedEvent, "eventually dropped")
	if !errors.Is(err, errOutboundDeliveryDropped) {
		t.Fatalf("send error = %v", err)
	}
	attemptsBeforeCooldown := len(channel.attemptTexts("123456"))
	if attemptsBeforeCooldown < 2 {
		t.Fatalf("attempts before drop = %d", attemptsBeforeCooldown)
	}
	if err := runtime.send(ctx, failedEvent, "drop immediately"); !errors.Is(err, errOutboundDeliveryDropped) {
		t.Fatalf("cooldown send error = %v", err)
	}
	if got := len(channel.attemptTexts("123456")); got != attemptsBeforeCooldown {
		t.Fatalf("cooldown reached NapCat: attempts=%d want=%d", got, attemptsBeforeCooldown)
	}

	otherEvent := MessageEvent{Kind: EventKindGroup, GroupID: "20005", UserID: "10002", MessageID: "other-1"}
	if err := runtime.send(ctx, otherEvent, "other group still works"); err != nil {
		t.Fatalf("other group send error = %v", err)
	}
}

func TestGroupOutboundBackoffDropsWhenReplyDeadlineInterruptsRetry(t *testing.T) {
	channel := newScriptedBackoffChannel("123456")
	channel.alwaysFail["123456"] = true
	runtime := NewRuntime(BotConfig{}, channel, NewPluginManager(), nil, nil, nil, nil)
	policy := fastOutboundDeliveryPolicy()
	policy.InitialDelay = 100 * time.Millisecond
	policy.MaximumDelay = 100 * time.Millisecond
	policy.FailureWindow = time.Second
	ctx, cancel := context.WithTimeout(withOutboundDeliveryPolicy(context.Background(), policy), 5*time.Millisecond)
	defer cancel()

	err := runtime.send(ctx, MessageEvent{Kind: EventKindGroup, GroupID: "123456"}, "drop without regenerating")
	if !errors.Is(err, errOutboundDeliveryDropped) {
		t.Fatalf("send error = %v", err)
	}
}

func TestResolverAlternativeUsesSameBackoffGate(t *testing.T) {
	tempDir := t.TempDir()
	videoPath := filepath.Join(tempDir, "video.mp4")
	if err := os.WriteFile(videoPath, []byte("fake video"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	channel := newScriptedBackoffChannel("123456")
	channel.attemptErrors = []error{errors.New("NapCat rejected direct video")}
	runtime := NewRuntime(BotConfig{}, channel, NewPluginManager(), nil, nil, nil, nil)
	runtime.SetLocalMediaSharer(&recordingLocalMediaSharer{url: "http://127.0.0.1:18080/media/token"})
	ctx := withOutboundDeliveryPolicy(context.Background(), fastOutboundDeliveryPolicy())
	event := MessageEvent{Kind: EventKindGroup, GroupID: "123456", UserID: "10001", MessageID: "video-1"}

	if err := runtime.sendDirectPluginResponse(ctx, event, "链接解析结果", nil, []string{videoPath}); err != nil {
		t.Fatalf("sendDirectPluginResponse() error = %v", err)
	}
	messages := channel.attemptMessages("123456")
	if len(messages) != 3 {
		t.Fatalf("attempted messages = %#v", messages)
	}
	if len(messages[0].VideoURLs) != 1 || len(messages[1].VideoURLs) != 0 || messages[1].Text != "链接解析结果" {
		t.Fatalf("media fallback attempts = %#v", messages)
	}
	if got := channel.apiActionCount("upload_group_file"); got != 1 {
		t.Fatalf("upload_group_file calls = %d, want 1", got)
	}
}

func fastOutboundDeliveryPolicy() outboundDeliveryPolicy {
	return outboundDeliveryPolicy{
		InitialDelay:  time.Millisecond,
		MaximumDelay:  2 * time.Millisecond,
		FailureWindow: 8 * time.Millisecond,
		DropCooldown:  50 * time.Millisecond,
	}
}

type failingOutboundChannel struct {
	mu           sync.Mutex
	attempts     int
	listAttempts int
	err          error
	groupListErr error
	groupIDs     []string
}

func (c *failingOutboundChannel) Connect(context.Context, EventHandler) error { return nil }

func (c *failingOutboundChannel) Send(context.Context, OutgoingMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.attempts++
	return c.err
}

func (c *failingOutboundChannel) CallAPI(_ context.Context, action string, _ map[string]any) (map[string]any, error) {
	if action != "get_group_list" {
		return nil, c.err
	}
	c.mu.Lock()
	c.listAttempts++
	listErr := c.groupListErr
	groupIDs := append([]string(nil), c.groupIDs...)
	c.mu.Unlock()
	if listErr != nil {
		return nil, listErr
	}
	items := make([]any, 0, len(groupIDs))
	for _, groupID := range groupIDs {
		items = append(items, map[string]any{"group_id": groupID})
	}
	return map[string]any{"items": items}, nil
}

func (c *failingOutboundChannel) Status() ChannelStatus {
	return ChannelStatus{Connected: true, SelfID: "10000"}
}

func (c *failingOutboundChannel) Close() error { return nil }

func (c *failingOutboundChannel) sendAttempts() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.attempts
}

func (c *failingOutboundChannel) groupListAttempts() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.listAttempts
}

type scriptedBackoffChannel struct {
	mu                sync.Mutex
	groups            []string
	failuresRemaining map[string]int
	alwaysFail        map[string]bool
	attemptErrors     []error
	attempts          map[string][]string
	messages          map[string][]OutgoingMessage
	apiActions        []string
	listAttempts      int
}

func newScriptedBackoffChannel(groupIDs ...string) *scriptedBackoffChannel {
	return &scriptedBackoffChannel{
		groups:            append([]string(nil), groupIDs...),
		failuresRemaining: make(map[string]int),
		alwaysFail:        make(map[string]bool),
		attempts:          make(map[string][]string),
		messages:          make(map[string][]OutgoingMessage),
	}
}

func (c *scriptedBackoffChannel) OutboundBackoffEnabled() bool { return true }
func (c *scriptedBackoffChannel) Connect(context.Context, EventHandler) error {
	return nil
}

func (c *scriptedBackoffChannel) Send(_ context.Context, msg OutgoingMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	groupID := msg.GroupID
	c.attempts[groupID] = append(c.attempts[groupID], msg.Text)
	c.messages[groupID] = append(c.messages[groupID], msg)
	if len(c.attemptErrors) > 0 {
		err := c.attemptErrors[0]
		c.attemptErrors = c.attemptErrors[1:]
		return err
	}
	if c.alwaysFail[groupID] {
		return errors.New("opaque send failure")
	}
	if c.failuresRemaining[groupID] > 0 {
		c.failuresRemaining[groupID]--
		return errors.New("opaque send failure")
	}
	return nil
}

func (c *scriptedBackoffChannel) CallAPI(_ context.Context, action string, _ map[string]any) (map[string]any, error) {
	c.mu.Lock()
	c.apiActions = append(c.apiActions, action)
	c.mu.Unlock()
	if action != "get_group_list" {
		return map[string]any{"message_id": int64(1)}, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.listAttempts++
	items := make([]any, 0, len(c.groups))
	for _, groupID := range c.groups {
		items = append(items, map[string]any{"group_id": groupID})
	}
	return map[string]any{"items": items}, nil
}

func (c *scriptedBackoffChannel) Status() ChannelStatus {
	return ChannelStatus{Connected: true, SelfID: "10000"}
}
func (c *scriptedBackoffChannel) Close() error { return nil }

func (c *scriptedBackoffChannel) attemptTexts(groupID string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.attempts[groupID]...)
}

func (c *scriptedBackoffChannel) groupListAttempts() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.listAttempts
}

func (c *scriptedBackoffChannel) attemptMessages(groupID string) []OutgoingMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]OutgoingMessage(nil), c.messages[groupID]...)
}

func (c *scriptedBackoffChannel) apiActionCount(action string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	count := 0
	for _, attempted := range c.apiActions {
		if attempted == action {
			count++
		}
	}
	return count
}
