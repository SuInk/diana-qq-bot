package qqbot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/gorilla/websocket"
)

// TestCQToSegmentsAndPlainText 验证对应功能场景。
func TestCQToSegmentsAndPlainText(t *testing.T) {
	got := CQToSegments("hi [CQ:at,qq=123] 看图 [CQ:image,file=a.jpg]")
	if len(got) != 4 {
		t.Fatalf("len = %d, want 4: %#v", len(got), got)
	}
	if got[1].Type != "at" || got[1].Data["qq"] != "123" {
		t.Fatalf("at segment = %#v", got[1])
	}
	if text := PlainText(got); text != "hi @123  看图 [图片]" {
		t.Fatalf("PlainText = %q", text)
	}
}

func TestPlainTextIncludesForwardSummary(t *testing.T) {
	got := PlainText(CQToSegments("[CQ:forward,id=abc123]"))
	if got != "[合并转发:abc123]" {
		t.Fatalf("PlainText = %q", got)
	}
}

// TestImageURLsExtractsRemoteAndBase64Images 验证图片段能提取远端或 data URL。
func TestImageURLsExtractsRemoteAndBase64Images(t *testing.T) {
	got := ImageURLs([]MessageSegment{
		{Type: "image", Data: map[string]string{"url": "https://example.com/a.jpg"}},
		{Type: "image", Data: map[string]string{"file": "base64://abcd"}},
		{Type: "image", Data: map[string]string{"file": "local-cache.jpg"}},
	})
	want := []string{"https://example.com/a.jpg", "data:image/jpeg;base64,abcd"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestVideoURLsExtractsRemoteAndLocalVideos 验证视频段能提取远端 URL、本地路径并去重。
func TestVideoURLsExtractsRemoteAndLocalVideos(t *testing.T) {
	local := filepath.Join(t.TempDir(), "video.mp4")
	if err := os.WriteFile(local, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	local, err := filepath.EvalSymlinks(local)
	if err != nil {
		t.Fatal(err)
	}
	got := VideoURLs([]MessageSegment{
		{Type: "video", Data: map[string]string{"file": "local-cache.mp4"}},
		{Type: "video", Data: map[string]string{"file": local}},
		{Type: "video", Data: map[string]string{"url": "https://example.com/a.mp4"}},
		{Type: "video", Data: map[string]string{"video_url": "https://example.com/a.mp4"}},
		{Type: "image", Data: map[string]string{"url": "https://example.com/ignore.jpg"}},
	})
	if len(got) != 2 || got[0] != local || got[1] != "https://example.com/a.mp4" {
		t.Fatalf("VideoURLs() = %#v", got)
	}
}

// TestTextToOneBotSegmentsKeepsCQAt 验证对应功能场景。
func TestTextToOneBotSegmentsKeepsCQAt(t *testing.T) {
	got := TextToOneBotSegments("[CQ:at,qq=123] hello")
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2: %#v", len(got), got)
	}
	if got[0].Type != "at" || got[0].Data["qq"] != "123" {
		t.Fatalf("first segment = %#v", got[0])
	}
}

func TestTextToOneBotSegmentsConvertsPlainQQMention(t *testing.T) {
	got := TextToOneBotSegments("看下 @10005 的好感度")
	if len(got) != 3 {
		t.Fatalf("segments = %#v", got)
	}
	if got[1].Type != "at" || got[1].Data["qq"] != "10005" {
		t.Fatalf("mention = %#v", got[1])
	}
	if got[2].Type != "text" || got[2].Data["text"] != " 的好感度" {
		t.Fatalf("text after mention = %#v", got[2])
	}
}

func TestTextToOneBotSegmentsAddsSpaceAfterMention(t *testing.T) {
	got := TextToOneBotSegments("[CQ:at,qq=10005]当前好感度是 5")
	if len(got) != 3 || got[0].Type != "at" || got[1].Type != "text" || got[1].Data["text"] != " " {
		t.Fatalf("segments = %#v", got)
	}
	plain := TextToOneBotSegments("联系 a@123456.com 或 https://example.com/@123456")
	for _, segment := range plain {
		if segment.Type == "at" {
			t.Fatalf("email or URL became a mention: %#v", plain)
		}
	}
}

// TestOneBotChannelSendPrefixesReplyAndMention 验证对应功能场景。
func TestOneBotChannelSendPrefixesReplyAndMention(t *testing.T) {
	message := buildOutgoingSegments(OutgoingMessage{
		GroupID:        "123",
		Text:           "你好",
		ImageURLs:      []string{"data:image/png;base64,abcd"},
		ReplyMessageID: "456",
		MentionUserID:  "789",
	})
	if len(message) < 4 {
		t.Fatalf("message = %#v", message)
	}
	if message[0]["type"] != "reply" || message[1]["type"] != "at" {
		t.Fatalf("message = %#v", message)
	}
	space, ok := message[2]["data"].(map[string]string)
	if message[2]["type"] != "text" || !ok || space["text"] != " " {
		t.Fatalf("mention spacer = %#v", message[2])
	}
	image, ok := message[len(message)-1]["data"].(map[string]string)
	if message[len(message)-1]["type"] != "image" || !ok || image["file"] != "base64://abcd" {
		t.Fatalf("image segment = %#v", message[len(message)-1])
	}
}

func TestForwardOutgoingSegmentsRemoveMentions(t *testing.T) {
	message := buildForwardOutgoingSegments(OutgoingMessage{
		Text:          "[CQ:at,qq=123] 第一位，@456 第二位",
		MentionUserID: "789",
		ImageURLs:     []string{"data:image/png;base64,abcd"},
	})
	if len(message) == 0 {
		t.Fatal("forward message is empty")
	}
	for _, segment := range message {
		if segment["type"] == "at" {
			t.Fatalf("forward message contains mention: %#v", message)
		}
	}
	if message[len(message)-1]["type"] != "image" {
		t.Fatalf("non-mention segments were lost: %#v", message)
	}
}

func TestBuildForwardNodesRemoveMentions(t *testing.T) {
	nodes := buildForwardNodes([]string{"[CQ:at,qq=123] 节点内容"}, "Diana", "42")
	if len(nodes) != 1 {
		t.Fatalf("nodes = %#v", nodes)
	}
	data, ok := nodes[0]["data"].(map[string]any)
	if !ok {
		t.Fatalf("node data = %#v", nodes[0]["data"])
	}
	content, ok := data["content"].([]map[string]any)
	if !ok || len(content) == 0 {
		t.Fatalf("node content = %#v", data["content"])
	}
	for _, segment := range content {
		if segment["type"] == "at" {
			t.Fatalf("forward node contains mention: %#v", content)
		}
	}
}

// TestMessageEventFromEnvelopeNoticeGroupIncrease 验证对应功能场景。
func TestMessageEventFromEnvelopeNoticeGroupIncrease(t *testing.T) {
	event := messageEventFromEnvelope(oneBotEnvelope{
		Time:     123,
		SelfID:   "42",
		PostType: "notice",
		SubType:  "group_increase",
		UserID:   "10001",
		GroupID:  "20002",
	})
	if event.Kind != EventKindNotice || event.SubType != "group_increase" {
		t.Fatalf("event = %#v", event)
	}
	if event.UserID != "10001" || event.GroupID != "20002" {
		t.Fatalf("event = %#v", event)
	}
}

// TestMessageEventFromEnvelopeNoticeTypeGroupRecall 验证 NapCat/OneBot 撤回 notice_type 能映射到内部 SubType。
func TestMessageEventFromEnvelopeNoticeTypeGroupRecall(t *testing.T) {
	event := messageEventFromEnvelope(oneBotEnvelope{
		Time:       123,
		SelfID:     "42",
		PostType:   "notice",
		NoticeType: "group_recall",
		UserID:     "10001",
		GroupID:    "20002",
		MessageID:  "old-1",
		OperatorID: "30003",
	})
	if event.Kind != EventKindNotice || event.SubType != "group_recall" || event.MessageID != "old-1" || event.OperatorID != "30003" {
		t.Fatalf("event = %#v", event)
	}
	if len(event.Segments) != 1 || event.Segments[0].Data["notice_type"] != "group_recall" || event.Segments[0].Data["operator_id"] != "30003" {
		t.Fatalf("segments = %#v", event.Segments)
	}
}

// TestOneBotEnvelopeAllowsObjectStatus 验证对应功能场景。
func TestOneBotEnvelopeAllowsObjectStatus(t *testing.T) {
	var envelope oneBotEnvelope
	err := json.Unmarshal([]byte(`{"status":{"online":true,"good":true},"retcode":0,"echo":"debug"}`), &envelope)
	if err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if !envelopeStatusOK(envelope) {
		t.Fatalf("status should be ok: %#v", envelope.Status)
	}
	if text := envelopeStatusText(envelope.Status); text == "" {
		t.Fatal("status text should not be empty")
	}
}

func TestOneBotDataMapWrapsArrayData(t *testing.T) {
	var envelope oneBotEnvelope
	err := json.Unmarshal([]byte(`{"status":"ok","retcode":0,"echo":"members","data":[{"user_id":10001}]}`), &envelope)
	if err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	data := oneBotDataMap(envelope.Data)
	items, ok := data["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("items = %#v", data["items"])
	}
}

func TestReverseServerStaleReadLoopCannotDisconnectReplacement(t *testing.T) {
	server := NewOneBotReverseServer(OneBotConfig{Endpoint: "/onebot/v11/ws"})
	oldConn := &websocket.Conn{}
	newConn := &websocket.Conn{}
	server.conn = newConn
	server.status = ChannelStatus{Connected: true, SelfID: "42"}

	server.disconnectIfCurrent(oldConn, "old connection closed")
	if server.conn != newConn || !server.Status().Connected {
		t.Fatal("stale read loop disconnected the replacement websocket")
	}

	server.disconnectIfCurrent(newConn, "current connection closed")
	status := server.Status()
	if server.conn != nil || status.Connected || status.LastError != "current connection closed" {
		t.Fatalf("current disconnect status = %#v", status)
	}
}
