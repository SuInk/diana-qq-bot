package qqbot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"diana-qq-bot/model/agent"
	"diana-qq-bot/model/llm"
)

func TestDianaImageAgentToolGeneratesFromResolvedPrompt(t *testing.T) {
	var submittedPrompt string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/media" {
			writeTestPNG(w)
			return
		}
		if r.URL.Path != "/v1/images/generations" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		var body struct {
			Prompt string `json:"prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		submittedPrompt = body.Prompt
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"c2VhcmNoLWRlcml2ZWQtaW1hZ2U="}]}`))
	}))
	defer server.Close()

	store := &stubLLMProfileStore{set: llm.NewProfileSet(llm.ProviderConfig{
		Provider:   llm.ProviderOpenAICompatible,
		APIKey:     "secret",
		BaseURL:    server.URL + "/v1",
		Model:      "gpt-test",
		ImageModel: "gpt-image-2",
	})}
	channel := &recordingChannel{apiResponses: map[string]map[string]any{
		"get_group_info": {
			"group_id":   "20005",
			"group_name": "测试群",
		},
		"get_group_member_info": {
			"group_id": "20005",
			"user_id":  "10001",
			"nickname": "Alice",
		},
	}}
	runtime := NewRuntime(BotConfig{}, channel, NewPluginManager(), store, nil, nil, nil)
	sharer := &recordingLocalMediaSharer{url: server.URL + "/media"}
	runtime.SetLocalMediaSharer(sharer)
	logs := &captureAppLogs{}
	runtime.SetAppLogWriter(logs)
	event := MessageEvent{Kind: EventKindGroup, GroupID: "20005", UserID: "10001", MessageID: "agent-image"}
	policy := RelationshipPolicyFor(UserMemoryProfile{Favorability: 20, MessageCount: 10}, "owner", event.UserID)
	tool := newDianaImageTool(runtime, event, policy)

	raw, err := tool.Run(context.Background(), map[string]any{
		"operation": "generate",
		"prompt":    "官方检索结果确认主色为靛蓝 #4B0082 与金色 #FFD700；据此创作一张平面海报。",
		"caption":   "按检索结果画好了。",
	})
	if err != nil {
		t.Fatal(err)
	}
	var queued dianaImageToolResult
	if err := json.Unmarshal([]byte(raw), &queued); err != nil {
		t.Fatalf("queued result = %q: %v", raw, err)
	}
	if !queued.OK || !queued.Queued || !strings.HasPrefix(queued.TaskID, "img-") || queued.Action != "generate" {
		t.Fatalf("queued result = %#v", queued)
	}
	if _, ok := tool.(agent.TerminalResultTool); ok {
		t.Fatal("diana.image must let the agent continue to its final text reply")
	}
	waitForCondition(t, 2*time.Second, func() bool {
		return runtime.activeSubagentTaskCount() == 0
	})
	if !strings.Contains(submittedPrompt, "#4B0082") || !strings.Contains(submittedPrompt, "#FFD700") || !strings.Contains(submittedPrompt, "群聊：测试群") {
		t.Fatalf("submitted prompt = %q", submittedPrompt)
	}
	if len(sharer.paths) != 1 {
		t.Fatalf("shared paths = %#v", sharer.paths)
	}
	defer os.Remove(sharer.paths[0])
	data, err := os.ReadFile(sharer.paths[0])
	if err != nil || string(data) != "search-derived-image" {
		t.Fatalf("shared image = %q, err = %v", data, err)
	}
	if len(channel.sent) != 1 || channel.sent[0].Text != "按检索结果画好了。" || len(channel.sent[0].ImageURLs) != 1 || channel.sent[0].ImageURLs[0] != sharer.url {
		t.Fatalf("sent = %#v", channel.sent)
	}
	var loggedPrompt string
	imageLogFound := false
	for _, entry := range logs.entries {
		if entry.Action == "qqbot.image.generate" {
			loggedPrompt, _ = entry.Metadata["prompt"].(string)
			imageLogFound = true
			break
		}
	}
	if !imageLogFound {
		t.Fatalf("logs = %#v", logs.entries)
	}
	if !strings.Contains(loggedPrompt, "#4B0082") || !strings.Contains(loggedPrompt, "群聊：测试群") {
		t.Fatalf("logged prompt = %q", loggedPrompt)
	}
}

