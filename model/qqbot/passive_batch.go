package qqbot

import (
	"context"
	"errors"
	"strings"
	"time"
)

const (
	defaultPassiveReplyBatchWindow  = 5 * time.Second
	defaultPassiveReplyBatchMaxWait = 10 * time.Second
	passiveReplyBatchMaxItems       = 20
	passiveReplyDecisionMaxItems    = 3
	passiveReplyDecisionWindow      = 15 * time.Second
	passiveReplyMaxReroutes         = 1
)

var errPassiveReplySuperseded = errors.New("qqbot: passive reply superseded by newer candidates")

type passiveReplyCandidate struct {
	Event      MessageEvent
	Text       string
	QueuedAt   time.Time
	Generation uint64
}

type passiveReplyBatch struct {
	items      []passiveReplyCandidate
	startedAt  time.Time
	generation uint64
	timer      *time.Timer
	processing bool
}

type passiveReplyRunContextKey struct{}

type passiveReplyRunContext struct {
	key              string
	generation       uint64
	allowSuperseding bool
}

type passiveReplyTurnContextKey struct{}

func withPassiveReplyRunContext(ctx context.Context, key string, generation uint64, allowSuperseding bool) context.Context {
	return context.WithValue(ctx, passiveReplyRunContextKey{}, passiveReplyRunContext{
		key:              key,
		generation:       generation,
		allowSuperseding: allowSuperseding,
	})
}

func passiveReplyRunFromContext(ctx context.Context) (passiveReplyRunContext, bool) {
	if ctx == nil {
		return passiveReplyRunContext{}, false
	}
	run, ok := ctx.Value(passiveReplyRunContextKey{}).(passiveReplyRunContext)
	return run, ok
}

func withPassiveReplyTurnContext(ctx context.Context, candidates []passiveReplyCandidate) context.Context {
	if len(candidates) == 0 {
		return ctx
	}
	return context.WithValue(ctx, passiveReplyTurnContextKey{}, append([]passiveReplyCandidate(nil), candidates...))
}

func passiveReplyTurnFromContext(ctx context.Context) []passiveReplyCandidate {
	if ctx == nil {
		return nil
	}
	candidates, _ := ctx.Value(passiveReplyTurnContextKey{}).([]passiveReplyCandidate)
	return candidates
}

// enqueuePassiveReply keeps passive routing off the inbound workers. Messages
// have already been persisted and remembered when they enter this buffer.
func (r *Runtime) enqueuePassiveReply(event MessageEvent, text string) bool {
	r.mu.RLock()
	ctx := r.runCtx
	running := r.running
	r.mu.RUnlock()
	if !running || ctx == nil || event.Kind != EventKindGroup || strings.TrimSpace(event.GroupID) == "" {
		return false
	}

	key := sessionKey(event)
	now := time.Now()
	r.passiveBatchMu.Lock()
	batch := r.passiveBatches[key]
	if batch == nil {
		batch = &passiveReplyBatch{startedAt: now}
		r.passiveBatches[key] = batch
	}
	batch.generation++
	batch.items = append(batch.items, passiveReplyCandidate{
		Event:      event,
		Text:       text,
		QueuedAt:   now,
		Generation: batch.generation,
	})
	if len(batch.items) > passiveReplyBatchMaxItems {
		batch.items = batch.items[len(batch.items)-passiveReplyBatchMaxItems:]
	}
	if batch.processing {
		r.passiveBatchMu.Unlock()
		return true
	}
	r.schedulePassiveReplyBatchLocked(ctx, key, batch, now)
	r.passiveBatchMu.Unlock()
	return true
}

func (r *Runtime) schedulePassiveReplyBatchLocked(ctx context.Context, key string, batch *passiveReplyBatch, now time.Time) {
	if batch.timer != nil {
		batch.timer.Stop()
	}
	generation := batch.generation
	wait := r.passiveBatchWindow
	remaining := r.passiveBatchMaxWait - now.Sub(batch.startedAt)
	if remaining < wait {
		wait = remaining
	}
	if wait < 0 {
		wait = 0
	}
	batch.timer = time.AfterFunc(wait, func() {
		r.flushPassiveReplyBatch(ctx, key, generation)
	})
}

