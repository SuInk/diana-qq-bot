package qqbot

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"diana-qq-bot/model/agent"
	"diana-qq-bot/model/llm"
)

func TestParseReplyIntentDecisionKeepsOnlyRegisteredTools(t *testing.T) {
	registry := agent.NewToolRegistry(
		&scopeTestTool{name: "web_search.search"},
		&scopeTestTool{name: "browser_render"},
	)
	decision, scope, ok := parseReplyIntentDecision(`{
		"action":"none",
		"prompt":"",
		"tools":["web_search.search","missing.tool","web_search.search"],
		"context_message_ids":["m2","m2","m4"],
		"keep_older_summary":true
	}`, registry)
	if !ok || decision.Action != visualIntentNone || !scope.Routed {
		t.Fatalf("decision = %#v scope = %#v ok = %v", decision, scope, ok)
	}
	if strings.Join(scope.ToolNames, ",") != "web_search.search" {
		t.Fatalf("tools = %#v", scope.ToolNames)
	}
	if strings.Join(scope.ContextMessageIDs, ",") != "m2,m4" || !scope.KeepContextSummary {
		t.Fatalf("scope = %#v", scope)
	}
}

func TestFilterAgentReplyHistoryKeepsSelectedReferencesAndNeighbors(t *testing.T) {
	history := make([]MessageEvent, 0, 10)
	for index := 1; index <= 10; index++ {
		history = append(history, MessageEvent{MessageID: "m" + strconv.Itoa(index), RawMessage: "history"})
	}
	event := MessageEvent{
		MessageID:               "current",
		SemanticSourceMessageID: "m9",
		Segments:                []MessageSegment{{Type: "reply", Data: map[string]string{"id": "m2"}}},
		Quoted:                  &QuotedMessage{MessageID: "m2"},
	}
	scope := agentReplyScope{Routed: true, ContextMessageIDs: []string{"m5"}}

	filtered := filterAgentReplyHistory(history, event, scope)
	got := make([]string, 0, len(filtered))
	for _, item := range filtered {
		got = append(got, item.MessageID)
	}
	want := "m1,m2,m3,m4,m5,m6,m8,m9,m10"
	if strings.Join(got, ",") != want {
		t.Fatalf("filtered IDs = %q, want %q", strings.Join(got, ","), want)
	}
}

