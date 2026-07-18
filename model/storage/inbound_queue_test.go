package storage

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"diana-qq-bot/model/qqbot"
)

func TestInboundQueuePersistsAndDeduplicates(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "inbound.db")
	store := openInboundTestStore(t, dbPath)

	legacy := inboundTestEvent("legacy", "old history", 10)
	if err := store.AppendMessageEvent(ctx, "group:100", legacy); err != nil {
		t.Fatal(err)
	}
	if count := pendingInboundCount(t, store); count != 0 {
		t.Fatalf("legacy history migrated into queue: count=%d", count)
	}
	if _, inserted, err := store.EnqueueInboundEvent(ctx, "group:100", legacy); err != nil || inserted {
		t.Fatalf("legacy event enqueue inserted=%v err=%v", inserted, err)
	}
	if count := pendingInboundCount(t, store); count != 0 {
		t.Fatalf("legacy history entered the reply queue: count=%d", count)
	}

	event := inboundTestEvent("message-1", "hello", 20)
	id, inserted, err := store.EnqueueInboundEvent(ctx, "group:100", event)
	if err != nil || !inserted {
		t.Fatalf("first enqueue id=%q inserted=%v err=%v", id, inserted, err)
	}
	duplicateID, inserted, err := store.EnqueueInboundEvent(ctx, "group:100", event)
	if err != nil || inserted || duplicateID != id {
		t.Fatalf("duplicate enqueue id=%q inserted=%v err=%v", duplicateID, inserted, err)
	}
	if count := pendingInboundCount(t, store); count != 1 {
		t.Fatalf("pending count=%d, want 1", count)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store = openInboundTestStore(t, dbPath)
	defer func() { _ = store.Close() }()
	item, ok, err := store.ClaimNextInboundEvent(ctx, "worker-1", time.Now().Add(time.Minute))
	if err != nil || !ok {
		t.Fatalf("claim ok=%v err=%v", ok, err)
	}
	if item.ID != id || item.Session != "group:100" || item.Event.MessageID != event.MessageID || item.Attempts != 1 {
		t.Fatalf("claimed item=%#v", item)
	}
	if count := pendingInboundCount(t, store); count != 1 {
		t.Fatalf("processing item must remain outstanding: count=%d", count)
	}
	if err := store.CompleteInboundEvent(ctx, id, "wrong-worker", "ignored"); err == nil {
		t.Fatal("completion with the wrong lease owner unexpectedly succeeded")
	}
	if err := store.CompleteInboundEvent(ctx, id, "worker-1", "handled"); err != nil {
		t.Fatal(err)
	}
	if count := pendingInboundCount(t, store); count != 0 {
		t.Fatalf("pending count after completion=%d", count)
	}

	var status, outcome string
	if err := store.db.QueryRowContext(ctx, `SELECT status, outcome FROM inbound_events WHERE id = ?`, id).Scan(&status, &outcome); err != nil {
		t.Fatal(err)
	}
	if status != inboundStatusDone || outcome != "handled" {
		t.Fatalf("terminal state status=%q outcome=%q", status, outcome)
	}
}

