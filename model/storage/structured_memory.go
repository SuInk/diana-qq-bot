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
	"unicode"

	"diana-qq-bot/model/qqbot"

	"github.com/google/uuid"
)

const (
	defaultStructuredMemoryCandidates = 80
	maxStructuredMemoryCandidates     = 200
	maxMemoryCandidatesPerWrite       = 8
)

type legacyMemoryProfile struct {
	UserID      string
	DisplayName string
	Memories    []qqbot.UserMemoryItem
}

// backfillLegacyUserMemories keeps pre-layered installations from losing all
// continuity on upgrade. Legacy snippets remain low-confidence, session-only
// episodes with a finite lifetime; they are never promoted to stable facts.
func (s *SQLiteStore) backfillLegacyUserMemories() error {
	rows, err := s.db.Query(`SELECT user_id, display_name, memories FROM user_profiles`)
	if err != nil {
		return fmt.Errorf("load legacy user memories: %w", err)
	}
	profiles := make([]legacyMemoryProfile, 0)
	for rows.Next() {
		var profile legacyMemoryProfile
		var displayName sql.NullString
		var raw string
		if err := rows.Scan(&profile.UserID, &displayName, &raw); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan legacy user memories: %w", err)
		}
		profile.DisplayName = displayName.String
		if strings.TrimSpace(raw) == "" || json.Unmarshal([]byte(raw), &profile.Memories) != nil {
			continue
		}
		profiles = append(profiles, profile)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate legacy user memories: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close legacy user memories: %w", err)
	}
	if len(profiles) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UTC()
	for _, profile := range profiles {
		for _, legacy := range profile.Memories {
			content := truncateMemoryText(strings.Join(strings.Fields(legacy.Text), " "), 320)
			if content == "" {
				continue
			}
			sourceTime := legacy.At.UTC()
			if sourceTime.IsZero() {
				sourceTime = now
			}
			session := "private:" + strings.TrimSpace(profile.UserID)
			if strings.TrimSpace(legacy.GroupID) != "" {
				session = "group:" + strings.TrimSpace(legacy.GroupID)
			}
			sourceMessageID := strings.TrimSpace(legacy.MessageID)
			if sourceMessageID == "" {
				sourceMessageID = "legacy:" + shortMemoryHash(profile.UserID+"|"+session+"|"+sourceTime.Format(time.RFC3339Nano)+"|"+content)
			}
			key := "legacy.event." + shortMemoryHash(session+"|"+sourceMessageID+"|"+content)
			id := uuid.NewSHA1(uuid.NameSpaceURL, []byte("diana-memory|"+profile.UserID+"|"+key)).String()
			expiresAt := sourceTime.Add(90 * 24 * time.Hour)
			_, err := tx.Exec(`
INSERT OR IGNORE INTO memory_items
  (id, scope_key, subject_user_id, subject_name, memory_key, kind, topic, entity,
   content, evidence, source_type, source_session, source_group_id, source_message_id,
   source_event_time, confidence, importance, visibility, sensitive, expires_at,
   last_verified_at, version, supersedes_id, status, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, 'episode', '历史聊天片段', '', ?, ?, 'inferred', ?, ?, ?, ?, 0.55, 0.35,
        'session', 1, ?, ?, 1, NULL, 'active', ?, ?)
`, id, session, profile.UserID, profile.DisplayName, key, content, content, session, legacy.GroupID,
				sourceMessageID, sourceTime.Unix(), expiresAt.Unix(), sourceTime.Unix(), now.Unix(), now.Unix())
			if err != nil {
				return fmt.Errorf("backfill legacy memory item: %w", err)
			}
			if _, err := tx.Exec(`
INSERT OR IGNORE INTO memory_sources
  (memory_id, source_session, source_group_id, source_message_id, source_event_time, source_type, evidence, created_at)
VALUES (?, ?, ?, ?, ?, 'inferred', ?, ?)
`, id, session, legacy.GroupID, sourceMessageID, sourceTime.Unix(), content, now.Unix()); err != nil {
				return fmt.Errorf("backfill legacy memory source: %w", err)
			}
		}
	}
	return tx.Commit()
}