func TestDianaImageAgentToolEnforcesRelationshipPermissions(t *testing.T) {
	initial := RelationshipPolicyFor(UserMemoryProfile{}, "owner", "user")
	if initial.allowedAgentToolNames()[dianaImageToolName] {
		t.Fatalf("initial allowlist = %#v", initial.allowedAgentToolNames())
	}
	runtime := NewRuntime(BotConfig{}, nilChannel{}, NewPluginManager(), nil, nil, nil, nil)
	tool := newDianaImageTool(runtime, MessageEvent{UserID: "user"}, initial)
	for _, operation := range []string{"generate", "edit"} {
		_, err := tool.Run(context.Background(), map[string]any{
			"operation": operation,
			"prompt":    "测试",
		})
		if err == nil || !strings.Contains(err.Error(), "好感度不足") || !strings.Contains(err.Error(), relationshipImageTierName) {
			t.Fatalf("%s error = %v", operation, err)
		}
	}
	hostile := RelationshipPolicyFor(UserMemoryProfile{Favorability: -20}, "owner", "user")
	if hostile.allowedAgentToolNames()[dianaImageToolName] {
		t.Fatalf("hostile allowlist = %#v", hostile.allowedAgentToolNames())
	}
}

func TestRuntimeAgentSearchesBeforeGeneratingImage(t *testing.T) {
	var submittedPrompt string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/media" {
			writeTestPNG(w)
			return
		}
		if r.URL.Path != "/v1/images/generations" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		var body struct {
			Prompt string `json:"prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		submittedPrompt = body.Prompt
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"YWdlbnQtaW1hZ2U="}]}`))
	}))
	defer server.Close()

	search := &recordingAgentSearchTool{result: "检索确认：官方资料使用靛蓝 #4B0082 与金色 #FFD700。"}
	plugins := NewPluginManager(&agentImageSearchPlugin{tool: search})
	provider := &sequenceLLMProvider{replies: []string{
		`{"action":"none","prompt":""}`,
		`{"action":"tool","tool":"web_search.search","input":{"query":"官方主题配色"}}`,
		`{"action":"tool","tool":"diana.image","input":{"operation":"generate","prompt":"根据已核验的官方资料创作平面海报，主色严格使用靛蓝 #4B0082 与金色 #FFD700，简洁几何构图，不添加文字。","caption":"按查到的官方配色画好了。"}}`,
		`{"action":"final","content":"文字说明先发给你，图片完成后会自动补上。"}`,
	}}
	store := &stubLLMProfileStore{set: llm.NewProfileSet(llm.ProviderConfig{
		Provider:   llm.ProviderOpenAICompatible,
		APIKey:     "secret",
		BaseURL:    server.URL + "/v1",
		Model:      "gpt-test",
		ImageModel: "gpt-image-2",
	})}
	channel := &recordingChannel{}
	runtime := NewRuntime(BotConfig{
		OwnerID:       "owner",
		AgentEnabled:  true,
		AgentWorkDir:  t.TempDir(),
		AgentMaxSteps: 4,
	}, channel, plugins, store, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	sharer := &recordingLocalMediaSharer{url: server.URL + "/media"}
	runtime.SetLocalMediaSharer(sharer)
	logs := &captureAppLogs{}
	runtime.SetAppLogWriter(logs)
	event := MessageEvent{
		Kind:       EventKindPrivate,
		UserID:     "owner",
		MessageID:  "search-then-image",
		RawMessage: "先搜索官方主题配色，核验后根据结果生成一张海报",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "先搜索官方主题配色，核验后根据结果生成一张海报"}}},
	}

	reply, err := runtime.replyTo(context.Background(), event, event.RawMessage)
	if err != nil {
		t.Fatal(err)
	}
	waitForCondition(t, 2*time.Second, func() bool {
		return runtime.activeSubagentTaskCount() == 0
	})
	if search.calls != 1 {
		t.Fatalf("search calls = %d", search.calls)
	}
	if len(provider.requests) != 4 {
		t.Fatalf("provider requests = %d", len(provider.requests))
	}
	if !requestMessagesContain(provider.requests[2].Messages, search.result) {
		t.Fatalf("image tool decision did not receive search result: %#v", provider.requests[2].Messages)
	}
	if !requestMessagesContain(provider.requests[1].Messages, "先完成搜索和必要的网页核验") || !requestMessagesContain(provider.requests[1].Messages, dianaImageToolName) {
		t.Fatalf("agent prompt does not enforce search-before-image: %#v", provider.requests[1].Messages)
	}
	if !strings.Contains(submittedPrompt, "#4B0082") || !strings.Contains(submittedPrompt, "#FFD700") {
		t.Fatalf("submitted prompt = %q", submittedPrompt)
	}
	if reply != "文字说明先发给你，图片完成后会自动补上。" {
		t.Fatalf("reply = %q", reply)
	}
	if len(channel.sent) != 2 {
		t.Fatalf("sent = %#v", channel.sent)
	}
	textFound := false
	imageFound := false
	for _, message := range channel.sent {
		if message.Text == reply && len(message.ImageURLs) == 0 {
			textFound = true
		}
		if message.Text == "按查到的官方配色画好了。" && len(message.ImageURLs) == 1 && message.ImageURLs[0] == sharer.url {
			imageFound = true
		}
	}
	if !textFound || !imageFound {
		t.Fatalf("sent = %#v", channel.sent)
	}
	if len(sharer.paths) != 1 {
		t.Fatalf("shared paths = %#v", sharer.paths)
	}
	defer os.Remove(sharer.paths[0])
	wantTargets := map[string]bool{"web_search.search": false, dianaImageToolName: false}
	imageLogFound := false
	for _, entry := range logs.entries {
		if entry.Action == "qqbot.agent_tool" {
			if _, ok := wantTargets[entry.Target]; ok {
				wantTargets[entry.Target] = true
			}
		}
		if entry.Action == "qqbot.image.generate" {
			imageLogFound = true
		}
	}
	if !wantTargets["web_search.search"] || !wantTargets[dianaImageToolName] || !imageLogFound {
		t.Fatalf("logs = %#v", logs.entries)
	}
}

