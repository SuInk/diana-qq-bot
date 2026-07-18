package qqbot

import (
	"context"
	"sort"
	"time"

	"diana-qq-bot/model/agent"
	"diana-qq-bot/model/applog"
)

func (r *Runtime) recordAgentToolSteps(event MessageEvent, steps []agent.Step) {
	writer := r.appLogWriter()
	if writer == nil || len(steps) == 0 {
		return
	}
	for _, step := range steps {
		kind := applog.KindOperation
		level := applog.LevelInfo
		message := "Agent 工具调用完成"
		if step.Error != "" {
			kind = applog.KindError
			level = applog.LevelError
			message = "Agent 工具调用失败"
		}
		keys := make([]string, 0, len(step.Input))
		for key := range step.Input {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		logCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = writer.AppendLog(logCtx, applog.Entry{
			Kind:    kind,
			Level:   level,
			Action:  "qqbot.agent_tool",
			Message: message,
			Detail:  step.Error,
			Actor:   qqEventActor(event),
			Target:  step.Tool,
			Metadata: map[string]any{
				"group_id":     event.GroupID,
				"user_id":      event.UserID,
				"input_keys":   keys,
				"output_chars": len([]rune(step.Output)),
			},
		})
		cancel()
	}
}
