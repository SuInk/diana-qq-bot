package qqbot

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"diana-qq-bot/model/applog"
	"diana-qq-bot/model/llm"

	"rsc.io/pdf"
)

// TestPluginManagerInstallEnableRun 验证对应功能场景。
func TestPluginManagerInstallEnableRun(t *testing.T) {
	manager := NewPluginManager(testPlugin{})
	state, ok := manager.Get("test")
	if !ok {
		t.Fatal("plugin missing")
	}
	if state.Installed {
		t.Fatalf("Installed = true, want false for non built-in plugin")
	}

	if _, err := manager.Install("test"); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	responses := manager.Run(context.Background(), PluginRequest{Text: "hello"})
	if len(responses) != 1 || responses[0].Context != "ctx: hello" {
		t.Fatalf("responses = %#v", responses)
	}

	if _, err := manager.SetEnabled("test", false); err != nil {
		t.Fatalf("SetEnabled() error = %v", err)
	}
	if responses := manager.Run(context.Background(), PluginRequest{Text: "hello"}); len(responses) != 0 {
		t.Fatalf("disabled responses = %#v", responses)
	}
}

func TestPluginManagerRunRecoversPluginPanic(t *testing.T) {
	manager := NewPluginManager(panicPlugin{}, testPlugin{})
	if _, err := manager.Install("panic"); err != nil {
		t.Fatalf("Install(panic) error = %v", err)
	}
	if _, err := manager.Install("test"); err != nil {
		t.Fatalf("Install(test) error = %v", err)
	}
	responses := manager.Run(context.Background(), PluginRequest{Text: "hello"})
	if len(responses) != 1 || responses[0].Context != "ctx: hello" {
		t.Fatalf("responses = %#v", responses)
	}
}

func TestPluginManagerObserveRecoversPluginPanic(t *testing.T) {
	manager := NewPluginManager(panicObserverPlugin{})
	event := manager.ObserveEvent(context.Background(), MessageEvent{
		Kind:       EventKindGroup,
		GroupID:    "123",
		MessageID:  "m1",
		RawMessage: "hello",
	})
	if event.RawMessage != "hello" {
		t.Fatalf("event = %#v", event)
	}
}

// TestResolverPluginExtractsKnownPlatformContext 验证对应功能场景。
func TestResolverPluginExtractsKnownPlatformContext(t *testing.T) {
	t.Setenv("DIANA_XHS_CK", "")
	t.Setenv("XHS_CK", "")
	t.Setenv("xhs_ck", "")
	plugin := NewResolverPlugin(&http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusNoContent,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	})})
	plugin.videoDownloader = func(context.Context, string) string {
		return ""
	}
	resp, err := plugin.Handle(context.Background(), PluginRequest{Text: "看这个 https://www.xiaohongshu.com/discovery/item/abc?xsec_source=pc_share&amp;xsec_token=tok"})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if resp == nil || !resp.Handled {
		t.Fatalf("resp = %#v", resp)
	}
	if strings.Contains(resp.Context, "链接解析结果") || !strings.Contains(resp.Context, "识别内容来自：【小红书】") {
		t.Fatalf("Context = %q", resp.Context)
	}
	if !resp.Forward || len(resp.ForwardMessages) != 1 {
		t.Fatalf("forward resp = %#v", resp)
	}
}

func TestFetchFinalURLDetailsReportsExpiredShortLink(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil)
	}))
	defer server.Close()

	finalURL, statusCode, err := fetchFinalURLDetails(context.Background(), server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if finalURL != server.URL || statusCode != http.StatusNotFound {
		t.Fatalf("finalURL=%q statusCode=%d", finalURL, statusCode)
	}
}

