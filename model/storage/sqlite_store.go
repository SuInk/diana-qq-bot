package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"diana-qq-bot/model/llm"
	"diana-qq-bot/model/qqbot"

	_ "modernc.org/sqlite"
)

const (
	defaultDatabasePath  = "data/diana-qq-bot.db"
	llmConfigKey         = "llm_config"
	llmProfilesKey       = "llm_profiles"
	qqbotConfigKey       = "qqbot_config"
	qqbotProfilesKey     = "qqbot_profiles"
	qqbotGroupConfigKey  = "qqbot_group_configs"
	pluginStateKey       = "plugin_states"
	remindersKey         = "reminders"
	replySuppressionsKey = "qqbot_reply_suppressions"
)

type SQLiteStore struct {
	db           *sql.DB
	userMemoryMu sync.Mutex
}

// NewSQLiteStore 打开 SQLite 数据库并执行迁移。
func NewSQLiteStore(path string) (*SQLiteStore, error) {
	if path == "" {
		path = defaultDatabasePath
	}
	// 数据库目录可能不存在，先创建目录再打开 SQLite 文件。
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// A single connection keeps transaction-scoped queue claims predictable. WAL
	// still lets external readers inspect the database while the worker writes.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.Exec(`
PRAGMA journal_mode = WAL;
PRAGMA busy_timeout = 5000;
PRAGMA foreign_keys = ON;
`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("configure sqlite: %w", err)
	}
	store := &SQLiteStore{db: db}
	if err := store.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

// Close 关闭 SQLite 数据库连接。
func (s *SQLiteStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// LoadLLMConfig 读取旧版单配置 LLM 数据。
func (s *SQLiteStore) LoadLLMConfig(ctx context.Context) (llm.ProviderConfig, bool, error) {
	var cfg llm.ProviderConfig
	ok, err := s.loadJSON(ctx, llmConfigKey, &cfg)
	return cfg, ok, err
}

// SaveLLMConfig 保存旧版单配置 LLM 数据。
func (s *SQLiteStore) SaveLLMConfig(ctx context.Context, cfg llm.ProviderConfig) error {
	return s.saveJSON(ctx, llmConfigKey, cfg)
}

// LoadLLMProfiles 读取 LLM 配置集。
func (s *SQLiteStore) LoadLLMProfiles(ctx context.Context) (llm.ProfileSet, bool, error) {
	var set llm.ProfileSet
	ok, err := s.loadJSON(ctx, llmProfilesKey, &set)
	return set, ok, err
}

// SaveLLMProfiles 保存 LLM 配置集。
func (s *SQLiteStore) SaveLLMProfiles(ctx context.Context, set llm.ProfileSet) error {
	return s.saveJSON(ctx, llmProfilesKey, set)
}

// LoadQQBotConfig 读取 QQ 机器人配置。
func (s *SQLiteStore) LoadQQBotConfig(ctx context.Context) (qqbot.BotConfig, bool, error) {
	var cfg qqbot.BotConfig
	ok, err := s.loadJSON(ctx, qqbotConfigKey, &cfg)
	return cfg, ok, err
}

// SaveQQBotConfig 保存 QQ 机器人配置。
func (s *SQLiteStore) SaveQQBotConfig(ctx context.Context, cfg qqbot.BotConfig) error {
	return s.saveJSON(ctx, qqbotConfigKey, cfg)
}

// LoadQQBotProfiles 读取 QQ 机器人配置集。
func (s *SQLiteStore) LoadQQBotProfiles(ctx context.Context) (qqbot.ProfileSet, bool, error) {
	var set qqbot.ProfileSet
	ok, err := s.loadJSON(ctx, qqbotProfilesKey, &set)
	return set, ok, err
}

// SaveQQBotProfiles 保存 QQ 机器人配置集。
func (s *SQLiteStore) SaveQQBotProfiles(ctx context.Context, set qqbot.ProfileSet) error {
	return s.saveJSON(ctx, qqbotProfilesKey, set)
}

// LoadQQBotGroupConfigs 读取 QQ 群级机器人配置。
func (s *SQLiteStore) LoadQQBotGroupConfigs(ctx context.Context) (qqbot.GroupConfigSet, bool, error) {
	var set qqbot.GroupConfigSet
	ok, err := s.loadJSON(ctx, qqbotGroupConfigKey, &set)
	return set, ok, err
}

// SaveQQBotGroupConfigs 保存 QQ 群级机器人配置。
func (s *SQLiteStore) SaveQQBotGroupConfigs(ctx context.Context, set qqbot.GroupConfigSet) error {
	return s.saveJSON(ctx, qqbotGroupConfigKey, set)
}

// LoadPluginStates 读取插件状态。
func (s *SQLiteStore) LoadPluginStates(ctx context.Context) (map[string]qqbot.PluginState, bool, error) {
	var states map[string]qqbot.PluginState
	ok, err := s.loadJSON(ctx, pluginStateKey, &states)
	return states, ok, err
}

// SavePluginStates 保存插件状态。
func (s *SQLiteStore) SavePluginStates(ctx context.Context, states map[string]qqbot.PluginState) error {
	return s.saveJSON(ctx, pluginStateKey, states)
}

// LoadReminders 读取提醒列表。
func (s *SQLiteStore) LoadReminders(ctx context.Context) ([]qqbot.Reminder, bool, error) {
	var reminders []qqbot.Reminder
	ok, err := s.loadJSON(ctx, remindersKey, &reminders)
	return reminders, ok, err
}

// SaveReminders 保存提醒列表。
func (s *SQLiteStore) SaveReminders(ctx context.Context, reminders []qqbot.Reminder) error {
	return s.saveJSON(ctx, remindersKey, reminders)
}

// LoadReplySuppressions reads temporary per-user response restrictions.
func (s *SQLiteStore) LoadReplySuppressions(ctx context.Context) ([]qqbot.ReplySuppression, bool, error) {
	var items []qqbot.ReplySuppression
	ok, err := s.loadJSON(ctx, replySuppressionsKey, &items)
	return items, ok, err
}

// SaveReplySuppressions persists temporary per-user response restrictions.
func (s *SQLiteStore) SaveReplySuppressions(ctx context.Context, items []qqbot.ReplySuppression) error {
	return s.saveJSON(ctx, replySuppressionsKey, items)
}

// migrate 创建或升级 SQLite 表结构。
func (s *SQLiteStore) migrate() error {
	// app_state 用 key-value JSON 保存配置类状态；app_logs 单独建表方便按时间倒序查询。
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS app_state (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS app_logs (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  level TEXT NOT NULL,
  action TEXT NOT NULL,
  message TEXT NOT NULL,
  detail TEXT,
  actor TEXT,
  target TEXT,
  metadata TEXT,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS message_events (
  id TEXT PRIMARY KEY,
  session TEXT NOT NULL,
  kind TEXT NOT NULL,
  group_id TEXT,
  user_id TEXT,
  message_id TEXT,
  sender_name TEXT,
  event_time INTEGER NOT NULL,
  text TEXT,
  payload TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS image_descriptions (
  content_sha256 TEXT PRIMARY KEY,
  description TEXT NOT NULL,
  source_session TEXT,
  source_message_id TEXT,
  source TEXT,
  version TEXT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS user_profiles (
  user_id TEXT PRIMARY KEY,
  display_name TEXT,
  favorability INTEGER NOT NULL,
  message_count INTEGER NOT NULL,
  memories TEXT NOT NULL,
  last_seen_at TEXT,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS memory_items (
  id TEXT PRIMARY KEY,
  scope_key TEXT NOT NULL,
  subject_user_id TEXT NOT NULL,
  subject_name TEXT,
  memory_key TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('fact', 'preference', 'episode', 'instruction', 'summary')),
  topic TEXT NOT NULL,
  entity TEXT,
  content TEXT NOT NULL,
  evidence TEXT,
  source_type TEXT NOT NULL CHECK (source_type IN ('explicit', 'inferred', 'summary')),
  source_session TEXT NOT NULL,
  source_group_id TEXT,
  source_message_id TEXT,
  source_event_time INTEGER NOT NULL,
  confidence REAL NOT NULL,
  importance REAL NOT NULL,
  visibility TEXT NOT NULL CHECK (visibility IN ('session', 'user')),
  sensitive INTEGER NOT NULL DEFAULT 0,
  expires_at INTEGER,
  last_verified_at INTEGER NOT NULL,
  version INTEGER NOT NULL DEFAULT 1,
  supersedes_id TEXT,
  status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'superseded', 'forgotten')),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  FOREIGN KEY(supersedes_id) REFERENCES memory_items(id)
);

CREATE TABLE IF NOT EXISTS memory_sources (
  memory_id TEXT NOT NULL,
  source_session TEXT NOT NULL,
  source_group_id TEXT,
  source_message_id TEXT NOT NULL,
  source_event_time INTEGER NOT NULL,
  source_type TEXT NOT NULL,
  evidence TEXT,
  created_at INTEGER NOT NULL,
  PRIMARY KEY(memory_id, source_session, source_message_id),
  FOREIGN KEY(memory_id) REFERENCES memory_items(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS memory_jobs (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL CHECK (kind IN ('event', 'summary')),
  session TEXT NOT NULL,
  payload TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'done')),
  attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  available_at INTEGER NOT NULL,
  lease_owner TEXT,
  lease_until INTEGER,
  last_error TEXT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  completed_at INTEGER
);

CREATE TABLE IF NOT EXISTS inbound_events (
  id TEXT PRIMARY KEY,
  session TEXT NOT NULL,
  kind TEXT NOT NULL,
  group_id TEXT,
  user_id TEXT,
  message_id TEXT,
  event_time INTEGER NOT NULL,
  payload TEXT NOT NULL,
  priority INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'done')),
  attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  available_at INTEGER NOT NULL,
  lease_owner TEXT,
  lease_until INTEGER,
  outcome TEXT,
  last_error TEXT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  completed_at INTEGER
);

CREATE INDEX IF NOT EXISTS idx_app_logs_kind_created_at ON app_logs(kind, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_app_logs_created_at ON app_logs(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_message_events_session_time ON message_events(session, event_time DESC, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_message_events_kind_group_time ON message_events(kind, group_id, event_time DESC, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_message_events_text ON message_events(text);
CREATE INDEX IF NOT EXISTS idx_image_descriptions_source_message
ON image_descriptions(source_session, source_message_id);
CREATE INDEX IF NOT EXISTS idx_user_profiles_updated_at ON user_profiles(updated_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_memory_items_active_key
ON memory_items(scope_key, subject_user_id, memory_key)
WHERE status = 'active';
CREATE INDEX IF NOT EXISTS idx_memory_items_subject_active
ON memory_items(subject_user_id, status, importance DESC, confidence DESC, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_memory_items_scope_active
ON memory_items(scope_key, status, importance DESC, confidence DESC, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_memory_sources_message
ON memory_sources(source_session, source_message_id);
CREATE INDEX IF NOT EXISTS idx_memory_jobs_claim
ON memory_jobs(status, available_at, created_at, id);
CREATE INDEX IF NOT EXISTS idx_memory_jobs_lease
ON memory_jobs(status, lease_until);
CREATE INDEX IF NOT EXISTS idx_inbound_events_claim_time ON inbound_events(status, available_at, event_time, created_at, id);
CREATE INDEX IF NOT EXISTS idx_inbound_events_lease ON inbound_events(status, lease_until);
CREATE INDEX IF NOT EXISTS idx_inbound_events_session_lease ON inbound_events(status, session, lease_until);
CREATE INDEX IF NOT EXISTS idx_inbound_events_session_time ON inbound_events(session, event_time, created_at, id);
CREATE INDEX IF NOT EXISTS idx_inbound_events_group_time ON inbound_events(group_id, event_time DESC);
`)
	if err != nil {
		return err
	}
	if err := s.ensureInboundPriorityColumn(); err != nil {
		return err
	}
	if _, err := s.db.Exec(`
CREATE INDEX IF NOT EXISTS idx_inbound_events_priority_claim
ON inbound_events(status, available_at, priority DESC, event_time, created_at, id);
`); err != nil {
		return err
	}
	if err := s.backfillLegacyUserMemories(); err != nil {
		return err
	}

	// Earlier builds used a two-minute replay window. Restore only rows that are
	// still inside the current window; genuinely old messages stay terminal.
	now := time.Now().UTC().UnixNano()
	cutoff := time.Now().Add(-qqbot.InboundReplayWindow).Unix()
	_, err = s.db.Exec(`
UPDATE inbound_events
SET status = 'pending', available_at = ?, lease_owner = NULL, lease_until = NULL,
    outcome = NULL, last_error = NULL, completed_at = NULL, updated_at = ?
WHERE status = 'done' AND outcome = 'ignored_stale' AND event_time >= ?
`, now, now, cutoff)
	return err
}

