package qqbot

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestDianaRelationshipToolUsesMentionedMemberAsTarget(t *testing.T) {
	memory := newMemoryUserMemoryStore()
	memory.profiles["10005"] = UserMemoryProfile{
		UserID:       "10005",
		DisplayName:  "Alice",
		Favorability: 5,
		MessageCount: 18,
	}
	channel := &recordingChannel{apiResponses: map[string]map[string]any{
		"get_group_member_info": {
			"group_id": 20002,
			"user_id":  10005,
			"nickname": "Alice",
			"role":     "member",
		},
	}}
	runtime := NewRuntime(BotConfig{OwnerID: "10001", BotQQ: "10000"}, channel, NewPluginManager(), nil, nil, nil, nil)
	runtime.SetUserMemoryStore(memory)
	event := MessageEvent{
		Kind:    EventKindGroup,
		SelfID:  "10000",
		UserID:  "10001",
		GroupID: "20002",
		Segments: []MessageSegment{
			{Type: "text", Data: map[string]string{"text": "嘉然看下"}},
			{Type: "at", Data: map[string]string{"qq": "10005"}},
			{Type: "text", Data: map[string]string{"text": " 的好感度"}},
		},
	}

	raw, err := newDianaRelationshipTool(runtime, event).Run(context.Background(), map[string]any{"operation": "get"})
	if err != nil {
		t.Fatal(err)
	}
	var result dianaRelationshipResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if result.Target == nil || result.Target.UserID != "10005" || result.Target.DisplayName != "Alice" {
		t.Fatalf("target = %#v", result.Target)
	}
	if result.Target.Favorability != 5 || result.Target.MessageCount != 18 || result.Target.RelationshipTier != RelationshipAcquaintance {
		t.Fatalf("relationship = %#v", result.Target)
	}
	if result.Target.ScheduleLimit != 1 || len(result.Target.Permissions) == 0 || result.Target.CanGenerateImage || result.Target.CanEditImage || result.Target.CanDocumentOCR {
		t.Fatalf("permissions = %#v", result.Target)
	}
	if result.Target.MentionCQ != "[CQ:at,qq=10005]" || !result.Target.HasHistory {
		t.Fatalf("mention/history = %#v", result.Target)
	}
}

func TestDianaRelationshipToolListsCurrentGroupForOwner(t *testing.T) {
	memory := newMemoryUserMemoryStore()
	memory.profiles["10001"] = UserMemoryProfile{UserID: "10001", DisplayName: "Alice", Favorability: 20, MessageCount: 12}
	memory.profiles["10002"] = UserMemoryProfile{UserID: "10002", DisplayName: "Bob", Favorability: 80, MessageCount: 50}
	memory.profiles["10003"] = UserMemoryProfile{UserID: "10003", DisplayName: "Carol", Favorability: 5, MessageCount: 2}
	channel := &recordingChannel{apiResponses: map[string]map[string]any{
		"get_group_member_list": {
			"members": []any{
				map[string]any{"group_id": "123", "user_id": "10001", "nickname": "Alice"},
				map[string]any{"group_id": "123", "user_id": "10002", "nickname": "Bob"},
				map[string]any{"group_id": "123", "user_id": "10003", "nickname": "Carol"},
			},
		},
	}}
	runtime := NewRuntime(BotConfig{OwnerID: "owner"}, channel, NewPluginManager(), nil, nil, nil, nil)
	runtime.SetUserMemoryStore(memory)
	tool := newDianaRelationshipTool(runtime, MessageEvent{Kind: EventKindGroup, GroupID: "123", UserID: "owner"})

	raw, err := tool.Run(context.Background(), map[string]any{"operation": "list", "limit": 2})
	if err != nil {
		t.Fatal(err)
	}
	var result dianaRelationshipResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 2 || result.Items[0].UserID != "10002" || result.Items[1].UserID != "10001" {
		t.Fatalf("items = %#v", result.Items)
	}
}