func TestInboundQueueMigrationRestoresStaleDrops(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "restore-stale.db")
	store := openInboundTestStore(t, dbPath)
	id, inserted, err := store.EnqueueInboundEvent(ctx, "group:100", inboundTestEvent("stale", "recover me", time.Now().Add(-time.Hour).Unix()))
	if err != nil || !inserted {
		t.Fatalf("enqueue inserted=%v err=%v", inserted, err)
	}
	if _, ok, err := store.ClaimNextInboundEvent(ctx, "old-worker", time.Now().Add(time.Minute)); err != nil || !ok {
		t.Fatalf("claim ok=%v err=%v", ok, err)
	}
	if err := store.CompleteInboundEvent(ctx, id, "old-worker", "ignored_stale"); err != nil {
		t.Fatal(err)
	}
	oldID, inserted, err := store.EnqueueInboundEvent(ctx, "group:100", inboundTestEvent("too-old", "leave terminal", time.Now().Add(-3*time.Hour).Unix()))
	if err != nil || !inserted {
		t.Fatalf("enqueue old inserted=%v err=%v", inserted, err)
	}
	if _, ok, err := store.ClaimNextInboundEvent(ctx, "old-worker", time.Now().Add(time.Minute)); err != nil || !ok {
		t.Fatalf("claim old ok=%v err=%v", ok, err)
	}
	if err := store.CompleteInboundEvent(ctx, oldID, "old-worker", "ignored_stale"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store = openInboundTestStore(t, dbPath)
	defer func() { _ = store.Close() }()
	if count := pendingInboundCount(t, store); count != 1 {
		t.Fatalf("restored pending count=%d, want 1", count)
	}
	item, ok, err := store.ClaimNextInboundEvent(ctx, "new-worker", time.Now().Add(time.Minute))
	if err != nil || !ok || item.ID != id || item.Attempts != 2 {
		t.Fatalf("restored item=%#v ok=%v err=%v", item, ok, err)
	}
	var oldStatus, oldOutcome string
	if err := store.db.QueryRowContext(ctx, `SELECT status, outcome FROM inbound_events WHERE id = ?`, oldID).Scan(&oldStatus, &oldOutcome); err != nil {
		t.Fatal(err)
	}
	if oldStatus != inboundStatusDone || oldOutcome != "ignored_stale" {
		t.Fatalf("old stale row status=%q outcome=%q", oldStatus, oldOutcome)
	}
}

func TestInboundQueueUsesStableHashWithoutMessageID(t *testing.T) {
	ctx := context.Background()
	store := openInboundTestStore(t, filepath.Join(t.TempDir(), "hash.db"))
	defer func() { _ = store.Close() }()

	event := qqbot.MessageEvent{
		Kind:       qqbot.EventKindGroup,
		GroupID:    "200",
		UserID:     "300",
		Time:       123,
		RawMessage: "message without an id",
		Segments: []qqbot.MessageSegment{{
			Type: "text",
			Data: map[string]string{"text": "message without an id"},
		}},
	}
	id, inserted, err := store.EnqueueInboundEvent(ctx, "group:200", event)
	if err != nil || !inserted {
		t.Fatalf("first enqueue id=%q inserted=%v err=%v", id, inserted, err)
	}
	if !strings.HasPrefix(id, "sha256:") || len(id) != len("sha256:")+64 {
		t.Fatalf("stable fallback id=%q", id)
	}
	idAgain, inserted, err := store.EnqueueInboundEvent(ctx, "group:200", event)
	if err != nil || inserted || idAgain != id {
		t.Fatalf("second enqueue id=%q inserted=%v err=%v", idAgain, inserted, err)
	}
	event.SenderName = "enriched later"
	event.ToMe = true
	idEnriched, inserted, err := store.EnqueueInboundEvent(ctx, "group:200", event)
	if err != nil || inserted || idEnriched != id {
		t.Fatalf("enriched duplicate id=%q inserted=%v err=%v", idEnriched, inserted, err)
	}
	if count := pendingInboundCount(t, store); count != 1 {
		t.Fatalf("pending count=%d", count)
	}
}

