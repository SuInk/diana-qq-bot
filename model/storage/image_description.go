package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"diana-qq-bot/model/qqbot"
)

func (s *SQLiteStore) GetImageDescription(ctx context.Context, contentSHA256 string) (qqbot.ImageDescriptionRecord, bool, error) {
	if s == nil || s.db == nil {
		return qqbot.ImageDescriptionRecord{}, false, nil
	}
	contentSHA256 = strings.ToLower(strings.TrimSpace(contentSHA256))
	if contentSHA256 == "" {
		return qqbot.ImageDescriptionRecord{}, false, nil
	}
	var record qqbot.ImageDescriptionRecord
	err := s.db.QueryRowContext(ctx, `
SELECT content_sha256, description, source_session, source_message_id, source, version, created_at, updated_at
FROM image_descriptions
WHERE content_sha256 = ?
`, contentSHA256).Scan(
		&record.ContentSHA256,
		&record.Description,
		&record.SourceSession,
		&record.SourceMessageID,
		&record.Source,
		&record.Version,
		&record.CreatedAt,
		&record.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return qqbot.ImageDescriptionRecord{}, false, nil
		}
		return qqbot.ImageDescriptionRecord{}, false, fmt.Errorf("load image description: %w", err)
	}
	return record, true, nil
}

func (s *SQLiteStore) SaveImageDescription(ctx context.Context, record qqbot.ImageDescriptionRecord) error {
	if s == nil || s.db == nil {
		return nil
	}
	record.ContentSHA256 = strings.ToLower(strings.TrimSpace(record.ContentSHA256))
	record.Description = strings.TrimSpace(record.Description)
	if record.ContentSHA256 == "" || record.Description == "" {
		return nil
	}
	now := time.Now().Unix()
	if record.CreatedAt <= 0 {
		record.CreatedAt = now
	}
	record.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
INSERT INTO image_descriptions (
  content_sha256, description, source_session, source_message_id, source, version, created_at, updated_at
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(content_sha256) DO UPDATE SET
  description=excluded.description,
  source_session=excluded.source_session,
  source_message_id=excluded.source_message_id,
  source=excluded.source,
  version=excluded.version,
  updated_at=excluded.updated_at
`,
		record.ContentSHA256,
		record.Description,
		strings.TrimSpace(record.SourceSession),
		strings.TrimSpace(record.SourceMessageID),
		strings.TrimSpace(record.Source),
		strings.TrimSpace(record.Version),
		record.CreatedAt,
		record.UpdatedAt,
	)
	return err
}