func TestIsXiaohongshuLiveURL(t *testing.T) {
	for _, rawURL := range []string{
		"http://xhslink.com/m/9ry2BJL0V4D",
		"https://www.xiaohongshu.com/live/123",
		"https://www.xiaohongshu.com/livestream/123",
	} {
		if !isXiaohongshuLiveURL(rawURL) {
			t.Fatalf("isXiaohongshuLiveURL(%q) = false", rawURL)
		}
	}
	if isXiaohongshuLiveURL("https://www.xiaohongshu.com/explore/note-id") {
		t.Fatal("ordinary note classified as live")
	}
}

func TestFetchXiaohongshuNoteIntegration(t *testing.T) {
	rawURL := strings.TrimSpace(os.Getenv("DIANA_XHS_TEST_URL"))
	if rawURL == "" {
		t.Skip("set DIANA_XHS_TEST_URL and XHS_CK to test the live parser")
	}
	note, status := fetchXiaohongshuNote(context.Background(), rawURL)
	if status != "" || len(note) == 0 {
		t.Fatalf("status=%q note=%#v", status, note)
	}
	t.Logf("parsed type=%q title=%q", anyString(note["type"]), anyString(note["title"]))
}

func TestResolverPluginExtractsURLFromRawMessage(t *testing.T) {
	plugin := NewResolverPlugin(&http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/html"}},
			Body:       io.NopCloser(strings.NewReader("<title>Raw 标题</title>")),
		}, nil
	})})
	plugin.videoDownloader = func(context.Context, string) string {
		return ""
	}
	resp, err := plugin.Handle(context.Background(), PluginRequest{
		Event: MessageEvent{RawMessage: "分享 https://github.com/SuInk/diana-qq-bot"},
	})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if resp == nil || !strings.Contains(resp.Context, "Raw 标题") {
		t.Fatalf("resp = %#v", resp)
	}
}

func TestResolverPluginDownloadsPlatformVideo(t *testing.T) {
	plugin := NewResolverPlugin(&http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/html"}},
			Body:       io.NopCloser(strings.NewReader("<title>X 视频</title>")),
		}, nil
	})})
	plugin.videoDownloader = func(_ context.Context, raw string) string {
		if !strings.Contains(raw, "x.com") {
			t.Fatalf("downloader raw=%q", raw)
		}
		return "/tmp/diana-test-video.mp4"
	}
	logs := &captureAppLogs{}

	resp, err := plugin.Handle(context.Background(), PluginRequest{
		Text:    "看这个 https://x.com/example/status/1",
		Event:   MessageEvent{Kind: EventKindPrivate, UserID: "10001"},
		AppLogs: logs,
	})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if resp == nil || !resp.Handled || len(resp.VideoURLs) != 1 {
		t.Fatalf("resp = %#v", resp)
	}
	if resp.VideoURLs[0] != "/tmp/diana-test-video.mp4" {
		t.Fatalf("VideoURLs = %#v", resp.VideoURLs)
	}
	if len(resp.ImageURLs) != 0 {
		t.Fatalf("ImageURLs = %#v", resp.ImageURLs)
	}
	if resp.Context != "识别：小蓝鸟学习版" {
		t.Fatalf("Context = %q", resp.Context)
	}
	if !resp.Forward || len(resp.ForwardMessages) != 2 {
		t.Fatalf("forward resp = %#v", resp)
	}
	if resp.ForwardMessages[0].Text != "识别：小蓝鸟学习版" {
		t.Fatalf("meta node = %#v", resp.ForwardMessages[0])
	}
	if len(resp.ForwardMessages[1].VideoURLs) != 1 || resp.ForwardMessages[1].VideoURLs[0] != "/tmp/diana-test-video.mp4" {
		t.Fatalf("video node = %#v", resp.ForwardMessages[1])
	}
	if len(logs.entries) != 1 || logs.entries[0].Action != "qqbot.resolver.video_download" || logs.entries[0].Kind != applog.KindOperation {
		t.Fatalf("logs = %#v", logs.entries)
	}
}