func (r *Runtime) flushPassiveReplyBatch(ctx context.Context, key string, generation uint64) {
	r.passiveBatchMu.Lock()
	batch := r.passiveBatches[key]
	if batch == nil || batch.generation != generation || batch.processing {
		r.passiveBatchMu.Unlock()
		return
	}
	batch.processing = true
	if batch.timer != nil {
		batch.timer.Stop()
		batch.timer = nil
	}
	r.passiveBatchMu.Unlock()

	reroutes := 0
	for {
		items, currentGeneration, ok := r.passiveReplyBatchSnapshot(key)
		if !ok || len(items) == 0 || ctx.Err() != nil {
			return
		}
		eligible := items[:0]
		for _, candidate := range items {
			if restriction, blocked := r.activeReplySuppression(candidate.Event, time.Now()); blocked {
				r.recordReplySuppressionBlocked(candidate.Event, restriction)
				continue
			}
			eligible = append(eligible, candidate)
		}
		items = eligible
		if len(items) == 0 {
			r.finishPassiveReplyBatch(ctx, key, currentGeneration)
			return
		}

		event, text, turn, allowed := r.routePassiveReplyBatch(ctx, items)
		changed, newer := r.passiveReplyBatchChanged(key, currentGeneration)
		if changed {
			if newer != nil && reroutes < passiveReplyMaxReroutes {
				r.recordPassiveReplySuperseded(ctx, event, newer.Event, "after_route")
				reroutes++
				continue
			}
			if newer == nil {
				return
			}
		}
		if !allowed || ctx.Err() != nil {
			r.finishPassiveReplyBatch(ctx, key, currentGeneration)
			return
		}
		if restriction, blocked := r.activeReplySuppression(event, time.Now()); blocked {
			r.recordReplySuppressionBlocked(event, restriction)
			r.finishPassiveReplyBatch(ctx, key, currentGeneration)
			return
		}

		replyCtx := withPassiveReplyRunContext(ctx, key, currentGeneration, reroutes < passiveReplyMaxReroutes)
		replyCtx = withPassiveReplyTurnContext(replyCtx, turn)
		outcome, err := func() (string, error) {
			select {
			case r.sem <- struct{}{}:
				r.incActive(1)
				defer func() {
					<-r.sem
					r.incActive(-1)
				}()
			case <-replyCtx.Done():
				return "", replyCtx.Err()
			}
			return r.replyAndRecord(replyCtx, event, text, "replied_passive_batch")
		}()
		if errors.Is(err, errPassiveReplySuperseded) && reroutes < passiveReplyMaxReroutes {
			reroutes++
			continue
		}
		r.finishPassiveReplyBatch(ctx, key, currentGeneration)
		if err != nil || outcome != "replied_passive_batch" || ctx.Err() != nil {
			return
		}
		evaluation, before, evaluated := r.evaluateRelationshipUpdate(ctx, event, text, true)
		if !evaluated {
			return
		}
		after, stored := before, true
		if delta := evaluation.effectiveDelta(); delta != 0 {
			after, stored = r.applyUserFavorabilityDelta(event, delta)
		}
		if stored {
			r.recordRelationshipEvaluation(ctx, event, before, after, evaluation)
		}
		return
	}
}

func (r *Runtime) passiveReplyBatchSnapshot(key string) ([]passiveReplyCandidate, uint64, bool) {
	r.passiveBatchMu.Lock()
	defer r.passiveBatchMu.Unlock()
	batch := r.passiveBatches[key]
	if batch == nil || !batch.processing {
		return nil, 0, false
	}
	items := append([]passiveReplyCandidate(nil), batch.items...)
	return passiveReplyDecisionCandidates(items), batch.generation, true
}

func passiveReplyDecisionCandidates(items []passiveReplyCandidate) []passiveReplyCandidate {
	if len(items) > passiveReplyDecisionMaxItems {
		items = items[len(items)-passiveReplyDecisionMaxItems:]
	}
	if len(items) < 2 {
		return append([]passiveReplyCandidate(nil), items...)
	}
	latestAt := passiveReplyCandidateTime(items[len(items)-1])
	if latestAt.IsZero() {
		return append([]passiveReplyCandidate(nil), items...)
	}
	first := 0
	for first < len(items)-1 {
		candidateAt := passiveReplyCandidateTime(items[first])
		if candidateAt.IsZero() || latestAt.Sub(candidateAt) <= passiveReplyDecisionWindow {
			break
		}
		first++
	}
	return append([]passiveReplyCandidate(nil), items[first:]...)
}

func passiveReplyCandidateTime(candidate passiveReplyCandidate) time.Time {
	if !candidate.QueuedAt.IsZero() {
		return candidate.QueuedAt
	}
	if candidate.Event.Time > 0 {
		return time.Unix(candidate.Event.Time, 0)
	}
	return time.Time{}
}

func (r *Runtime) passiveReplyBatchChanged(key string, generation uint64) (bool, *passiveReplyCandidate) {
	r.passiveBatchMu.Lock()
	defer r.passiveBatchMu.Unlock()
	batch := r.passiveBatches[key]
	if batch == nil || !batch.processing {
		return true, nil
	}
	if batch.generation <= generation {
		return false, nil
	}
	for i := len(batch.items) - 1; i >= 0; i-- {
		if batch.items[i].Generation > generation {
			newer := batch.items[i]
			return true, &newer
		}
	}
	return true, nil
}

func (r *Runtime) finishPassiveReplyBatch(ctx context.Context, key string, throughGeneration uint64) {
	now := time.Now()
	r.passiveBatchMu.Lock()
	defer r.passiveBatchMu.Unlock()
	batch := r.passiveBatches[key]
	if batch == nil {
		return
	}
	pending := make([]passiveReplyCandidate, 0, len(batch.items))
	for _, item := range batch.items {
		if item.Generation > throughGeneration {
			pending = append(pending, item)
		}
	}
	if len(pending) == 0 {
		delete(r.passiveBatches, key)
		return
	}
	batch.items = pending
	batch.processing = false
	batch.startedAt = passiveReplyCandidateTime(pending[0])
	if batch.startedAt.IsZero() {
		batch.startedAt = now
	}
	r.schedulePassiveReplyBatchLocked(ctx, key, batch, now)
}

func (r *Runtime) cancelPassiveReplyBatch(event MessageEvent) {
	if event.Kind != EventKindGroup || strings.TrimSpace(event.GroupID) == "" {
		return
	}
	r.passiveBatchMu.Lock()
	if batch := r.passiveBatches[sessionKey(event)]; batch != nil {
		if batch.timer != nil {
			batch.timer.Stop()
		}
		delete(r.passiveBatches, sessionKey(event))
	}
	r.passiveBatchMu.Unlock()
}

func (r *Runtime) clearPassiveReplyBatches() {
	r.passiveBatchMu.Lock()
	for key, batch := range r.passiveBatches {
		if batch.timer != nil {
			batch.timer.Stop()
		}
		delete(r.passiveBatches, key)
	}
	r.passiveBatchMu.Unlock()
}
