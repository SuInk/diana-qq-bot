package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"diana-qq-bot/model/applog"

	"github.com/google/uuid"
)

// 从 storage 重新导出日志类型，旧 WebUI 代码可继续用 storage.AppLogEntry，
// QQ 插件则只依赖 model/applog，不需要知道 SQLite 存储细节。
type AppLogKind = applog.Kind

const (
	LogKindOperation = applog.KindOperation
	LogKindError     = applog.KindError
)

type AppLogLevel = applog.Level

const (
	LogLevelInfo  = applog.LevelInfo
	LogLevelError = applog.LevelError
)

type AppLogEntry = applog.Entry
type AppLogFilter = applog.Filter

const (
	defaultAppLogLimit = 100
	maxAppLogLimit     = 500
)

// AppendLog 将审计日志追加到 SQLite。
func (s *SQLiteStore) AppendLog(ctx context.Context, entry AppLogEntry) error {
	if s == nil || s.db == nil {
		return nil
	}
	entry = normalizeLogEntry(entry)
	var metadata string
	if len(entry.Metadata) > 0 {
		// Metadata 保持 map 形态方便各子系统扩展，落库时统一编码成 JSON，避免频繁改表结构。
		data, err := json.Marshal(entry.Metadata)
		if err != nil {
			return err
		}
		metadata = string(data)
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO app_logs (id, kind, level, action, message, detail, actor, target, metadata, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, entry.ID, entry.Kind, entry.Level, entry.Action, entry.Message, entry.Detail, entry.Actor, entry.Target, metadata, entry.CreatedAt.Format(time.RFC3339Nano))
	return err
}

// ListLogs 按筛选条件读取最近日志。
func (s *SQLiteStore) ListLogs(ctx context.Context, filter AppLogFilter) ([]AppLogEntry, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	limit := normalizeLogLimit(filter.Limit)
	where := make([]string, 0, 2)
	args := make([]any, 0, 3)
	if filter.Kind != "" {
		where = append(where, "kind = ?")
		args = append(args, string(filter.Kind))
	}
	if filter.Level != "" {
		where = append(where, "level = ?")
		args = append(args, string(filter.Level))
	}
	query := `
SELECT id, kind, level, action, message, detail, actor, target, metadata, created_at
FROM app_logs`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	// 最新日志优先展示，并配合 migrate 中的 created_at 索引降低 limit 查询成本。
	query += " ORDER BY created_at DESC, id DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	logs := make([]AppLogEntry, 0, limit)
	for rows.Next() {
		entry, err := scanLogEntry(rows)
		if err != nil {
			return nil, err
		}
		logs = append(logs, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return logs, nil
}

// normalizeLogEntry 补齐日志条目的默认字段。
func normalizeLogEntry(entry AppLogEntry) AppLogEntry {
	// 写入前补齐默认值，调用方只需要提供 action/message 等关键信息。
	entry.ID = strings.TrimSpace(entry.ID)
	if entry.ID == "" {
		entry.ID = uuid.NewString()
	}
	if entry.Kind == "" {
		entry.Kind = LogKindOperation
	}
	if entry.Level == "" {
		if entry.Kind == LogKindError {
			entry.Level = LogLevelError
		} else {
			entry.Level = LogLevelInfo
		}
	}
	entry.Action = strings.TrimSpace(entry.Action)
	if entry.Action == "" {
		entry.Action = "system.log"
	}
	entry.Message = strings.TrimSpace(entry.Message)
	if entry.Message == "" {
		entry.Message = entry.Action
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
	return entry
}

// normalizeLogLimit 规范化日志查询返回数量。
func normalizeLogLimit(limit int) int {
	// 限制最大返回量，避免日志中心一次性拉太多数据拖慢 WebUI。
	if limit <= 0 {
		return defaultAppLogLimit
	}
	if limit > maxAppLogLimit {
		return maxAppLogLimit
	}
	return limit
}

type logScanner interface {
	Scan(dest ...any) error
}

// scanLogEntry 从数据库扫描结果构造日志条目。
func scanLogEntry(scanner logScanner) (AppLogEntry, error) {
	var entry AppLogEntry
	var kind, level, createdAt string
	var detail, actor, target, metadata sql.NullString
	if err := scanner.Scan(&entry.ID, &kind, &level, &entry.Action, &entry.Message, &detail, &actor, &target, &metadata, &createdAt); err != nil {
		return AppLogEntry{}, err
	}
	entry.Kind = AppLogKind(kind)
	entry.Level = AppLogLevel(level)
	entry.Detail = detail.String
	entry.Actor = actor.String
	entry.Target = target.String
	if metadata.Valid && strings.TrimSpace(metadata.String) != "" {
		// metadata 是可选列，只有非空时才解码，兼容老数据和无扩展信息的日志。
		var parsed map[string]any
		if err := json.Unmarshal([]byte(metadata.String), &parsed); err != nil {
			return AppLogEntry{}, fmt.Errorf("decode log metadata: %w", err)
		}
		entry.Metadata = parsed
	}
	parsedAt, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return AppLogEntry{}, fmt.Errorf("decode log created_at: %w", err)
	}
	entry.CreatedAt = parsedAt
	return entry, nil
}
