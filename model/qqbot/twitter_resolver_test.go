package qqbot

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseTwitterPostResponsePreservesMixedMediaOrder(t *testing.T) {
	post, ok := parseTwitterPostResponse([]byte(`{
		"code": 200,
		"tweet": {
			"text": "正文第一行\n正文第二行",
			"author": {"name": "Diana", "screen_name": "DianaVup"},
			"media": {"all": [
				{"type": "photo", "url": "https://pbs.twimg.com/media/one.jpg?name=orig"},
				{"type": "video", "url": "https://video.twimg.com/one.mp4"},
				{"type": "gif", "url": "https://video.twimg.com/loop.mp4"},
				{"type": "video", "url": "https://video.twimg.com/two.mp4"}
			]}
		}
	}`))
	if !ok {
		t.Fatal("parseTwitterPostResponse() = false")
	}
	if post.Text != "正文第一行\n正文第二行" || post.AuthorName != "Diana" || post.AuthorHandle != "DianaVup" {
		t.Fatalf("post metadata = %#v", post)
	}
	wantTypes := []string{"photo", "video", "gif", "video"}
	if len(post.Media) != len(wantTypes) {
		t.Fatalf("media = %#v", post.Media)
	}
	for index, want := range wantTypes {
		if post.Media[index].Type != want {
			t.Fatalf("media[%d].Type = %q, want %q", index, post.Media[index].Type, want)
		}
	}
	if post.Media[2].sendAsImage() {
		t.Fatal("MP4 animated GIF must be sent as moving media, not its static thumbnail")
	}
}

func TestParseTwitterPostResponseKeepsLegacyDataURL(t *testing.T) {
	post, ok := parseTwitterPostResponse([]byte(`{"data":{"url":"https://pbs.twimg.com/media/legacy.jpg?name=orig"}}`))
	if !ok || len(post.Media) != 1 {
		t.Fatalf("post = %#v, ok = %v", post, ok)
	}
	if post.Media[0].Type != "photo" || post.Media[0].URL != "https://pbs.twimg.com/media/legacy.jpg?name=orig" {
		t.Fatalf("legacy media = %#v", post.Media[0])
	}
}

func TestTwitterMetadataAPIURLCanBeConfigured(t *testing.T) {
	raw := "https://x.com/example/status/123456?ref=share"
	t.Setenv("DIANA_TWITTER_METADATA_API", "https://resolver.example/status/{id}")
	if got := twitterMetadataAPIURL(raw); got != "https://resolver.example/status/123456" {
		t.Fatalf("configured id URL = %q", got)
	}
	t.Setenv("DIANA_TWITTER_METADATA_API", "https://resolver.example/parse?target={url}")
	got := twitterMetadataAPIURL(raw)
	if !strings.HasPrefix(got, "https://resolver.example/parse?target=") || !strings.Contains(got, "%2Fstatus%2F123456") {
		t.Fatalf("configured source URL = %q", got)
	}
	t.Setenv("DIANA_TWITTER_METADATA_API", "http://resolver.example/status/{id}")
	if got := twitterMetadataAPIURL(raw); got != "" {
		t.Fatalf("insecure metadata URL = %q", got)
	}
}

