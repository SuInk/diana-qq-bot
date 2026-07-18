package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"diana-qq-bot/model/qqbot"
)

const (
	inboundStatusPending    = "pending"
	inboundStatusProcessing = "processing"
	inboundStatusDone       = "done"
)

// EnqueueInboundEvent atomically records message history and makes a new event
// available to the durable worker queue. Existing history rows remain
// deduplicated so pre-queue chat history is never replayed after an upgrade.
func (s *SQLiteStore) EnqueueInboundEvent(ctx context.Context, session string, event qqbot.MessageEvent, priorities ...int) (string, bool, error) {
	if s == nil || s.db == nil {
		return "", false, errors.New("enqueue inbound event: sqlite store is not configured")
	}
	session = strings.TrimSpace(session)
	if session == "" {
		return "", false, errors.New("enqueue inbound event: session is required")
	}

	id, err := stableInboundEventID(session, event)
	if err != nil {
		return "", false, err
	}
	now := time.Now().UTC()
	priority := inboundPriorityValue(priorities)
	if event.Time <= 0 {
		event.Time = now.Unix()
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return "", false, fmt.Errorf("encode inbound event: %w", err)
	}
	text := strings.TrimSpace(qqbot.PlainText(event.Segments))
	if text == "" {
		text = strings.TrimSpace(event.RawMessage)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", false, fmt.Errorf("begin inbound enqueue: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	createdAt := now.Format(time.RFC3339Nano)
	historyResult, err := tx.ExecContext(ctx, `
INSERT OR IGNORE INTO message_events (id, session, kind, group_id, user_id, message_id, sender_name, event_time, text, payload, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, id, session, string(event.Kind), event.GroupID, event.UserID, event.MessageID, event.SenderName, event.Time, text, string(payload), createdAt)
	if err != nil {
		return "", false, fmt.Errorf("persist inbound message history: %w", err)
	}
	historyInserted, err := historyResult.RowsAffected()
	if err != nil {
		return "", false, fmt.Errorf("inspect inbound message history: %w", err)
	}
	if historyInserted == 0 {
		if _, err := tx.ExecContext(ctx, `
UPDATE message_events
SET kind = ?, group_id = ?, user_id = ?, message_id = ?, sender_name = ?,
    event_time = ?, text = ?, payload = ?, created_at = ?
WHERE id = ?
		`, string(event.Kind), event.GroupID, event.UserID, event.MessageID, event.SenderName, event.Time, text, string(payload), createdAt, id); err != nil {
			return "", false, fmt.Errorf("refresh inbound message history: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE inbound_events
SET priority = CASE WHEN priority < ? THEN ? ELSE priority END,
    payload = ?, updated_at = ?
WHERE id = ? AND status = ?
		`, priority, priority, string(payload), now.UnixNano(), id, inboundStatusPending); err != nil {
			return "", false, fmt.Errorf("refresh inbound queue priority: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return "", false, fmt.Errorf("commit duplicate inbound history: %w", err)
		}
		return id, false, nil
	}

	nowNanos := now.UnixNano()
	result, err := tx.ExecContext(ctx, `
INSERT OR IGNORE INTO inbound_events (
  id, session, kind, group_id, user_id, message_id, event_time, payload,
  priority, status, attempts, available_at, created_at, updated_at
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?)
`, id, session, string(event.Kind), event.GroupID, event.UserID, event.MessageID, event.Time, string(payload), priority, inboundStatusPending, nowNanos, nowNanos, nowNanos)
	if err != nil {
		return "", false, fmt.Errorf("enqueue inbound event: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return "", false, fmt.Errorf("inspect inbound enqueue: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", false, fmt.Errorf("commit inbound enqueue: %w", err)
	}
	return id, inserted > 0, nil
}

// ClaimNextInboundEvent atomically leases the highest-priority available event,
// preserving FIFO order within each priority. Expired processing leases are
// eligible for recovery by another worker.
func (s *SQLiteStore) ClaimNextInboundEvent(ctx context.Context, leaseOwner string, leaseUntil time.Time, groupConcurrency ...int) (qqbot.InboundQueueItem, bool, error) {
	if s == nil || s.db == nil {
		return qqbot.InboundQueueItem{}, false, errors.New("claim inbound event: sqlite store is not configured")
	}
	leaseOwner = strings.TrimSpace(leaseOwner)
	if leaseOwner == "" {
		return qqbot.InboundQueueItem{}, false, errors.New("claim inbound event: lease owner is required")
	}
	now := time.Now().UTC()
	if leaseUntil.IsZero() || !leaseUntil.After(now) {
		return qqbot.InboundQueueItem{}, false, errors.New("claim inbound event: lease must expire in the future")
	}
	groupLimit := inboundGroupConcurrencyValue(groupConcurrency)

	var item qqbot.InboundQueueItem
	var payload string
	err := s.db.QueryRowContext(ctx, `
WITH candidate AS (
  SELECT queued.id
  FROM inbound_events AS queued
  WHERE ((queued.status = ? AND queued.available_at <= ?)
     OR (queued.status = ? AND queued.lease_until IS NOT NULL AND queued.lease_until <= ?))
    AND (
      SELECT COUNT(*)
      FROM inbound_events AS active
      WHERE active.session = queued.session
        AND active.status = ?
        AND active.lease_until > ?
    ) < CASE WHEN queued.kind = ? THEN ? ELSE 1 END
  ORDER BY
    queued.priority DESC,
    queued.event_time ASC,
    queued.created_at ASC,
    queued.id ASC
  LIMIT 1
)
UPDATE inbound_events
SET status = ?,
    lease_owner = ?,
    lease_until = ?,
    attempts = attempts + 1,
    updated_at = ?
WHERE id = (SELECT id FROM candidate)
RETURNING id, session, payload, attempts, priority
`, inboundStatusPending, now.UnixNano(), inboundStatusProcessing, now.UnixNano(), inboundStatusProcessing, now.UnixNano(), string(qqbot.EventKindGroup), groupLimit,
		inboundStatusProcessing, leaseOwner, leaseUntil.UTC().UnixNano(), now.UnixNano()).Scan(
		&item.ID, &item.Session, &payload, &item.Attempts, &item.Priority,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return qqbot.InboundQueueItem{}, false, nil
		}
		return qqbot.InboundQueueItem{}, false, fmt.Errorf("claim inbound event: %w", err)
	}
	if err := json.Unmarshal([]byte(payload), &item.Event); err != nil {
		return qqbot.InboundQueueItem{}, false, fmt.Errorf("decode inbound event %q: %w", item.ID, err)
	}
	return item, true, nil
}

func inboundPriorityValue(values []int) int {
	if len(values) == 0 || values[0] < 0 {
		return qqbot.InboundPriorityNormal
	}
	return values[0]
}

func inboundGroupConcurrencyValue(values []int) int {
	if len(values) == 0 || values[0] <= 0 {
		return 1
	}
	return values[0]
}

// CompleteInboundEvent marks a leased event terminal without deleting its
// audit record.
func (s *SQLiteStore) CompleteInboundEvent(ctx context.Context, id string, leaseOwner string, outcome string) error {
	if s == nil || s.db == nil {
		return errors.New("complete inbound event: sqlite store is not configured")
	}
	id = strings.TrimSpace(id)
	leaseOwner = strings.TrimSpace(leaseOwner)
	if id == "" || leaseOwner == "" {
		return errors.New("complete inbound event: id and lease owner are required")
	}
	now := time.Now().UTC().UnixNano()
	result, err := s.db.ExecContext(ctx, `
UPDATE inbound_events
SET status = ?, outcome = ?, last_error = NULL,
    lease_owner = NULL, lease_until = NULL,
    completed_at = ?, updated_at = ?
WHERE id = ? AND status = ? AND lease_owner = ?
`, inboundStatusDone, strings.TrimSpace(outcome), now, now, id, inboundStatusProcessing, leaseOwner)
	if err != nil {
		return fmt.Errorf("complete inbound event %q: %w", id, err)
	}
	return requireInboundLeaseUpdate(result, "complete", id)
}

// RetryInboundEvent returns a leased event to the queue at the requested time.
func (s *SQLiteStore) RetryInboundEvent(ctx context.Context, id string, leaseOwner string, availableAt time.Time, lastError string) error {
	if s == nil || s.db == nil {
		return errors.New("retry inbound event: sqlite store is not configured")
	}
	id = strings.TrimSpace(id)
	leaseOwner = strings.TrimSpace(leaseOwner)
	if id == "" || leaseOwner == "" {
		return errors.New("retry inbound event: id and lease owner are required")
	}
	now := time.Now().UTC()
	if availableAt.IsZero() {
		availableAt = now
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE inbound_events
SET status = ?, available_at = ?, last_error = ?, outcome = NULL,
    lease_owner = NULL, lease_until = NULL, completed_at = NULL, updated_at = ?
WHERE id = ? AND status = ? AND lease_owner = ?
`, inboundStatusPending, availableAt.UTC().UnixNano(), strings.TrimSpace(lastError), now.UnixNano(), id, inboundStatusProcessing, leaseOwner)
	if err != nil {
		return fmt.Errorf("retry inbound event %q: %w", id, err)
	}
	return requireInboundLeaseUpdate(result, "retry", id)
}

// ReleaseInboundLeases immediately returns every lease held by one worker to
// the pending queue, for example during a graceful shutdown.
func (s *SQLiteStore) ReleaseInboundLeases(ctx context.Context, leaseOwner string) error {
	if s == nil || s.db == nil {
		return errors.New("release inbound leases: sqlite store is not configured")
	}
	leaseOwner = strings.TrimSpace(leaseOwner)
	now := time.Now().UTC().UnixNano()
	query := `
UPDATE inbound_events
SET status = ?, available_at = ?, lease_owner = NULL, lease_until = NULL, updated_at = ?
WHERE status = ?`
	args := []any{inboundStatusPending, now, now, inboundStatusProcessing}
	if leaseOwner != "" {
		query += ` AND lease_owner = ?`
		args = append(args, leaseOwner)
	}
	_, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("release inbound leases for %q: %w", leaseOwner, err)
	}
	return nil
}

// PendingInboundCount reports all non-terminal work, including currently
// leased events.
func (s *SQLiteStore) PendingInboundCount(ctx context.Context) (int, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("count pending inbound events: sqlite store is not configured")
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM inbound_events WHERE status IN (?, ?)
`, inboundStatusPending, inboundStatusProcessing).Scan(&count); err != nil {
		return 0, fmt.Errorf("count pending inbound events: %w", err)
	}
	return count, nil
}

// GroupHistoryWatermark returns the newest persisted event timestamp for one
// group, including history that predates the durable queue migration.
func (s *SQLiteStore) GroupHistoryWatermark(ctx context.Context, groupID string) (int64, bool, error) {
	if s == nil || s.db == nil {
		return 0, false, errors.New("load group history watermark: sqlite store is not configured")
	}
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		return 0, false, errors.New("load group history watermark: group id is required")
	}
	var watermark sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `
SELECT MAX(event_time)
FROM message_events
WHERE kind = ? AND group_id = ?
`, string(qqbot.EventKindGroup), groupID).Scan(&watermark); err != nil {
		return 0, false, fmt.Errorf("load group history watermark %q: %w", groupID, err)
	}
	if !watermark.Valid {
		return 0, false, nil
	}
	return watermark.Int64, true, nil
}

