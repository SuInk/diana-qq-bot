package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"diana-qq-bot/model/qqbot"
)

func TestStructuredMemoryVersionsSourcesAndForget(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	request := qqbot.MemoryWriteRequest{
		SubjectUserID:   "10001",
		SubjectName:     "Alice",
		Session:         "group:123",
		EventKind:       qqbot.EventKindGroup,
		GroupID:         "123",
		SourceMessageID: "m1",
		SourceEventTime: time.Unix(100, 0),
		Candidates: []qqbot.MemoryCandidate{{
			Action:     qqbot.MemoryActionUpsert,
			Key:        "preference.food.spicy",
			Kind:       qqbot.MemoryKindPreference,
			Topic:      "饮食偏好",
			Entity:     "辣味食物",
			Content:    "Alice偏好辣味食物",
			Evidence:   "我喜欢吃辣",
			SourceType: qqbot.MemorySourceExplicit,
			Confidence: 0.98,
			Importance: 0.72,
			Visibility: qqbot.MemoryVisibilityUser,
		}},
	}
	written, err := store.ApplyMemoryCandidates(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 1 || written[0].Version != 1 || written[0].SupersedesID != "" {
		t.Fatalf("first write = %#v", written)
	}
	firstID := written[0].ID

	request.SourceMessageID = "m2"
	request.SourceEventTime = time.Unix(200, 0)
	written, err = store.ApplyMemoryCandidates(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 1 || written[0].ID != firstID || written[0].Version != 1 {
		t.Fatalf("confirmation created another version: %#v", written)
	}
	var sourceCount int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_sources WHERE memory_id = ?`, firstID).Scan(&sourceCount); err != nil {
		t.Fatal(err)
	}
	if sourceCount != 2 {
		t.Fatalf("source count = %d, want 2", sourceCount)
	}

	request.SourceMessageID = "m3"
	request.SourceEventTime = time.Unix(300, 0)
	request.Candidates[0].Content = "Alice现在不喜欢辣味食物"
	request.Candidates[0].Evidence = "我现在不吃辣了"
	written, err = store.ApplyMemoryCandidates(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 1 || written[0].Version != 2 || written[0].SupersedesID != firstID || written[0].ID == firstID {
		t.Fatalf("conflict version = %#v", written)
	}
	secondID := written[0].ID
	var firstStatus string
	if err := store.db.QueryRowContext(ctx, `SELECT status FROM memory_items WHERE id = ?`, firstID).Scan(&firstStatus); err != nil {
		t.Fatal(err)
	}
	if firstStatus != string(qqbot.MemoryStatusSuperseded) {
		t.Fatalf("first status = %q", firstStatus)
	}

	// A crash after applying the memory but before completing its queue job must
	// not create a third version when the LLM phrases the retry differently.
	request.Candidates[0].Content = "Alice已经停止食用辣味食物"
	written, err = store.ApplyMemoryCandidates(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 1 || written[0].ID != secondID || written[0].Version != 2 {
		t.Fatalf("same source retry was not idempotent: %#v", written)
	}

	items, err := store.ListStructuredMemories(ctx, qqbot.StructuredMemoryQuery{
		SubjectUserID: "10001",
		Session:       "group:999",
		Now:           time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != secondID || items[0].Content != "Alice现在不喜欢辣味食物" {
		t.Fatalf("active global memory = %#v", items)
	}

	request.SourceMessageID = "m4"
	request.Candidates[0] = qqbot.MemoryCandidate{
		Action:     qqbot.MemoryActionForget,
		Key:        "preference.food.spicy",
		Kind:       qqbot.MemoryKindPreference,
		Topic:      "饮食偏好",
		SourceType: qqbot.MemorySourceExplicit,
		Confidence: 0.99,
		Importance: 0.9,
		Visibility: qqbot.MemoryVisibilityUser,
	}
	written, err = store.ApplyMemoryCandidates(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 1 || written[0].Status != qqbot.MemoryStatusForgotten {
		t.Fatalf("forget result = %#v", written)
	}
	items, err = store.ListStructuredMemories(ctx, qqbot.StructuredMemoryQuery{SubjectUserID: "10001", Session: "group:123", Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("forgotten memory is still active: %#v", items)
	}
}

func TestStructuredMemoryVisibilityAndExpiry(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	now := time.Now().UTC()
	base := qqbot.MemoryWriteRequest{
		SubjectUserID:   "user-a",
		SubjectName:     "Alice",
		Session:         "group:one",
		EventKind:       qqbot.EventKindGroup,
		GroupID:         "one",
		SourceMessageID: "global",
		SourceEventTime: now,
		Candidates: []qqbot.MemoryCandidate{{
			Action:     qqbot.MemoryActionUpsert,
			Key:        "profile.pet.cat",
			Kind:       qqbot.MemoryKindFact,
			Topic:      "宠物",
			Content:    "Alice养了一只猫",
			SourceType: qqbot.MemorySourceExplicit,
			Confidence: 0.98,
			Importance: 0.8,
			Visibility: qqbot.MemoryVisibilityUser,
		}},
	}
	if _, err := store.ApplyMemoryCandidates(ctx, base); err != nil {
		t.Fatal(err)
	}
	base.SourceMessageID = "sensitive"
	base.Candidates[0] = qqbot.MemoryCandidate{
		Action:        qqbot.MemoryActionUpsert,
		Key:           "health.current.medication",
		Kind:          qqbot.MemoryKindFact,
		Topic:         "健康",
		Content:       "Alice正在服用某种药物",
		SourceType:    qqbot.MemorySourceExplicit,
		Confidence:    0.98,
		Importance:    0.9,
		Visibility:    qqbot.MemoryVisibilityUser,
		Sensitive:     true,
		RetentionDays: 1,
	}
	if _, err := store.ApplyMemoryCandidates(ctx, base); err != nil {
		t.Fatal(err)
	}

	otherSession, err := store.ListStructuredMemories(ctx, qqbot.StructuredMemoryQuery{
		SubjectUserID: "user-a",
		Session:       "group:two",
		Now:           now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(otherSession) != 1 || otherSession[0].Key != "profile.pet.cat" {
		t.Fatalf("cross-session memories = %#v", otherSession)
	}
	sameSession, err := store.ListStructuredMemories(ctx, qqbot.StructuredMemoryQuery{
		SubjectUserID: "user-a",
		Session:       "group:one",
		Now:           now.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sameSession) != 2 {
		t.Fatalf("same-session memories = %#v", sameSession)
	}
	expired, err := store.ListStructuredMemories(ctx, qqbot.StructuredMemoryQuery{
		SubjectUserID: "user-a",
		Session:       "group:one",
		Now:           now.Add(48 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(expired) != 1 || expired[0].Key != "profile.pet.cat" {
		t.Fatalf("expired memories = %#v", expired)
	}
}

func TestMemoryJobQueueIsDurableAndDeduplicated(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	payload := qqbot.MemoryJobPayload{
		Kind:    qqbot.MemoryJobEvent,
		Session: "group:123",
		Event: qqbot.MessageEvent{
			Kind:      qqbot.EventKindGroup,
			GroupID:   "123",
			UserID:    "10001",
			MessageID: "m1",
			Time:      100,
			Segments:  []qqbot.MessageSegment{{Type: "text", Data: map[string]string{"text": "我喜欢吃辣"}}},
		},
	}
	id, inserted, err := store.EnqueueMemoryJob(ctx, payload)
	if err != nil || !inserted || id == "" {
		t.Fatalf("first enqueue id=%q inserted=%v err=%v", id, inserted, err)
	}
	duplicateID, inserted, err := store.EnqueueMemoryJob(ctx, payload)
	if err != nil || inserted || duplicateID != id {
		t.Fatalf("duplicate enqueue id=%q inserted=%v err=%v", duplicateID, inserted, err)
	}

	job, ok, err := store.ClaimNextMemoryJob(ctx, "worker-old", time.Now().Add(time.Minute))
	if err != nil || !ok || job.ID != id || job.Attempts != 1 {
		t.Fatalf("first claim job=%#v ok=%v err=%v", job, ok, err)
	}
	if err := store.ReleaseMemoryJobLeases(ctx, ""); err != nil {
		t.Fatal(err)
	}
	job, ok, err = store.ClaimNextMemoryJob(ctx, "worker-new", time.Now().Add(time.Minute))
	if err != nil || !ok || job.Attempts != 2 {
		t.Fatalf("recovered claim job=%#v ok=%v err=%v", job, ok, err)
	}
	if err := store.RetryMemoryJob(ctx, id, "worker-new", time.Now().Add(time.Hour), "temporary"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.ClaimNextMemoryJob(ctx, "worker-new", time.Now().Add(time.Minute)); err != nil || ok {
		t.Fatalf("future retry was claimable ok=%v err=%v", ok, err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE memory_jobs SET available_at = ? WHERE id = ?`, time.Now().Add(-time.Second).UnixNano(), id); err != nil {
		t.Fatal(err)
	}
	job, ok, err = store.ClaimNextMemoryJob(ctx, "worker-new", time.Now().Add(time.Minute))
	if err != nil || !ok || job.Attempts != 3 {
		t.Fatalf("retry claim job=%#v ok=%v err=%v", job, ok, err)
	}
	if err := store.CompleteMemoryJob(ctx, id, "worker-new"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.ClaimNextMemoryJob(ctx, "worker-new", time.Now().Add(time.Minute)); err != nil || ok {
		t.Fatalf("completed job was claimable ok=%v err=%v", ok, err)
	}
}

func TestStructuredMemoryBackfillsLegacyProfileAsUncertainEpisode(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "memory.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	event := qqbot.MessageEvent{
		Kind: qqbot.EventKindGroup, GroupID: "123", UserID: "user", SenderName: "Alice",
		MessageID: "legacy-message", Time: time.Now().Unix(),
		Segments: []qqbot.MessageSegment{{Type: "text", Data: map[string]string{"text": "我家的猫叫小白"}}},
	}
	if _, err := store.UpdateUserMemory(ctx, event, qqbot.UserMemoryUpdate{}); err != nil {
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
	items, err := store.ListStructuredMemories(ctx, qqbot.StructuredMemoryQuery{
		SubjectUserID: "user",
		Session:       "group:123",
		Now:           time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("backfilled items = %#v", items)
	}
	item := items[0]
	if item.Kind != qqbot.MemoryKindEpisode || item.SourceType != qqbot.MemorySourceInferred || !item.Sensitive || item.Confidence >= 0.7 || item.Topic != "历史聊天片段" {
		t.Fatalf("legacy item was promoted too strongly: %#v", item)
	}
}
