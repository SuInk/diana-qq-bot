package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"diana-qq-bot/model/qqbot"

	"github.com/gin-gonic/gin"
)

// TestQQBotHandlerConfigKeepsTokenHidden 验证对应功能场景。
func TestQQBotHandlerConfigKeepsTokenHidden(t *testing.T) {
	runtime := qqbot.NewRuntime(
		qqbot.BotConfig{
			Enabled:                 false,
			OneBotReverseWSEndpoint: "ws://127.0.0.1:18080/onebot/v11/ws",
			OneBotAccessToken:       "secret",
			NoneBotBridgeEnabled:    true,
			NoneBotBridgeEndpoint:   "ws://127.0.0.1:8080/onebot/v11/ws",
			NoneBotBridgeToken:      "nonebot-secret",
		},
		fakeChannel{},
		qqbot.NewDefaultPluginManager(),
		nil,
		nil,
		nil,
		nil,
	)
	handler := NewQQBotHandlerWithFactory(context.Background(), runtime, func(qqbot.BotConfig) qqbot.Channel {
		return fakeChannel{}
	})
	router := qqBotTestRouter(handler)

	body := []byte(`{"enabled":false,"onebot_reverse_ws_endpoint":"ws://127.0.0.1:18080/onebot/v11/ws","nonebot_bridge_enabled":true,"nonebot_bridge_endpoint":"ws://127.0.0.1:8080/onebot/v11/ws","group_triggers":["Diana"],"disabled_groups":["123456"],"welcome_enabled":true,"welcome_message":"欢迎 {user_id}","request_timeout_ms":1000}`)
	req := httptest.NewRequest(http.MethodPost, "/api/qqbot/config", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var payload qqbot.ConfigPayload
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if payload.OneBotAccessToken != "" || !payload.OneBotAccessTokenConfigured {
		t.Fatalf("token leaked or flag wrong: %#v", payload)
	}
	if payload.NoneBotBridgeToken != "" || !payload.NoneBotBridgeTokenConfigured {
		t.Fatalf("nonebot token leaked or flag wrong: %#v", payload)
	}
	if len(payload.DisabledGroups) != 1 || payload.DisabledGroups[0] != "123456" {
		t.Fatalf("disabled groups wrong: %#v", payload.DisabledGroups)
	}
	if !payload.WelcomeEnabled || payload.WelcomeMessage != "欢迎 {user_id}" {
		t.Fatalf("welcome payload wrong: %#v", payload)
	}
	if runtime.Config().OneBotAccessToken != "secret" {
		t.Fatalf("stored token = %q", runtime.Config().OneBotAccessToken)
	}
	if runtime.Config().NoneBotBridgeToken != "nonebot-secret" {
		t.Fatalf("stored nonebot token = %q", runtime.Config().NoneBotBridgeToken)
	}
}

// TestQQBotHandlerPluginInstallAndEnable 验证对应功能场景。
func TestQQBotHandlerPluginInstallAndEnable(t *testing.T) {
	runtime := qqbot.NewRuntime(qqbot.DefaultBotConfig(), fakeChannel{}, qqbot.NewDefaultPluginManager(), nil, nil, nil, nil)
	handler := NewQQBotHandlerWithFactory(context.Background(), runtime, func(qqbot.BotConfig) qqbot.Channel {
		return fakeChannel{}
	})
	router := qqBotTestRouter(handler)

	req := httptest.NewRequest(http.MethodPost, "/api/qqbot/plugins/official.nonebot-plugin-resolver-go/enabled", bytes.NewReader([]byte(`{"enabled":false}`)))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var state qqbot.PluginState
	if err := json.NewDecoder(rec.Body).Decode(&state); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if state.Enabled {
		t.Fatalf("Enabled = true, want false")
	}
}

// TestQQBotHandlerRejectsShortTokens 验证对应功能场景。
func TestQQBotHandlerRejectsShortTokens(t *testing.T) {
	runtime := qqbot.NewRuntime(qqbot.DefaultBotConfig(), fakeChannel{}, qqbot.NewDefaultPluginManager(), nil, nil, nil, nil)
	handler := NewQQBotHandlerWithFactory(context.Background(), runtime, func(qqbot.BotConfig) qqbot.Channel {
		return fakeChannel{}
	})
	router := qqBotTestRouter(handler)

	body := []byte(`{"enabled":false,"onebot_reverse_ws_endpoint":"ws://127.0.0.1:18080/onebot/v11/ws","onebot_access_token":"short","group_triggers":["Diana"],"request_timeout_ms":1000}`)
	req := httptest.NewRequest(http.MethodPost, "/api/qqbot/config", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

// TestQQBotHandlerGroupTestSendsMessage 验证QQ群收发测试会调用当前 channel 发群消息。
func TestQQBotHandlerGroupTestSendsMessage(t *testing.T) {
	channel := &recordingFakeChannel{}
	runtime := qqbot.NewRuntime(qqbot.DefaultBotConfig(), channel, qqbot.NewDefaultPluginManager(), nil, nil, nil, nil)
	handler := NewQQBotHandlerWithFactory(context.Background(), runtime, func(qqbot.BotConfig) qqbot.Channel {
		return channel
	})
	handler.SetFeatureFlags(QQBotFeatureFlags{GroupTest: true})
	router := qqBotTestRouter(handler)

	req := httptest.NewRequest(http.MethodPost, "/api/qqbot/group-test", bytes.NewReader([]byte(`{"group_id":"123456","message":"测试消息"}`)))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(channel.calls) != 1 {
		t.Fatalf("calls = %#v", channel.calls)
	}
	call := channel.calls[0]
	if call.action != "send_group_msg" {
		t.Fatalf("action = %q", call.action)
	}
	if call.params["group_id"] != int64(123456) {
		t.Fatalf("group_id = %#v", call.params["group_id"])
	}
	segments, ok := call.params["message"].([]map[string]any)
	if !ok || len(segments) != 1 {
		t.Fatalf("message = %#v", call.params["message"])
	}
	if segments[0]["type"] != "text" {
		t.Fatalf("segment type = %#v", segments[0])
	}
	var payload groupTestResponse
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if !payload.Sent || payload.GroupID != "123456" || payload.Message != "测试消息" || payload.MessageID != "42" {
		t.Fatalf("payload = %#v", payload)
	}
}

// TestQQBotHandlerGroupTestRequiresGroupID 验证QQ群收发测试必须填写群号。
func TestQQBotHandlerGroupTestRequiresGroupID(t *testing.T) {
	runtime := qqbot.NewRuntime(qqbot.DefaultBotConfig(), fakeChannel{}, qqbot.NewDefaultPluginManager(), nil, nil, nil, nil)
	handler := NewQQBotHandlerWithFactory(context.Background(), runtime, func(qqbot.BotConfig) qqbot.Channel {
		return fakeChannel{}
	})
	handler.SetFeatureFlags(QQBotFeatureFlags{GroupTest: true})
	router := qqBotTestRouter(handler)

	req := httptest.NewRequest(http.MethodPost, "/api/qqbot/group-test", bytes.NewReader([]byte(`{"message":"测试消息"}`)))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

// TestQQBotHandlerGroupTestRequiresNumericGroupID 验证群测试不会把明显非法群号透传给 OneBot。
func TestQQBotHandlerGroupTestRequiresNumericGroupID(t *testing.T) {
	channel := &recordingFakeChannel{}
	runtime := qqbot.NewRuntime(qqbot.DefaultBotConfig(), channel, qqbot.NewDefaultPluginManager(), nil, nil, nil, nil)
	handler := NewQQBotHandlerWithFactory(context.Background(), runtime, func(qqbot.BotConfig) qqbot.Channel {
		return channel
	})
	handler.SetFeatureFlags(QQBotFeatureFlags{GroupTest: true})
	router := qqBotTestRouter(handler)

	req := httptest.NewRequest(http.MethodPost, "/api/qqbot/group-test", bytes.NewReader([]byte(`{"group_id":"abc","message":"测试消息"}`)))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(channel.calls) != 0 {
		t.Fatalf("calls = %#v", channel.calls)
	}
}

// TestQQBotHandlerGroupTestReturnsRecentGroupEvents 验证QQ群收发测试能读取指定群最近收到的消息。
func TestQQBotHandlerGroupTestReturnsRecentGroupEvents(t *testing.T) {
	runtime := qqbot.NewRuntime(qqbot.DefaultBotConfig(), fakeChannel{}, qqbot.NewDefaultPluginManager(), nil, nil, nil, nil)
	if err := runtime.HandleEvent(context.Background(), qqbot.MessageEvent{
		Kind:       qqbot.EventKindGroup,
		GroupID:    "123456",
		UserID:     "10001",
		RawMessage: "普通群消息",
	}); err != nil {
		t.Fatalf("HandleEvent() error = %v", err)
	}
	handler := NewQQBotHandlerWithFactory(context.Background(), runtime, func(qqbot.BotConfig) qqbot.Channel {
		return fakeChannel{}
	})
	handler.SetFeatureFlags(QQBotFeatureFlags{GroupTest: true})
	router := qqBotTestRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/api/qqbot/group-test?group_id=123456", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var payload groupTestResponse
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if len(payload.RecentEvents) != 1 {
		t.Fatalf("RecentEvents = %#v", payload.RecentEvents)
	}
	if payload.RecentEvents[0].GroupID != "123456" || payload.RecentEvents[0].Text != "普通群消息" {
		t.Fatalf("RecentEvents[0] = %#v", payload.RecentEvents[0])
	}
}

func TestQQBotHandlerGroupTestRecallsMessage(t *testing.T) {
	channel := &recordingFakeChannel{}
	runtime := qqbot.NewRuntime(qqbot.DefaultBotConfig(), channel, qqbot.NewDefaultPluginManager(), nil, nil, nil, nil)
	handler := NewQQBotHandlerWithFactory(context.Background(), runtime, func(qqbot.BotConfig) qqbot.Channel { return channel })
	handler.SetFeatureFlags(QQBotFeatureFlags{GroupTest: true})
	router := qqBotTestRouter(handler)

	req := httptest.NewRequest(http.MethodPost, "/api/qqbot/group-test/recall", bytes.NewReader([]byte(`{"message_id":"12345"}`)))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(channel.calls) != 1 || channel.calls[0].action != "delete_msg" || channel.calls[0].params["message_id"] != int64(12345) {
		t.Fatalf("calls = %#v", channel.calls)
	}
}

func TestQQBotHandlerGroupTestListsRootFiles(t *testing.T) {
	channel := &recordingFakeChannel{}
	runtime := qqbot.NewRuntime(qqbot.DefaultBotConfig(), channel, qqbot.NewDefaultPluginManager(), nil, nil, nil, nil)
	handler := NewQQBotHandlerWithFactory(context.Background(), runtime, func(qqbot.BotConfig) qqbot.Channel { return channel })
	handler.SetFeatureFlags(QQBotFeatureFlags{GroupTest: true})
	router := qqBotTestRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/api/qqbot/group-test/files?group_id=123456", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(channel.calls) != 1 || channel.calls[0].action != "get_group_root_files" || channel.calls[0].params["group_id"] != int64(123456) {
		t.Fatalf("calls = %#v", channel.calls)
	}
}

func TestQQBotHandlerGroupTestUploadsFile(t *testing.T) {
	channel := &recordingFakeChannel{}
	runtime := qqbot.NewRuntime(qqbot.DefaultBotConfig(), channel, qqbot.NewDefaultPluginManager(), nil, nil, nil, nil)
	handler := NewQQBotHandlerWithFactory(context.Background(), runtime, func(qqbot.BotConfig) qqbot.Channel { return channel })
	handler.SetFeatureFlags(QQBotFeatureFlags{GroupTest: true})
	router := qqBotTestRouter(handler)

	req := httptest.NewRequest(http.MethodPost, "/api/qqbot/group-test/upload-file", bytes.NewReader([]byte(`{"group_id":"123456","file":"/tmp/report.txt","name":"report.txt"}`)))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(channel.calls) != 1 || channel.calls[0].action != "upload_group_file" || channel.calls[0].params["group_id"] != int64(123456) || channel.calls[0].params["file"] != "/tmp/report.txt" {
		t.Fatalf("calls = %#v", channel.calls)
	}
}

func TestQQBotHandlerGroupTestSharesLocalFileBeforeUpload(t *testing.T) {
	channel := &recordingFakeChannel{}
	runtime := qqbot.NewRuntime(qqbot.DefaultBotConfig(), channel, qqbot.NewDefaultPluginManager(), nil, nil, nil, nil)
	handler := NewQQBotHandlerWithFactory(context.Background(), runtime, func(qqbot.BotConfig) qqbot.Channel { return channel })
	handler.SetFeatureFlags(QQBotFeatureFlags{GroupTest: true})
	handler.SetLocalMediaSharer(fakeLocalMediaSharer{url: "http://127.0.0.1:18080/api/qqbot/media/token"})
	router := qqBotTestRouter(handler)

	req := httptest.NewRequest(http.MethodPost, "/api/qqbot/group-test/upload-file", bytes.NewReader([]byte(`{"group_id":"123456","file":"/tmp/report.txt","name":"report.txt"}`)))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := channel.calls[0].params["file"]; got != "http://127.0.0.1:18080/api/qqbot/media/token" {
		t.Fatalf("shared file = %#v", got)
	}
}

func TestQQBotHandlerGroupTestAllowsReadOnlyOneBotCall(t *testing.T) {
	channel := &recordingFakeChannel{}
	runtime := qqbot.NewRuntime(qqbot.DefaultBotConfig(), channel, qqbot.NewDefaultPluginManager(), nil, nil, nil, nil)
	handler := NewQQBotHandlerWithFactory(context.Background(), runtime, func(qqbot.BotConfig) qqbot.Channel { return channel })
	handler.SetFeatureFlags(QQBotFeatureFlags{GroupTest: true})
	router := qqBotTestRouter(handler)

	req := httptest.NewRequest(http.MethodPost, "/api/qqbot/group-test/onebot", bytes.NewReader([]byte(`{"action":"get_group_msg_history","params":{"group_id":123456,"count":20}}`)))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || len(channel.calls) != 1 || channel.calls[0].action != "get_group_msg_history" {
		t.Fatalf("status = %d body = %s calls = %#v", rec.Code, rec.Body.String(), channel.calls)
	}
}

func TestQQBotHandlerGroupTestAllowsGetImage(t *testing.T) {
	channel := &recordingFakeChannel{}
	runtime := qqbot.NewRuntime(qqbot.DefaultBotConfig(), channel, qqbot.NewDefaultPluginManager(), nil, nil, nil, nil)
	handler := NewQQBotHandlerWithFactory(context.Background(), runtime, func(qqbot.BotConfig) qqbot.Channel { return channel })
	handler.SetFeatureFlags(QQBotFeatureFlags{GroupTest: true})
	router := qqBotTestRouter(handler)

	req := httptest.NewRequest(http.MethodPost, "/api/qqbot/group-test/onebot", bytes.NewReader([]byte(`{"action":"get_image","params":{"file":"image.jpg"}}`)))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || len(channel.calls) != 1 || channel.calls[0].action != "get_image" {
		t.Fatalf("status = %d body = %s calls = %#v", rec.Code, rec.Body.String(), channel.calls)
	}
}

func TestQQBotHandlerGroupTestRejectsMutatingOneBotCall(t *testing.T) {
	channel := &recordingFakeChannel{}
	runtime := qqbot.NewRuntime(qqbot.DefaultBotConfig(), channel, qqbot.NewDefaultPluginManager(), nil, nil, nil, nil)
	handler := NewQQBotHandlerWithFactory(context.Background(), runtime, func(qqbot.BotConfig) qqbot.Channel { return channel })
	handler.SetFeatureFlags(QQBotFeatureFlags{GroupTest: true})
	router := qqBotTestRouter(handler)

	req := httptest.NewRequest(http.MethodPost, "/api/qqbot/group-test/onebot", bytes.NewReader([]byte(`{"action":"send_group_msg","params":{}}`)))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || len(channel.calls) != 0 {
		t.Fatalf("status = %d body = %s calls = %#v", rec.Code, rec.Body.String(), channel.calls)
	}
}

func TestQQBotHandlerGroupTestParsesRealFileFlow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("real file parser content"))
	}))
	defer server.Close()
	channel := &fileFakeChannel{url: server.URL + "/report.txt"}
	runtime := qqbot.NewRuntime(qqbot.DefaultBotConfig(), channel, qqbot.NewDefaultPluginManager(), nil, nil, nil, nil)
	handler := NewQQBotHandlerWithFactory(context.Background(), runtime, func(qqbot.BotConfig) qqbot.Channel { return channel })
	handler.SetFeatureFlags(QQBotFeatureFlags{GroupTest: true})
	router := qqBotTestRouter(handler)

	req := httptest.NewRequest(http.MethodPost, "/api/qqbot/group-test/file", bytes.NewReader([]byte(`{"group_id":"123456","file_id":"file-1","name":"report.txt"}`)))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "real file parser content") {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(channel.calls) == 0 || channel.calls[0].action != "get_group_file_url" {
		t.Fatalf("calls = %#v", channel.calls)
	}
}

