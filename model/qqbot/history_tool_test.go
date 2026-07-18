package qqbot

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"diana-qq-bot/model/llm"
)

func TestDianaChatHistoryToolReadsAroundQuotedMessageBeyondShortContext(t *testing.T) {
	runtime := NewRuntime(BotConfig{RecentContextLimit: 3, ContextSummaryThreshold: 3}, nilChannel{}, NewPluginManager(), nil, nil, nil, nil)
	store := newSemanticTimelineStore()
	runtime.SetMessageHistoryStore(store)
	for _, event := range []MessageEvent{
		{
			Kind:       EventKindGroup,
			Time:       100,
			GroupID:    "group-1",
			UserID:     "milk",
			SenderName: "Alice",
			MessageID:  "settings-image",
			Segments:   []MessageSegment{{Type: "image", Data: map[string]string{"cached_file": "/tmp/settings.png"}}},
		},
		chatHistoryTextEvent(101, "alice", "Alice", "settings-text", "项目版本可以在设置页查看"),
		chatHistoryTextEvent(102, "bob", "Bob", "quoted", "这个也能查啊"),
		chatHistoryTextEvent(103, "alice", "Alice", "after", "可以"),
	} {
		runtime.remember(event)
	}
	for index := 0; index < 8; index++ {
		runtime.remember(chatHistoryTextEvent(int64(110+index), "other", "其他人", fmt.Sprintf("filler-%d", index), "后续聊天"))
	}
	if history := runtime.contextHistory(MessageEvent{Kind: EventKindGroup, GroupID: "group-1"}); semanticHistoryContainsMessage(history, "quoted") {
		t.Fatal("quoted message unexpectedly remained in short context")
	}

	tool := newDianaChatHistoryTool(runtime, MessageEvent{
		Kind:    EventKindGroup,
		Time:    200,
		GroupID: "group-1",
		UserID:  "owner",
		Quoted: &QuotedMessage{
			MessageID:  "quoted",
			SenderName: "Bob",
			Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "这个也能查啊"}}},
		},
	})
	raw, err := tool.Run(context.Background(), map[string]any{"operation": "around", "before": 3, "after": 1})
	if err != nil {
		t.Fatal(err)
	}
	var result dianaChatHistoryResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if result.AnchorMessageID != "quoted" || len(result.Items) != 4 {
		t.Fatalf("result = %#v", result)
	}
	if result.Items[0].MessageID != "settings-image" || result.Items[0].ImageCount != 1 || result.Items[1].MessageID != "settings-text" || result.Items[1].Text != "项目版本可以在设置页查看" || result.Items[2].MessageID != "quoted" {
		t.Fatalf("items = %#v", result.Items)
	}
}

func TestDianaChatHistoryToolEnforcesBounds(t *testing.T) {
	if got := chatHistoryBoundedInt(map[string]any{"before": 999}, "before", 4, maximumChatHistoryAroundRadius); got != maximumChatHistoryAroundRadius {
		t.Fatalf("before = %d", got)
	}
	if got := chatHistoryPositiveInt(map[string]any{"hours": 999}, "hours", defaultChatHistorySearchHours, maximumChatHistorySearchHours); got != maximumChatHistorySearchHours {
		t.Fatalf("hours = %d", got)
	}
	if got := chatHistoryPositiveInt(map[string]any{"limit": 999}, "limit", defaultChatHistoryRecentLimit, maximumChatHistoryResultLimit); got != maximumChatHistoryResultLimit {
		t.Fatalf("limit = %d", got)
	}
	result := dianaChatHistoryResult{OK: true, Action: "search", Total: maximumChatHistoryResultLimit}
	for index := 0; index < maximumChatHistoryResultLimit; index++ {
		result.Items = append(result.Items, dianaChatHistoryItem{MessageID: fmt.Sprintf("message-%d", index), Sender: "测试用户", Text: strings.Repeat("很长的历史消息", 80)})
	}
	raw, err := marshalDianaChatHistoryResult(result)
	if err != nil {
		t.Fatal(err)
	}
	if len([]rune(raw)) > maximumChatHistoryOutputRunes {
		t.Fatalf("output runes = %d", len([]rune(raw)))
	}
	var bounded dianaChatHistoryResult
	if err := json.Unmarshal([]byte(raw), &bounded); err != nil {
		t.Fatalf("bounded JSON is invalid: %v", err)
	}
	if !bounded.Limited || len(bounded.Items) >= maximumChatHistoryResultLimit {
		t.Fatalf("bounded result = %#v", bounded)
	}
}

