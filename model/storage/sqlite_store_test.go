package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"diana-qq-bot/model/llm"
	"diana-qq-bot/model/qqbot"
)

func TestSQLiteStoreUsesPrivateFilePermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private-data")
	path := filepath.Join(dir, "app.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("database mode = %#o, want 0600", got)
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("database directory mode = %#o, want 0700", got)
	}
}

func TestSQLiteStoreRejectsSymlinkDatabasePath(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.db")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.db")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if store, err := NewSQLiteStore(link); err == nil {
		_ = store.Close()
		t.Fatal("symlink database path was accepted")
	}
}

// TestSQLiteStorePersistsConfigsAndPluginStates 验证对应功能场景。
func TestSQLiteStorePersistsConfigsAndPluginStates(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	llmCfg := llm.ProviderConfig{
		Provider:        llm.ProviderOpenAICompatible,
		APIKey:          "secret-key",
		BaseURL:         "https://example.com/v1",
		Model:           "example-chat-model",
		ImageModel:      "gpt-image-1",
		UserAgent:       "test-client/1.0",
		Headers:         map[string]string{"X-Relay": "example-relay"},
		MaxOutputTokens: 512,
	}
	if err := store.SaveLLMConfig(ctx, llmCfg); err != nil {
		t.Fatal(err)
	}
	gotLLM, ok, err := store.LoadLLMConfig(ctx)
	if err != nil || !ok {
		t.Fatalf("LoadLLMConfig() ok=%v err=%v", ok, err)
	}
	if gotLLM.APIKey != "secret-key" || gotLLM.Model != "example-chat-model" || gotLLM.UserAgent != "test-client/1.0" || gotLLM.Headers["X-Relay"] != "example-relay" {
		t.Fatalf("gotLLM = %#v", gotLLM)
	}

	botCfg := qqbot.BotConfig{
		Enabled:                 true,
		OneBotReverseWSEndpoint: "ws://127.0.0.1:18080/onebot/v11/ws",
		OneBotAccessToken:       "test-onebot-token",
		NoneBotBridgeToken:      "test-nonebot-token",
		BotQQ:                   "123456",
		RequestTimeout:          30 * time.Second,
	}.WithDefaults()
	if err := store.SaveQQBotConfig(ctx, botCfg); err != nil {
		t.Fatal(err)
	}
	gotBot, ok, err := store.LoadQQBotConfig(ctx)
	if err != nil || !ok {
		t.Fatalf("LoadQQBotConfig() ok=%v err=%v", ok, err)
	}
	if gotBot.OneBotAccessToken != botCfg.OneBotAccessToken || gotBot.BotQQ != botCfg.BotQQ {
		t.Fatalf("gotBot = %#v", gotBot)
	}

	groupConfigs := qqbot.GroupConfigSet{
		Groups: []qqbot.GroupConfig{
			{
				GroupID:            "123456",
				Enabled:            true,
				GroupTriggers:      []string{"Diana"},
				WelcomeEnabled:     true,
				WelcomeMessage:     "欢迎 {user_id}",
				RecentContextLimit: 8,
				MaxReplyChars:      1200,
				PluginOverrides:    map[string]bool{"official.file-parser-go": true},
			},
		},
	}
	if err := store.SaveQQBotGroupConfigs(ctx, groupConfigs); err != nil {
		t.Fatal(err)
	}
	gotGroupConfigs, ok, err := store.LoadQQBotGroupConfigs(ctx)
	if err != nil || !ok {
		t.Fatalf("LoadQQBotGroupConfigs() ok=%v err=%v", ok, err)
	}
	gotGroup, ok := gotGroupConfigs.ConfigForGroup("123456")
	if !ok || !gotGroup.PluginOverrides["official.file-parser-go"] || gotGroup.RecentContextLimit != 8 {
		t.Fatalf("gotGroupConfigs = %#v", gotGroupConfigs)
	}

	pluginStates := map[string]qqbot.PluginState{
		"official.file-parser-go": {
			Manifest:  qqbot.PluginManifest{ID: "official.file-parser-go"},
			Installed: true,
			Enabled:   false,
		},
	}
	if err := store.SavePluginStates(ctx, pluginStates); err != nil {
		t.Fatal(err)
	}
	gotStates, ok, err := store.LoadPluginStates(ctx)
	if err != nil || !ok {
		t.Fatalf("LoadPluginStates() ok=%v err=%v", ok, err)
	}
	if gotStates["official.file-parser-go"].Enabled {
		t.Fatalf("gotStates = %#v", gotStates)
	}

	profiles := llm.NewProfileSet(llmCfg)
	profiles.Profiles[0].Description = "主配置"
	profiles.Profiles = append(profiles.Profiles, llm.Profile{
		ID:          "secondary",
		Name:        "备用",
		Description: "备用配置",
		UpdatedAt:   time.Now().Add(-time.Hour),
		Config: llm.ProviderConfig{
			Provider: llm.ProviderAnthropic,
			APIKey:   "anthropic-key",
			Model:    "claude-sonnet-4-5",
		},
	})
	profiles.ActiveID = "secondary"
	if err := store.SaveLLMProfiles(ctx, profiles); err != nil {
		t.Fatal(err)
	}
	gotProfiles, ok, err := store.LoadLLMProfiles(ctx)
	if err != nil || !ok {
		t.Fatalf("LoadLLMProfiles() ok=%v err=%v", ok, err)
	}
	if gotProfiles.ActiveID != "secondary" || len(gotProfiles.Profiles) != 2 {
		t.Fatalf("gotProfiles = %#v", gotProfiles)
	}
	if gotProfiles.Profiles[0].Config.Headers["X-Relay"] != "example-relay" || gotProfiles.Profiles[1].Description != "备用配置" || gotProfiles.Profiles[1].UpdatedAt.IsZero() {
		t.Fatalf("gotProfiles metadata = %#v", gotProfiles.Profiles[1])
	}

	reminders := []qqbot.Reminder{
		{
			ID:              "r1",
			Kind:            qqbot.ReminderKindQuery,
			OwnerID:         "10001",
			UserID:          "10001",
			Message:         "查询最新公告",
			TriggerAt:       time.Now().Add(6 * time.Hour),
			IntervalSeconds: int64((6 * time.Hour) / time.Second),
			CreatedAt:       time.Now(),
		},
	}
	if err := store.SaveReminders(ctx, reminders); err != nil {
		t.Fatal(err)
	}
	gotReminders, ok, err := store.LoadReminders(ctx)
	if err != nil || !ok {
		t.Fatalf("LoadReminders() ok=%v err=%v", ok, err)
	}
	if len(gotReminders) != 1 || gotReminders[0].Message != "查询最新公告" || gotReminders[0].Kind != qqbot.ReminderKindQuery || gotReminders[0].IntervalSeconds != int64((6*time.Hour)/time.Second) {
		t.Fatalf("gotReminders = %#v", gotReminders)
	}
}

