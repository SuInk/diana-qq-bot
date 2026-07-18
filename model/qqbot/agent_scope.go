package qqbot

import (
	"context"
	"strings"

	"diana-qq-bot/model/agent"
	"diana-qq-bot/model/applog"
)

const agentScopeContextRadius = 1

type agentReplyScope struct {
	Routed                bool
	ToolNames             []string
	ContextMessageIDs     []string
	KeepContextSummary    bool
	KeepContextSummarySet bool
}

func (r *Runtime) newAgentRegistry(ctx context.Context, cfg BotConfig, event MessageEvent, relationship RelationshipPolicy, extraTools ...agent.Tool) (*agent.ToolRegistry, error) {
	agentCfg := agent.Config{
		WorkDir:          cfg.AgentWorkDir,
		MaxSteps:         cfg.AgentMaxSteps,
		SkillRoots:       cfg.AgentSkillRoots,
		MCPConfigPath:    cfg.AgentMCPConfigPath,
		CommandAllowlist: cfg.AgentCommandAllowlist,
		CommandTimeoutMS: cfg.AgentCommandTimeoutMS,
		BrowserCDPURL:    cfg.AgentBrowserCDPURL,
		BrowserTimeoutMS: cfg.AgentBrowserTimeoutMS,
	}
	var registry *agent.ToolRegistry
	var err error
	if relationship.Owner {
		registry, err = agent.NewAgentToolRegistry(ctx, agentCfg)
	} else {
		registry, err = agent.NewDefaultToolRegistry(agentCfg)
	}
	if err != nil {
		return nil, err
	}
	if relationship.Owner {
		registry.Register(newDianaConfigTool(r))
	}
	for _, tool := range extraTools {
		registry.Register(tool)
	}
	registry.Retain(relationship.allowedAgentToolNames())
	return registry, nil
}

func (scope agentReplyScope) toolSet() map[string]bool {
	if !scope.Routed {
		return nil
	}
	selected := make(map[string]bool, len(scope.ToolNames))
	for _, name := range scope.ToolNames {
		if name = strings.TrimSpace(name); name != "" {
			selected[name] = true
		}
	}
	return selected
}

func withoutAgentTool(names []string, excluded string) []string {
	excluded = strings.TrimSpace(excluded)
	filtered := make([]string, 0, len(names))
	for _, name := range names {
		if strings.TrimSpace(name) != excluded {
			filtered = append(filtered, name)
		}
	}
	return filtered
}

func filterAgentReplyHistory(history []MessageEvent, event MessageEvent, scope agentReplyScope) []MessageEvent {
	if !scope.Routed {
		return history
	}
	wanted := map[string]bool{}
	add := func(messageID string) {
		if messageID = strings.TrimSpace(messageID); messageID != "" {
			wanted[messageID] = true
		}
	}
	for _, messageID := range scope.ContextMessageIDs {
		add(messageID)
	}
	add(event.SemanticSourceMessageID)
	for _, messageID := range replyReferenceIDs(event.Segments) {
		add(messageID)
	}
	if event.Quoted != nil {
		add(event.Quoted.MessageID)
		add(event.Quoted.SemanticSourceMessageID)
		for _, messageID := range replyReferenceIDs(event.Quoted.Segments) {
			add(messageID)
		}
	}
	if len(wanted) == 0 {
		return nil
	}

	include := map[int]bool{}
	for index, item := range history {
		if !wanted[strings.TrimSpace(item.MessageID)] {
			continue
		}
		left := index - agentScopeContextRadius
		if left < 0 {
			left = 0
		}
		right := index + agentScopeContextRadius
		if right >= len(history) {
			right = len(history) - 1
		}
		for nearby := left; nearby <= right; nearby++ {
			include[nearby] = true
		}
	}
	filtered := make([]MessageEvent, 0, len(include))
	for index, item := range history {
		if include[index] {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func (r *Runtime) recordAgentScope(ctx context.Context, event MessageEvent, scope agentReplyScope, toolsBefore, contextBefore, contextAfter int) {
	writer := r.appLogWriter()
	if writer == nil || !scope.Routed {
		return
	}
	_ = writer.AppendLog(ctx, applog.Entry{
		Kind:    applog.KindOperation,
		Level:   applog.LevelInfo,
		Action:  "qqbot.agent_scope",
		Message: "LLM 已选择本轮上下文和工具",
		Actor:   qqEventActor(event),
		Target:  event.MessageID,
		Metadata: map[string]any{
			"group_id":           event.GroupID,
			"user_id":            event.UserID,
			"selected_tools":     append([]string(nil), scope.ToolNames...),
			"tools_before":       toolsBefore,
			"tools_after":        len(scope.ToolNames),
			"context_before":     contextBefore,
			"context_after":      contextAfter,
			"keep_older_summary": scope.KeepContextSummary,
		},
	})
}