func TestResolverPluginDedupesHTMLEscapedURLs(t *testing.T) {
	raw := "https://www.xiaohongshu.com/discovery/item/abc?xsec_source=pc_share&xsec_token=tok"
	escaped := "https://www.xiaohongshu.com/discovery/item/abc?xsec_source=pc_share&amp;xsec_token=tok"
	doubleEscaped := "https://www.xiaohongshu.com/discovery/item/abc?xsec_source=pc_share&amp;amp;xsec_token=tok"
	urls := extractResolverRequestURLs(PluginRequest{
		Text:  "看这个 " + escaped + " " + doubleEscaped,
		Event: MessageEvent{RawMessage: "分享 " + raw},
	})
	if len(urls) != 1 || urls[0] != raw {
		t.Fatalf("urls = %#v", urls)
	}
}

func TestResolverPluginExtractsEscapedQQMiniAppURL(t *testing.T) {
	raw := `[CQ:json,data={"meta":{"detail_1":{"title":"哔哩哔哩","qqdocurl":"https:\/\/b23.tv\/tOnAfAQ?share_medium=android&amp;share_source=qq"}}}]`
	urls := extractResolverRequestURLs(PluginRequest{
		Event: MessageEvent{RawMessage: raw},
	})
	want := "https://b23.tv/tOnAfAQ?share_medium=android&share_source=qq"
	if len(urls) != 1 || urls[0] != want {
		t.Fatalf("urls = %#v, want %#v", urls, want)
	}
	platformURLs := knownResolverPlatformURLs(raw)
	if len(platformURLs) != 1 || platformURLs[0] != want {
		t.Fatalf("platformURLs = %#v, want %#v", platformURLs, want)
	}
}

// TestDefaultPluginManagerIncludesFileParser 验证对应功能场景。
func TestDefaultPluginManagerIncludesFileParser(t *testing.T) {
	manager := NewDefaultPluginManager()
	state, ok := manager.Get("official.file-parser-go")
	if !ok {
		t.Fatal("file parser plugin missing")
	}
	if !state.Installed || !state.Enabled {
		t.Fatalf("file parser state = %#v", state)
	}
}

// TestDefaultPluginManagerIncludesLLMConfigSkill 验证对应功能场景。
func TestDefaultPluginManagerIncludesLLMConfigSkill(t *testing.T) {
	manager := NewDefaultPluginManager()
	state, ok := manager.Get("official.llm-config-skill")
	if !ok {
		t.Fatal("llm config skill missing")
	}
	if !state.Installed || !state.Enabled {
		t.Fatalf("llm config skill state = %#v", state)
	}
}

// TestPluginManagerRestoreKeepsBuiltInDisabledChoice 验证对应功能场景。
func TestPluginManagerRestoreKeepsBuiltInDisabledChoice(t *testing.T) {
	manager := NewDefaultPluginManager()
	manager.Restore(map[string]PluginState{
		"official.file-parser-go": {
			Enabled: false,
		},
	})
	state, ok := manager.Get("official.file-parser-go")
	if !ok {
		t.Fatal("file parser plugin missing")
	}
	if !state.Installed {
		t.Fatalf("Installed = false, want true")
	}
	if state.Enabled {
		t.Fatalf("Enabled = true, want false")
	}
}

// TestFileParserPluginParsesTextFileURL 验证对应功能场景。
func TestFileParserPluginParsesTextFileURL(t *testing.T) {
	plugin := NewFileParserPlugin(&http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/plain; charset=utf-8"}},
			Body:       io.NopCloser(strings.NewReader("hello\nworld")),
		}, nil
	})})

	resp, err := plugin.Handle(context.Background(), PluginRequest{Text: "看文件 https://example.com/report.txt"})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if resp == nil || !resp.Handled {
		t.Fatalf("resp = %#v", resp)
	}
	if !strings.Contains(resp.Context, "report.txt") || !strings.Contains(resp.Context, "hello") {
		t.Fatalf("Context = %q", resp.Context)
	}
}

