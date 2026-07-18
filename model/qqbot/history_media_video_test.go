package qqbot

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"diana-qq-bot/model/llm"
)

func TestCacheMessageEventVideosPersistsFrames(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}
	t.Setenv("DIANA_HISTORY_MEDIA_DIR", t.TempDir())
	videoPath := filepath.Join(t.TempDir(), "incoming.mp4")
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=10:duration=3", "-pix_fmt", "yuv420p", videoPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create sample video: %v: %s", err, output)
	}

	event := cacheMessageEventVideos(context.Background(), MessageEvent{
		Kind:      EventKindGroup,
		GroupID:   "group-1",
		UserID:    "user-2",
		MessageID: "video-1",
		Segments:  []MessageSegment{{Type: "video", Data: map[string]string{"url": videoPath}}},
	})
	frames := cachedVideoFrameURLs(event.Segments)
	if len(frames) != 4 {
		t.Fatalf("cached frame count = %d, want 4: %#v", len(frames), event.Segments)
	}
	for _, frame := range frames {
		if info, err := os.Stat(frame); err != nil || info.Size() == 0 {
			t.Fatalf("cached frame invalid: path=%q info=%v err=%v", frame, info, err)
		}
	}
	again := cacheMessageEventVideos(context.Background(), event)
	if len(cachedVideoFrameURLs(again.Segments)) != len(frames) {
		t.Fatalf("cache duplicated frames: %#v", again.Segments)
	}
}

func TestCacheMessageEventVideosWaitsForNapCatPath(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}
	t.Setenv("DIANA_HISTORY_MEDIA_DIR", t.TempDir())
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.mp4")
	pendingPath := filepath.Join(dir, "pending.mp4")
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=10:duration=1", "-pix_fmt", "yuv420p", sourcePath)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create sample video: %v: %s", err, output)
	}
	moveDone := make(chan error, 1)
	go func() {
		time.Sleep(150 * time.Millisecond)
		moveDone <- os.Rename(sourcePath, pendingPath)
	}()

	event := cacheMessageEventVideos(context.Background(), MessageEvent{
		Kind:      EventKindGroup,
		GroupID:   "group-1",
		UserID:    "user-2",
		MessageID: "video-delayed",
		Segments:  []MessageSegment{{Type: "video", Data: map[string]string{"file": pendingPath}}},
	})
	if err := <-moveDone; err != nil {
		t.Fatal(err)
	}
	if frames := cachedVideoFrameURLs(event.Segments); len(frames) == 0 {
		t.Fatalf("delayed NapCat video was not cached: %#v", event.Segments)
	}
}

func TestCacheMessageEventVideosIgnoresNapCatThumbnailAndWaitsForMP4(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}
	t.Setenv("DIANA_HISTORY_MEDIA_DIR", t.TempDir())
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.mp4")
	pendingPath := filepath.Join(dir, "Ori", "incoming.mp4")
	thumbnailPath := filepath.Join(dir, "Thumb", "incoming_0.png")
	if err := os.MkdirAll(filepath.Dir(pendingPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(thumbnailPath), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=10:duration=1", "-pix_fmt", "yuv420p", sourcePath)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create sample video: %v: %s", err, output)
	}
	if err := os.WriteFile(thumbnailPath, tinyJPEGBytes(t), 0o600); err != nil {
		t.Fatal(err)
	}
	moveDone := make(chan error, 1)
	go func() {
		time.Sleep(150 * time.Millisecond)
		moveDone <- os.Rename(sourcePath, pendingPath)
	}()

	event := cacheMessageEventVideos(context.Background(), MessageEvent{
		Kind:      EventKindPrivate,
		UserID:    "user-1",
		MessageID: "napcat-thumbnail-first",
		Segments: []MessageSegment{{Type: "video", Data: map[string]string{
			"file": "incoming.mp4",
			"url":  pendingPath,
			"path": thumbnailPath,
		}}},
	})
	if err := <-moveDone; err != nil {
		t.Fatal(err)
	}
	if frames := cachedVideoFrameURLs(event.Segments); len(frames) != 4 {
		t.Fatalf("cached frame count = %d, want 4: %#v", len(frames), event.Segments)
	}
}