func TestRuntimeAgentCanQueryHistoryAroundCurrentQuote(t *testing.T) {
	provider := &privacyAwareTestProvider{}
	provider.generate = func(call int, req llm.GenerateRequest) (string, error) {
		switch call {
		case 1:
			// The scope router may omit the tool. A direct quote whose target fell
			// outside the short context must still leave history lookup available.
			return `{"action":"none","tools":[],"context_message_ids":[],"keep_older_summary":false}`, nil
		case 2:
			if !requestMessagesContain(req.Messages, dianaChatHistoryToolName) {
				return "", fmt.Errorf("history tool missing from Agent prompt")
			}
			return `{"action":"tool","tool":"diana.chat_history","input":{"operation":"around","before":3,"after":1}}`, nil
		case 3:
			if !requestMessagesContain(req.Messages, "项目版本可以在设置页查看") {
				return "", fmt.Errorf("history tool result missing from Agent follow-up")
			}
			return `{"action":"final","content":"这里的“这个”指项目版本也可以在设置页查看。"}`, nil
		default:
			return "", fmt.Errorf("unexpected LLM call %d", call)
		}
	}
	runtime := NewRuntime(BotConfig{
		BotQQ:                   "10000",
		RecentContextLimit:      3,
		ContextSummaryThreshold: 3,
		AgentEnabled:            true,
		AgentWorkDir:            t.TempDir(),
		AgentMaxSteps:           3,
	}, &recordingChannel{}, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return provider, nil
	})
	store := newSemanticTimelineStore()
	runtime.SetMessageHistoryStore(store)
	for _, event := range []MessageEvent{
		chatHistoryTextEvent(100, "alice", "Alice", "context", "项目版本可以在设置页查看"),
		chatHistoryTextEvent(101, "bob", "Bob", "quoted", "这个也能查啊"),
	} {
		runtime.remember(event)
	}
	for index := 0; index < 8; index++ {
		runtime.remember(chatHistoryTextEvent(int64(110+index), "other", "其他人", fmt.Sprintf("filler-%d", index), "后续聊天"))
	}
	event := MessageEvent{
		Kind:       EventKindGroup,
		Time:       200,
		SelfID:     "10000",
		GroupID:    "group-1",
		UserID:     "owner",
		SenderName: "TestOwner",
		MessageID:  "question",
		RawMessage: "Diana，这里说的这个是什么",
		Segments:   []MessageSegment{{Type: "reply", Data: map[string]string{"id": "quoted"}}, {Type: "text", Data: map[string]string{"text": "Diana，这里说的这个是什么"}}},
		Quoted: &QuotedMessage{
			MessageID:  "quoted",
			UserID:     "bob",
			SenderName: "Bob",
			Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "这个也能查啊"}}},
		},
		ToMe: true,
	}
	reply, err := runtime.replyTo(context.Background(), event, event.RawMessage)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reply, "项目版本") || len(provider.requests) != 3 {
		t.Fatalf("reply = %q requests = %d", reply, len(provider.requests))
	}
}

func chatHistoryTextEvent(eventTime int64, userID, sender, messageID, text string) MessageEvent {
	return MessageEvent{
		Kind:       EventKindGroup,
		Time:       eventTime,
		GroupID:    "group-1",
		UserID:     userID,
		SenderName: sender,
		MessageID:  messageID,
		Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": text}}},
	}
}
