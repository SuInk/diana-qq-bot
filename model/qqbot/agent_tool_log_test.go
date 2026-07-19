package qqbot

import (
	"context"
	"testing"

	"diana-qq-bot/model/agent"
)

func TestAgentRunObserverWritesCorrelatedLifecycleLogs(t *testing.T) {
	logs := &captureAppLogs{}
	runtime := &Runtime{}
	runtime.SetAppLogWriter(logs)
	event := MessageEvent{
		Kind:      EventKindGroup,
		GroupID:   "20001",
		UserID:    "10001",
		MessageID: "30001",
	}
	observe := runtime.agentRunObserver(event)
	observe(context.Background(), agent.RunEvent{
		TraceID:      "trace-1",
		Phase:        agent.RunPhaseToolStarted,
		ModelTurn:    2,
		ToolCall:     1,
		MaxToolCalls: 8,
		Tool:         "demo.tool",
		InputKeys:    []string{"query"},
	})
	observe(context.Background(), agent.RunEvent{
		TraceID:      "trace-1",
		Phase:        agent.RunPhaseCompleted,
		ModelTurn:    3,
		ToolCall:     1,
		MaxToolCalls: 8,
		DurationMS:   42,
		FinishReason: "final",
	})

	if len(logs.entries) != 2 {
		t.Fatalf("entries = %#v", logs.entries)
	}
	started := logs.entries[0]
	if started.Action != "qqbot.agent_tool" || started.Message != "Agent 工具调用开始 [#-------] 1/8" || started.Target != "demo.tool" {
		t.Fatalf("tool log = %#v", started)
	}
	if started.Metadata["trace_id"] != "trace-1" || started.Metadata["message_id"] != "30001" || started.Metadata["tool_call"] != 1 || started.Metadata["progress_percent"] != 12 {
		t.Fatalf("tool metadata = %#v", started.Metadata)
	}
	completed := logs.entries[1]
	if completed.Action != "qqbot.agent_run" || completed.Message != "Agent 运行完成 [########] done" || completed.Metadata["finish_reason"] != "final" || completed.Metadata["progress_percent"] != 100 {
		t.Fatalf("completed log = %#v", completed)
	}
}
