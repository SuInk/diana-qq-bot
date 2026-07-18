package qqbot

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"strings"
	"sync"
	"time"

	"diana-qq-bot/model/applog"
)

var (
	errOutboundSend            = errors.New("qqbot: outbound send failed")
	errGroupSendUnavailable    = errors.New("qqbot: group send target is unavailable")
	errOutboundDeliveryDropped = errors.New("qqbot: outbound delivery dropped after backoff")
)

const (
	defaultOutboundInitialDelay  = time.Minute
	defaultOutboundMaximumDelay  = 15 * time.Minute
	defaultOutboundFailureWindow = 30 * time.Minute
	defaultOutboundDropCooldown  = 30 * time.Minute
)

type outboundDeliveryPolicy struct {
	InitialDelay  time.Duration
	MaximumDelay  time.Duration
	FailureWindow time.Duration
	DropCooldown  time.Duration
}

type outboundDeliveryPolicyContextKey struct{}
type continuousOutboundDeliveryContextKey struct{}
type alternativeOutboundDeliveryContextKey struct{}

type outboundBackoffChannel interface {
	OutboundBackoffEnabled() bool
}

type groupOutboundDelivery struct {
	mu           sync.Mutex
	failures     int
	firstFailure time.Time
	nextAttempt  time.Time
	dropUntil    time.Time
	lastError    string
}

type unavailableGroupSend struct {
	BlockedAt time.Time
	Reason    string
}

type outboundSendError struct {
	GroupID          string
	Cause            error
	GroupUnavailable bool
	DeliveryDropped  bool
}

func (e *outboundSendError) Error() string {
	if e == nil || e.Cause == nil {
		return errOutboundSend.Error()
	}
	return e.Cause.Error()
}