// TestFileParserPluginCollectsRecentPDFFile 验证当前问题能引用最近文件消息里的 PDF。
func TestFileParserPluginCollectsRecentPDFFile(t *testing.T) {
	refs := collectFileRefs(PluginRequest{
		Text: "这两个文件有什么区别",
		RecentEvents: []MessageEvent{{
			Kind:    EventKindGroup,
			GroupID: "123456",
			Segments: []MessageSegment{{
				Type: "file",
				Data: map[string]string{
					"name":    "项目说明文档.pdf",
					"file_id": "file-1",
					"busid":   "101",
				},
			}},
		}},
	})
	if len(refs) != 1 {
		t.Fatalf("refs = %#v", refs)
	}
	if refs[0].Name != "项目说明文档.pdf" || refs[0].FileID != "file-1" || refs[0].GroupID != "123456" {
		t.Fatalf("ref = %#v", refs[0])
	}
}

func TestFileParserPluginDoesNotCollectUnreferencedRecentFile(t *testing.T) {
	for _, text := range []string{"宝", "什么意思", "重试下"} {
		t.Run(text, func(t *testing.T) {
			refs := collectFileRefs(PluginRequest{
				Text: text,
				RecentEvents: []MessageEvent{{
					Kind:    EventKindGroup,
					GroupID: "123456",
					Segments: []MessageSegment{{
						Type: "file",
						Data: map[string]string{
							"name":    "讲义.pdf",
							"file_id": "file-1",
						},
					}},
				}},
			})
			if len(refs) != 0 {
				t.Fatalf("refs = %#v, want none", refs)
			}
		})
	}
}

func TestFileParserPluginCollectsCurrentAndQuotedFiles(t *testing.T) {
	refs := collectFileRefs(PluginRequest{
		Text: "看看",
		Event: MessageEvent{
			Kind:    EventKindGroup,
			GroupID: "123456",
			Segments: []MessageSegment{{
				Type: "file",
				Data: map[string]string{"name": "当前.txt", "file_id": "current-file"},
			}},
			Quoted: &QuotedMessage{
				GroupID: "654321",
				Segments: []MessageSegment{{
					Type: "file",
					Data: map[string]string{"name": "引用.pdf", "file_id": "quoted-file"},
				}},
			},
		},
	})
	if len(refs) != 2 {
		t.Fatalf("refs = %#v", refs)
	}
	if refs[0].FileID != "current-file" || refs[0].GroupID != "123456" {
		t.Fatalf("current ref = %#v", refs[0])
	}
	if refs[1].FileID != "quoted-file" || refs[1].GroupID != "654321" {
		t.Fatalf("quoted ref = %#v", refs[1])
	}
}

func TestJoinPDFTextLineUsesGlyphGapsForWordSpacing(t *testing.T) {
	items := []pdf.Text{
		{S: "U", X: 0, W: 5, FontSize: 10},
		{S: "N", X: 5, W: 5, FontSize: 10},
		{S: "I", X: 13, W: 2, FontSize: 10},
		{S: "T", X: 15, W: 5, FontSize: 10},
		{S: "E", X: 20, W: 5, FontSize: 10},
		{S: "D", X: 25, W: 5, FontSize: 10},
		{S: ",", X: 30, W: 2, FontSize: 10},
		{S: " ", X: 32, W: 3, FontSize: 10},
		{S: "中", X: 35, W: 10, FontSize: 10},
		{S: "文", X: 45, W: 10, FontSize: 10},
	}
	if got := joinPDFTextLine(items); got != "UN ITED, 中文" {
		t.Fatalf("joinPDFTextLine() = %q", got)
	}
}

