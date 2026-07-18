package qqbot

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"diana-qq-bot/model/llm"
)

type recallImageTestStore struct {
	mu           sync.Mutex
	descriptions map[string]ImageDescriptionRecord
	timeline     []MessageEvent
	saves        int
}

func newRecallImageTestStore() *recallImageTestStore {
	return &recallImageTestStore{descriptions: map[string]ImageDescriptionRecord{}}
}

func (s *recallImageTestStore) AppendMessageEvent(context.Context, string, MessageEvent) error {
	return nil
}

func (s *recallImageTestStore) ListRecentMessageEvents(context.Context, string, int) ([]MessageEvent, error) {
	return append([]MessageEvent(nil), s.timeline...), nil
}

func (s *recallImageTestStore) ListMessageEventsBetween(context.Context, string, int64, int64) ([]MessageEvent, error) {
	return append([]MessageEvent(nil), s.timeline...), nil
}

func (s *recallImageTestStore) GetImageDescription(_ context.Context, hash string) (ImageDescriptionRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.descriptions[hash]
	return record, ok, nil
}

func (s *recallImageTestStore) SaveImageDescription(_ context.Context, record ImageDescriptionRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.descriptions[record.ContentSHA256] = record
	s.saves++
	return nil
}

type recallImageVisionProvider struct {
	mu    sync.Mutex
	calls int
}

func (p *recallImageVisionProvider) Generate(_ context.Context, req llm.GenerateRequest) (*llm.GenerateResponse, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	if len(req.Messages) != 2 || len(req.Messages[1].Parts) != 2 || req.Messages[1].Parts[1].Type != llm.ContentPartImageURL {
		return nil, context.Canceled
	}
	return &llm.GenerateResponse{Text: "一张系统监控截图，请求命中率为 63%，并显示上游流量统计。"}, nil
}

