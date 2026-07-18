package qqbot

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"diana-qq-bot/model/applog"
	"diana-qq-bot/model/llm"
)

const (
	relationshipEvaluationMinConfidence     = 0.75
	naturalInteractionFavorabilityThreshold = 20
)

type relationshipEvaluationDecision struct {
	ShouldUpdate bool    `json:"should_update"`
	Delta        int     `json:"delta"`
	Confidence   float64 `json:"confidence"`
	Reason       string  `json:"reason"`
}

func (decision relationshipEvaluationDecision) effectiveDelta() int {
	if !decision.ShouldUpdate || decision.Confidence < relationshipEvaluationMinConfidence {
		return 0
	}
	return decision.Delta
}

type relationshipEvaluationPayload struct {
	Message                       passiveReplyPayload `json:"message"`
	CurrentScore                  int                 `json:"current_score"`
	CurrentTier                   string              `json:"current_tier"`
	MessageCount                  int                 `json:"message_count"`
	NaturalInteractionGainEnabled bool                `json:"natural_interaction_gain_enabled"`
	NaturalInteractionThreshold   int                 `json:"natural_interaction_threshold"`
}

func (r *Runtime) evaluateRelationshipUpdate(ctx context.Context, event MessageEvent, text string, handled bool) (relationshipEvaluationDecision, UserMemoryProfile, bool) {
	if !handled || !r.relationshipEvaluationAvailable(event) {
		return relationshipEvaluationDecision{}, UserMemoryProfile{}, false
	}
	profile, _ := r.loadUserMemoryProfile(ctx, event)
	policy := RelationshipPolicyFor(profile, r.effectiveConfigForEvent(event).OwnerID, event.UserID)
	payload := relationshipEvaluationPayload{
		Message:                       r.passiveReplyPayload(event, r.cleanInput(event, text)),
		CurrentScore:                  profile.Favorability,
		CurrentTier:                   policy.Name,
		MessageCount:                  profile.MessageCount,
		NaturalInteractionGainEnabled: profile.Favorability < naturalInteractionFavorabilityThreshold,
		NaturalInteractionThreshold:   naturalInteractionFavorabilityThreshold,
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		r.recordRelationshipEvaluationError(ctx, event, err)
		return relationshipEvaluationDecision{}, profile, false
	}
	messages := []llm.Message{
		{
			Role: llm.RoleSystem,
			Content: strings.TrimSpace(`你是 QQ 机器人 Diana/嘉然的关系变化评估器。请判断当前发言是否对“当前发言者与机器人之间的关系”产生了真实、明确的变化。

必须遵守：
1. 必须理解整句话、引用对象和最近对话，不得按关键词、子串、前缀或正则机械加减分。
2. 查询关系状态、权限或功能，要求设置分数，讨论关系计分规则，复述或引用别人的话，提到褒义或贬义表达但并非在表达对机器人的态度，都必须 should_update=false、delta=0。
3. 当 natural_interaction_gain_enabled=true 时，当前仍处于自然熟悉阶段。一次真实、有内容且面向机器人的普通闲聊、提问或任务互动，默认应 should_update=true、delta=1，表示相处带来的轻微熟悉；不能仅以“普通提问”“功能请求”或“任务指令”为理由判为 0。纯 @、只有称呼、无实质内容、重复或近似重复消息、刷屏、自动回复、故障反馈，以及明显只为刷分的互动仍为 0。必须理解语义判断，不得用关键词计分。
4. 当 natural_interaction_gain_enabled=false 时，普通提问、任务请求、唤醒和闲聊默认 delta=0，不能因为 @ 机器人或机器人会回复就加分。
5. 无论是否处于自然熟悉阶段，当前发言者对机器人表达清晰且有上下文支撑的善意、感谢、信任、关心或持续亲近时可以加分；明确针对机器人的轻视、攻击、骚扰、威胁或恶意时应减分。
6. 玩笑、昵称和亲密调侃必须结合双方最近语境判断；拿不准时不更新。混合表达要按整体含义判断，严重威胁不能因同时出现亲密表达而加分。
7. delta 只能是 -3、-2、-1、0、1、2、3。自然熟悉阶段的普通互动只能用 1；其他轻微变化用 1，明确变化用 2，极强且罕见的变化用 3。confidence 是对关系变化判断的置信度，范围 0 到 1。
8. 只输出一个合法 JSON 对象，不要输出 Markdown 或额外文字。格式固定为：{"should_update":false,"delta":0,"confidence":0.96,"reason":"中性查询，不改变关系"}`),
		},
		{
			Role:    llm.RoleUser,
			Content: "请评估这条消息是否改变当前发言者与机器人的关系。上下文 JSON：\n" + string(payloadJSON),
		},
	}
	callCtx, cancel := context.WithTimeout(ctx, relationshipEvaluationTimeout(r.effectiveConfigForEvent(event)))
	defer cancel()
	raw, err := r.runLLMRouterProvider(callCtx, func(client LLMProvider) (string, error) {
		resp, err := client.Generate(callCtx, llm.GenerateRequest{Messages: messages})
		if err != nil {
			return "", err
		}
		return resp.Text, nil
	})
	if err != nil {
		r.recordRelationshipEvaluationError(ctx, event, err)
		return relationshipEvaluationDecision{}, profile, false
	}
	decision, ok := parseRelationshipEvaluationDecision(raw)
	if !ok {
		r.recordRelationshipEvaluationError(ctx, event, fmt.Errorf("invalid relationship evaluation response"))
		return relationshipEvaluationDecision{}, profile, false
	}
	return decision, profile, true
}

