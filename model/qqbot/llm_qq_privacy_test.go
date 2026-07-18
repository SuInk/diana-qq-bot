package qqbot

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"diana-qq-bot/model/agent"
	"diana-qq-bot/model/llm"
)

type privacyRequestProvider struct {
	request llm.GenerateRequest
	reply   string
}

func (p *privacyRequestProvider) Generate(_ context.Context, req llm.GenerateRequest) (*llm.GenerateResponse, error) {
	p.request = req
	return &llm.GenerateResponse{Text: p.reply}, nil
}

type privacyAwareTestProvider struct {
	requests []llm.GenerateRequest
	generate func(call int, req llm.GenerateRequest) (string, error)
}

func (p *privacyAwareTestProvider) Generate(_ context.Context, req llm.GenerateRequest) (*llm.GenerateResponse, error) {
	p.requests = append(p.requests, req)
	if p.generate == nil {
		return nil, fmt.Errorf("privacy test provider has no response for call %d", len(p.requests))
	}
	text, err := p.generate(len(p.requests), req)
	if err != nil {
		return nil, err
	}
	return &llm.GenerateResponse{Provider: llm.ProviderOpenAICompatible, Model: "test", Text: text}, nil
}

func TestQQPrivacyProviderMasksRequestsAndRestoresReplies(t *testing.T) {
	scope := newQQPrivacyScope()
	ownerAlias := scope.register("10001", "owner")
	currentAlias := scope.register("10002", "current_user")
	groupAlias := scope.register("20001", "group")
	provider := &privacyRequestProvider{
		reply: fmt.Sprintf(`{"action":"final","content":"[CQ:at,qq=%s] 已处理 %s"}`, currentAlias, groupAlias),
	}
	client := &qqPrivacyProvider{provider: provider, scope: scope}

	response, err := client.Generate(context.Background(), llm.GenerateRequest{Messages: []llm.Message{
		{Role: llm.RoleSystem, Content: `owner_id=10001；日期 20260716；message_id=30003`},
		{Role: llm.RoleUser, Content: `{"source_user_id":"10002","target_group_id":"20001","message_id":"30003"}\nQQ号：10001\n[CQ:at,qq=10002]`},
	}})
	if err != nil {
		t.Fatal(err)
	}
	protected := requestTextForPrivacyTest(provider.request)
	for _, realID := range []string{"10001", "10002", "20001"} {
		if strings.Contains(protected, realID) {
			t.Fatalf("protected request leaked %s: %s", realID, protected)
		}
	}
	for _, alias := range []string{ownerAlias, currentAlias, groupAlias} {
		if !strings.Contains(protected, alias) {
			t.Fatalf("protected request missing alias %q: %s", alias, protected)
		}
	}
	if !strings.Contains(protected, llmQQPrivacyPrompt) {
		t.Fatalf("protected request missing privacy instructions: %s", protected)
	}
	if !strings.Contains(protected, "20260716") || !strings.Contains(protected, "30003") {
		t.Fatalf("non-QQ date or message ID was changed: %s", protected)
	}
	want := `{"action":"final","content":"[CQ:at,qq=10002] 已处理 20001"}`
	if response.Text != want {
		t.Fatalf("response = %q, want %q", response.Text, want)
	}
}

type privacyRoundTripTool struct {
	targetUserID string
}

func (*privacyRoundTripTool) Name() string { return "test.qq_lookup" }

func (*privacyRoundTripTool) Description() string {
	return `测试 QQ 标识往返。input: {"target_user_id":"QQ 用户标识"}`
}

func (t *privacyRoundTripTool) Run(_ context.Context, input map[string]any) (string, error) {
	t.targetUserID = strings.TrimSpace(fmt.Sprint(input["target_user_id"]))
	return fmt.Sprintf(`{"user_id":"%s","mention_cq":"[CQ:at,qq=%s]"}`, t.targetUserID, t.targetUserID), nil
}

type privacyRoundTripProvider struct {
	calls       int
	realID      string
	alias       string
	requests    []llm.GenerateRequest
	leakedReal  bool
	sawToolData bool
}