// TestFileParserPluginResolvesOneBotFileID 验证 QQ 文件段只有 file_id 时会调用 OneBot 获取文件。
func TestFileParserPluginResolvesOneBotFileID(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "report.txt")
	if err := os.WriteFile(filePath, []byte("hello from onebot file"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	channel := &fileResolveChannel{filePath: filePath}
	plugin := NewFileParserPlugin(nil)
	resp, err := plugin.Handle(context.Background(), PluginRequest{
		Channel: channel,
		Event: MessageEvent{
			Kind:    EventKindGroup,
			GroupID: "123456",
			Segments: []MessageSegment{{
				Type: "file",
				Data: map[string]string{
					"name":    "report.txt",
					"file_id": "file-1",
				},
			}},
		},
	})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if resp == nil || !strings.Contains(resp.Context, "hello from onebot file") {
		t.Fatalf("resp = %#v", resp)
	}
	if len(channel.calls) == 0 || channel.calls[0] != "get_group_file_url" {
		t.Fatalf("calls = %#v", channel.calls)
	}
}

func TestOneBotFileResolveRequestsFallsBackToFilename(t *testing.T) {
	requests := oneBotFileResolveRequests(fileRef{
		Name:    "report.pdf",
		FileID:  "stale-file-id",
		GroupID: "123456",
	})
	if len(requests) != 4 {
		t.Fatalf("requests = %#v", requests)
	}
	filenameFallback := requests[1]
	if filenameFallback.action != "get_file" || filenameFallback.params["file"] != "report.pdf" {
		t.Fatalf("filename fallback = %#v", filenameFallback)
	}
}

func TestFileParserPluginRefreshesStaleFileFromGroupHistory(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "report.txt")
	if err := os.WriteFile(filePath, []byte("history refreshed file"), 0o600); err != nil {
		t.Fatal(err)
	}
	channel := &historyFileResolveChannel{filePath: filePath}
	plugin := NewFileParserPlugin(nil)
	resp, err := plugin.Handle(context.Background(), PluginRequest{
		Channel: channel,
		Event: MessageEvent{
			Kind:    EventKindGroup,
			GroupID: "123456",
			Segments: []MessageSegment{{Type: "file", Data: map[string]string{
				"name":    "report.txt",
				"file_id": "stale-file-id",
			}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil || !strings.Contains(resp.Context, "history refreshed file") {
		t.Fatalf("resp = %#v", resp)
	}
	if !containsString(channel.calls, "get_group_msg_history") {
		t.Fatalf("calls = %#v", channel.calls)
	}
}

func TestRefreshOneBotFilePaginatesOlderGroupHistory(t *testing.T) {
	channel := &pagedHistoryFileResolveChannel{}
	ref, ok := refreshOneBotFileFromGroupHistory(context.Background(), channel, fileRef{
		Name:    "older.pdf",
		FileID:  "stale-file-id",
		GroupID: "123456",
	})
	if !ok || ref.FileID != "fresh-file-id" {
		t.Fatalf("ref = %#v ok = %v", ref, ok)
	}
	if len(channel.params) != 2 || channel.params[1]["message_seq"] != "oldest-page-1" || channel.params[1]["reverse_order"] != true {
		t.Fatalf("params = %#v", channel.params)
	}
}

// TestFileParserPluginIgnoresUnsupportedURL 验证对应功能场景。
func TestFileParserPluginIgnoresUnsupportedURL(t *testing.T) {
	plugin := NewFileParserPlugin(nil)
	resp, err := plugin.Handle(context.Background(), PluginRequest{Text: "图片 https://example.com/a.png"})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if resp != nil {
		t.Fatalf("resp = %#v, want nil", resp)
	}
}

type fileResolveChannel struct {
	filePath string
	calls    []string
}

type historyFileResolveChannel struct {
	filePath string
	calls    []string
}

type pagedHistoryFileResolveChannel struct {
	params []map[string]any
}

func (c *pagedHistoryFileResolveChannel) Connect(context.Context, EventHandler) error { return nil }
func (c *pagedHistoryFileResolveChannel) Send(context.Context, OutgoingMessage) error { return nil }
func (c *pagedHistoryFileResolveChannel) Status() ChannelStatus                       { return ChannelStatus{} }
func (c *pagedHistoryFileResolveChannel) Close() error                                { return nil }
func (c *pagedHistoryFileResolveChannel) CallAPI(_ context.Context, action string, params map[string]any) (map[string]any, error) {
	if action != "get_group_msg_history" {
		return nil, errors.New("unexpected action")
	}
	c.params = append(c.params, params)
	if len(c.params) == 1 {
		return map[string]any{"messages": []any{map[string]any{
			"message_id": "oldest-page-1",
			"message":    []any{map[string]any{"type": "text", "data": map[string]any{"text": "newer"}}},
		}}}, nil
	}
	return map[string]any{"messages": []any{map[string]any{
		"message_id": "oldest-page-2",
		"message": []any{map[string]any{
			"type": "file",
			"data": map[string]any{"file": "older.pdf", "file_id": "fresh-file-id"},
		}},
	}}}, nil
}

func (c *historyFileResolveChannel) Connect(context.Context, EventHandler) error { return nil }
func (c *historyFileResolveChannel) Send(context.Context, OutgoingMessage) error { return nil }
func (c *historyFileResolveChannel) Status() ChannelStatus                       { return ChannelStatus{} }
func (c *historyFileResolveChannel) Close() error                                { return nil }
func (c *historyFileResolveChannel) CallAPI(_ context.Context, action string, params map[string]any) (map[string]any, error) {
	c.calls = append(c.calls, action)
	switch action {
	case "get_group_msg_history":
		return map[string]any{"messages": []any{map[string]any{
			"message": []any{map[string]any{
				"type": "file",
				"data": map[string]any{"file": "report.txt", "file_id": "fresh-file-id"},
			}},
		}}}, nil
	case "get_file":
		if params["file_id"] == "fresh-file-id" || params["file"] == "fresh-file-id" {
			return map[string]any{"file": c.filePath}, nil
		}
	}
	return nil, errors.New("not found")
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func (c *fileResolveChannel) Connect(context.Context, EventHandler) error { return nil }
func (c *fileResolveChannel) Send(context.Context, OutgoingMessage) error { return nil }
func (c *fileResolveChannel) Status() ChannelStatus                       { return ChannelStatus{} }
func (c *fileResolveChannel) Close() error                                { return nil }
func (c *fileResolveChannel) CallAPI(_ context.Context, action string, _ map[string]any) (map[string]any, error) {
	c.calls = append(c.calls, action)
	return map[string]any{"file": c.filePath}, nil
}

// TestLLMConfigPluginUpdatesProviderAndModel 验证对应功能场景。
func TestDianaLLMConfigToolUpdatesProviderAndModel(t *testing.T) {
	store := &stubLLMProfileStore{
		set: llm.ProfileSet{
			ActiveID: "main",
			Profiles: []llm.Profile{
				{
					ID:   "main",
					Name: "主配置",
					Config: llm.ProviderConfig{
						Provider: llm.ProviderOpenAICompatible,
						APIKey:   "valid-key",
						Model:    "example-chat-model",
					},
				},
			},
		},
	}
	logs := &captureAppLogs{}
	runtime := NewRuntime(BotConfig{OwnerID: "10001"}, nilChannel{}, NewPluginManager(), store, nil, nil, nil)
	runtime.SetAppLogWriter(logs)
	runtime.SetLLMModelLister(func(context.Context, llm.ProviderConfig) ([]llm.ModelInfo, error) {
		return []llm.ModelInfo{{ID: "gemini-2.5-pro", ContextWindowTokens: 200000, MaxOutputTokens: 8192}}, nil
	})
	output, err := newDianaLLMConfigTool(runtime, MessageEvent{Kind: EventKindGroup, UserID: "10001", GroupID: "20002"}).Run(context.Background(), map[string]any{
		"operation": "update", "provider": "gemini", "model": "gemini-2.5-pro",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(output, "已更新当前 LLM") {
		t.Fatalf("output = %q", output)
	}
	got := store.Current()
	if got.Provider != llm.ProviderGemini || got.Model != "gemini-2.5-pro" || got.ContextWindowTokens != 200000 || got.MaxContextTokens != llm.DefaultMaxContextTokens {
		t.Fatalf("current = %#v", got)
	}
	if len(logs.entries) != 1 {
		t.Fatalf("logs = %#v", logs.entries)
	}
	if logs.entries[0].Kind != applog.KindOperation || logs.entries[0].Actor != "qq:10001" {
		t.Fatalf("log entry = %#v", logs.entries[0])
	}
	if logs.entries[0].Metadata["group_id"] != "20002" || logs.entries[0].Metadata["new_model"] != "gemini-2.5-pro" {
		t.Fatalf("log metadata = %#v", logs.entries[0].Metadata)
	}
}

// TestLLMConfigPluginUpdatesModelOnly 验证对应功能场景。
func TestDianaLLMConfigToolUpdatesModelOnly(t *testing.T) {
	store := &stubLLMProfileStore{
		set: llm.ProfileSet{
			ActiveID: "main",
			Profiles: []llm.Profile{
				{
					ID:   "main",
					Name: "主配置",
					Config: llm.ProviderConfig{
						Provider: llm.ProviderOpenAICompatible,
						APIKey:   "valid-key",
						Model:    "example-chat-model",
					},
				},
			},
		},
	}
	runtime := NewRuntime(BotConfig{OwnerID: "10001"}, nilChannel{}, NewPluginManager(), store, nil, nil, nil)
	runtime.SetLLMModelLister(func(context.Context, llm.ProviderConfig) ([]llm.ModelInfo, error) {
		return []llm.ModelInfo{{ID: "example-chat-model"}, {ID: "gpt-4.1-mini"}}, nil
	})
	output, err := newDianaLLMConfigTool(runtime, MessageEvent{UserID: "10001"}).Run(context.Background(), map[string]any{"model": "gpt-4.1-mini"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(output, "gpt-4.1-mini") {
		t.Fatalf("output = %q", output)
	}
	got := store.Current()
	if got.Provider != llm.ProviderOpenAICompatible || got.Model != "gpt-4.1-mini" {
		t.Fatalf("current = %#v", got)
	}
}

// TestLLMConfigPluginRejectsModelOutsideList 验证对应功能场景。
func TestDianaLLMConfigToolRejectsModelOutsideList(t *testing.T) {
	store := &stubLLMProfileStore{
		set: llm.ProfileSet{
			ActiveID: "main",
			Profiles: []llm.Profile{
				{
					ID:   "main",
					Name: "主配置",
					Config: llm.ProviderConfig{
						Provider: llm.ProviderOpenAICompatible,
						APIKey:   "valid-key",
						Model:    "example-chat-model",
					},
				},
			},
		},
	}
	runtime := NewRuntime(BotConfig{OwnerID: "10001"}, nilChannel{}, NewPluginManager(), store, nil, nil, nil)
	runtime.SetLLMModelLister(func(context.Context, llm.ProviderConfig) ([]llm.ModelInfo, error) {
		return []llm.ModelInfo{{ID: "example-chat-model"}}, nil
	})
	_, err := newDianaLLMConfigTool(runtime, MessageEvent{UserID: "10001"}).Run(context.Background(), map[string]any{"model": "gemini-9-ultra"})
	if err == nil || !strings.Contains(err.Error(), "不在") {
		t.Fatalf("error = %v", err)
	}
	if got := store.Current(); got.Provider != llm.ProviderOpenAICompatible || got.Model != "example-chat-model" {
		t.Fatalf("current = %#v", got)
	}
}

// TestLLMConfigPluginRejectsNonOwner 验证对应功能场景。
func TestDianaLLMConfigToolRejectsNonOwner(t *testing.T) {
	store := &stubLLMProfileStore{
		set: llm.ProfileSet{
			ActiveID: "main",
			Profiles: []llm.Profile{
				{ID: "main", Config: llm.ProviderConfig{Provider: llm.ProviderOpenAICompatible, APIKey: "valid-key", Model: "example-chat-model"}},
			},
		},
	}
	logs := &captureAppLogs{}
	runtime := NewRuntime(BotConfig{OwnerID: "10001"}, nilChannel{}, NewPluginManager(), store, nil, nil, nil)
	runtime.SetAppLogWriter(logs)
	_, err := newDianaLLMConfigTool(runtime, MessageEvent{UserID: "20002"}).Run(context.Background(), map[string]any{"model": "gpt-4.1-mini"})
	if err == nil || !strings.Contains(err.Error(), "只有主人") {
		t.Fatalf("error = %v", err)
	}
	if got := store.Current(); got.Model != "example-chat-model" {
		t.Fatalf("current = %#v", got)
	}
	if len(logs.entries) != 0 {
		t.Fatalf("logs = %#v", logs.entries)
	}
}

// TestLLMConfigPluginIgnoresModelQuestion 验证对应功能场景。
func TestLLMConfigPluginIgnoresModelQuestion(t *testing.T) {
	plugin := NewLLMConfigPlugin()
	resp, err := plugin.Handle(context.Background(), PluginRequest{
		Event:   MessageEvent{UserID: "10001"},
		Text:    "怎么用 gpt-4.1-mini 写代码？",
		OwnerID: "10001",
	})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if resp != nil {
		t.Fatalf("resp = %#v, want nil", resp)
	}
}

func TestLLMConfigPluginDoesNotInterceptModelAndAPIDiscussion(t *testing.T) {
	plugin := NewLLMConfigPlugin()
	texts := []string{
		"这个 API 网关还支持哪些功能？",
		"请比较几个模型处理长文档的能力",
		"用更强的模型分析这段意图识别结果",
	}
	for _, text := range texts {
		resp, err := plugin.Handle(context.Background(), PluginRequest{Event: MessageEvent{UserID: "20002"}, Text: text, OwnerID: "10001"})
		if err != nil || resp != nil {
			t.Fatalf("text=%q resp=%#v err=%v", text, resp, err)
		}
	}
}

type testPlugin struct{}

type panicPlugin struct{}

type panicObserverPlugin struct{}

type captureAppLogs struct {
	entries []applog.Entry
}

// AppendLog 封装当前模块的 AppendLog 逻辑。
func (c *captureAppLogs) AppendLog(_ context.Context, entry applog.Entry) error {
	c.entries = append(c.entries, entry)
	return nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

// RoundTrip 封装当前模块的 RoundTrip 逻辑。
func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

// Manifest 返回插件清单信息。
func (testPlugin) Manifest() PluginManifest {
	return PluginManifest{ID: "test", Name: "Test"}
}

// Handle 处理当前插件请求。
func (testPlugin) Handle(_ context.Context, req PluginRequest) (*PluginResponse, error) {
	return &PluginResponse{Handled: true, Context: "ctx: " + req.Text}, nil
}

func (panicPlugin) Manifest() PluginManifest {
	return PluginManifest{ID: "panic", Name: "Panic"}
}

func (panicPlugin) Handle(context.Context, PluginRequest) (*PluginResponse, error) {
	panic("boom")
}

func (panicObserverPlugin) Manifest() PluginManifest {
	return PluginManifest{ID: "panic-observer", Name: "Panic Observer", BuiltIn: true}
}

func (panicObserverPlugin) Handle(context.Context, PluginRequest) (*PluginResponse, error) {
	return nil, nil
}

func (panicObserverPlugin) Observe(context.Context, MessageEvent) MessageEvent {
	panic("observe boom")
}