func (r *Runtime) relationshipEvaluationAvailable(event MessageEvent) bool {
	cfg := r.effectiveConfigForEvent(event)
	if strings.TrimSpace(cfg.OwnerID) != "" && strings.TrimSpace(cfg.OwnerID) == strings.TrimSpace(event.UserID) {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.userMemory != nil && (r.llmFactory != nil || (r.llmCfgFactory != nil && r.llmStore != nil))
}

func relationshipEvaluationTimeout(cfg BotConfig) time.Duration {
	if cfg.RequestTimeout > 0 && cfg.RequestTimeout < 20*time.Second {
		return cfg.RequestTimeout
	}
	return 20 * time.Second
}

func parseRelationshipEvaluationDecision(raw string) (relationshipEvaluationDecision, bool) {
	raw = strings.TrimSpace(stripJSONCodeFence(raw))
	start, end := strings.Index(raw, "{"), strings.LastIndex(raw, "}")
	if start < 0 || end < start {
		return relationshipEvaluationDecision{}, false
	}
	var payload struct {
		ShouldUpdate *bool    `json:"should_update"`
		Delta        *int     `json:"delta"`
		Confidence   *float64 `json:"confidence"`
		Reason       *string  `json:"reason"`
	}
	if err := json.Unmarshal([]byte(raw[start:end+1]), &payload); err != nil || payload.ShouldUpdate == nil || payload.Delta == nil || payload.Confidence == nil || payload.Reason == nil {
		return relationshipEvaluationDecision{}, false
	}
	decision := relationshipEvaluationDecision{
		ShouldUpdate: *payload.ShouldUpdate,
		Delta:        *payload.Delta,
		Confidence:   *payload.Confidence,
		Reason:       strings.TrimSpace(*payload.Reason),
	}
	if decision.Delta < -3 || decision.Delta > 3 || decision.Confidence < 0 || decision.Confidence > 1 {
		return relationshipEvaluationDecision{}, false
	}
	if !decision.ShouldUpdate {
		decision.Delta = 0
	}
	return decision, true
}

func (r *Runtime) recordRelationshipEvaluation(ctx context.Context, event MessageEvent, before UserMemoryProfile, after UserMemoryProfile, decision relationshipEvaluationDecision) {
	writer := r.appLogWriter()
	if writer == nil {
		return
	}
	_ = writer.AppendLog(ctx, applog.Entry{
		Kind:    applog.KindOperation,
		Level:   applog.LevelInfo,
		Action:  "qqbot.relationship_evaluation",
		Message: "LLM 已完成关系变化评估",
		Actor:   qqEventActor(event),
		Target:  event.MessageID,
		Metadata: map[string]any{
			"group_id":      event.GroupID,
			"user_id":       event.UserID,
			"before_score":  before.Favorability,
			"after_score":   after.Favorability,
			"delta":         decision.effectiveDelta(),
			"confidence":    decision.Confidence,
			"should_update": decision.ShouldUpdate,
			"reason":        truncateRunesFromStart(decision.Reason, 240),
		},
	})
}

func (r *Runtime) recordRelationshipEvaluationError(ctx context.Context, event MessageEvent, err error) {
	writer := r.appLogWriter()
	if writer == nil || err == nil {
		return
	}
	_ = writer.AppendLog(ctx, applog.Entry{
		Kind:    applog.KindError,
		Level:   applog.LevelError,
		Action:  "qqbot.relationship_evaluation",
		Message: "关系变化语义评估失败，本条不改变好感度",
		Detail:  err.Error(),
		Actor:   qqEventActor(event),
		Target:  event.MessageID,
		Metadata: map[string]any{
			"group_id": event.GroupID,
			"user_id":  event.UserID,
		},
	})
}