func TestRuntimeResolvesImageWithoutURLAndCachesIt(t *testing.T) {
	t.Setenv("DIANA_HISTORY_MEDIA_DIR", t.TempDir())
	body := tinyJPEGBytes(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(body)
	}))
	defer server.Close()
	channel := &recordingChannel{apiResponses: map[string]map[string]any{
		"get_image": {"url": server.URL + "/image.jpg"},
	}}
	runtime := NewRuntime(BotConfig{}, channel, NewPluginManager(), nil, nil, nil, nil)
	event := runtime.enrichMediaReferences(context.Background(), MessageEvent{
		Kind:      EventKindGroup,
		GroupID:   "group-1",
		MessageID: "image-no-url",
		Segments: []MessageSegment{{Type: "image", Data: map[string]string{
			"file": "CEC014F0C4280214A9F672B17116581B.png",
		}}},
	})
	event = cacheMessageEventImages(context.Background(), event)
	if event.Segments[0].Data["url"] == "" || event.Segments[0].Data["cached_file"] == "" {
		t.Fatalf("image source was not resolved and cached: %#v", event.Segments)
	}
}

func TestRuntimeCachesMP4FileWithoutReplying(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}
	t.Setenv("DIANA_HISTORY_MEDIA_DIR", t.TempDir())
	videoPath := filepath.Join(t.TempDir(), "IMG_1939.mp4")
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=10:duration=1", "-pix_fmt", "yuv420p", videoPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create sample video: %v: %s", err, output)
	}
	channel := &recordingChannel{apiResponses: map[string]map[string]any{
		"get_group_file_url": {"path": videoPath},
	}}
	runtime := NewRuntime(BotConfig{}, channel, NewPluginManager(), nil, nil, nil, nil)
	event, _, handled, outcome := runtime.prepareMessageEvent(context.Background(), MessageEvent{
		Kind:       EventKindGroup,
		GroupID:    "group-1",
		UserID:     "user-1",
		MessageID:  "file-video",
		RawMessage: "[文件:IMG_1939.mp4]",
		Segments: []MessageSegment{{Type: "file", Data: map[string]string{
			"file": "IMG_1939.mp4", "file_id": "/video-id",
		}}},
	})
	if handled || outcome != "ignored_video" {
		t.Fatalf("video file triggered chat: handled=%v outcome=%q", handled, outcome)
	}
	if frames := cachedVideoFrameURLs(event.Segments); len(frames) == 0 {
		t.Fatalf("video file frames were not cached: %#v", event.Segments)
	}
}

func TestRuntimeReplacesUnavailableNapCatVideoPath(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}
	t.Setenv("DIANA_HISTORY_MEDIA_DIR", t.TempDir())
	videoPath := filepath.Join(t.TempDir(), "downloaded.mp4")
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=10:duration=1", "-pix_fmt", "yuv420p", videoPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create sample video: %v: %s", err, output)
	}
	missingPath := filepath.Join(t.TempDir(), "QQ", "Video", "Ori", "video.mp4")
	channel := &recordingChannel{apiResponses: map[string]map[string]any{
		"get_file": {"path": videoPath},
	}}
	runtime := NewRuntime(BotConfig{}, channel, NewPluginManager(), nil, nil, nil, nil)
	event := runtime.enrichMediaReferences(context.Background(), MessageEvent{
		Kind:      EventKindGroup,
		GroupID:   "group-1",
		MessageID: "napcat-video",
		Segments: []MessageSegment{{Type: "video", Data: map[string]string{
			"file": "video.mp4", "url": missingPath,
		}}},
	})
	if got := event.Segments[0].Data["path"]; got != videoPath {
		t.Fatalf("resolved path = %q, want %q: %#v", got, videoPath, event.Segments)
	}
	if len(channel.calls) != 1 || channel.calls[0].action != "get_file" {
		t.Fatalf("OneBot fallback calls = %#v", channel.calls)
	}
	event = cacheMessageEventVideos(context.Background(), event)
	if frames := cachedVideoFrameURLs(event.Segments); len(frames) != 4 {
		t.Fatalf("cached frame count = %d, want 4: %#v", len(frames), event.Segments)
	}
}