func TestRouteReplyIntentUsesCompactToolCatalog(t *testing.T) {
	provider := &scopeRouteProvider{response: `{
		"action":"none",
		"prompt":"",
		"tools":["web_search.search"],
		"context_message_ids":["m1"],
		"keep_older_summary":false
	}`}
	runtime := NewRuntime(BotConfig{}, nil, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	runtime.remember(MessageEvent{Kind: EventKindGroup, GroupID: "g1", UserID: "u1", MessageID: "m1", RawMessage: "之前在聊长鑫存储"})
	event := MessageEvent{Kind: EventKindGroup, GroupID: "g1", UserID: "u1", MessageID: "m2", RawMessage: "搜索一下具体 IPO 时间"}
	registry := agent.NewToolRegistry(&scopeTestTool{
		name:        "web_search.search",
		description: `实时搜索。input: {"query":"keywords","num_results":10}`,
	})

	decision, scope, ok := runtime.routeReplyIntent(context.Background(), event, event.RawMessage, registry, false)
	if !ok || decision.Action != visualIntentNone || !scope.Routed || strings.Join(scope.ToolNames, ",") != "web_search.search" {
		t.Fatalf("decision = %#v scope = %#v ok = %v", decision, scope, ok)
	}
	if len(provider.request.Messages) != 2 {
		t.Fatalf("request messages = %#v", provider.request.Messages)
	}
	content := provider.request.Messages[1].Content
	start := strings.Index(content, "{")
	if start < 0 {
		t.Fatalf("router payload missing JSON: %s", content)
	}
	var payload visualIntentPayload
	if err := json.Unmarshal([]byte(content[start:]), &payload); err != nil {
		t.Fatalf("decode router payload: %v\n%s", err, content)
	}
	if len(payload.AvailableTools) != 1 || payload.AvailableTools[0].Name != "web_search.search" {
		t.Fatalf("available tools = %#v", payload.AvailableTools)
	}
	if strings.Contains(strings.ToLower(payload.AvailableTools[0].Description), "input:") || strings.Contains(payload.AvailableTools[0].Description, "num_results") {
		t.Fatalf("router catalog leaked schema: %#v", payload.AvailableTools[0])
	}
}

func TestQQSystemPromptOmitsUnselectedToolRules(t *testing.T) {
	runtime := NewRuntime(BotConfig{}, nil, NewPluginManager(), nil, nil, nil, nil)
	registry := agent.NewToolRegistry(&scopeTestTool{name: "web_search.search"})
	prompt := runtime.systemPromptWithRelationshipAndAgentTools(
		MessageEvent{Kind: EventKindGroup, GroupID: "g1", UserID: "owner"},
		nil,
		false,
		RelationshipPolicy{Owner: true, AllowPersonalSchedule: true},
		true,
		registry,
	)
	for _, unexpected := range []string{"diana.config", "diana.llm_config", "diana.relationship", "diana.tasks", "diana.reminder", "diana.schedule", "diana.tts", "diana.qq_group"} {
		if strings.Contains(prompt, unexpected) {
			t.Fatalf("prompt unexpectedly contains unselected tool %q: %s", unexpected, prompt)
		}
	}
}

func TestReplyToSkipsAgentProtocolWhenRouterSelectsNoTools(t *testing.T) {
	provider := &scopeRouteProvider{response: `{
		"action":"none",
		"prompt":"",
		"tools":[],
		"context_message_ids":[],
		"keep_older_summary":false
	}`}
	channel := &recordingChannel{}
	workDir := t.TempDir()
	runtime := NewRuntime(BotConfig{
		BotQQ:              "42",
		OwnerID:            "owner",
		AgentEnabled:       true,
		AgentWorkDir:       workDir,
		AgentSkillRoots:    []string{filepath.Join(workDir, "skills")},
		AgentMCPConfigPath: filepath.Join(workDir, "missing-mcp.json"),
	}, channel, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	provider.reply = "普通自然语言回复"
	event := MessageEvent{Kind: EventKindPrivate, UserID: "owner", MessageID: "m1", RawMessage: "你好"}

	reply, err := runtime.replyTo(context.Background(), event, event.RawMessage)
	if err != nil {
		t.Fatal(err)
	}
	if reply != provider.reply || provider.replyCalls != 1 {
		t.Fatalf("reply = %q reply calls = %d", reply, provider.replyCalls)
	}
	if len(channel.sent) != 1 || channel.sent[0].Text != provider.reply {
		t.Fatalf("sent = %#v", channel.sent)
	}
	for _, message := range provider.replyRequest.Messages {
		if strings.Contains(message.Content, "Diana QQ Bot 的内置 Agent") || strings.Contains(message.Content, `{"action":"tool"`) {
			t.Fatalf("ordinary reply leaked Agent protocol: %s", message.Content)
		}
	}
}

type scopeTestTool struct {
	name        string
	description string
}

func (t *scopeTestTool) Name() string { return t.name }
func (t *scopeTestTool) Description() string {
	if t.description != "" {
		return t.description
	}
	return t.name
}
func (t *scopeTestTool) Run(context.Context, map[string]any) (string, error) { return "", nil }

type scopeRouteProvider struct {
	request      llm.GenerateRequest
	response     string
	replyRequest llm.GenerateRequest
	reply        string
	replyCalls   int
}

func (p *scopeRouteProvider) Generate(_ context.Context, req llm.GenerateRequest) (*llm.GenerateResponse, error) {
	if requestMessagesContain(req.Messages, "功能路由器") {
		p.request = req
		return &llm.GenerateResponse{Provider: llm.ProviderOpenAICompatible, Model: "test", Text: p.response}, nil
	}
	p.replyCalls++
	p.replyRequest = req
	return &llm.GenerateResponse{Provider: llm.ProviderOpenAICompatible, Model: "test", Text: p.reply}, nil
}
