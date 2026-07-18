package qqbot

import "testing"

// TestOneBotEventPayloadForGroup 验证对应功能场景。
func TestOneBotEventPayloadForGroup(t *testing.T) {
	event := MessageEvent{
		Kind:        EventKindGroup,
		Time:        123,
		SelfID:      "42",
		UserID:      "1001",
		GroupID:     "2002",
		MessageID:   "3003",
		MessageType: "group",
		RawMessage:  "hello",
		Segments:    []MessageSegment{{Type: "text", Data: map[string]string{"text": "hello"}}},
		SenderName:  "Alice",
	}

	payload := oneBotEventPayload(event)
	if payload["post_type"] != "message" || payload["message_type"] != "group" {
		t.Fatalf("payload = %#v", payload)
	}
	if payload["self_id"] != int64(42) || payload["group_id"] != int64(2002) {
		t.Fatalf("numeric ids not converted: %#v", payload)
	}
	if payload["raw_message"] != "hello" {
		t.Fatalf("raw_message = %#v", payload["raw_message"])
	}
}

// TestConfigFromPayloadKeepsNoneBotBridgeToken 验证对应功能场景。
func TestConfigFromPayloadKeepsNoneBotBridgeToken(t *testing.T) {
	got := ConfigFromPayload(ConfigPayload{
		Enabled:               true,
		NoneBotBridgeEnabled:  true,
		NoneBotBridgeEndpoint: "ws://127.0.0.1:8080/onebot/v11/ws",
	}, BotConfig{NoneBotBridgeToken: "old-token"})

	if got.NoneBotBridgeToken != "old-token" {
		t.Fatalf("NoneBotBridgeToken = %q", got.NoneBotBridgeToken)
	}
}

func TestConfigPayloadKeepsPassiveReplyChance(t *testing.T) {
	cfg := ConfigFromPayload(ConfigPayload{
		Enabled:               true,
		PassiveReplyChance:    0.4,
		PassiveReplyThreshold: 0.92,
	}, BotConfig{})
	if cfg.PassiveReplyChance != 0.4 {
		t.Fatalf("PassiveReplyChance = %v", cfg.PassiveReplyChance)
	}
	payload := PayloadFromConfig(cfg)
	if payload.PassiveReplyChance != 0.4 {
		t.Fatalf("payload PassiveReplyChance = %v", payload.PassiveReplyChance)
	}
	if cfg.PassiveReplyThreshold != 0.92 || payload.PassiveReplyThreshold != 0.92 {
		t.Fatalf("threshold cfg=%v payload=%v", cfg.PassiveReplyThreshold, payload.PassiveReplyThreshold)
	}
}

func TestConfigPayloadKeepsEditablePrompts(t *testing.T) {
	cfg := ConfigFromPayload(ConfigPayload{
		Enabled:                  true,
		SystemPrompt:             "custom system prompt",
		PassiveReplyRouterPrompt: "custom router prompt",
		PassiveReplyPrompt:       "custom passive reply prompt",
	}, BotConfig{})
	payload := PayloadFromConfig(cfg)

	if payload.SystemPrompt != "custom system prompt" {
		t.Fatalf("SystemPrompt = %q", payload.SystemPrompt)
	}
	if payload.PassiveReplyRouterPrompt != "custom router prompt" {
		t.Fatalf("PassiveReplyRouterPrompt = %q", payload.PassiveReplyRouterPrompt)
	}
	if payload.PassiveReplyPrompt != "custom passive reply prompt" {
		t.Fatalf("PassiveReplyPrompt = %q", payload.PassiveReplyPrompt)
	}
}
