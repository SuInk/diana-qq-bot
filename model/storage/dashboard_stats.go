package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

type DashboardStats struct {
	Since              time.Time               `json:"since"`
	Until              time.Time               `json:"until"`
	ReceivedMessages   int64                   `json:"received_messages"`
	RepliedMessages    int64                   `json:"replied_messages"`
	TextReplies        int64                   `json:"text_replies"`
	ImageGenerations   int64                   `json:"image_generations"`
	ImageEdits         int64                   `json:"image_edits"`
	SearchCalls        int64                   `json:"search_calls"`
	APICalls           int64                   `json:"api_calls"`
	LLMCalls           int64                   `json:"llm_calls"`
	LLMInputTokens     int64                   `json:"llm_input_tokens"`
	LLMOutputTokens    int64                   `json:"llm_output_tokens"`
	LLMTotalTokens     int64                   `json:"llm_total_tokens"`
	Server             DashboardServerStats    `json:"server"`
	Hourly             []DashboardStatsBucket  `json:"hourly"`
	OperationBreakdown []DashboardStatsMeasure `json:"operation_breakdown"`
}

type DashboardServerStats struct {
	CollectedAt               time.Time `json:"collected_at"`
	Hostname                  string    `json:"hostname,omitempty"`
	OS                        string    `json:"os"`
	Arch                      string    `json:"arch"`
	ProcessID                 int       `json:"process_id"`
	ProcessUptimeSeconds      int64     `json:"process_uptime_seconds,omitempty"`
	CPUModel                  string    `json:"cpu_model,omitempty"`
	CPUCores                  int       `json:"cpu_cores"`
	CPUUsagePercent           float64   `json:"cpu_usage_percent,omitempty"`
	ProcessCPUPercent         float64   `json:"process_cpu_percent,omitempty"`
	MemoryTotalBytes          uint64    `json:"memory_total_bytes,omitempty"`
	MemoryUsedBytes           uint64    `json:"memory_used_bytes,omitempty"`
	MemoryUsagePercent        float64   `json:"memory_usage_percent,omitempty"`
	ProcessMemoryBytes        uint64    `json:"process_memory_bytes,omitempty"`
	GoHeapAllocBytes          uint64    `json:"go_heap_alloc_bytes,omitempty"`
	GoHeapSystemBytes         uint64    `json:"go_heap_system_bytes,omitempty"`
	GoRoutines                int       `json:"go_routines"`
	RuntimeVersion            string    `json:"runtime_version,omitempty"`
	MetricsUnavailableReason  string    `json:"metrics_unavailable_reason,omitempty"`
	ProcessMetricsUnavailable string    `json:"process_metrics_unavailable,omitempty"`
}

type DashboardStatsBucket struct {
	Hour     string `json:"hour"`
	Messages int64  `json:"messages"`
	Replies  int64  `json:"replies"`
	Searches int64  `json:"searches"`
	Images   int64  `json:"images"`
}

type DashboardStatsMeasure struct {
	Label string `json:"label"`
	Value int64  `json:"value"`
}

// DashboardStatsForDay 汇总本地当天的消息处理和 API 使用统计。
func (s *SQLiteStore) DashboardStatsForDay(ctx context.Context, now time.Time) (DashboardStats, error) {
	if s == nil || s.db == nil {
		return DashboardStats{}, nil
	}
	if now.IsZero() {
		now = time.Now()
	}
	localNow := now.Local()
	since := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, localNow.Location())
	until := localNow
	stats := DashboardStats{
		Since:  since,
		Until:  until,
		Hourly: make([]DashboardStatsBucket, 24),
	}
	for i := range stats.Hourly {
		stats.Hourly[i].Hour = since.Add(time.Duration(i) * time.Hour).Format("15:04")
	}

	sinceUnix, untilUnix := since.Unix(), until.Unix()
	sinceNano, untilNano := since.UnixNano(), until.UnixNano()
	if err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM message_events
WHERE kind IN ('group', 'private')
  AND event_time >= ? AND event_time < ?
`, sinceUnix, untilUnix).Scan(&stats.ReceivedMessages); err != nil {
		return DashboardStats{}, fmt.Errorf("count dashboard messages: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM inbound_events
WHERE outcome IN ('replied', 'error_replied')
  AND completed_at >= ? AND completed_at < ?
`, sinceNano, untilNano).Scan(&stats.RepliedMessages); err != nil {
		return DashboardStats{}, fmt.Errorf("count dashboard replies: %w", err)
	}
	if err := fillDashboardMessageBuckets(ctx, s.db, stats.Hourly, since, until, sinceUnix, untilUnix); err != nil {
		return DashboardStats{}, err
	}
	if err := fillDashboardReplyBuckets(ctx, s.db, stats.Hourly, since, until, sinceNano, untilNano); err != nil {
		return DashboardStats{}, err
	}
	if err := s.fillDashboardLogStats(ctx, &stats, since, until); err != nil {
		return DashboardStats{}, err
	}
	imageOps := stats.ImageGenerations + stats.ImageEdits
	stats.TextReplies = stats.RepliedMessages - imageOps
	if stats.TextReplies < 0 {
		stats.TextReplies = 0
	}
	stats.APICalls = stats.LLMCalls + imageOps + stats.SearchCalls
	stats.OperationBreakdown = []DashboardStatsMeasure{
		{Label: "文本回复", Value: stats.TextReplies},
		{Label: "生图/修图", Value: imageOps},
		{Label: "联网搜索", Value: stats.SearchCalls},
		{Label: "LLM API", Value: stats.LLMCalls},
	}
	return stats, nil
}