func TestResolverTwitterBuildsCaptionedMixedForwardWithMultipleVideos(t *testing.T) {
	t.Setenv("DIANA_RESOLVER_NICKNAME", "嘉然")
	plugin := NewResolverPlugin(nil)
	plugin.twitterPostFetcher = func(context.Context, string) (twitterPost, bool) {
		return twitterPost{
			Text:         "今天的完整文案",
			AuthorName:   "Diana",
			AuthorHandle: "DianaVup",
			Media: []twitterMedia{
				{Type: "photo", URL: "https://pbs.twimg.com/media/one.jpg"},
				{Type: "video", URL: "https://video.twimg.com/one.mp4"},
				{Type: "gif", URL: "https://video.twimg.com/loop.mp4"},
				{Type: "video", URL: "https://video.twimg.com/two.mp4"},
			},
		}, true
	}
	plugin.twitterMediaDownloader = func(_ context.Context, media twitterMedia) string {
		return map[string]string{
			"https://pbs.twimg.com/media/one.jpg": "/tmp/x-one.jpg",
			"https://video.twimg.com/one.mp4":     "/tmp/x-one.mp4",
			"https://video.twimg.com/loop.mp4":    "/tmp/x-loop.mp4",
			"https://video.twimg.com/two.mp4":     "/tmp/x-two.mp4",
		}[media.URL]
	}

	resp, err := plugin.Handle(context.Background(), PluginRequest{
		Text:  "https://x.com/DianaVup/status/123456",
		Event: MessageEvent{Kind: EventKindPrivate, UserID: "10001"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil || !resp.Handled || !resp.Forward {
		t.Fatalf("resp = %#v", resp)
	}
	if resp.Context != "嘉然识别：小蓝鸟学习版\n作者：Diana (@DianaVup)\n文案：今天的完整文案" {
		t.Fatalf("Context = %q", resp.Context)
	}
	if len(resp.ImageURLs) != 1 || len(resp.VideoURLs) != 3 {
		t.Fatalf("images = %#v, videos = %#v", resp.ImageURLs, resp.VideoURLs)
	}
	if len(resp.ForwardMessages) != 5 {
		t.Fatalf("ForwardMessages = %#v", resp.ForwardMessages)
	}
	if len(resp.ForwardMessages[1].ImageURLs) != 1 ||
		len(resp.ForwardMessages[2].VideoURLs) != 1 ||
		len(resp.ForwardMessages[3].VideoURLs) != 1 ||
		len(resp.ForwardMessages[4].VideoURLs) != 1 {
		t.Fatalf("mixed media order = %#v", resp.ForwardMessages)
	}
}

func TestTwitterResolverRequiresGroupLevel40ByDefault(t *testing.T) {
	t.Setenv("DIANA_TWITTER_MIN_GROUP_LEVEL", "")
	for _, test := range []struct {
		name    string
		level   string
		userID  string
		ownerID string
		want    bool
	}{
		{name: "below", level: "LV39", userID: "member", want: false},
		{name: "boundary", level: "40", userID: "member", want: true},
		{name: "owner bypass", level: "1", userID: "owner", ownerID: "owner", want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fetches := 0
			plugin := NewResolverPlugin(nil)
			plugin.twitterPostFetcher = func(context.Context, string) (twitterPost, bool) {
				fetches++
				return twitterPost{Text: "正文"}, true
			}
			resp, err := plugin.Handle(context.Background(), PluginRequest{
				Text: "https://x.com/example/status/123456",
				Event: MessageEvent{
					Kind:        EventKindGroup,
					GroupID:     "20001",
					UserID:      test.userID,
					SenderLevel: test.level,
				},
				OwnerID: test.ownerID,
			})
			if err != nil {
				t.Fatal(err)
			}
			if got := resp != nil; got != test.want {
				t.Fatalf("handled = %v, want %v; resp = %#v", got, test.want, resp)
			}
			if got := fetches > 0; got != test.want {
				t.Fatalf("metadata fetched = %v, want %v", got, test.want)
			}
		})
	}
}

type twitterLevelLookupChannel struct {
	nilChannel
	level string
	calls int
}

func (c *twitterLevelLookupChannel) CallAPI(_ context.Context, action string, _ map[string]any) (map[string]any, error) {
	if action == "get_group_member_info" {
		c.calls++
		return map[string]any{"user_id": "member", "level": c.level}, nil
	}
	return nil, nil
}

func TestTwitterResolverLooksUpMissingGroupLevel(t *testing.T) {
	t.Setenv("DIANA_TWITTER_MIN_GROUP_LEVEL", "40")
	channel := &twitterLevelLookupChannel{level: "LV69"}
	plugin := NewResolverPlugin(nil)
	plugin.twitterPostFetcher = func(context.Context, string) (twitterPost, bool) {
		return twitterPost{Text: "正文"}, true
	}
	resp, err := plugin.Handle(context.Background(), PluginRequest{
		Text:    "https://x.com/example/status/123456",
		Event:   MessageEvent{Kind: EventKindGroup, GroupID: "20001", UserID: "member"},
		Channel: channel,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil || channel.calls != 1 {
		t.Fatalf("resp = %#v, calls = %d", resp, channel.calls)
	}
}

func TestRuntimeLowLevelTwitterMentionIsSilentWithoutLLM(t *testing.T) {
	t.Setenv("DIANA_TWITTER_MIN_GROUP_LEVEL", "40")
	metadataFetches := 0
	plugin := NewResolverPlugin(nil)
	plugin.twitterPostFetcher = func(context.Context, string) (twitterPost, bool) {
		metadataFetches++
		return twitterPost{Text: "不应读取"}, true
	}
	channel := &recordingChannel{}
	llmCalls := 0
	runtime := NewRuntime(BotConfig{BotQQ: "42", OwnerID: "owner"}, channel, NewPluginManager(plugin), nil, nil, nil, func() (LLMProvider, error) {
		llmCalls++
		return &capturingLLMProvider{reply: "不应回复"}, nil
	})
	event := MessageEvent{
		Kind:        EventKindGroup,
		SelfID:      "42",
		GroupID:     "20001",
		UserID:      "member",
		SenderLevel: "39",
		ToMe:        true,
		RawMessage:  "[CQ:at,qq=42] https://x.com/example/status/123456",
		Segments: []MessageSegment{
			{Type: "at", Data: map[string]string{"qq": "42"}},
			{Type: "text", Data: map[string]string{"text": " https://x.com/example/status/123456"}},
		},
	}
	reply, err := runtime.replyTo(context.Background(), event, PlainText(event.Segments))
	if err != nil {
		t.Fatal(err)
	}
	if reply != "" || metadataFetches != 0 || llmCalls != 0 || len(channel.callsSnapshot()) != 0 {
		t.Fatalf("reply = %q, metadata = %d, llm = %d, calls = %#v", reply, metadataFetches, llmCalls, channel.callsSnapshot())
	}
}

func TestResolverMediaURLIsImageIgnoresQueryString(t *testing.T) {
	if !resolverMediaURLIsImage("https://pbs.twimg.com/media/example.jpg?name=orig") {
		t.Fatal("Twitter image URL with a query was not recognized")
	}
	if resolverMediaURLIsImage("https://video.twimg.com/example.mp4?tag=12") {
		t.Fatal("Twitter video URL was recognized as an image")
	}
}

func TestDownloadedImageExtensionRejectsNonImage(t *testing.T) {
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "image.bin")
	if err := os.WriteFile(imagePath, []byte("not an image"), 0o600); err != nil {
		t.Fatal(err)
	}
	if extension := downloadedImageExtension(imagePath); extension != "" {
		t.Fatalf("extension = %q", extension)
	}
	jpegPath := filepath.Join(dir, "photo.bin")
	jpeg := append([]byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00}, []byte(strings.Repeat("x", 32))...)
	if err := os.WriteFile(jpegPath, jpeg, 0o600); err != nil {
		t.Fatal(err)
	}
	if extension := downloadedImageExtension(jpegPath); extension != ".jpg" {
		t.Fatalf("extension = %q", extension)
	}
}

func TestTwitterResolverLiveDownloadsPhotoVideoAndGIF(t *testing.T) {
	if os.Getenv("DIANA_TWITTER_LIVE_TEST") != "1" {
		t.Skip("set DIANA_TWITTER_LIVE_TEST=1 to run live X media downloads")
	}
	t.Setenv("DIANA_TWITTER_RESOLVER_API", "")
	tests := []struct {
		name      string
		url       string
		mediaType string
	}{
		{name: "photo", url: "https://x.com/SpaceX/status/1848831595014459513", mediaType: "photo"},
		{name: "video", url: "https://x.com/DivineDropbear/status/1841206275088290279", mediaType: "video"},
		{name: "gif", url: "https://x.com/RoRoFli/status/2031318660011188567", mediaType: "gif"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			post, ok := fetchTwitterPost(ctx, test.url)
			if !ok || strings.TrimSpace(post.Text) == "" || len(post.Media) == 0 {
				t.Fatalf("post = %#v, ok = %v", post, ok)
			}
			media := post.Media[0]
			if media.Type != test.mediaType {
				t.Fatalf("media type = %q, want %q", media.Type, test.mediaType)
			}
			path := downloadTwitterMediaFile(ctx, media)
			if path == "" {
				t.Fatal("downloadTwitterMediaFile() returned an empty path")
			}
			defer os.RemoveAll(filepath.Dir(path))
			info, err := os.Stat(path)
			if err != nil || info.IsDir() || info.Size() == 0 {
				t.Fatalf("downloaded path = %q, info = %#v, err = %v", path, info, err)
			}
			t.Logf("downloaded %s: %s (%d bytes)", test.mediaType, path, info.Size())
		})
	}
}