func (s *SQLiteStore) EnqueueMemoryJob(ctx context.Context, payload qqbot.MemoryJobPayload) (string, bool, error) {
	if s == nil || s.db == nil {
		return "", false, nil
	}
	payload.Session = strings.TrimSpace(payload.Session)
	if payload.Session == "" || (payload.Kind != qqbot.MemoryJobEvent && payload.Kind != qqbot.MemoryJobSummary) {
		return "", false, fmt.Errorf("invalid memory job payload")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", false, err
	}
	id := memoryJobID(payload)
	now := time.Now().UTC().UnixNano()
	result, err := s.db.ExecContext(ctx, `
INSERT OR IGNORE INTO memory_jobs
  (id, kind, session, payload, status, attempts, available_at, created_at, updated_at)
VALUES (?, ?, ?, ?, 'pending', 0, ?, ?, ?)
`, id, string(payload.Kind), payload.Session, string(raw), now, now, now)
	if err != nil {
		return "", false, err
	}
	rows, err := result.RowsAffected()
	return id, rows > 0, err
}

func (s *SQLiteStore) ClaimNextMemoryJob(ctx context.Context, leaseOwner string, leaseUntil time.Time) (qqbot.MemoryJob, bool, error) {
	if s == nil || s.db == nil {
		return qqbot.MemoryJob{}, false, nil
	}
	leaseOwner = strings.TrimSpace(leaseOwner)
	if leaseOwner == "" {
		return qqbot.MemoryJob{}, false, fmt.Errorf("memory lease owner is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return qqbot.MemoryJob{}, false, err
	}
	defer func() { _ = tx.Rollback() }()

	var id, raw string
	var attempts int
	err = tx.QueryRowContext(ctx, `
SELECT id, payload, attempts
FROM memory_jobs
WHERE status = 'pending' AND available_at <= ?
ORDER BY available_at, created_at, id
LIMIT 1
`, time.Now().UTC().UnixNano()).Scan(&id, &raw, &attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return qqbot.MemoryJob{}, false, nil
	}
	if err != nil {
		return qqbot.MemoryJob{}, false, err
	}
	result, err := tx.ExecContext(ctx, `
UPDATE memory_jobs
SET status = 'processing', attempts = attempts + 1, lease_owner = ?, lease_until = ?, updated_at = ?
WHERE id = ? AND status = 'pending'
`, leaseOwner, leaseUntil.UTC().UnixNano(), time.Now().UTC().UnixNano(), id)
	if err != nil {
		return qqbot.MemoryJob{}, false, err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return qqbot.MemoryJob{}, false, err
	}
	var payload qqbot.MemoryJobPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return qqbot.MemoryJob{}, false, fmt.Errorf("decode memory job: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return qqbot.MemoryJob{}, false, err
	}
	return qqbot.MemoryJob{ID: id, Payload: payload, Attempts: attempts + 1}, true, nil
}

func (s *SQLiteStore) CompleteMemoryJob(ctx context.Context, id string, leaseOwner string) error {
	if s == nil || s.db == nil {
		return nil
	}
	now := time.Now().UTC().UnixNano()
	_, err := s.db.ExecContext(ctx, `
UPDATE memory_jobs
SET status = 'done', lease_owner = NULL, lease_until = NULL, last_error = NULL,
    completed_at = ?, updated_at = ?
WHERE id = ? AND status = 'processing' AND lease_owner = ?
`, now, now, strings.TrimSpace(id), strings.TrimSpace(leaseOwner))
	return err
}

func (s *SQLiteStore) RetryMemoryJob(ctx context.Context, id string, leaseOwner string, availableAt time.Time, lastError string) error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
UPDATE memory_jobs
SET status = 'pending', available_at = ?, lease_owner = NULL, lease_until = NULL,
    last_error = ?, updated_at = ?
WHERE id = ? AND status = 'processing' AND lease_owner = ?
`, availableAt.UTC().UnixNano(), truncateMemoryText(lastError, 800), time.Now().UTC().UnixNano(), strings.TrimSpace(id), strings.TrimSpace(leaseOwner))
	return err
}

func (s *SQLiteStore) ReleaseMemoryJobLeases(ctx context.Context, leaseOwner string) error {
	if s == nil || s.db == nil {
		return nil
	}
	leaseOwner = strings.TrimSpace(leaseOwner)
	query := `
UPDATE memory_jobs
SET status = 'pending', available_at = ?, lease_owner = NULL, lease_until = NULL, updated_at = ?
WHERE status = 'processing'`
	args := []any{time.Now().UTC().UnixNano(), time.Now().UTC().UnixNano()}
	if leaseOwner != "" {
		query += " AND lease_owner = ?"
		args = append(args, leaseOwner)
	}
	_, err := s.db.ExecContext(ctx, query, args...)
	return err
}

func (s *SQLiteStore) ApplyMemoryCandidates(ctx context.Context, request qqbot.MemoryWriteRequest) ([]qqbot.StructuredMemoryItem, error) {
	if s == nil || s.db == nil || strings.TrimSpace(request.Session) == "" {
		return nil, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UTC()
	sourceTime := request.SourceEventTime.UTC()
	if sourceTime.IsZero() {
		sourceTime = now
	}
	candidates := request.Candidates
	if len(candidates) > maxMemoryCandidatesPerWrite {
		candidates = candidates[:maxMemoryCandidatesPerWrite]
	}
	written := make([]qqbot.StructuredMemoryItem, 0, len(candidates))
	for _, rawCandidate := range candidates {
		candidate, ok := normalizeMemoryCandidate(rawCandidate, request)
		if !ok {
			continue
		}
		scopeKey := request.Session
		if candidate.Visibility == qqbot.MemoryVisibilityUser {
			scopeKey = "user:" + strings.TrimSpace(request.SubjectUserID)
		}
		key := candidate.Key
		if candidate.Kind == qqbot.MemoryKindEpisode && candidate.Action == qqbot.MemoryActionUpsert {
			key += "." + shortMemoryHash(firstNonEmptyMemory(request.SourceMessageID, sourceTime.Format(time.RFC3339Nano)))
		}
		sourceMessageID := memorySourceMessageID(request, candidate, sourceTime)
		if processed, found, err := findMemoryBySourceAndKey(ctx, tx, request.Session, sourceMessageID, request.SubjectUserID, key); err != nil {
			return nil, err
		} else if found {
			written = append(written, processed)
			continue
		}

		active, found, err := findActiveMemory(ctx, tx, scopeKey, request.SubjectUserID, key)
		if err != nil {
			return nil, err
		}
		if candidate.Action == qqbot.MemoryActionForget {
			if !found {
				continue
			}
			if _, err := tx.ExecContext(ctx, `
UPDATE memory_items SET status = 'forgotten', updated_at = ? WHERE id = ? AND status = 'active'
`, now.Unix(), active.ID); err != nil {
				return nil, err
			}
			active.Status = qqbot.MemoryStatusForgotten
			active.UpdatedAt = now
			written = append(written, active)
			continue
		}

		expiresAt := memoryCandidateExpiry(candidate, sourceTime)
		if found && equivalentMemoryContent(active.Content, candidate.Content) {
			if _, err := tx.ExecContext(ctx, `
UPDATE memory_items
SET subject_name = CASE WHEN ? = '' THEN subject_name ELSE ? END,
    topic = ?, entity = ?, evidence = CASE WHEN ? = '' THEN evidence ELSE ? END,
    confidence = MAX(confidence, ?), importance = MAX(importance, ?),
    expires_at = CASE WHEN ? = 0 THEN expires_at ELSE MAX(COALESCE(expires_at, 0), ?) END,
    last_verified_at = ?, updated_at = ?
WHERE id = ? AND status = 'active'
`, request.SubjectName, request.SubjectName, candidate.Topic, candidate.Entity,
				candidate.Evidence, candidate.Evidence, candidate.Confidence, candidate.Importance,
				timeToUnixMemory(expiresAt), timeToUnixMemory(expiresAt), sourceTime.Unix(), now.Unix(), active.ID); err != nil {
				return nil, err
			}
			if err := insertMemorySource(ctx, tx, active.ID, request, candidate, sourceTime, now); err != nil {
				return nil, err
			}
			updated, _, err := findMemoryByID(ctx, tx, active.ID)
			if err != nil {
				return nil, err
			}
			written = append(written, updated)
			continue
		}

		version := 1
		supersedesID := ""
		if found {
			version = active.Version + 1
			supersedesID = active.ID
			if _, err := tx.ExecContext(ctx, `
UPDATE memory_items SET status = 'superseded', updated_at = ? WHERE id = ? AND status = 'active'
`, now.Unix(), active.ID); err != nil {
				return nil, err
			}
		}
		item := qqbot.StructuredMemoryItem{
			ID:              uuid.NewString(),
			ScopeKey:        scopeKey,
			SubjectUserID:   strings.TrimSpace(request.SubjectUserID),
			SubjectName:     strings.TrimSpace(request.SubjectName),
			Key:             key,
			Kind:            candidate.Kind,
			Topic:           candidate.Topic,
			Entity:          candidate.Entity,
			Content:         candidate.Content,
			Evidence:        candidate.Evidence,
			SourceType:      candidate.SourceType,
			SourceSession:   request.Session,
			SourceGroupID:   request.GroupID,
			SourceMessageID: request.SourceMessageID,
			SourceEventTime: sourceTime,
			Confidence:      candidate.Confidence,
			Importance:      candidate.Importance,
			Visibility:      candidate.Visibility,
			Sensitive:       candidate.Sensitive,
			ExpiresAt:       expiresAt,
			LastVerifiedAt:  sourceTime,
			Version:         version,
			SupersedesID:    supersedesID,
			Status:          qqbot.MemoryStatusActive,
			CreatedAt:       now,
			UpdatedAt:       now,
		}
		if err := insertMemoryItem(ctx, tx, item); err != nil {
			return nil, err
		}
		if err := insertMemorySource(ctx, tx, item.ID, request, candidate, sourceTime, now); err != nil {
			return nil, err
		}
		written = append(written, item)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return written, nil
}

func (s *SQLiteStore) ListStructuredMemories(ctx context.Context, query qqbot.StructuredMemoryQuery) ([]qqbot.StructuredMemoryItem, error) {
	if s == nil || s.db == nil || strings.TrimSpace(query.Session) == "" {
		return nil, nil
	}
	now := query.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	limit := query.MaxCandidates
	if limit <= 0 {
		limit = defaultStructuredMemoryCandidates
	}
	if limit > maxStructuredMemoryCandidates {
		limit = maxStructuredMemoryCandidates
	}
	args := []any{now.Unix(), strings.TrimSpace(query.Session), strings.TrimSpace(query.SubjectUserID)}
	kindClause := ""
	if len(query.Kinds) > 0 {
		placeholders := make([]string, 0, len(query.Kinds))
		for _, kind := range query.Kinds {
			placeholders = append(placeholders, "?")
			args = append(args, string(kind))
		}
		kindClause = " AND kind IN (" + strings.Join(placeholders, ",") + ")"
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, `
SELECT id, scope_key, subject_user_id, subject_name, memory_key, kind, topic, entity,
       content, evidence, source_type, source_session, source_group_id, source_message_id,
       source_event_time, confidence, importance, visibility, sensitive, expires_at,
       last_verified_at, version, supersedes_id, status, created_at, updated_at
FROM memory_items
WHERE status = 'active'
  AND (expires_at IS NULL OR expires_at = 0 OR expires_at > ?)
  AND (scope_key = ? OR (subject_user_id = ? AND visibility = 'user'))`+kindClause+`
ORDER BY importance DESC, confidence DESC, last_verified_at DESC, updated_at DESC
LIMIT ?
`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]qqbot.StructuredMemoryItem, 0, limit)
	for rows.Next() {
		item, err := scanStructuredMemory(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func normalizeMemoryCandidate(candidate qqbot.MemoryCandidate, request qqbot.MemoryWriteRequest) (qqbot.MemoryCandidate, bool) {
	if candidate.Action == "" {
		candidate.Action = qqbot.MemoryActionUpsert
	}
	if candidate.Action != qqbot.MemoryActionUpsert && candidate.Action != qqbot.MemoryActionForget {
		return qqbot.MemoryCandidate{}, false
	}
	candidate.Key = normalizeMemoryKey(candidate.Key)
	candidate.Topic = truncateMemoryText(strings.TrimSpace(candidate.Topic), 80)
	candidate.Entity = truncateMemoryText(strings.TrimSpace(candidate.Entity), 80)
	contentLimit := 320
	if candidate.Kind == qqbot.MemoryKindSummary {
		contentLimit = 1200
	}
	candidate.Content = truncateMemoryText(strings.Join(strings.Fields(candidate.Content), " "), contentLimit)
	candidate.Evidence = truncateMemoryText(strings.Join(strings.Fields(candidate.Evidence), " "), 180)
	if candidate.Key == "" || candidate.Topic == "" || (candidate.Action == qqbot.MemoryActionUpsert && candidate.Content == "") {
		return qqbot.MemoryCandidate{}, false
	}
	switch candidate.Kind {
	case qqbot.MemoryKindFact, qqbot.MemoryKindPreference, qqbot.MemoryKindEpisode, qqbot.MemoryKindInstruction, qqbot.MemoryKindSummary:
	default:
		return qqbot.MemoryCandidate{}, false
	}
	switch candidate.SourceType {
	case qqbot.MemorySourceExplicit, qqbot.MemorySourceInferred, qqbot.MemorySourceSummary:
	default:
		candidate.SourceType = qqbot.MemorySourceInferred
	}
	if candidate.Confidence < 0 || candidate.Confidence > 1 || candidate.Importance < 0 || candidate.Importance > 1 {
		return qqbot.MemoryCandidate{}, false
	}
	if candidate.SourceType == qqbot.MemorySourceInferred && candidate.Confidence < 0.9 {
		return qqbot.MemoryCandidate{}, false
	}
	if candidate.SourceType != qqbot.MemorySourceInferred && candidate.Confidence < 0.7 {
		return qqbot.MemoryCandidate{}, false
	}
	if candidate.Visibility != qqbot.MemoryVisibilityUser {
		candidate.Visibility = qqbot.MemoryVisibilitySession
	}
	if candidate.Sensitive || candidate.SourceType != qqbot.MemorySourceExplicit || strings.TrimSpace(request.SubjectUserID) == "" {
		candidate.Visibility = qqbot.MemoryVisibilitySession
	}
	if request.EventKind == qqbot.EventKindPrivate || candidate.Kind == qqbot.MemoryKindSummary {
		candidate.Visibility = qqbot.MemoryVisibilitySession
	}
	if candidate.RetentionDays < 0 {
		candidate.RetentionDays = 0
	}
	if candidate.RetentionDays > 3650 {
		candidate.RetentionDays = 3650
	}
	return candidate, true
}

func insertMemoryItem(ctx context.Context, tx *sql.Tx, item qqbot.StructuredMemoryItem) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO memory_items
  (id, scope_key, subject_user_id, subject_name, memory_key, kind, topic, entity,
   content, evidence, source_type, source_session, source_group_id, source_message_id,
   source_event_time, confidence, importance, visibility, sensitive, expires_at,
   last_verified_at, version, supersedes_id, status, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, item.ID, item.ScopeKey, item.SubjectUserID, item.SubjectName, item.Key, string(item.Kind), item.Topic,
		item.Entity, item.Content, item.Evidence, string(item.SourceType), item.SourceSession, item.SourceGroupID,
		item.SourceMessageID, item.SourceEventTime.Unix(), item.Confidence, item.Importance, string(item.Visibility),
		boolMemoryInt(item.Sensitive), nullableMemoryUnix(item.ExpiresAt), item.LastVerifiedAt.Unix(), item.Version,
		nullableMemoryString(item.SupersedesID), string(item.Status), item.CreatedAt.Unix(), item.UpdatedAt.Unix())
	return err
}

func insertMemorySource(ctx context.Context, tx *sql.Tx, memoryID string, request qqbot.MemoryWriteRequest, candidate qqbot.MemoryCandidate, sourceTime time.Time, now time.Time) error {
	messageID := memorySourceMessageID(request, candidate, sourceTime)
	_, err := tx.ExecContext(ctx, `
INSERT OR IGNORE INTO memory_sources
  (memory_id, source_session, source_group_id, source_message_id, source_event_time, source_type, evidence, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
`, memoryID, request.Session, request.GroupID, messageID, sourceTime.Unix(), string(candidate.SourceType), candidate.Evidence, now.Unix())
	return err
}

func findMemoryBySourceAndKey(ctx context.Context, tx *sql.Tx, session string, messageID string, subjectUserID string, key string) (qqbot.StructuredMemoryItem, bool, error) {
	var memoryID string
	err := tx.QueryRowContext(ctx, `
SELECT memory_items.id
FROM memory_items
JOIN memory_sources AS source ON source.memory_id = memory_items.id
WHERE source.source_session = ? AND source.source_message_id = ?
  AND memory_items.subject_user_id = ? AND memory_items.memory_key = ?
ORDER BY memory_items.version DESC
LIMIT 1`, session, messageID, strings.TrimSpace(subjectUserID), key).Scan(&memoryID)
	if errors.Is(err, sql.ErrNoRows) {
		return qqbot.StructuredMemoryItem{}, false, nil
	}
	if err != nil {
		return qqbot.StructuredMemoryItem{}, false, err
	}
	return findMemoryByID(ctx, tx, memoryID)
}

func findActiveMemory(ctx context.Context, tx *sql.Tx, scopeKey string, subjectUserID string, key string) (qqbot.StructuredMemoryItem, bool, error) {
	return scanMemoryRow(tx.QueryRowContext(ctx, structuredMemorySelect+`
WHERE scope_key = ? AND subject_user_id = ? AND memory_key = ? AND status = 'active'
LIMIT 1`, scopeKey, strings.TrimSpace(subjectUserID), key))
}

func findMemoryByID(ctx context.Context, tx *sql.Tx, id string) (qqbot.StructuredMemoryItem, bool, error) {
	return scanMemoryRow(tx.QueryRowContext(ctx, structuredMemorySelect+` WHERE id = ? LIMIT 1`, id))
}

const structuredMemorySelect = `
SELECT id, scope_key, subject_user_id, subject_name, memory_key, kind, topic, entity,
       content, evidence, source_type, source_session, source_group_id, source_message_id,
       source_event_time, confidence, importance, visibility, sensitive, expires_at,
       last_verified_at, version, supersedes_id, status, created_at, updated_at
FROM memory_items`

type memoryScanner interface {
	Scan(dest ...any) error
}

func scanMemoryRow(scanner memoryScanner) (qqbot.StructuredMemoryItem, bool, error) {
	item, err := scanStructuredMemory(scanner)
	if errors.Is(err, sql.ErrNoRows) {
		return qqbot.StructuredMemoryItem{}, false, nil
	}
	return item, err == nil, err
}

func scanStructuredMemory(scanner memoryScanner) (qqbot.StructuredMemoryItem, error) {
	var item qqbot.StructuredMemoryItem
	var kind, sourceType, visibility, status string
	var subjectName, entity, evidence, groupID, messageID, supersedes sql.NullString
	var sourceTime, lastVerified, createdAt, updatedAt int64
	var expiresAt sql.NullInt64
	var sensitive int
	err := scanner.Scan(
		&item.ID, &item.ScopeKey, &item.SubjectUserID, &subjectName, &item.Key, &kind, &item.Topic, &entity,
		&item.Content, &evidence, &sourceType, &item.SourceSession, &groupID, &messageID,
		&sourceTime, &item.Confidence, &item.Importance, &visibility, &sensitive, &expiresAt,
		&lastVerified, &item.Version, &supersedes, &status, &createdAt, &updatedAt,
	)
	if err != nil {
		return qqbot.StructuredMemoryItem{}, err
	}
	item.SubjectName = subjectName.String
	item.Entity = entity.String
	item.Evidence = evidence.String
	item.SourceGroupID = groupID.String
	item.SourceMessageID = messageID.String
	item.SupersedesID = supersedes.String
	item.Kind = qqbot.MemoryKind(kind)
	item.SourceType = qqbot.MemorySourceType(sourceType)
	item.Visibility = qqbot.MemoryVisibility(visibility)
	item.Status = qqbot.MemoryStatus(status)
	item.Sensitive = sensitive != 0
	item.SourceEventTime = time.Unix(sourceTime, 0).UTC()
	item.LastVerifiedAt = time.Unix(lastVerified, 0).UTC()
	item.CreatedAt = time.Unix(createdAt, 0).UTC()
	item.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	if expiresAt.Valid && expiresAt.Int64 > 0 {
		item.ExpiresAt = time.Unix(expiresAt.Int64, 0).UTC()
	}
	return item, nil
}

func memoryCandidateExpiry(candidate qqbot.MemoryCandidate, sourceTime time.Time) time.Time {
	days := candidate.RetentionDays
	if days == 0 {
		switch {
		case candidate.Kind == qqbot.MemoryKindEpisode:
			days = 90
		case candidate.SourceType == qqbot.MemorySourceInferred:
			days = 180
		}
	}
	if days <= 0 {
		return time.Time{}
	}
	return sourceTime.Add(time.Duration(days) * 24 * time.Hour)
}

func memorySourceMessageID(request qqbot.MemoryWriteRequest, candidate qqbot.MemoryCandidate, sourceTime time.Time) string {
	if messageID := strings.TrimSpace(request.SourceMessageID); messageID != "" {
		return messageID
	}
	return "source:" + shortMemoryHash(request.Session+"|"+sourceTime.Format(time.RFC3339Nano)+"|"+candidate.Key)
}

func memoryJobID(payload qqbot.MemoryJobPayload) string {
	parts := []string{string(payload.Kind), strings.TrimSpace(payload.Session)}
	if payload.Kind == qqbot.MemoryJobEvent {
		parts = append(parts, payload.Event.MessageID, payload.Event.UserID, fmt.Sprint(payload.Event.Time), payload.Event.RawMessage)
	} else if len(payload.Events) > 0 {
		first := payload.Events[0]
		last := payload.Events[len(payload.Events)-1]
		parts = append(parts, first.MessageID, fmt.Sprint(first.Time), last.MessageID, fmt.Sprint(last.Time), fmt.Sprint(len(payload.Events)))
	}
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(strings.Join(parts, "|"))).String()
}

func normalizeMemoryKey(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	var builder strings.Builder
	lastSeparator := false
	for _, r := range value {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			builder.WriteRune(r)
			lastSeparator = false
		case r == '.' || r == '-' || r == '_' || unicode.IsSpace(r):
			if builder.Len() > 0 && !lastSeparator {
				builder.WriteByte('.')
				lastSeparator = true
			}
		}
	}
	return strings.Trim(strings.TrimSpace(truncateMemoryText(builder.String(), 96)), ".")
}

func equivalentMemoryContent(left string, right string) bool {
	normalize := func(value string) string {
		return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), ""))
	}
	return normalize(left) == normalize(right)
}

func truncateMemoryText(value string, maxRunes int) string {
	runes := []rune(strings.TrimSpace(value))
	if maxRunes <= 0 || len(runes) <= maxRunes {
		return string(runes)
	}
	return string(runes[:maxRunes])
}

func shortMemoryHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:6])
}

func boolMemoryInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullableMemoryUnix(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC().Unix()
}

func timeToUnixMemory(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UTC().Unix()
}

func nullableMemoryString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.TrimSpace(value)
}

func firstNonEmptyMemory(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