func fillDashboardMessageBuckets(ctx context.Context, db *sql.DB, buckets []DashboardStatsBucket, since time.Time, until time.Time, sinceUnix int64, untilUnix int64) error {
	rows, err := db.QueryContext(ctx, `
SELECT event_time
FROM message_events
WHERE kind IN ('group', 'private')
  AND event_time >= ? AND event_time < ?
`, sinceUnix, untilUnix)
	if err != nil {
		return fmt.Errorf("query dashboard message buckets: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var ts int64
		if err := rows.Scan(&ts); err != nil {
			return err
		}
		index := int(time.Unix(ts, 0).In(since.Location()).Sub(since) / time.Hour)
		if index >= 0 && index < len(buckets) && time.Unix(ts, 0).Before(until) {
			buckets[index].Messages++
		}
	}
	return rows.Err()
}

func fillDashboardReplyBuckets(ctx context.Context, db *sql.DB, buckets []DashboardStatsBucket, since time.Time, until time.Time, sinceNano int64, untilNano int64) error {
	rows, err := db.QueryContext(ctx, `
SELECT completed_at
FROM inbound_events
WHERE outcome IN ('replied', 'error_replied')
  AND completed_at >= ? AND completed_at < ?
`, sinceNano, untilNano)
	if err != nil {
		return fmt.Errorf("query dashboard reply buckets: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var nanos int64
		if err := rows.Scan(&nanos); err != nil {
			return err
		}
		t := time.Unix(0, nanos).In(since.Location())
		index := int(t.Sub(since) / time.Hour)
		if index >= 0 && index < len(buckets) && t.Before(until) {
			buckets[index].Replies++
		}
	}
	return rows.Err()
}

func (s *SQLiteStore) fillDashboardLogStats(ctx context.Context, stats *DashboardStats, since time.Time, until time.Time) error {
	rows, err := s.db.QueryContext(ctx, `
SELECT action, target, metadata, created_at
FROM app_logs
WHERE created_at >= ? AND created_at < ?
`, since.UTC().Format(time.RFC3339Nano), until.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("query dashboard logs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var action, createdAt string
		var target, metadata sql.NullString
		if err := rows.Scan(&action, &target, &metadata, &createdAt); err != nil {
			return err
		}
		parsedAt, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			continue
		}
		bucketIndex := int(parsedAt.In(stats.Since.Location()).Sub(stats.Since) / time.Hour)
		meta := map[string]any{}
		if metadata.Valid && strings.TrimSpace(metadata.String) != "" {
			_ = json.Unmarshal([]byte(metadata.String), &meta)
		}
		switch action {
		case "qqbot.llm_usage":
			stats.LLMCalls++
			stats.LLMInputTokens += int64FromAny(meta["input_tokens"])
			stats.LLMOutputTokens += int64FromAny(meta["output_tokens"])
			stats.LLMTotalTokens += int64FromAny(meta["total_tokens"])
		case "qqbot.image.generate":
			stats.ImageGenerations++
			if bucketIndex >= 0 && bucketIndex < len(stats.Hourly) {
				stats.Hourly[bucketIndex].Images++
			}
		case "qqbot.image.edit":
			stats.ImageEdits++
			if bucketIndex >= 0 && bucketIndex < len(stats.Hourly) {
				stats.Hourly[bucketIndex].Images++
			}
		case "qqbot.agent_tool":
			if isDashboardSearchTool(target.String) {
				stats.SearchCalls++
				if bucketIndex >= 0 && bucketIndex < len(stats.Hourly) {
					stats.Hourly[bucketIndex].Searches++
				}
			}
		}
	}
	return rows.Err()
}

func isDashboardSearchTool(target string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	return strings.Contains(target, "search") || strings.Contains(target, "openwebsearch") || strings.Contains(target, "web_search")
}

func int64FromAny(value any) int64 {
	switch typed := value.(type) {
	case int64:
		return typed
	case int:
		return int64(typed)
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return 0
		}
		return int64(typed)
	case json.Number:
		n, _ := typed.Int64()
		return n
	default:
		return 0
	}
}