func (p *recallImageVisionProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func TestBuildRecallPluginResponseUsesCachedDescriptionAndAttachesOnlyMissingImages(t *testing.T) {
	recalls := []MessageEvent{
		{
			Kind:      EventKindNotice,
			SubType:   "group_recall",
			GroupID:   "group-1",
			MessageID: "image-described",
			Segments: []MessageSegment{{Type: "image", Data: map[string]string{
				imageContentSHA256Key:     strings.Repeat("a", 64),
				recallImageDescriptionKey: "一张已经识别过的版本信息截图。",
				"url":                     "https://example.com/described.png",
			}}},
		},
		{
			Kind:      EventKindNotice,
			SubType:   "group_recall",
			GroupID:   "group-1",
			MessageID: "image-missing",
			Segments: []MessageSegment{{Type: "image", Data: map[string]string{
				imageContentSHA256Key: strings.Repeat("b", 64),
				"url":                 "https://example.com/missing.png",
			}}},
		},
	}
	resp := buildRecallPluginResponse(recalls, 1_800_000_000, true)
	if len(resp.ContextImageURLs) != 1 || resp.ContextImageURLs[0] != "https://example.com/missing.png" {
		t.Fatalf("context images = %#v", resp.ContextImageURLs)
	}
	for _, want := range []string{"图片1内容描述=一张已经识别过的版本信息截图。", "请查看多模态附件1后客观描述"} {
		if !strings.Contains(resp.Context, want) {
			t.Fatalf("context missing %q: %s", want, resp.Context)
		}
	}
}

func TestRecallImageDescriptionUsesPersistentCacheWithoutLLM(t *testing.T) {
	imagePath, hash := writeRecallImageFixture(t)
	store := newRecallImageTestStore()
	store.descriptions[hash] = ImageDescriptionRecord{ContentSHA256: hash, Description: "缓存中的图片描述", Source: "vision"}
	provider := &recallImageVisionProvider{}
	runtime := NewRuntime(BotConfig{BotQQ: "bot"}, nilChannel{}, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	runtime.SetMessageHistoryStore(store)

	got := runtime.enrichRecallImageDescriptions(context.Background(), MessageEvent{Kind: EventKindGroup, GroupID: "group-1"}, []MessageEvent{recallImageEvent("source-1", imagePath)})
	if got[0].Segments[0].Data[recallImageDescriptionKey] != "缓存中的图片描述" {
		t.Fatalf("description = %#v", got[0].Segments[0].Data)
	}
	if provider.callCount() != 0 {
		t.Fatalf("vision calls = %d, want 0", provider.callCount())
	}
}

func TestRecallImageDescriptionBackfillsHistoricalSemanticReply(t *testing.T) {
	imagePath, hash := writeRecallImageFixture(t)
	store := newRecallImageTestStore()
	store.timeline = []MessageEvent{
		{
			Kind:      EventKindGroup,
			Time:      100,
			SelfID:    "bot",
			UserID:    "user",
			GroupID:   "group-1",
			MessageID: "old-copy",
			Segments:  []MessageSegment{{Type: "image", Data: map[string]string{"cached_file": imagePath}}},
		},
		{
			Kind:                    EventKindGroup,
			Time:                    110,
			SelfID:                  "bot",
			UserID:                  "bot",
			GroupID:                 "group-1",
			MessageID:               "bot-answer",
			SenderName:              "Diana",
			SemanticSourceMessageID: "old-copy",
			Segments:                []MessageSegment{{Type: "text", Data: map[string]string{"text": "历史回答已经识别出这是舞萌 DX 查分图，DX Rating 为 13470。"}}},
		},
	}
	provider := &recallImageVisionProvider{}
	runtime := NewRuntime(BotConfig{BotQQ: "bot", Name: "Diana"}, nilChannel{}, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	runtime.SetMessageHistoryStore(store)

	got := runtime.enrichRecallImageDescriptions(context.Background(), MessageEvent{Kind: EventKindGroup, GroupID: "group-1", Time: 120}, []MessageEvent{recallImageEvent("new-copy", imagePath)})
	if description := got[0].Segments[0].Data[recallImageDescriptionKey]; !strings.Contains(description, "DX Rating 为 13470") {
		t.Fatalf("description = %q", description)
	}
	if provider.callCount() != 0 {
		t.Fatalf("vision calls = %d, want 0", provider.callCount())
	}
	if record := store.descriptions[hash]; record.Source != "history" || record.SourceMessageID != "new-copy" {
		t.Fatalf("cached record = %#v", record)
	}
}

func TestRecallImageDescriptionCallsVisionOnceThenReusesCache(t *testing.T) {
	imagePath, hash := writeRecallImageFixture(t)
	store := newRecallImageTestStore()
	provider := &recallImageVisionProvider{}
	runtime := NewRuntime(BotConfig{BotQQ: "bot"}, nilChannel{}, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	runtime.SetMessageHistoryStore(store)
	event := MessageEvent{Kind: EventKindGroup, GroupID: "group-1", Time: 200}
	recall := recallImageEvent("source-1", imagePath)

	first := runtime.enrichRecallImageDescriptions(context.Background(), event, []MessageEvent{recall})
	second := runtime.enrichRecallImageDescriptions(context.Background(), event, []MessageEvent{recall})
	for _, got := range [][]MessageEvent{first, second} {
		if !strings.Contains(got[0].Segments[0].Data[recallImageDescriptionKey], "命中率为 63%") {
			t.Fatalf("description = %#v", got[0].Segments[0].Data)
		}
	}
	if provider.callCount() != 1 {
		t.Fatalf("vision calls = %d, want 1", provider.callCount())
	}
	if record := store.descriptions[hash]; record.Source != "vision" || record.Version != recallImageDescriptionVersion {
		t.Fatalf("cached record = %#v", record)
	}
}

func recallImageEvent(messageID, path string) MessageEvent {
	return MessageEvent{
		Kind:      EventKindNotice,
		SubType:   "group_recall",
		GroupID:   "group-1",
		MessageID: messageID,
		Segments:  []MessageSegment{{Type: "image", Data: map[string]string{"cached_file": path}}},
	}
}

func writeRecallImageFixture(t *testing.T) (string, string) {
	t.Helper()
	body, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "image.png")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, imageBytesSHA256(body)
}
