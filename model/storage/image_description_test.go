package storage

import (
	"context"
	"path/filepath"
	"testing"

	"diana-qq-bot/model/qqbot"
)

func TestSQLiteStorePersistsImageDescriptionByContentHash(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	record := qqbot.ImageDescriptionRecord{
		ContentSHA256:   "ABCDEF",
		Description:     "一张版本信息截图",
		SourceSession:   "group:1",
		SourceMessageID: "message-1",
		Source:          "vision",
		Version:         "v1",
	}
	if err := store.SaveImageDescription(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.GetImageDescription(context.Background(), "abcdef")
	if err != nil || !ok {
		t.Fatalf("GetImageDescription() ok=%v err=%v", ok, err)
	}
	if got.Description != record.Description || got.SourceMessageID != "message-1" || got.Source != "vision" || got.CreatedAt <= 0 || got.UpdatedAt <= 0 {
		t.Fatalf("record = %#v", got)
	}
}