func TestDianaRelationshipOwnerCanSetAndAdjustOthersFavorability(t *testing.T) {
	memory := newMemoryUserMemoryStore()
	memory.profiles["10002"] = UserMemoryProfile{UserID: "10002", Favorability: 5, MessageCount: 18}
	runtime := NewRuntime(BotConfig{OwnerID: "10001"}, nilChannel{}, NewPluginManager(), nil, nil, nil, nil)
	runtime.SetUserMemoryStore(memory)
	ownerTool := newDianaRelationshipTool(runtime, MessageEvent{UserID: "10001"})

	raw, err := ownerTool.Run(context.Background(), map[string]any{"operation": "set", "target_user_id": "10002", "value": 80})
	if err != nil {
		t.Fatal(err)
	}
	var result dianaRelationshipResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if result.Target == nil || result.Target.Favorability != 80 || result.Target.MessageCount != 18 || memory.profiles["10002"].MessageCount != 18 {
		t.Fatalf("set result=%#v profile=%#v", result, memory.profiles["10002"])
	}
	raw, err = ownerTool.Run(context.Background(), map[string]any{"operation": "adjust", "target_user_id": "10002", "delta": -10})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if result.Target == nil || result.Target.Favorability != 70 || result.Target.MessageCount != 18 {
		t.Fatalf("adjust result=%#v", result)
	}

	_, err = newDianaRelationshipTool(runtime, MessageEvent{UserID: "10002"}).Run(context.Background(), map[string]any{"operation": "set", "target_user_id": "10003", "value": 100})
	if err == nil || !strings.Contains(err.Error(), "只有主人") {
		t.Fatalf("non-owner error=%v", err)
	}
	_, err = ownerTool.Run(context.Background(), map[string]any{"operation": "set", "target_user_id": "10001", "value": 0})
	if err == nil || !strings.Contains(err.Error(), "不能修改主人") {
		t.Fatalf("owner self error=%v", err)
	}
}

func TestRuntimeAgentQueriesMentionedUsersRelationship(t *testing.T) {
	memory := newMemoryUserMemoryStore()
	memory.profiles["10005"] = UserMemoryProfile{
		UserID:       "10005",
		DisplayName:  "Alice",
		Favorability: 5,
		MessageCount: 18,
	}
	channel := &recordingChannel{apiResponses: map[string]map[string]any{
		"get_group_member_info": {
			"group_id": 20002,
			"user_id":  10005,
			"nickname": "Alice",
		},
	}}
	provider := &sequenceLLMProvider{replies: []string{
		`{"action":"none","prompt":""}`,
		`{"action":"tool","tool":"diana.relationship","input":{"operation":"get"}}`,
		`{"action":"final","content":"[CQ:at,qq=10005] 当前好感度是 5，关系等级是初识，互动 18 次。当前权限：基础聊天、媒体理解、网页搜索和 1 个提醒或订阅额度。"}`,
	}}
	runtime := NewRuntime(BotConfig{
		OwnerID:       "10001",
		BotQQ:         "10000",
		AgentEnabled:  true,
		AgentWorkDir:  t.TempDir(),
		AgentMaxSteps: 3,
	}, channel, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	runtime.SetUserMemoryStore(memory)
	event := MessageEvent{
		Kind:      EventKindGroup,
		SelfID:    "10000",
		UserID:    "10001",
		GroupID:   "20002",
		MessageID: "30004",
		Segments: []MessageSegment{
			{Type: "text", Data: map[string]string{"text": "嘉然看下"}},
			{Type: "at", Data: map[string]string{"qq": "10005"}},
			{Type: "text", Data: map[string]string{"text": " 的好感度"}},
		},
	}

	reply, err := runtime.replyTo(context.Background(), event, PlainText(event.Segments))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reply, "[CQ:at,qq=10005]") || !strings.Contains(reply, "好感度是 5") || !strings.Contains(reply, "当前权限") {
		t.Fatalf("reply = %q", reply)
	}
	if len(provider.requests) != 3 || !requestMessagesContain(provider.requests[2].Messages, `"favorability": 5`) {
		t.Fatalf("requests = %#v", provider.requests)
	}
	if !requestMessagesContain(provider.requests[1].Messages, "必须调用 diana.relationship") || !requestMessagesContain(provider.requests[1].Messages, "最终回复必须同时说明") {
		t.Fatalf("relationship guidance missing: %#v", provider.requests[1].Messages)
	}
}