type recordingAgentSearchTool struct {
	result string
	calls  int
}

func (t *recordingAgentSearchTool) Name() string { return "web_search.search" }

func (t *recordingAgentSearchTool) Description() string {
	return `测试搜索工具。input: {"query":"搜索词"}`
}

func (t *recordingAgentSearchTool) Run(context.Context, map[string]any) (string, error) {
	t.calls++
	return t.result, nil
}

type agentImageSearchPlugin struct {
	tool agent.Tool
}

func (p *agentImageSearchPlugin) Manifest() PluginManifest {
	return PluginManifest{ID: "test.agent-image-search", Name: "Agent image search test", BuiltIn: true}
}

func (p *agentImageSearchPlugin) Handle(context.Context, PluginRequest) (*PluginResponse, error) {
	return nil, nil
}

func (p *agentImageSearchPlugin) AgentTools() []agent.Tool {
	if p == nil || p.tool == nil {
		return nil
	}
	return []agent.Tool{p.tool}
}

var _ agent.Tool = (*recordingAgentSearchTool)(nil)
var _ Plugin = (*agentImageSearchPlugin)(nil)
var _ AgentToolProviderPlugin = (*agentImageSearchPlugin)(nil)

func writeTestPNG(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write([]byte("\x89PNG\r\n\x1a\nmock-image"))
}
