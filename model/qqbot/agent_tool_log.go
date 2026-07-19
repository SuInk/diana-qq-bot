package qqbot

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"diana-qq-bot/model/agent"
	"diana-qq-bot/model/applog"
)

func (r *Runtime) agentRunObserver(event MessageEvent) agent.RunObserver {
	return func(_ context.Context, runEvent agent.RunEvent) {
		writer := r.appLogWriter()
		if writer == nil {
			return
		}
		kind := applog.KindOperation
		level := applog.LevelInfo
		action := "qqbot.agent_run"
		message := "Agent 运行状态已更新"
		switch runEvent.Phase {
		case agent.RunPhaseStarted:
			message = "Agent 运行开始"
		case agent.RunPhaseModelCompleted:
			action = "qqbot.agent_model"
			message = "Agent 模型轮次完成"
		case agent.RunPhaseProtocolRepair:
			action = "qqbot.agent_protocol"
			message = "Agent 协议已自动修正"
		case agent.RunPhaseToolStarted:
			action = "qqbot.agent_tool"
			message = "Agent 工具调用开始"
		case agent.RunPhaseToolCompleted:
			action = "qqbot.agent_tool"
			message = "Agent 工具调用完成"
			if runEvent.Error != "" {
				kind = applog.KindError
				level = applog.LevelError
				message = "Agent 工具调用失败"
			}
		case agent.RunPhaseCompleted:
			message = "Agent 运行完成"
		case agent.RunPhaseFailed:
			kind = applog.KindError
			level = applog.LevelError
			message = "Agent 运行失败"
		}
		progressBar, progressLabel, progressCurrent, progressTotal, progressPercent := formatAgentProgress(runEvent)
		message = message + " " + progressBar + " " + progressLabel
		target := strings.TrimSpace(event.MessageID)
		if strings.TrimSpace(runEvent.Tool) != "" {
			target = runEvent.Tool
		}
		logCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = writer.AppendLog(logCtx, applog.Entry{
			Kind:    kind,
			Level:   level,
			Action:  action,
			Message: message,
			Detail:  runEvent.Error,
			Actor:   qqEventActor(event),
			Target:  target,
			Metadata: map[string]any{
				"trace_id":         runEvent.TraceID,
				"phase":            runEvent.Phase,
				"group_id":         event.GroupID,
				"user_id":          event.UserID,
				"message_id":       event.MessageID,
				"model_turn":       runEvent.ModelTurn,
				"tool_call":        runEvent.ToolCall,
				"max_tool_calls":   runEvent.MaxToolCalls,
				"tool":             runEvent.Tool,
				"input_keys":       runEvent.InputKeys,
				"output_chars":     runEvent.OutputChars,
				"duration_ms":      runEvent.DurationMS,
				"finish_reason":    runEvent.FinishReason,
				"input_tokens":     runEvent.Usage.InputTokens,
				"output_tokens":    runEvent.Usage.OutputTokens,
				"total_tokens":     runEvent.Usage.TotalTokens,
				"progress_bar":     progressBar,
				"progress_current": progressCurrent,
				"progress_total":   progressTotal,
				"progress_percent": progressPercent,
			},
		})
		log.Printf("qqbot agent progress: trace=%s %s %s phase=%s model_turn=%d tool=%s duration_ms=%d",
			runEvent.TraceID, progressBar, progressLabel, runEvent.Phase, runEvent.ModelTurn, runEvent.Tool, runEvent.DurationMS)
	}
}

func formatAgentProgress(event agent.RunEvent) (bar, label string, current, total, percent int) {
	total = event.MaxToolCalls
	if total <= 0 {
		total = agent.DefaultMaxSteps
	}
	current = min(max(event.ToolCall, 0), total)
	if event.Phase == agent.RunPhaseCompleted {
		current = total
	}
	percent = current * 100 / total
	const width = 8
	filled := current * width / total
	if current > 0 && filled == 0 {
		filled = 1
	}
	if event.Phase == agent.RunPhaseCompleted {
		filled = width
		percent = 100
	}
	bar = "[" + strings.Repeat("#", filled) + strings.Repeat("-", width-filled) + "]"
	switch event.Phase {
	case agent.RunPhaseCompleted:
		label = "done"
	case agent.RunPhaseFailed:
		label = fmt.Sprintf("failed %d/%d", current, total)
	default:
		label = fmt.Sprintf("%d/%d", current, total)
	}
	return bar, label, current, total, percent
}