func (s *SQLiteStore) ensureInboundPriorityColumn() error {
	rows, err := s.db.Query(`PRAGMA table_info(inbound_events)`)
	if err != nil {
		return fmt.Errorf("inspect inbound queue schema: %w", err)
	}
	found := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return fmt.Errorf("inspect inbound queue column: %w", err)
		}
		if name == "priority" {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate inbound queue schema: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close inbound queue schema rows: %w", err)
	}
	if found {
		return nil
	}
	if _, err := s.db.Exec(`ALTER TABLE inbound_events ADD COLUMN priority INTEGER NOT NULL DEFAULT 0`); err != nil {
		return fmt.Errorf("add inbound queue priority: %w", err)
	}
	return nil
}

// saveJSON 将指定 key 的结构体编码为 JSON 保存。
func (s *SQLiteStore) saveJSON(ctx context.Context, key string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	// 同一个 key 反复覆盖，updated_at 用于后续排查配置最后一次写入时间。
	_, err = s.db.ExecContext(ctx, `
INSERT INTO app_state (key, value, updated_at)
VALUES (?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=CURRENT_TIMESTAMP
`, key, string(data))
	return err
}

// loadJSON 读取指定 key 的 JSON 并解码。
func (s *SQLiteStore) loadJSON(ctx context.Context, key string, dest any) (bool, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM app_state WHERE key = ?`, key).Scan(&raw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// bool 返回值表示“没有保存过”，调用方据此使用默认配置或环境变量。
			return false, nil
		}
		return false, err
	}
	if err := json.Unmarshal([]byte(raw), dest); err != nil {
		return false, fmt.Errorf("decode %s: %w", key, err)
	}
	return true, nil
}
