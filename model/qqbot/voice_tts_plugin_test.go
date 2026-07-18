package qqbot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVoiceTTSPluginIsAgentOnlyAndEnabledByDefault(t *testing.T) {
	t.Setenv("DIANA_TTS_VOICE_NAME", "测试")
	manager := NewDefaultPluginManager()
	state, ok := manager.Get(voiceTTSPluginID)
	if !ok || !state.Installed || !state.Enabled {
		t.Fatalf("state=%#v ok=%v", state, ok)
	}
	plugin := NewVoiceTTSPlugin(nil)
	resp, err := plugin.Handle(context.Background(), PluginRequest{Text: "请用语音回复"})
	if err != nil || resp != nil {
		t.Fatalf("Handle() resp=%#v err=%v", resp, err)
	}
	if got := plugin.AgentTools()[0].Name(); got != voiceTTSToolName {
		t.Fatalf("tool name=%q", got)
	}
	if description := plugin.AgentTools()[0].Description(); !strings.Contains(description, "测试音色") {
		t.Fatalf("tool description=%q", description)
	}
}

func TestDianaTTSToolSynthesizesAndReturnsTerminalRecord(t *testing.T) {
	var requestBody map[string]any
	var handlerErr error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			handlerErr = fmt.Errorf("method=%s", r.Method)
			http.Error(w, handlerErr.Error(), http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			handlerErr = err
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := json.Unmarshal(body, &requestBody); err != nil {
			handlerErr = err
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(testWAVBytes())
	}))
	defer server.Close()

	outputDir := t.TempDir()
	t.Setenv("DIANA_TTS_ENDPOINT", server.URL)
	t.Setenv("DIANA_TTS_REF_AUDIO_PATH", "/models/reference.wav")
	t.Setenv("DIANA_TTS_OUTPUT_DIR", outputDir)
	t.Setenv("DIANA_TTS_SILK_ENCODER_PATH", "")
	plugin := NewVoiceTTSPlugin(server.Client())
	sharer := &recordingLocalMediaSharer{url: "http://127.0.0.1:18080/api/qqbot/media/voice-token"}
	plugin.SetLocalMediaSharer(sharer)
	tool := plugin.AgentTools()[0]

	raw, err := tool.Run(context.Background(), map[string]any{"text": "大家晚上好，我是语音助手。"})
	if err != nil {
		t.Fatal(err)
	}
	if handlerErr != nil {
		t.Fatal(handlerErr)
	}
	if requestBody["text"] != "大家晚上好，我是语音助手。" || requestBody["text_lang"] != "zh" || requestBody["media_type"] != "wav" {
		t.Fatalf("request=%#v", requestBody)
	}
	if len(sharer.paths) != 1 || filepath.Dir(sharer.paths[0]) != outputDir {
		t.Fatalf("shared paths=%#v", sharer.paths)
	}
	if _, err := os.Stat(sharer.paths[0]); err != nil {
		t.Fatalf("audio cache missing: %v", err)
	}
	terminal, ok := tool.(interface {
		TerminalResult(string) (string, bool)
	})
	if !ok {
		t.Fatal("tts tool is not terminal")
	}
	reply, done := terminal.TerminalResult(raw)
	if !done || reply != "[CQ:record,file=http://127.0.0.1:18080/api/qqbot/media/voice-token]" {
		t.Fatalf("terminal reply=%q done=%v raw=%s", reply, done, raw)
	}
	segments := buildOutgoingSegments(OutgoingMessage{Text: reply})
	if len(segments) != 1 || segments[0]["type"] != "record" {
		t.Fatalf("segments=%#v", segments)
	}
}

func TestDianaTTSToolPreEncodesTencentSilkForNapCat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(testWAVBytes())
	}))
	defer server.Close()

	outputDir := t.TempDir()
	t.Setenv("DIANA_TTS_ENDPOINT", server.URL)
	t.Setenv("DIANA_TTS_OUTPUT_DIR", outputDir)
	t.Setenv("DIANA_TTS_FFMPEG_PATH", "/test/ffmpeg")
	t.Setenv("DIANA_TTS_SILK_ENCODER_PATH", "/test/silk-encoder")
	t.Setenv("DIANA_TTS_SILK_BITRATE", "24000")

	plugin := NewVoiceTTSPlugin(server.Client())
	var commands []string
	plugin.commandRunner = func(_ context.Context, name string, args ...string) ([]byte, error) {
		commands = append(commands, name+" "+strings.Join(args, " "))
		switch name {
		case "/test/ffmpeg":
			return nil, os.WriteFile(args[len(args)-1], []byte{0, 1, 2, 3}, 0o600)
		case "/test/silk-encoder":
			for i := 0; i+1 < len(args); i++ {
				if args[i] == "-o" {
					return nil, os.WriteFile(args[i+1], append([]byte{0x02}, []byte("#!SILK_V3\x00voice")...), 0o600)
				}
			}
			return nil, fmt.Errorf("missing -o")
		default:
			return nil, fmt.Errorf("unexpected command %q", name)
		}
	}
	sharer := &recordingLocalMediaSharer{url: "http://127.0.0.1:18080/api/qqbot/media/silk-token"}
	plugin.SetLocalMediaSharer(sharer)

	if _, err := plugin.AgentTools()[0].Run(context.Background(), map[string]any{"text": "用语音说晚安"}); err != nil {
		t.Fatal(err)
	}
	if len(commands) != 2 || !strings.Contains(commands[0], "-ar 24000 -ac 1 -f s16le") || !strings.Contains(commands[1], "-rate 24000") {
		t.Fatalf("commands=%#v", commands)
	}
	if len(sharer.paths) != 1 || filepath.Ext(sharer.paths[0]) != ".silk" {
		t.Fatalf("shared paths=%#v", sharer.paths)
	}
	header, err := readFilePrefix(sharer.paths[0], 16)
	if err != nil || !looksLikeTencentSilk(header) {
		t.Fatalf("silk header=%q err=%v", header, err)
	}
	entries, err := os.ReadDir(outputDir)
	if err != nil || len(entries) != 1 || filepath.Ext(entries[0].Name()) != ".silk" {
		t.Fatalf("cache entries=%#v err=%v", entries, err)
	}
}

func TestDianaTTSToolRejectsNonAudioResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"detail":"model unavailable"}`))
	}))
	defer server.Close()

	t.Setenv("DIANA_TTS_ENDPOINT", server.URL)
	t.Setenv("DIANA_TTS_OUTPUT_DIR", t.TempDir())
	t.Setenv("DIANA_TTS_SILK_ENCODER_PATH", "")
	plugin := NewVoiceTTSPlugin(server.Client())
	plugin.SetLocalMediaSharer(&recordingLocalMediaSharer{url: "http://127.0.0.1/audio"})
	_, err := plugin.AgentTools()[0].Run(context.Background(), map[string]any{"text": "测试"})
	if err == nil || !strings.Contains(err.Error(), "有效 WAV") {
		t.Fatalf("err=%v", err)
	}
}

func TestRuntimeAgentUsesTTSForModelSelectedVoiceRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(testWAVBytes())
	}))
	defer server.Close()
	t.Setenv("DIANA_TTS_ENDPOINT", server.URL)
	t.Setenv("DIANA_TTS_OUTPUT_DIR", t.TempDir())
	t.Setenv("DIANA_TTS_SILK_ENCODER_PATH", "")

	provider := &sequenceLLMProvider{replies: []string{
		`{"action":"none","prompt":""}`,
		`{"action":"tool","tool":"diana.tts","input":{"text":"晚上好呀，今天也要开心。"}}`,
	}}
	channel := &recordingChannel{}
	plugins := NewDefaultPluginManager()
	runtime := NewRuntime(BotConfig{OwnerID: "owner", AgentEnabled: true, AgentWorkDir: t.TempDir(), AgentMaxSteps: 3}, channel, plugins, nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	runtime.SetLocalMediaSharer(&recordingLocalMediaSharer{url: "http://127.0.0.1:18080/api/qqbot/media/voice-token"})
	event := MessageEvent{
		Kind:      EventKindPrivate,
		UserID:    "user",
		MessageID: "voice-request",
		Segments:  []MessageSegment{{Type: "text", Data: map[string]string{"text": "请用语音跟我说晚安"}}},
	}
	reply, err := runtime.replyTo(context.Background(), event, "请用语音跟我说晚安")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(reply, "[CQ:record,file=") || len(channel.sent) != 1 {
		t.Fatalf("reply=%q sent=%#v", reply, channel.sent)
	}
	segments := buildOutgoingSegments(channel.sent[0])
	if len(segments) != 1 || segments[0]["type"] != "record" {
		t.Fatalf("segments=%#v", segments)
	}
	if len(provider.requests) != 2 || !requestMessagesContain(provider.requests[1].Messages, "只有用户明确要求用语音回复") {
		t.Fatalf("requests=%d", len(provider.requests))
	}
}

func TestRuntimeGroupTTSVoiceIsAStandaloneRecord(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(testWAVBytes())
	}))
	defer server.Close()
	t.Setenv("DIANA_TTS_ENDPOINT", server.URL)
	t.Setenv("DIANA_TTS_OUTPUT_DIR", t.TempDir())
	t.Setenv("DIANA_TTS_SILK_ENCODER_PATH", "")

	provider := &sequenceLLMProvider{replies: []string{
		`{"action":"none","prompt":""}`,
		`{"action":"tool","tool":"diana.tts","input":{"text":"晚安，做个好梦。"}}`,
	}}
	channel := &recordingChannel{}
	runtime := NewRuntime(BotConfig{OwnerID: "owner", AgentEnabled: true, AgentWorkDir: t.TempDir(), AgentMaxSteps: 3}, channel, NewDefaultPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	runtime.SetLocalMediaSharer(&recordingLocalMediaSharer{url: "http://127.0.0.1:18080/api/qqbot/media/group-voice-token"})
	event := MessageEvent{
		Kind:      EventKindGroup,
		GroupID:   "123456",
		UserID:    "10001",
		SelfID:    "20002",
		MessageID: "group-voice-request",
		Segments:  []MessageSegment{{Type: "text", Data: map[string]string{"text": "嘉然，用语音跟我说晚安"}}},
	}
	if _, err := runtime.replyTo(context.Background(), event, "嘉然，用语音跟我说晚安"); err != nil {
		t.Fatal(err)
	}
	if len(channel.sent) != 1 {
		t.Fatalf("sent=%#v", channel.sent)
	}
	message := channel.sent[0]
	if message.GroupID != event.GroupID || message.ReplyMessageID != "" || message.MentionUserID != "" {
		t.Fatalf("message=%#v", message)
	}
	segments := buildOutgoingSegments(message)
	if len(segments) != 1 || segments[0]["type"] != "record" {
		t.Fatalf("segments=%#v", segments)
	}
}

func testWAVBytes() []byte {
	return []byte{
		'R', 'I', 'F', 'F', 36, 0, 0, 0,
		'W', 'A', 'V', 'E', 'f', 'm', 't', ' ',
		16, 0, 0, 0, 1, 0, 1, 0,
		0x80, 0x3e, 0, 0, 0, 0x7d, 0, 0,
		2, 0, 16, 0, 'd', 'a', 't', 'a',
		0, 0, 0, 0,
	}
}