func (e *outboundSendError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (e *outboundSendError) Is(target error) bool {
	if target == errOutboundSend {
		return true
	}
	if e == nil {
		return false
	}
	return (e.GroupUnavailable && target == errGroupSendUnavailable) ||
		(e.DeliveryDropped && target == errOutboundDeliveryDropped)
}

func defaultOutboundDeliveryPolicy() outboundDeliveryPolicy {
	return outboundDeliveryPolicy{
		InitialDelay:  defaultOutboundInitialDelay,
		MaximumDelay:  defaultOutboundMaximumDelay,
		FailureWindow: defaultOutboundFailureWindow,
		DropCooldown:  defaultOutboundDropCooldown,
	}
}

func withOutboundDeliveryPolicy(ctx context.Context, policy outboundDeliveryPolicy) context.Context {
	return context.WithValue(ctx, outboundDeliveryPolicyContextKey{}, policy)
}

func outboundDeliveryPolicyFromContext(ctx context.Context) outboundDeliveryPolicy {
	policy, _ := ctx.Value(outboundDeliveryPolicyContextKey{}).(outboundDeliveryPolicy)
	defaults := defaultOutboundDeliveryPolicy()
	if policy.InitialDelay <= 0 {
		policy.InitialDelay = defaults.InitialDelay
	}
	if policy.MaximumDelay <= 0 {
		policy.MaximumDelay = defaults.MaximumDelay
	}
	if policy.MaximumDelay < policy.InitialDelay {
		policy.MaximumDelay = policy.InitialDelay
	}
	if policy.FailureWindow <= 0 {
		policy.FailureWindow = defaults.FailureWindow
	}
	if policy.DropCooldown <= 0 {
		policy.DropCooldown = defaults.DropCooldown
	}
	return policy
}

func withContinuousOutboundDelivery(ctx context.Context) context.Context {
	return context.WithValue(ctx, continuousOutboundDeliveryContextKey{}, true)
}

func continuousOutboundDelivery(ctx context.Context) bool {
	continuous, _ := ctx.Value(continuousOutboundDeliveryContextKey{}).(bool)
	return continuous
}

func withAlternativeOutboundDelivery(ctx context.Context) context.Context {
	return context.WithValue(ctx, alternativeOutboundDeliveryContextKey{}, true)
}

func alternativeOutboundDelivery(ctx context.Context) bool {
	alternative, _ := ctx.Value(alternativeOutboundDeliveryContextKey{}).(bool)
	return alternative
}

func (r *Runtime) outboundBackoffEnabled() bool {
	r.mu.RLock()
	channel := r.channel
	r.mu.RUnlock()
	capable, ok := channel.(outboundBackoffChannel)
	return ok && capable.OutboundBackoffEnabled()
}

func (r *Runtime) groupOutboundDelivery(groupID string) *groupOutboundDelivery {
	groupID = strings.TrimSpace(groupID)
	r.outboundDeliveryMu.Lock()
	defer r.outboundDeliveryMu.Unlock()
	if r.outboundDeliveries == nil {
		r.outboundDeliveries = make(map[string]*groupOutboundDelivery)
	}
	gate := r.outboundDeliveries[groupID]
	if gate == nil {
		gate = &groupOutboundDelivery{}
		r.outboundDeliveries[groupID] = gate
	}
	return gate
}

func (r *Runtime) executeOutboundCall(
	ctx context.Context,
	event MessageEvent,
	action string,
	call func(context.Context) (map[string]any, error),
) (map[string]any, error) {
	if blockedErr := r.blockedGroupSendError(event); blockedErr != nil {
		return nil, blockedErr
	}
	groupID := strings.TrimSpace(event.GroupID)
	if event.Kind != EventKindGroup || groupID == "" || !r.outboundBackoffEnabled() {
		result, err := call(ctx)
		return result, r.wrapOutboundSendError(ctx, event, err)
	}

	gate := r.groupOutboundDelivery(groupID)
	gate.mu.Lock()
	defer gate.mu.Unlock()
	policy := outboundDeliveryPolicyFromContext(ctx)
	for {
		if err := ctx.Err(); err != nil {
			if gate.failures == 0 || r.runtimeContextStopped() {
				return nil, err
			}
			if strings.TrimSpace(gate.lastError) == "" {
				gate.lastError = err.Error()
			}
			r.enterOutboundDropCooldown(event, action, gate, policy, time.Now())
			return nil, droppedOutboundSendError(groupID, gate.lastError)
		}
		now := time.Now()
		if !gate.dropUntil.IsZero() {
			if now.Before(gate.dropUntil) {
				return nil, droppedOutboundSendError(groupID, gate.lastError)
			}
			gate.reset()
		}
		if !gate.firstFailure.IsZero() && now.Sub(gate.firstFailure) >= policy.FailureWindow {
			r.enterOutboundDropCooldown(event, action, gate, policy, now)
			return nil, droppedOutboundSendError(groupID, gate.lastError)
		}
		if wait := time.Until(gate.nextAttempt); wait > 0 {
			if err := waitForOutboundRetry(ctx, wait); err != nil {
				if r.runtimeContextStopped() {
					return nil, err
				}
				if strings.TrimSpace(gate.lastError) == "" {
					gate.lastError = err.Error()
				}
				r.enterOutboundDropCooldown(event, action, gate, policy, time.Now())
				return nil, droppedOutboundSendError(groupID, gate.lastError)
			}
		}

		result, err := call(ctx)
		if err == nil {
			failures := gate.failures
			gate.reset()
			if failures > 0 {
				r.recordOutboundDeliveryRecovered(event, action, failures)
			}
			return result, nil
		}
		wrapped := r.wrapOutboundSendError(ctx, event, err)
		if errors.Is(wrapped, errGroupSendUnavailable) {
			return nil, wrapped
		}

		now = time.Now()
		if gate.firstFailure.IsZero() {
			gate.firstFailure = now
		}
		gate.failures++
		gate.lastError = err.Error()
		if now.Sub(gate.firstFailure) >= policy.FailureWindow {
			r.enterOutboundDropCooldown(event, action, gate, policy, now)
			return nil, droppedOutboundSendError(groupID, gate.lastError)
		}
		delay := outboundBackoffDelay(event, action, gate.failures, policy)
		gate.nextAttempt = now.Add(delay)
		r.recordOutboundDeliveryBackoff(event, action, gate.failures, delay, err)
		// Some resolver/forward paths have a known alternate representation. Let
		// that alternate enter the same gate after one failure; all ordinary sends
		// retry the exact payload until success or the group failure window expires.
		if alternativeOutboundDelivery(ctx) && !continuousOutboundDelivery(ctx) {
			return nil, wrapped
		}
	}
}

func (r *Runtime) runtimeContextStopped() bool {
	r.mu.RLock()
	runCtx := r.runCtx
	r.mu.RUnlock()
	return runCtx != nil && runCtx.Err() != nil
}

func (gate *groupOutboundDelivery) reset() {
	gate.failures = 0
	gate.firstFailure = time.Time{}
	gate.nextAttempt = time.Time{}
	gate.dropUntil = time.Time{}
	gate.lastError = ""
}

func waitForOutboundRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func outboundBackoffDelay(event MessageEvent, action string, failures int, policy outboundDeliveryPolicy) time.Duration {
	if failures < 1 {
		failures = 1
	}
	delay := policy.InitialDelay
	for attempt := 1; attempt < failures && delay < policy.MaximumDelay; attempt++ {
		delay *= 2
		if delay > policy.MaximumDelay {
			delay = policy.MaximumDelay
		}
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(sessionKey(event)))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(action))
	_, _ = hash.Write([]byte(fmt.Sprintf("\x00%d", failures)))
	// Deterministic 100%-120% jitter prevents several groups from retrying
	// together without making any retry earlier than its configured interval.
	permille := int64(1000 + hash.Sum32()%201)
	jittered := time.Duration(int64(delay) * permille / 1000)
	if jittered > policy.MaximumDelay {
		return policy.MaximumDelay
	}
	return jittered
}