func (p *privacyRoundTripProvider) Generate(_ context.Context, req llm.GenerateRequest) (*llm.GenerateResponse, error) {
	p.calls++
	p.requests = append(p.requests, req)
	requestText := requestTextForPrivacyTest(req)
	if strings.Contains(requestText, p.realID) {
		p.leakedReal = true
	}
	switch p.calls {
	case 1:
		p.alias = regexp.MustCompile(`qq_current_user_[0-9a-f]+`).FindString(requestText)
		if p.alias == "" {
			return nil, fmt.Errorf("current-user alias missing from request: %s", requestText)
		}
		return &llm.GenerateResponse{Text: fmt.Sprintf(`{"action":"tool","tool":"test.qq_lookup","input":{"target_user_id":"%s"}}`, p.alias)}, nil
	case 2:
		p.sawToolData = strings.Contains(requestText, `"user_id":"`+p.alias+`"`) && strings.Contains(requestText, `[CQ:at,qq=`+p.alias+`]`)
		return &llm.GenerateResponse{Text: fmt.Sprintf(`{"action":"final","content":"[CQ:at,qq=%s] 查询完成"}`, p.alias)}, nil
	default:
		return nil, fmt.Errorf("unexpected Generate call %d", p.calls)
	}
}

func TestQQPrivacyProviderRoundTripsAgentToolIdentifiers(t *testing.T) {
	const realID = "10002"
	scope := newQQPrivacyScope()
	scope.register(realID, "current_user")
	provider := &privacyRoundTripProvider{realID: realID}
	tool := &privacyRoundTripTool{}
	runner, err := agent.NewRunner(
		&qqPrivacyProvider{provider: provider, scope: scope},
		agent.Config{WorkDir: t.TempDir(), MaxSteps: 2},
		agent.NewToolRegistry(tool),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()

	response, err := runner.Run(context.Background(), agent.Request{Messages: []llm.Message{{
		Role:    llm.RoleUser,
		Content: `查询 {"user_id":"10002"} 并在回复中提及该用户`,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if provider.leakedReal {
		t.Fatalf("provider saw real QQ ID in requests: %#v", provider.requests)
	}
	if tool.targetUserID != realID {
		t.Fatalf("tool target_user_id = %q, want %q", tool.targetUserID, realID)
	}
	if !provider.sawToolData {
		t.Fatalf("tool output was not remasked before the next model call: %#v", provider.requests)
	}
	if response.Text != "[CQ:at,qq=10002] 查询完成" {
		t.Fatalf("response = %q", response.Text)
	}
}

func TestLLMQQIDMaskingDefaultsOnAndCanBeDisabled(t *testing.T) {
	if !llmQQIDMaskingEnabled(BotConfig{}) {
		t.Fatal("QQ ID masking should default to enabled")
	}
	disabled := boolPointer(false)
	cfg := ConfigFromPayload(ConfigPayload{LLMQQIDMaskingEnabled: disabled}, BotConfig{})
	if llmQQIDMaskingEnabled(cfg) {
		t.Fatal("QQ ID masking should respect an explicit false value")
	}
	payload := PayloadFromConfig(cfg)
	if payload.LLMQQIDMaskingEnabled == nil || *payload.LLMQQIDMaskingEnabled {
		t.Fatalf("config round trip lost explicit false: %#v", payload.LLMQQIDMaskingEnabled)
	}
}

func requestTextForPrivacyTest(req llm.GenerateRequest) string {
	var chunks []string
	for _, message := range req.Messages {
		chunks = append(chunks, message.Content)
		for _, part := range message.Parts {
			chunks = append(chunks, part.Text)
		}
	}
	return strings.Join(chunks, "\n")
}

func privacyAliasForDisplayName(req llm.GenerateRequest, displayName string) string {
	pattern := regexp.MustCompile(`"user_id"\s*:\s*"(qq_[a-z_]+_[0-9a-f]+)"\s*,\s*"display_name"\s*:\s*"` + regexp.QuoteMeta(displayName) + `"`)
	match := pattern.FindStringSubmatch(requestTextForPrivacyTest(req))
	if len(match) < 2 {
		return ""
	}
	return match[1]
}

func privacyAliasForIdentitySource(req llm.GenerateRequest, source string) string {
	pattern := regexp.MustCompile(`"source"\s*:\s*"` + regexp.QuoteMeta(source) + `"\s*,\s*"user_id"\s*:\s*"(qq_[a-z_]+_[0-9a-f]+)"`)
	match := pattern.FindStringSubmatch(requestTextForPrivacyTest(req))
	if len(match) < 2 {
		return ""
	}
	return match[1]
}