// TestSQLiteStorePersistsAppLogs 验证对应功能场景。
func TestSQLiteStorePersistsAppLogs(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	oldAt := time.Date(2026, 6, 27, 8, 0, 0, 0, time.UTC)
	newAt := oldAt.Add(time.Minute)
	if err := store.AppendLog(ctx, AppLogEntry{
		ID:        "op-old",
		Kind:      LogKindOperation,
		Action:    "qqbot.start",
		Message:   "started",
		Target:    "bot",
		CreatedAt: oldAt,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendLog(ctx, AppLogEntry{
		ID:        "err-new",
		Kind:      LogKindError,
		Action:    "llm.test",
		Message:   "failed",
		Detail:    "bad gateway",
		Metadata:  map[string]any{"provider": "openai_compatible", "count": 2},
		CreatedAt: newAt,
	}); err != nil {
		t.Fatal(err)
	}

	all, err := store.ListLogs(ctx, AppLogFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].ID != "err-new" || all[1].ID != "op-old" {
		t.Fatalf("all logs = %#v", all)
	}
	if all[0].Level != LogLevelError || all[0].Metadata["provider"] != "openai_compatible" {
		t.Fatalf("error log = %#v", all[0])
	}

	operations, err := store.ListLogs(ctx, AppLogFilter{Kind: LogKindOperation, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(operations) != 1 || operations[0].Action != "qqbot.start" {
		t.Fatalf("operation logs = %#v", operations)
	}
}

func TestSQLiteStorePersistsMessageEvents(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	session := "group:123"
	first := qqbot.MessageEvent{
		Kind:       qqbot.EventKindGroup,
		GroupID:    "123",
		UserID:     "10001",
		MessageID:  "m1",
		Time:       100,
		RawMessage: "聊最近的漫展",
		Segments:   []qqbot.MessageSegment{{Type: "text", Data: map[string]string{"text": "聊最近的漫展"}}},
	}
	second := first
	second.MessageID = "m2"
	second.Time = 101
	second.RawMessage = "然后问刚刚那个"
	second.Segments = []qqbot.MessageSegment{{Type: "text", Data: map[string]string{"text": second.RawMessage}}}

	if err := store.AppendMessageEvent(ctx, session, first); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessageEvent(ctx, session, second); err != nil {
		t.Fatal(err)
	}
	got, err := store.ListRecentMessageEvents(ctx, session, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].MessageID != "m2" || got[0].RawMessage != "然后问刚刚那个" {
		t.Fatalf("got = %#v", got)
	}
	got, err = store.ListRecentMessageEvents(ctx, session, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].MessageID != "m1" || got[1].MessageID != "m2" {
		t.Fatalf("got chronological = %#v", got)
	}

	third := first
	third.MessageID = "m3"
	third.Time = 200
	third.RawMessage = "时间线之外"
	third.Segments = []qqbot.MessageSegment{{Type: "text", Data: map[string]string{"text": third.RawMessage}}}
	if err := store.AppendMessageEvent(ctx, session, third); err != nil {
		t.Fatal(err)
	}
	window, err := store.ListMessageEventsBetween(ctx, session, 100, 101)
	if err != nil {
		t.Fatal(err)
	}
	if len(window) != 2 || window[0].MessageID != "m1" || window[1].MessageID != "m2" {
		t.Fatalf("timeline window = %#v", window)
	}
}

func TestSQLiteStorePersistsUserMemory(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	first := qqbot.MessageEvent{
		Kind:       qqbot.EventKindGroup,
		GroupID:    "123",
		UserID:     "10001",
		MessageID:  "m1",
		Time:       100,
		SenderName: "Alice",
		RawMessage: "我最近在看漫展",
		Segments:   []qqbot.MessageSegment{{Type: "text", Data: map[string]string{"text": "我最近在看漫展"}}},
	}
	profile, err := store.UpdateUserMemory(ctx, first, qqbot.UserMemoryUpdate{})
	if err != nil {
		t.Fatal(err)
	}
	if profile.Favorability != 0 || profile.MessageCount != 1 || len(profile.Memories) != 1 {
		t.Fatalf("profile = %#v", profile)
	}

	second := first
	second.MessageID = "m2"
	second.Time = 101
	second.RawMessage = "谢谢你太强了"
	second.Segments = []qqbot.MessageSegment{{Type: "text", Data: map[string]string{"text": second.RawMessage}}}
	profile, err = store.UpdateUserMemory(ctx, second, qqbot.UserMemoryUpdate{FavorabilityDelta: 3})
	if err != nil {
		t.Fatal(err)
	}
	if profile.Favorability != 3 || profile.MessageCount != 2 {
		t.Fatalf("profile after interaction = %#v", profile)
	}
	third := first
	third.MessageID = "m3"
	third.Time = 102
	third.RawMessage = "还是说骂笨蛋，然后减几滴"
	third.Segments = []qqbot.MessageSegment{{Type: "text", Data: map[string]string{"text": third.RawMessage}}}
	profile, err = store.UpdateUserMemory(ctx, third, qqbot.UserMemoryUpdate{})
	if err != nil {
		t.Fatal(err)
	}
	if profile.Favorability != 3 || profile.MessageCount != 3 {
		t.Fatalf("message text changed score without semantic delta: %#v", profile)
	}
	adminValue := 80
	profile, err = store.UpdateUserMemory(ctx, qqbot.MessageEvent{UserID: "10001"}, qqbot.UserMemoryUpdate{
		OwnerID: "20002", SetFavorability: &adminValue, Administrative: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if profile.Favorability != 80 || profile.MessageCount != 3 || len(profile.Memories) != 3 {
		t.Fatalf("administrative update changed interaction history: %#v", profile)
	}

	got, ok, err := store.GetUserMemory(ctx, "10001")
	if err != nil || !ok {
		t.Fatalf("GetUserMemory() ok=%v err=%v", ok, err)
	}
	if got.DisplayName != "Alice" || got.Favorability != 80 || got.MessageCount != 3 || len(got.Memories) != 3 || got.Memories[0].Text != "我最近在看漫展" || got.Memories[1].Text != "谢谢你太强了" || got.Memories[2].Text != third.RawMessage {
		t.Fatalf("got = %#v", got)
	}

	owner, err := store.UpdateUserMemory(ctx, qqbot.MessageEvent{
		Kind:      qqbot.EventKindPrivate,
		UserID:    "20002",
		MessageID: "owner-1",
		Segments:  []qqbot.MessageSegment{{Type: "text", Data: map[string]string{"text": "在吗"}}},
	}, qqbot.UserMemoryUpdate{OwnerID: "20002", FavorabilityDelta: 1})
	if err != nil {
		t.Fatal(err)
	}
	if owner.Favorability < 100 {
		t.Fatalf("owner favorability = %d, want at least 100", owner.Favorability)
	}
}

func TestSQLiteStoreSerializesConcurrentUserMemoryUpdates(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	const updates = 20
	errs := make(chan error, updates*2)
	var wg sync.WaitGroup
	for index := 0; index < updates; index++ {
		wg.Add(2)
		go func(index int) {
			defer wg.Done()
			_, err := store.UpdateUserMemory(ctx, qqbot.MessageEvent{
				Kind:      qqbot.EventKindPrivate,
				UserID:    "user",
				MessageID: fmt.Sprintf("message-%d", index),
				Segments:  []qqbot.MessageSegment{{Type: "text", Data: map[string]string{"text": fmt.Sprintf("message %d", index)}}},
			}, qqbot.UserMemoryUpdate{})
			errs <- err
		}(index)
		go func() {
			defer wg.Done()
			_, err := store.UpdateUserMemory(ctx, qqbot.MessageEvent{UserID: "user"}, qqbot.UserMemoryUpdate{
				FavorabilityDelta: 1,
				Administrative:    true,
			})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	profile, ok, err := store.GetUserMemory(ctx, "user")
	if err != nil || !ok {
		t.Fatalf("GetUserMemory() ok=%v err=%v", ok, err)
	}
	if profile.Favorability != updates || profile.MessageCount != updates {
		t.Fatalf("profile = %#v, want favorability and message count %d", profile, updates)
	}
}