func TestRuntimeDownloadsVideoWithNapCatFileToken(t *testing.T) {
	videoPath := filepath.Join(t.TempDir(), "downloaded.mp4")
	if err := os.WriteFile(videoPath, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	const token = "napcat-video-token"
	channel := &recordingChannel{apiResponses: map[string]map[string]any{
		"get_file": {"file": videoPath},
	}}
	runtime := NewRuntime(BotConfig{}, channel, NewPluginManager(), nil, nil, nil, nil)
	event := runtime.enrichMediaReferences(context.Background(), MessageEvent{
		Kind:      EventKindPrivate,
		UserID:    "user-1",
		MessageID: "video-token",
		Segments: []MessageSegment{{Type: "video", Data: map[string]string{
			"file": token,
		}}},
	})
	if got := event.Segments[0].Data["path"]; got != videoPath {
		t.Fatalf("resolved path = %q, want %q: %#v", got, videoPath, event.Segments)
	}
	if len(channel.calls) != 1 || channel.calls[0].action != "get_file" || channel.calls[0].params["file"] != token {
		t.Fatalf("get_file calls = %#v", channel.calls)
	}
}

func TestRuntimeRefreshesNapCatVideoTokenThroughGetMsg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}
	t.Setenv("DIANA_HISTORY_MEDIA_DIR", t.TempDir())
	videoPath := filepath.Join(t.TempDir(), "refreshed.mp4")
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=10:duration=1", "-pix_fmt", "yuv420p", videoPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create sample video: %v: %s", err, output)
	}
	channel := &stagedNapCatVideoChannel{
		fileName:  "incoming.mp4",
		videoPath: videoPath,
	}
	runtime := NewRuntime(BotConfig{}, channel, NewPluginManager(), nil, nil, nil, nil)
	event := runtime.enrichMediaReferences(context.Background(), MessageEvent{
		Kind:      EventKindPrivate,
		UserID:    "user-1",
		MessageID: "30007",
		Segments: []MessageSegment{{Type: "video", Data: map[string]string{
			"file": "incoming.mp4",
			"url":  "/missing/QQ/Video/Ori/incoming.mp4",
		}}},
	})
	if got := event.Segments[0].Data["path"]; got != videoPath {
		t.Fatalf("resolved path = %q, want %q: %#v", got, videoPath, event.Segments)
	}
	if len(channel.calls) < 3 || channel.calls[len(channel.calls)-2].action != "get_msg" || channel.calls[len(channel.calls)-1].action != "get_file" {
		t.Fatalf("expected get_msg followed by get_file, calls = %#v", channel.calls)
	}
	event = cacheMessageEventVideos(context.Background(), event)
	if frames := cachedVideoFrameURLs(event.Segments); len(frames) != 4 {
		t.Fatalf("cached frame count = %d, want 4: %#v", len(frames), event.Segments)
	}
}

type stagedNapCatVideoChannel struct {
	recordingChannel
	fileName  string
	videoPath string
	refreshed bool
}

func (c *stagedNapCatVideoChannel) CallAPI(_ context.Context, action string, params map[string]any) (map[string]any, error) {
	c.calls = append(c.calls, recordingAPICall{action: action, params: params})
	switch action {
	case "get_msg":
		c.refreshed = true
		return map[string]any{
			"message": []any{map[string]any{
				"type": "video",
				"data": map[string]any{"file": c.fileName},
			}},
		}, nil
	case "get_file":
		if c.refreshed && params["file"] == c.fileName {
			return map[string]any{"file": c.videoPath}, nil
		}
		return nil, errors.New("file not found")
	default:
		return map[string]any{}, nil
	}
}