func TestQQBotHandlerGroupTestParsesLocalFileThroughProductionPlugin(t *testing.T) {
	localPath := t.TempDir() + "/report.txt"
	if err := os.WriteFile(localPath, []byte("local cross-app file content"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := qqbot.NewRuntime(qqbot.DefaultBotConfig(), fakeChannel{}, qqbot.NewDefaultPluginManager(), nil, nil, nil, nil)
	handler := NewQQBotHandlerWithFactory(context.Background(), runtime, func(qqbot.BotConfig) qqbot.Channel { return fakeChannel{} })
	handler.SetFeatureFlags(QQBotFeatureFlags{GroupTest: true})
	router := qqBotTestRouter(handler)
	body, err := json.Marshal(map[string]string{"name": "report.txt", "local_path": localPath})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/qqbot/group-test/file", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "local cross-app file content") {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestQQBotHandlerGroupTestSharesOnlyNapCatQRCode(t *testing.T) {
	runtime := qqbot.NewRuntime(qqbot.DefaultBotConfig(), fakeChannel{}, qqbot.NewDefaultPluginManager(), nil, nil, nil, nil)
	handler := NewQQBotHandlerWithFactory(context.Background(), runtime, func(qqbot.BotConfig) qqbot.Channel { return fakeChannel{} })
	handler.SetFeatureFlags(QQBotFeatureFlags{GroupTest: true})
	handler.SetLocalMediaSharer(fakeLocalMediaSharer{url: "http://127.0.0.1:18080/api/qqbot/media/qr-token"})
	router := qqBotTestRouter(handler)

	req := httptest.NewRequest(http.MethodPost, "/api/qqbot/group-test/napcat-qrcode", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "/api/qqbot/media/qr-token") {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

// TestQQBotHandlerGroupTestDisabledByDefault 验证QQ群收发测试默认不暴露到正式环境。
func TestQQBotHandlerGroupTestDisabledByDefault(t *testing.T) {
	runtime := qqbot.NewRuntime(qqbot.DefaultBotConfig(), fakeChannel{}, qqbot.NewDefaultPluginManager(), nil, nil, nil, nil)
	handler := NewQQBotHandlerWithFactory(context.Background(), runtime, func(qqbot.BotConfig) qqbot.Channel {
		return fakeChannel{}
	})
	router := qqBotTestRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/api/qqbot/group-test?group_id=123456", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/qqbot/features", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("features status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var flags QQBotFeatureFlags
	if err := json.NewDecoder(rec.Body).Decode(&flags); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if flags.GroupTest {
		t.Fatalf("GroupTest = true, want false")
	}
}

func TestGroupAdminAllowsConfiguredBotOwnerWithoutNapCatRoleLookup(t *testing.T) {
	channel := &recordingFakeChannel{}
	runtime := qqbot.NewRuntime(qqbot.BotConfig{OwnerID: "10001"}, channel, qqbot.NewPluginManager(), nil, nil, nil, nil)
	handler := NewQQBotHandlerWithFactory(context.Background(), runtime, func(qqbot.BotConfig) qqbot.Channel { return channel })

	if err := handler.requireGroupAdmin(context.Background(), "123456", "10001"); err != nil {
		t.Fatal(err)
	}
	if len(channel.calls) != 0 {
		t.Fatalf("bot owner should not require a NapCat role lookup: %#v", channel.calls)
	}
}

func TestSanitizeGroupConfigPayloadKeepsReplyPolicyInRange(t *testing.T) {
	cfg := sanitizeGroupConfigPayload(qqbot.GroupConfig{
		PassiveReplyChance:      2,
		PassiveReplyThreshold:   0.2,
		MinimumReplyMemberLevel: 2000,
	}, "123456")
	if cfg.PassiveReplyChance != 1 || cfg.PassiveReplyThreshold != 0.5 || cfg.MinimumReplyMemberLevel != 1000 {
		t.Fatalf("sanitized reply policy = %#v", cfg)
	}
}

// qqBotTestRouter 封装当前模块的 qqBotTestRouter 逻辑。
func qqBotTestRouter(handler *QQBotHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler.Register(router)
	return router
}

type fakeChannel struct{}

// Connect 封装当前模块的 Connect 逻辑。
func (fakeChannel) Connect(context.Context, qqbot.EventHandler) error { return nil }

// Send 封装当前模块的 Send 逻辑。
func (c fakeChannel) Send(context.Context, qqbot.OutgoingMessage) error { return nil }

type recordingFakeChannel struct {
	fakeChannel
	calls []apiCall
}

type fileFakeChannel struct {
	fakeChannel
	url   string
	calls []apiCall
}

type fakeLocalMediaSharer struct {
	url string
}

func (s fakeLocalMediaSharer) Share(string, time.Duration) (string, bool) {
	return s.url, s.url != ""
}

func (c *fileFakeChannel) CallAPI(_ context.Context, action string, params map[string]any) (map[string]any, error) {
	c.calls = append(c.calls, apiCall{action: action, params: params})
	return map[string]any{"url": c.url}, nil
}

type apiCall struct {
	action string
	params map[string]any
}

// CallAPI 封装当前模块的 CallAPI 逻辑。
func (fakeChannel) CallAPI(context.Context, string, map[string]any) (map[string]any, error) {
	return nil, nil
}

// CallAPI 记录 API 调用，并模拟 OneBot 标准的 message_id 返回。
func (c *recordingFakeChannel) CallAPI(_ context.Context, action string, params map[string]any) (map[string]any, error) {
	c.calls = append(c.calls, apiCall{action: action, params: params})
	return map[string]any{"message_id": int64(42)}, nil
}

// Status 返回当前状态快照。
func (fakeChannel) Status() qqbot.ChannelStatus { return qqbot.ChannelStatus{} }

// Close 释放当前对象持有的资源。
func (fakeChannel) Close() error { return nil }
