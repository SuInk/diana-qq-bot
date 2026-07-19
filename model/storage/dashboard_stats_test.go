package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"diana-qq-bot/model/qqbot"
)

func TestDashboardStatsForDayCountsDistinctActiveMembers(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "dashboard.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	now := time.Date(2026, time.July, 19, 15, 30, 0, 0, time.Local)
	events := []struct {
		session string
		event   qqbot.MessageEvent
	}{
		{"group:100", qqbot.MessageEvent{Kind: qqbot.EventKindGroup, Time: now.Add(-3 * time.Hour).Unix(), GroupID: "100", UserID: "20001", MessageID: "m1"}},
		{"group:100", qqbot.MessageEvent{Kind: qqbot.EventKindGroup, Time: now.Add(-2 * time.Hour).Unix(), GroupID: "100", UserID: "20001", MessageID: "m2"}},
		{"private:20002", qqbot.MessageEvent{Kind: qqbot.EventKindPrivate, Time: now.Add(-time.Hour).Unix(), UserID: "20002", MessageID: "m3"}},
		{"group:100", qqbot.MessageEvent{Kind: qqbot.EventKindNotice, Time: now.Add(-30 * time.Minute).Unix(), GroupID: "100", UserID: "20003", MessageID: "notice"}},
		{"group:100", qqbot.MessageEvent{Kind: qqbot.EventKindGroup, Time: now.Add(-24 * time.Hour).Unix(), GroupID: "100", UserID: "20004", MessageID: "old"}},
	}
	for _, item := range events {
		if err := store.AppendMessageEvent(ctx, item.session, item.event); err != nil {
			t.Fatal(err)
		}
	}

	stats, err := store.DashboardStatsForDay(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if stats.ReceivedMessages != 3 {
		t.Fatalf("received messages = %d, want 3", stats.ReceivedMessages)
	}
	if stats.ActiveMembers != 2 {
		t.Fatalf("active members = %d, want 2", stats.ActiveMembers)
	}
}