func droppedOutboundSendError(groupID, cause string) error {
	cause = strings.TrimSpace(cause)
	if cause == "" {
		cause = "delivery did not recover before the retry window expired"
	}
	return &outboundSendError{
		GroupID:         groupID,
		Cause:           fmt.Errorf("qqbot: dropped outbound delivery for group %s: %s", groupID, cause),
		DeliveryDropped: true,
	}
}

func (r *Runtime) enterOutboundDropCooldown(event MessageEvent, action string, gate *groupOutboundDelivery, policy outboundDeliveryPolicy, now time.Time) {
	gate.nextAttempt = time.Time{}
	gate.dropUntil = now.Add(policy.DropCooldown)
	writer := r.appLogWriter()
	if writer == nil {
		return
	}
	logCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = writer.AppendLog(logCtx, applog.Entry{
		Kind:    applog.KindError,
		Level:   applog.LevelError,
		Action:  "qqbot.outbound_delivery_dropped",
		Message: "群消息连续发送失败，已丢弃等待结果并进入冷却",
		Detail:  gate.lastError,
		Actor:   qqEventActor(event),
		Target:  event.GroupID,
		Metadata: map[string]any{
			"group_id":               event.GroupID,
			"message_id":             event.MessageID,
			"onebot_action":          action,
			"failure_count":          gate.failures,
			"failure_window_seconds": int(policy.FailureWindow / time.Second),
			"drop_cooldown_seconds":  int(policy.DropCooldown / time.Second),
		},
	})
}

func (r *Runtime) recordOutboundDeliveryBackoff(event MessageEvent, action string, failures int, delay time.Duration, cause error) {
	writer := r.appLogWriter()
	if writer == nil {
		return
	}
	logCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = writer.AppendLog(logCtx, applog.Entry{
		Kind:    applog.KindOperation,
		Level:   applog.LevelInfo,
		Action:  "qqbot.outbound_delivery_backoff",
		Message: "群消息发送失败，已按指数退避等待下次尝试",
		Detail:  cause.Error(),
		Actor:   qqEventActor(event),
		Target:  event.GroupID,
		Metadata: map[string]any{
			"group_id":       event.GroupID,
			"message_id":     event.MessageID,
			"onebot_action":  action,
			"failure_count":  failures,
			"retry_delay_ms": delay.Milliseconds(),
		},
	})
}

func (r *Runtime) recordOutboundDeliveryRecovered(event MessageEvent, action string, failures int) {
	writer := r.appLogWriter()
	if writer == nil {
		return
	}
	logCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = writer.AppendLog(logCtx, applog.Entry{
		Kind:    applog.KindOperation,
		Level:   applog.LevelInfo,
		Action:  "qqbot.outbound_delivery_recovered",
		Message: "群消息发送已恢复，后续消息将连续放行",
		Actor:   qqEventActor(event),
		Target:  event.GroupID,
		Metadata: map[string]any{
			"group_id":      event.GroupID,
			"message_id":    event.MessageID,
			"onebot_action": action,
			"failures":      failures,
		},
	})
}

func (r *Runtime) wrapOutboundSendError(ctx context.Context, event MessageEvent, err error) error {
	if err == nil || errors.Is(err, errOutboundSend) {
		return err
	}
	groupID := strings.TrimSpace(event.GroupID)
	unavailable := false
	if event.Kind == EventKindGroup && groupID != "" {
		// NapCat error wording is not a stable protocol. Confirm terminal group
		// failures against its structured group list instead of matching text.
		unavailable, _ = r.groupMissingFromOneBot(groupID)
	}
	wrapped := &outboundSendError{
		GroupID:          groupID,
		Cause:            err,
		GroupUnavailable: unavailable,
	}
	if unavailable {
		r.markGroupSendUnavailable(ctx, event, err)
	}
	return wrapped
}

