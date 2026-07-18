package storage

import (
	"context"
	"path/filepath"
	"testing"

	"diana-qq-bot/model/qqbot"
)

func TestSQLiteStoreKeepsOriginalMessageAndRecallNotice(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "app.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}

	original := qqbot.MessageEvent{
		Kind:       qqbot.EventKindGroup,
		Time:       100,
		GroupID:    "123",
		UserID:     "20002",
		MessageID:  "old-1",
		RawMessage: "持久化的撤回正文",
		Segments:   []qqbot.MessageSegment{{Type: "text", Data: map[string]string{"text": "持久化的撤回正文"}}},
		SenderName: "Alice",
	}
	if err := store.AppendMessageEvent(ctx, "group:123", original); err != nil {
		t.Fatal(err)
	}
	recall := original
	recall.Kind = qqbot.EventKindNotice
	recall.SubType = "group_recall"
	recall.Time = 101
	if err := store.AppendMessageEvent(ctx, "group:123", recall); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	all, err := store.ListRecentMessageEvents(ctx, "group:123", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Kind != qqbot.EventKindGroup {
		t.Fatalf("persisted events = %#v", all)
	}
	var rowCount int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM message_events WHERE session = ?`, "group:123").Scan(&rowCount); err != nil {
		t.Fatal(err)
	}
	if rowCount != 2 {
		t.Fatalf("message_events row count = %d", rowCount)
	}
	found, ok, err := store.FindMessageEvent(ctx, "group:123", "old-1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || found.Kind != qqbot.EventKindGroup || found.RawMessage != original.RawMessage {
		t.Fatalf("found original = %#v, ok = %v", found, ok)
	}
	recalls, err := store.ListGroupRecallEvents(ctx, "123")
	if err != nil {
		t.Fatal(err)
	}
	if len(recalls) != 1 || recalls[0].RawMessage != original.RawMessage || recalls[0].SenderName != "Alice" || recalls[0].OriginalTime != original.Time {
		t.Fatalf("recalls = %#v", recalls)
	}
}

func TestSQLiteStoreListsRecallsNewestFirst(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	for _, event := range []qqbot.MessageEvent{
		{Kind: qqbot.EventKindNotice, SubType: "group_recall", Time: 100, GroupID: "123", MessageID: "older", RawMessage: "older"},
		{Kind: qqbot.EventKindNotice, SubType: "group_recall", Time: 200, GroupID: "123", MessageID: "newer", RawMessage: "newer"},
	} {
		if err := store.AppendMessageEvent(ctx, "group:123", event); err != nil {
			t.Fatal(err)
		}
	}
	recalls, err := store.ListGroupRecallEvents(ctx, "123")
	if err != nil {
		t.Fatal(err)
	}
	if len(recalls) != 2 || recalls[0].MessageID != "newer" || recalls[1].MessageID != "older" {
		t.Fatalf("recalls = %#v", recalls)
	}
}