func TestLLMMessageUsesPersistedVideoFramesAfterSourceDisappears(t *testing.T) {
	framePath := filepath.Join(t.TempDir(), "frame.jpg")
	if err := os.WriteFile(framePath, tinyJPEGBytes(t), 0o600); err != nil {
		t.Fatal(err)
	}
	msg := llmMessageFromEventWithVideoFrames(context.Background(), MessageEvent{
		RawMessage: "这是什么",
		Quoted: &QuotedMessage{
			Semantic: true,
			Segments: []MessageSegment{
				{Type: "video", Data: map[string]string{"file": "expired-video.mp4"}},
				{Type: "image", Data: map[string]string{"cached_file": framePath, "source_type": "video_frame"}},
			},
		},
	}, "这是什么", nil)
	if len(msg.Parts) < 2 || msg.Parts[1].Type != "image_url" {
		t.Fatalf("persisted frame missing from LLM message: %#v", msg)
	}
}

func TestRuntimeCachesIncomingVideoThenRoutesFollowupToItsFrames(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}
	t.Setenv("DIANA_HISTORY_MEDIA_DIR", t.TempDir())
	videoPath := filepath.Join(t.TempDir(), "incoming.mp4")
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=10:duration=3", "-pix_fmt", "yuv420p", videoPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create sample video: %v: %s", err, output)
	}

	channel := &recordingChannel{}
	provider := &sequenceLLMProvider{replies: []string{
		`{"message_id":"video-1","confidence":0.98,"reason":"当前问题指向群友刚才发送的视频"}`,
		`{"action":"none","prompt":""}`,
		"视频里是测试画面。",
	}}
	runtime := NewRuntime(BotConfig{BotQQ: "42", RecentContextLimit: 20}, channel, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	prepared, _, handled, _ := runtime.prepareMessageEvent(context.Background(), MessageEvent{
		Kind:       EventKindGroup,
		GroupID:    "group-1",
		UserID:     "other-user",
		SenderName: "群友",
		MessageID:  "video-1",
		RawMessage: "[视频]",
		Segments:   []MessageSegment{{Type: "video", Data: map[string]string{"file": videoPath}}},
	})
	if handled {
		t.Fatal("video-only message should be cached without an immediate reply")
	}
	if frames := cachedVideoFrameURLs(prepared.Segments); len(frames) == 0 {
		t.Fatalf("incoming video was not cached: %#v", prepared.Segments)
	}
	if err := os.Remove(videoPath); err != nil {
		t.Fatal(err)
	}

	reply, err := runtime.replyTo(context.Background(), MessageEvent{
		Kind:       EventKindGroup,
		GroupID:    "group-1",
		UserID:     "current-user",
		SenderName: "提问者",
		MessageID:  "question-1",
		ToMe:       true,
		RawMessage: "这是什么",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "这是什么"}}},
	}, "这是什么")
	if err != nil {
		t.Fatal(err)
	}
	if reply != "视频里是测试画面。" || len(channel.sent) != 1 {
		t.Fatalf("reply=%q sent=%#v", reply, channel.sent)
	}
	if len(provider.requests) != 3 || requestImageCount(provider.requests[2]) != 4 {
		t.Fatalf("selected video frames missing from final request: %#v", provider.requests)
	}
}

func requestHasAnyImage(req llm.GenerateRequest) bool {
	return requestImageCount(req) > 0
}

func requestImageCount(req llm.GenerateRequest) int {
	count := 0
	for _, message := range req.Messages {
		for _, part := range message.Parts {
			if part.Type == llm.ContentPartImageURL && part.ImageURL != "" {
				count++
			}
		}
	}
	return count
}

func tinyJPEGBytes(t *testing.T) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tiny.jpg")
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "color=c=green:size=16x16:duration=0.1", "-frames:v", "1", path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create JPEG: %v: %s", err, output)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