// ListHistorySessions returns each known group/private conversation and its
// latest persisted event time for reconnect backfill.
func (s *SQLiteStore) ListHistorySessions(ctx context.Context) ([]qqbot.HistorySession, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("list history sessions: sqlite store is not configured")
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT kind, session_id, MAX(event_time)
FROM (
  SELECT kind, group_id AS session_id, event_time
  FROM message_events
  WHERE kind = ? AND group_id IS NOT NULL AND group_id != ''
  UNION ALL
  SELECT kind, user_id AS session_id, event_time
  FROM message_events
  WHERE kind = ? AND user_id IS NOT NULL AND user_id != ''
)
GROUP BY kind, session_id
ORDER BY kind ASC, session_id ASC
`, string(qqbot.EventKindGroup), string(qqbot.EventKindPrivate))
	if err != nil {
		return nil, fmt.Errorf("list history sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	sessions := make([]qqbot.HistorySession, 0)
	for rows.Next() {
		var kind string
		var item qqbot.HistorySession
		if err := rows.Scan(&kind, &item.ID, &item.LastEventTime); err != nil {
			return nil, fmt.Errorf("scan history session: %w", err)
		}
		item.Kind = qqbot.EventKind(kind)
		sessions = append(sessions, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list history sessions: %w", err)
	}
	return sessions, nil
}

func stableInboundEventID(session string, event qqbot.MessageEvent) (string, error) {
	if strings.TrimSpace(event.MessageID) != "" {
		return persistedMessageID(session, event), nil
	}
	canonical, err := json.Marshal(struct {
		Session     string                 `json:"session"`
		Kind        qqbot.EventKind        `json:"kind"`
		SubType     string                 `json:"sub_type,omitempty"`
		Time        int64                  `json:"time,omitempty"`
		SelfID      string                 `json:"self_id,omitempty"`
		UserID      string                 `json:"user_id,omitempty"`
		OperatorID  string                 `json:"operator_id,omitempty"`
		GroupID     string                 `json:"group_id,omitempty"`
		MessageType string                 `json:"message_type,omitempty"`
		RawMessage  string                 `json:"raw_message,omitempty"`
		Segments    []qqbot.MessageSegment `json:"segments,omitempty"`
	}{
		Session:     session,
		Kind:        event.Kind,
		SubType:     event.SubType,
		Time:        event.Time,
		SelfID:      event.SelfID,
		UserID:      event.UserID,
		OperatorID:  event.OperatorID,
		GroupID:     event.GroupID,
		MessageType: event.MessageType,
		RawMessage:  event.RawMessage,
		Segments:    event.Segments,
	})
	if err != nil {
		return "", fmt.Errorf("encode inbound event identity: %w", err)
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func requireInboundLeaseUpdate(result sql.Result, action string, id string) error {
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s inbound event %q: inspect update: %w", action, id, err)
	}
	if updated == 0 {
		return fmt.Errorf("%s inbound event %q: lease is not held by this worker", action, id)
	}
	return nil
}
