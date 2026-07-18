package qqbot

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"diana-qq-bot/model/llm"
)

func TestDianaQQGroupToolListsOtherMembersWithMentions(t *testing.T) {
	channel := &recordingChannel{apiResponses: map[string]map[string]any{
		"get_group_member_list": {
			"items": []any{
				map[string]any{"group_id": "20001", "user_id": "10001", "nickname": "TestOwner"},
				map[string]any{"group_id": "20001", "user_id": "10002", "nickname": "Alice", "card": "Alice Card"},
				map[string]any{"group_id": "20001", "user_id": "20002", "nickname": "Alice"},
				map[string]any{"group_id": "20001", "user_id": "10000", "nickname": "Diana"},
			},
		},
	}}
	runtime := NewRuntime(BotConfig{BotQQ: "10000"}, channel, NewPluginManager(), nil, nil, nil, nil)
	tool := newDianaQQGroupTool(runtime, MessageEvent{
		Kind:    EventKindGroup,
		SelfID:  "10000",
		GroupID: "20001",
		UserID:  "10001",
	})

	raw, err := tool.Run(context.Background(), map[string]any{
		"operation":              "members",
		"exclude_current_sender": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var result dianaQQGroupResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Members) != 2 || result.Members[0].UserID != "10002" || result.Members[1].UserID != "20002" {
		t.Fatalf("members = %#v", result.Members)
	}
	if result.Members[0].MentionCQ != "[CQ:at,qq=10002]" || result.Members[0].DisplayName != "Alice Card" {
		t.Fatalf("Alice = %#v", result.Members[0])
	}
}

func TestRuntimeAgentUsesQQGroupToolToMentionOtherMembers(t *testing.T) {
	channel := &recordingChannel{apiResponses: map[string]map[string]any{
		"get_group_member_list": {
			"items": []any{
				map[string]any{"group_id": "20001", "user_id": "10001", "nickname": "TestOwner"},
				map[string]any{"group_id": "20001", "user_id": "10002", "nickname": "Alice"},
			},
		},
	}}
	provider := &privacyAwareTestProvider{}
	var targetAlias string
	provider.generate = func(call int, req llm.GenerateRequest) (string, error) {
		switch call {
		case 1:
			return `{"action":"none"}`, nil
		case 2:
			return `{"action":"tool","tool":"diana.qq_group","input":{"operation":"members","exclude_current_sender":true}}`, nil
		case 3:
			targetAlias = privacyAliasForDisplayName(req, "Alice")
			if targetAlias == "" {
				return "", fmt.Errorf("Alice privacy alias missing from tool result")
			}
			return fmt.Sprintf(`{"action":"final","content":"[CQ:at,qq=%s] 喊你一下。"}`, targetAlias), nil
		default:
			return "", fmt.Errorf("unexpected LLM call %d", call)
		}
	}
	runtime := NewRuntime(BotConfig{
		BotQQ:         "10000",
		AgentEnabled:  true,
		AgentWorkDir:  t.TempDir(),
		AgentMaxSteps: 3,
	}, channel, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	event := MessageEvent{
		Kind:       EventKindGroup,
		SelfID:     "10000",
		GroupID:    "20001",
		UserID:     "10001",
		MessageID:  "ask-1",
		SenderName: "TestOwner",
		RawMessage: "然然@下除了我以外的其余人",
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "然然@下除了我以外的其余人"}}},
		ToMe:       true,
	}

	reply, err := runtime.replyTo(context.Background(), event, event.RawMessage)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reply, "[CQ:at,qq=10002]") {
		t.Fatalf("reply = %q", reply)
	}
	if len(provider.requests) != 3 || !requestMessagesContain(provider.requests[1].Messages, "diana.qq_group") || !requestMessagesContain(provider.requests[2].Messages, `"mention_cq": "[CQ:at,qq=`+targetAlias+`]"`) {
		t.Fatalf("requests = %#v", provider.requests)
	}
	for _, req := range provider.requests {
		protected := requestTextForPrivacyTest(req)
		for _, realID := range []string{"10000", "20001", "10001", "10002"} {
			if strings.Contains(protected, realID) {
				t.Fatalf("provider request leaked QQ ID %s: %s", realID, protected)
			}
		}
	}
	segments := buildOutgoingSegments(channel.sent[0])
	atCount := 0
	for _, segment := range segments {
		if segment["type"] == "at" && segment["data"].(map[string]string)["qq"] == "10002" {
			atCount++
		}
	}
	if atCount != 1 {
		t.Fatalf("segments = %#v", segments)
	}
}

func TestDianaQQGroupToolSearchesByCardOrNickname(t *testing.T) {
	channel := &recordingChannel{apiResponses: map[string]map[string]any{
		"get_group_member_list": {
			"items": []any{
				map[string]any{"group_id": "123", "user_id": "10001", "nickname": "Alice", "card": "阿梨"},
				map[string]any{"group_id": "123", "user_id": "10002", "nickname": "Bob"},
			},
		},
	}}
	runtime := NewRuntime(BotConfig{}, channel, NewPluginManager(), nil, nil, nil, nil)
	raw, err := newDianaQQGroupTool(runtime, MessageEvent{Kind: EventKindGroup, GroupID: "123", UserID: "owner"}).Run(context.Background(), map[string]any{
		"operation": "members",
		"query":     "阿梨",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, `"user_id": "10001"`) || strings.Contains(raw, `"user_id": "10002"`) {
		t.Fatalf("raw = %s", raw)
	}
}
