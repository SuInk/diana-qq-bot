package applog

import (
	"context"
	"time"
)

// Kind 区分正常操作审计和失败事件，WebUI 日志中心会按它分成两个视图。
type Kind string

const (
	KindOperation Kind = "operation"
	KindError     Kind = "error"
)

type Level string

const (
	LevelInfo  Level = "info"
	LevelError Level = "error"
)

// Entry 是跨包共享的审计日志结构，放在 storage 外面是为了让 QQ 插件不用依赖 SQLite 实现。
type Entry struct {
	ID        string         `json:"id"`
	Kind      Kind           `json:"kind"`
	Level     Level          `json:"level"`
	Action    string         `json:"action"`
	Message   string         `json:"message"`
	Detail    string         `json:"detail,omitempty"`
	Actor     string         `json:"actor,omitempty"`
	Target    string         `json:"target,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

// Filter 目前只保留前端需要的筛选项；新增筛选条件时先扩展这里，再改 SQLite 查询。
type Filter struct {
	Kind  Kind
	Level Level
	Limit int
}

// Writer 是只写日志路径的最小依赖，方便 WebUI 和聊天技能共用。
type Writer interface {
	AppendLog(context.Context, Entry) error
}
