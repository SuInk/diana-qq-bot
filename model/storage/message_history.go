package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"diana-qq-bot/model/qqbot"

	"github.com/google/uuid"
)

const (
	defaultMessageHistoryLimit = 20
	maxMessageHistoryLimit     = 200
)

// AppendMessageEvent persists a QQ message event for later context recovery.
func (s *SQLiteStore) AppendMessageEvent(ctx context.Context, session string, event qqbot.MessageEvent) error {
	if s == nil || s.db == nil {
		return nil
	}
	session = strings.TrimSpace(session)
	if session == "" {
		return nil
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	id := persistedMessageID(session, event)
	eventTime := event.Time
	if eventTime <= 0 {
		eventTime = time.Now().Unix()
	}
	text := strings.TrimSpace(qqbot.PlainText(event.Segments))
	if text == "" {
		text = strings.TrimSpace(event.RawMessage)
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO message_events (id, session, kind, group_id, user_id, message_id, sender_name, event_time, text, payload, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  kind=excluded.kind,
  group_id=excluded.group_id,
  user_id=excluded.user_id,
  message_id=excluded.message_id,
  sender_name=excluded.sender_name,
  event_time=excluded.event_time,
  text=excluded.text,
  payload=excluded.payload,
  created_at=excluded.created_at
`, id, session, string(event.Kind), event.GroupID, event.UserID, event.MessageID, event.SenderName, eventTime, text, string(payload), time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

// ListRecentMessageEvents returns recent message events in chronological order.
func (s *SQLiteStore) ListRecentMessageEvents(ctx context.Context, session string, limit int) ([]qqbot.MessageEvent, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	session = strings.TrimSpace(session)
	if session == "" {
		return nil, nil
	}
	limit = normalizeMessageHistoryLimit(limit)
	rows, err := s.db.QueryContext(ctx, `
SELECT payload
FROM message_events
WHERE session = ? AND kind != ?
ORDER BY event_time DESC, created_at DESC, id DESC
LIMIT ?
`, session, string(qqbot.EventKindNotice), limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	reversed := make([]qqbot.MessageEvent, 0, limit)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var event qqbot.MessageEvent
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			return nil, fmt.Errorf("decode message event: %w", err)
		}
		reversed = append(reversed, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	return reversed, nil
}

// ListMessageEventsBetween returns the complete persisted timeline inside a
// semantic time window. Callers are responsible for ranking a bounded set of
// candidates before sending anything to an LLM.
func (s *SQLiteStore) ListMessageEventsBetween(ctx context.Context, session string, fromTime, throughTime int64) ([]qqbot.MessageEvent, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	session = strings.TrimSpace(session)
	if session == "" {
		return nil, nil
	}
	if fromTime < 0 {
		fromTime = 0
	}
	if throughTime <= 0 {
		throughTime = time.Now().Unix()
	}
	if fromTime > throughTime {
		fromTime, throughTime = throughTime, fromTime
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT payload
FROM message_events
WHERE session = ?
  AND kind != ?
  AND event_time BETWEEN ? AND ?
ORDER BY event_time ASC, created_at ASC, id ASC
`, session, string(qqbot.EventKindNotice), fromTime, throughTime)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	events := make([]qqbot.MessageEvent, 0)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var event qqbot.MessageEvent
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			return nil, fmt.Errorf("decode message event: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

// FindMessageEvent returns the persisted non-notice message with the given OneBot message ID.
func (s *SQLiteStore) FindMessageEvent(ctx context.Context, session string, messageID string) (qqbot.MessageEvent, bool, error) {
	if s == nil || s.db == nil {
		return qqbot.MessageEvent{}, false, nil
	}
	session = strings.TrimSpace(session)
	messageID = strings.TrimSpace(messageID)
	if session == "" || messageID == "" {
		return qqbot.MessageEvent{}, false, nil
	}
	var raw string
	err := s.db.QueryRowContext(ctx, `
SELECT payload
FROM message_events
WHERE session = ? AND message_id = ? AND kind != ?
ORDER BY event_time DESC, created_at DESC, id DESC
LIMIT 1
`, session, messageID, string(qqbot.EventKindNotice)).Scan(&raw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return qqbot.MessageEvent{}, false, nil
		}
		return qqbot.MessageEvent{}, false, err
	}
	var event qqbot.MessageEvent
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		return qqbot.MessageEvent{}, false, fmt.Errorf("decode message event: %w", err)
	}
	return event, true, nil
}

// ListGroupRecallEvents returns every persisted group recall, newest first.
func (s *SQLiteStore) ListGroupRecallEvents(ctx context.Context, groupID string) ([]qqbot.MessageEvent, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT recall.payload,
       (SELECT original.event_time
        FROM message_events AS original
        WHERE original.session = recall.session
          AND original.message_id = recall.message_id
          AND original.kind != ?
        ORDER BY original.event_time DESC, original.created_at DESC, original.id DESC
        LIMIT 1) AS original_time
FROM message_events AS recall
WHERE recall.kind = ? AND recall.group_id = ? AND json_extract(recall.payload, '$.sub_type') = 'group_recall'
ORDER BY recall.event_time DESC, recall.created_at DESC, recall.id DESC
`, string(qqbot.EventKindNotice), string(qqbot.EventKindNotice), groupID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	events := make([]qqbot.MessageEvent, 0)
	for rows.Next() {
		var raw string
		var originalTime sql.NullInt64
		if err := rows.Scan(&raw, &originalTime); err != nil {
			return nil, err
		}
		var event qqbot.MessageEvent
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			return nil, fmt.Errorf("decode recall event: %w", err)
		}
		if event.OriginalTime == 0 && originalTime.Valid {
			event.OriginalTime = originalTime.Int64
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func persistedMessageID(session string, event qqbot.MessageEvent) string {
	if strings.TrimSpace(event.MessageID) != "" {
		if event.Kind == qqbot.EventKindNotice {
			return session + ":notice:" + strings.TrimSpace(event.SubType) + ":" + strings.TrimSpace(event.MessageID)
		}
		return session + ":" + strings.TrimSpace(event.MessageID)
	}
	return session + ":" + uuid.NewString()
}

func normalizeMessageHistoryLimit(limit int) int {
	if limit <= 0 {
		return defaultMessageHistoryLimit
	}
	if limit > maxMessageHistoryLimit {
		return maxMessageHistoryLimit
	}
	return limit
}