func TestInboundQueueRollsBackHistoryWhenQueueInsertFails(t *testing.T) {
	ctx := context.Background()
	store := openInboundTestStore(t, filepath.Join(t.TempDir(), "rollback.db"))
	defer func() { _ = store.Close() }()
	if _, err := store.db.Exec(`
CREATE TRIGGER reject_inbound_insert
BEFORE INSERT ON inbound_events
BEGIN
  SELECT RAISE(ABORT, 'injected queue failure');
END;
`); err != nil {
		t.Fatal(err)
	}

	event := inboundTestEvent("rollback", "must be atomic", 1)
	id, err := stableInboundEventID("group:1", event)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.EnqueueInboundEvent(ctx, "group:1", event); err == nil {
		t.Fatal("enqueue unexpectedly succeeded")
	}
	var historyRows int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM message_events WHERE id = ?`, id).Scan(&historyRows); err != nil {
		t.Fatal(err)
	}
	if historyRows != 0 {
		t.Fatalf("message history survived rolled-back enqueue: rows=%d", historyRows)
	}
}

func TestInboundQueueFIFOLeaseRecoveryRetryAndRelease(t *testing.T) {
	ctx := context.Background()
	store := openInboundTestStore(t, filepath.Join(t.TempDir(), "leases.db"))
	defer func() { _ = store.Close() }()

	firstID, inserted, err := store.EnqueueInboundEvent(ctx, "group:1", inboundTestEvent("first", "first", 200))
	if err != nil || !inserted {
		t.Fatalf("enqueue first inserted=%v err=%v", inserted, err)
	}
	time.Sleep(time.Millisecond)
	secondID, inserted, err := store.EnqueueInboundEvent(ctx, "group:1", inboundTestEvent("second", "second", 100))
	if err != nil || !inserted {
		t.Fatalf("enqueue second inserted=%v err=%v", inserted, err)
	}

	first, ok, err := store.ClaimNextInboundEvent(ctx, "worker-a", time.Now().Add(time.Minute))
	if err != nil || !ok || first.ID != secondID {
		t.Fatalf("first claim item=%#v ok=%v err=%v", first, ok, err)
	}
	if err := store.CompleteInboundEvent(ctx, first.ID, "worker-a", "ignored"); err != nil {
		t.Fatal(err)
	}
	second, ok, err := store.ClaimNextInboundEvent(ctx, "worker-a", time.Now().Add(20*time.Millisecond))
	if err != nil || !ok || second.ID != firstID {
		t.Fatalf("second claim item=%#v ok=%v err=%v", second, ok, err)
	}

	time.Sleep(30 * time.Millisecond)
	recovered, ok, err := store.ClaimNextInboundEvent(ctx, "worker-b", time.Now().Add(time.Minute))
	if err != nil || !ok || recovered.ID != firstID || recovered.Attempts != 2 {
		t.Fatalf("recovered item=%#v ok=%v err=%v", recovered, ok, err)
	}
	if err := store.RetryInboundEvent(ctx, recovered.ID, "worker-b", time.Now().Add(25*time.Millisecond), "temporary failure"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.ClaimNextInboundEvent(ctx, "worker-c", time.Now().Add(time.Minute)); err != nil || ok {
		t.Fatalf("future retry was claimable ok=%v err=%v", ok, err)
	}

	time.Sleep(35 * time.Millisecond)
	retried, ok, err := store.ClaimNextInboundEvent(ctx, "worker-c", time.Now().Add(time.Minute))
	if err != nil || !ok || retried.ID != firstID || retried.Attempts != 3 {
		t.Fatalf("retried item=%#v ok=%v err=%v", retried, ok, err)
	}
	if err := store.ReleaseInboundLeases(ctx, "worker-c"); err != nil {
		t.Fatal(err)
	}
	released, ok, err := store.ClaimNextInboundEvent(ctx, "worker-d", time.Now().Add(time.Minute))
	if err != nil || !ok || released.ID != firstID || released.Attempts != 4 {
		t.Fatalf("released item=%#v ok=%v err=%v", released, ok, err)
	}
}

func TestInboundQueueReleasesAllLeasesForStartupRecovery(t *testing.T) {
	ctx := context.Background()
	store := openInboundTestStore(t, filepath.Join(t.TempDir(), "release-all.db"))
	defer func() { _ = store.Close() }()

	for i, messageID := range []string{"first", "second"} {
		session := fmt.Sprintf("group:%d", i+1)
		if _, inserted, err := store.EnqueueInboundEvent(ctx, session, inboundTestEvent(messageID, messageID, 1)); err != nil || !inserted {
			t.Fatalf("enqueue %s inserted=%v err=%v", messageID, inserted, err)
		}
	}
	for _, owner := range []string{"worker-a", "worker-b"} {
		if _, ok, err := store.ClaimNextInboundEvent(ctx, owner, time.Now().Add(time.Minute)); err != nil || !ok {
			t.Fatalf("claim owner=%s ok=%v err=%v", owner, ok, err)
		}
	}
	if err := store.ReleaseInboundLeases(ctx, ""); err != nil {
		t.Fatal(err)
	}
	for _, owner := range []string{"worker-c", "worker-d"} {
		if item, ok, err := store.ClaimNextInboundEvent(ctx, owner, time.Now().Add(time.Minute)); err != nil || !ok || item.Attempts != 2 {
			t.Fatalf("recovered owner=%s item=%#v ok=%v err=%v", owner, item, ok, err)
		}
	}
}

func TestInboundQueueClaimIsAtomic(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "atomic.db")
	stores := []*SQLiteStore{
		openInboundTestStore(t, dbPath),
		openInboundTestStore(t, dbPath),
	}
	defer func() {
		for _, store := range stores {
			_ = store.Close()
		}
	}()
	if _, inserted, err := stores[0].EnqueueInboundEvent(ctx, "group:1", inboundTestEvent("only", "only", 1)); err != nil || !inserted {
		t.Fatalf("enqueue inserted=%v err=%v", inserted, err)
	}

	type claimResult struct {
		ok  bool
		err error
	}
	start := make(chan struct{})
	results := make(chan claimResult, 2)
	var wg sync.WaitGroup
	for i, owner := range []string{"worker-1", "worker-2"} {
		store := stores[i]
		owner := owner
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, ok, err := store.ClaimNextInboundEvent(ctx, owner, time.Now().Add(time.Minute))
			results <- claimResult{ok: ok, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	claimed := 0
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.ok {
			claimed++
		}
	}
	if claimed != 1 {
		t.Fatalf("atomic claims=%d, want 1", claimed)
	}
}

func TestInboundQueueSerializesEachSession(t *testing.T) {
	ctx := context.Background()
	store := openInboundTestStore(t, filepath.Join(t.TempDir(), "session-order.db"))
	defer func() { _ = store.Close() }()

	first := inboundTestEvent("a-1", "first in a", 1)
	second := inboundTestEvent("a-2", "second in a", 2)
	other := inboundTestEvent("b-1", "first in b", 3)
	for _, item := range []struct {
		session string
		event   qqbot.MessageEvent
	}{{"group:a", first}, {"group:a", second}, {"group:b", other}} {
		if _, inserted, err := store.EnqueueInboundEvent(ctx, item.session, item.event); err != nil || !inserted {
			t.Fatalf("enqueue %s inserted=%v err=%v", item.event.MessageID, inserted, err)
		}
	}

	claimedFirst, ok, err := store.ClaimNextInboundEvent(ctx, "worker-1", time.Now().Add(time.Minute))
	if err != nil || !ok || claimedFirst.Event.MessageID != "a-1" {
		t.Fatalf("first claim=%#v ok=%v err=%v", claimedFirst, ok, err)
	}
	claimedOther, ok, err := store.ClaimNextInboundEvent(ctx, "worker-2", time.Now().Add(time.Minute))
	if err != nil || !ok || claimedOther.Event.MessageID != "b-1" {
		t.Fatalf("parallel claim=%#v ok=%v err=%v", claimedOther, ok, err)
	}
	if err := store.CompleteInboundEvent(ctx, claimedFirst.ID, "worker-1", "done"); err != nil {
		t.Fatal(err)
	}
	claimedSecond, ok, err := store.ClaimNextInboundEvent(ctx, "worker-3", time.Now().Add(time.Minute))
	if err != nil || !ok || claimedSecond.Event.MessageID != "a-2" {
		t.Fatalf("second session claim=%#v ok=%v err=%v", claimedSecond, ok, err)
	}
}

func TestInboundQueuePrioritizesTriggeredEvents(t *testing.T) {
	ctx := context.Background()
	store := openInboundTestStore(t, filepath.Join(t.TempDir(), "priority.db"))
	defer func() { _ = store.Close() }()

	if _, inserted, err := store.EnqueueInboundEvent(ctx, "group:1", inboundTestEvent("normal", "ordinary chat", 1), qqbot.InboundPriorityNormal); err != nil || !inserted {
		t.Fatalf("enqueue normal inserted=%v err=%v", inserted, err)
	}
	priorityID, inserted, err := store.EnqueueInboundEvent(ctx, "group:1", inboundTestEvent("triggered", "direct trigger", 2), qqbot.InboundPriorityTriggered)
	if err != nil || !inserted {
		t.Fatalf("enqueue triggered inserted=%v err=%v", inserted, err)
	}

	item, ok, err := store.ClaimNextInboundEvent(ctx, "worker", time.Now().Add(time.Minute), 3)
	if err != nil || !ok {
		t.Fatalf("claim ok=%v err=%v", ok, err)
	}
	if item.ID != priorityID || item.Event.MessageID != "triggered" || item.Priority != qqbot.InboundPriorityTriggered {
		t.Fatalf("claimed item=%#v", item)
	}
}

func TestInboundQueueAllowsThreeGroupTasksButKeepsPrivateSerial(t *testing.T) {
	ctx := context.Background()
	store := openInboundTestStore(t, filepath.Join(t.TempDir(), "bounded-concurrency.db"))
	defer func() { _ = store.Close() }()

	for index := 1; index <= 4; index++ {
		messageID := fmt.Sprintf("group-%d", index)
		if _, inserted, err := store.EnqueueInboundEvent(ctx, "group:1", inboundTestEvent(messageID, messageID, int64(index))); err != nil || !inserted {
			t.Fatalf("enqueue %s inserted=%v err=%v", messageID, inserted, err)
		}
	}
	claimed := make([]qqbot.InboundQueueItem, 0, 3)
	for index := 1; index <= 3; index++ {
		owner := fmt.Sprintf("group-worker-%d", index)
		item, ok, err := store.ClaimNextInboundEvent(ctx, owner, time.Now().Add(time.Minute), 3)
		if err != nil || !ok {
			t.Fatalf("group claim %d item=%#v ok=%v err=%v", index, item, ok, err)
		}
		claimed = append(claimed, item)
	}
	if item, ok, err := store.ClaimNextInboundEvent(ctx, "group-worker-4", time.Now().Add(time.Minute), 3); err != nil || ok {
		t.Fatalf("fourth group task escaped limit: item=%#v ok=%v err=%v", item, ok, err)
	}
	if err := store.CompleteInboundEvent(ctx, claimed[0].ID, "group-worker-1", "done"); err != nil {
		t.Fatal(err)
	}
	if item, ok, err := store.ClaimNextInboundEvent(ctx, "group-worker-4", time.Now().Add(time.Minute), 3); err != nil || !ok || item.Event.MessageID != "group-4" {
		t.Fatalf("group task after release: item=%#v ok=%v err=%v", item, ok, err)
	}

	for index := 1; index <= 2; index++ {
		event := inboundTestEvent(fmt.Sprintf("private-%d", index), "private", int64(10+index))
		event.Kind = qqbot.EventKindPrivate
		event.GroupID = ""
		if _, inserted, err := store.EnqueueInboundEvent(ctx, "private:2", event); err != nil || !inserted {
			t.Fatalf("enqueue private %d inserted=%v err=%v", index, inserted, err)
		}
	}
	private, ok, err := store.ClaimNextInboundEvent(ctx, "private-worker-1", time.Now().Add(time.Minute), 3)
	if err != nil || !ok || private.Event.MessageID != "private-1" {
		t.Fatalf("private first claim=%#v ok=%v err=%v", private, ok, err)
	}
	if item, ok, err := store.ClaimNextInboundEvent(ctx, "private-worker-2", time.Now().Add(time.Minute), 3); err != nil || ok {
		t.Fatalf("private session was not serial: item=%#v ok=%v err=%v", item, ok, err)
	}
}

func TestInboundQueueHistorySessionsAndWatermark(t *testing.T) {
	ctx := context.Background()
	store := openInboundTestStore(t, filepath.Join(t.TempDir(), "sessions.db"))
	defer func() { _ = store.Close() }()

	for _, event := range []qqbot.MessageEvent{
		{Kind: qqbot.EventKindGroup, GroupID: "100", UserID: "1", MessageID: "g1", Time: 10},
		{Kind: qqbot.EventKindGroup, GroupID: "100", UserID: "2", MessageID: "g2", Time: 30},
		{Kind: qqbot.EventKindGroup, GroupID: "200", UserID: "3", MessageID: "g3", Time: 20},
		{Kind: qqbot.EventKindPrivate, UserID: "300", MessageID: "p1", Time: 40},
		{Kind: qqbot.EventKindNotice, GroupID: "100", MessageID: "n1", Time: 50},
	} {
		session := "private:" + event.UserID
		if event.Kind == qqbot.EventKindGroup || event.Kind == qqbot.EventKindNotice {
			session = "group:" + event.GroupID
		}
		if err := store.AppendMessageEvent(ctx, session, event); err != nil {
			t.Fatal(err)
		}
	}

	watermark, ok, err := store.GroupHistoryWatermark(ctx, "100")
	if err != nil || !ok || watermark != 30 {
		t.Fatalf("watermark=%d ok=%v err=%v", watermark, ok, err)
	}
	if _, ok, err := store.GroupHistoryWatermark(ctx, "missing"); err != nil || ok {
		t.Fatalf("missing watermark ok=%v err=%v", ok, err)
	}

	sessions, err := store.ListHistorySessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []qqbot.HistorySession{
		{Kind: qqbot.EventKindGroup, ID: "100", LastEventTime: 30},
		{Kind: qqbot.EventKindGroup, ID: "200", LastEventTime: 20},
		{Kind: qqbot.EventKindPrivate, ID: "300", LastEventTime: 40},
	}
	if len(sessions) != len(want) {
		t.Fatalf("sessions=%#v", sessions)
	}
	for i := range want {
		if sessions[i] != want[i] {
			t.Fatalf("sessions[%d]=%#v want %#v", i, sessions[i], want[i])
		}
	}
}

func TestSQLiteStoreConfiguresQueuePragmas(t *testing.T) {
	store := openInboundTestStore(t, filepath.Join(t.TempDir(), "pragmas.db"))
	defer func() { _ = store.Close() }()

	var journalMode string
	if err := store.db.QueryRow(`PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatal(err)
	}
	var busyTimeout, foreignKeys int
	if err := store.db.QueryRow(`PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if journalMode != "wal" || busyTimeout != 5000 || foreignKeys != 1 {
		t.Fatalf("journal_mode=%q busy_timeout=%d foreign_keys=%d", journalMode, busyTimeout, foreignKeys)
	}
	if max := store.db.Stats().MaxOpenConnections; max != 1 {
		t.Fatalf("MaxOpenConnections=%d", max)
	}
}

func TestInboundQueueMigrationAddsPriorityColumn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "priority-migration.db")
	store := openInboundTestStore(t, path)
	if _, err := store.db.Exec(`
DROP TABLE inbound_events;
CREATE TABLE inbound_events (
  id TEXT PRIMARY KEY,
  session TEXT NOT NULL,
  kind TEXT NOT NULL,
  group_id TEXT,
  user_id TEXT,
  message_id TEXT,
  event_time INTEGER NOT NULL,
  payload TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending',
  attempts INTEGER NOT NULL DEFAULT 0,
  available_at INTEGER NOT NULL,
  lease_owner TEXT,
  lease_until INTEGER,
  outcome TEXT,
  last_error TEXT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  completed_at INTEGER
);
INSERT INTO inbound_events (
  id, session, kind, group_id, user_id, message_id, event_time, payload,
  status, attempts, available_at, created_at, updated_at
) VALUES ('legacy', 'group:1', 'group', '1', '2', 'legacy', 1, '{}',
          'pending', 0, 1, 1, 1);
`); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store = openInboundTestStore(t, path)
	defer func() { _ = store.Close() }()
	var priority int
	if err := store.db.QueryRow(`SELECT priority FROM inbound_events WHERE id = 'legacy'`).Scan(&priority); err != nil {
		t.Fatal(err)
	}
	if priority != qqbot.InboundPriorityNormal {
		t.Fatalf("migrated priority=%d, want %d", priority, qqbot.InboundPriorityNormal)
	}
	var indexCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_inbound_events_priority_claim'`).Scan(&indexCount); err != nil {
		t.Fatal(err)
	}
	if indexCount != 1 {
		t.Fatalf("priority claim index count=%d, want 1", indexCount)
	}
}

func openInboundTestStore(t *testing.T, path string) *SQLiteStore {
	t.Helper()
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func inboundTestEvent(messageID string, text string, eventTime int64) qqbot.MessageEvent {
	return qqbot.MessageEvent{
		Kind:       qqbot.EventKindGroup,
		GroupID:    "1",
		UserID:     "2",
		MessageID:  messageID,
		Time:       eventTime,
		RawMessage: text,
		Segments: []qqbot.MessageSegment{{
			Type: "text",
			Data: map[string]string{"text": text},
		}},
	}
}

func pendingInboundCount(t *testing.T, store *SQLiteStore) int {
	t.Helper()
	count, err := store.PendingInboundCount(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return count
}