func (r *Runtime) groupMissingFromOneBot(groupID string) (missing bool, verified bool) {
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		return false, false
	}
	r.mu.RLock()
	channel := r.channel
	r.mu.RUnlock()
	if channel == nil {
		return false, false
	}
	checkCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	data, err := channel.CallAPI(checkCtx, "get_group_list", map[string]any{"no_cache": true})
	if err != nil {
		return false, false
	}
	items, recognized := structuredGroupListItems(data)
	if !recognized {
		return false, false
	}
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if strings.TrimSpace(stringFromAny(item["group_id"])) == groupID {
			return false, true
		}
	}
	return true, true
}

func structuredGroupListItems(data map[string]any) ([]any, bool) {
	for _, key := range []string{"items", "list", "groups"} {
		value, exists := data[key]
		if !exists {
			continue
		}
		switch items := value.(type) {
		case []any:
			return items, true
		case []map[string]any:
			out := make([]any, 0, len(items))
			for _, item := range items {
				out = append(out, item)
			}
			return out, true
		}
	}
	return nil, false
}

func (r *Runtime) blockedGroupSendError(event MessageEvent) error {
	if event.Kind != EventKindGroup {
		return nil
	}
	groupID := strings.TrimSpace(event.GroupID)
	if groupID == "" {
		return nil
	}
	r.unavailableGroupMu.RLock()
	state, blocked := r.unavailableGroups[groupID]
	r.unavailableGroupMu.RUnlock()
	if !blocked {
		return nil
	}
	reason := strings.TrimSpace(state.Reason)
	if reason == "" {
		reason = "previous OneBot response reported that the bot cannot send to this group"
	}
	return &outboundSendError{
		GroupID:          groupID,
		Cause:            fmt.Errorf("qqbot: sending to group %s is disabled: %s", groupID, reason),
		GroupUnavailable: true,
	}
}

func (r *Runtime) markGroupSendUnavailable(ctx context.Context, event MessageEvent, cause error) {
	groupID := strings.TrimSpace(event.GroupID)
	if groupID == "" {
		return
	}
	now := time.Now()
	reason := strings.TrimSpace(cause.Error())
	if len([]rune(reason)) > 500 {
		reason = string([]rune(reason)[:500])
	}
	r.unavailableGroupMu.Lock()
	if r.unavailableGroups == nil {
		r.unavailableGroups = make(map[string]unavailableGroupSend)
	}
	_, alreadyBlocked := r.unavailableGroups[groupID]
	if !alreadyBlocked {
		r.unavailableGroups[groupID] = unavailableGroupSend{BlockedAt: now, Reason: reason}
	}
	r.unavailableGroupMu.Unlock()

	r.cancelPassiveReplyBatch(MessageEvent{Kind: EventKindGroup, GroupID: groupID})
	if alreadyBlocked {
		return
	}
	writer := r.appLogWriter()
	if writer == nil {
		return
	}
	logCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = writer.AppendLog(logCtx, applog.Entry{
		Kind:    applog.KindError,
		Level:   applog.LevelError,
		Action:  "qqbot.group_send_disabled",
		Message: "群发送目标失效，已停止向该群发送后续消息",
		Detail:  reason,
		Actor:   qqEventActor(event),
		Target:  groupID,
		Metadata: map[string]any{
			"group_id":   groupID,
			"message_id": event.MessageID,
			"blocked_at": now,
		},
	})
}

// ignoreUnavailableGroupEvent keeps persisted and already queued events from
// starting another LLM request after NapCat reports that the bot left a group.
// A genuinely newer live event proves that the bot has rejoined and clears it.
func (r *Runtime) ignoreUnavailableGroupEvent(event MessageEvent) bool {
	if event.Kind != EventKindGroup {
		return false
	}
	groupID := strings.TrimSpace(event.GroupID)
	if groupID == "" {
		return false
	}
	r.unavailableGroupMu.RLock()
	state, blocked := r.unavailableGroups[groupID]
	r.unavailableGroupMu.RUnlock()
	if !blocked {
		return false
	}
	if event.Time <= 0 || !time.Unix(event.Time, 0).After(state.BlockedAt) {
		return true
	}

	r.unavailableGroupMu.Lock()
	current, stillBlocked := r.unavailableGroups[groupID]
	if stillBlocked && current.BlockedAt.Equal(state.BlockedAt) {
		delete(r.unavailableGroups, groupID)
	}
	r.unavailableGroupMu.Unlock()
	return false
}
