package qqbot

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"diana-qq-bot/model/applog"
)

const maximumReplyMemberLevel = 1000

type groupReplyLevelDecision struct {
	Minimum   int
	Level     int
	LevelSet  bool
	Role      string
	Reason    string
	LookupErr error
}

func normalizeQQGroupRole(role string) string {
	return strings.ToLower(strings.TrimSpace(role))
}

func qqGroupRoleCanConfigure(role string) bool {
	switch normalizeQQGroupRole(role) {
	case "owner", "admin":
		return true
	default:
		return false
	}
}

func parseQQGroupLevel(raw string) (int, bool) {
	raw = strings.TrimSpace(raw)
	if len(raw) >= 2 && strings.EqualFold(raw[:2], "lv") {
		raw = strings.TrimSpace(raw[2:])
	}
	if raw == "" {
		return 0, false
	}
	for _, char := range raw {
		if char < '0' || char > '9' {
			return 0, false
		}
	}
	level, err := strconv.Atoi(raw)
	if err != nil || level < 0 {
		return 0, false
	}
	return level, true
}

func eventDirectlyMentionsBot(event MessageEvent, cfg BotConfig) bool {
	if event.Kind != EventKindGroup {
		return false
	}
	if event.ToMe {
		return true
	}
	for _, botID := range []string{event.SelfID, cfg.BotQQ} {
		botID = strings.TrimSpace(botID)
		if botID == "" {
			continue
		}
		if hasAt(event.Segments, botID) || strings.Contains(event.RawMessage, "[CQ:at,qq="+botID+"]") {
			return true
		}
	}
	return false
}

func eventRepliesToBot(event MessageEvent, cfg BotConfig) bool {
	if event.Kind != EventKindGroup || event.Quoted == nil {
		return false
	}
	quotedUserID := strings.TrimSpace(event.Quoted.UserID)
	if quotedUserID == "" {
		return false
	}
	for _, botID := range []string{event.SelfID, cfg.BotQQ} {
		if botID = strings.TrimSpace(botID); botID != "" && quotedUserID == botID {
			return true
		}
	}
	return false
}

func (r *Runtime) canConfigureGroup(ctx context.Context, event MessageEvent) (string, error) {
	if event.Kind != EventKindGroup || strings.TrimSpace(event.GroupID) == "" || strings.TrimSpace(event.UserID) == "" {
		return "", fmt.Errorf("群配置只能在 QQ 群聊中修改")
	}
	cfg := r.effectiveConfigForEvent(event)
	if ownerID := strings.TrimSpace(cfg.OwnerID); ownerID != "" && ownerID == strings.TrimSpace(event.UserID) {
		return "bot_owner", nil
	}
	if role := normalizeQQGroupRole(event.SenderRole); qqGroupRoleCanConfigure(role) {
		return role, nil
	}
	member, err := r.GetGroupMemberInfo(ctx, event.GroupID, event.UserID)
	if err != nil {
		return "", fmt.Errorf("无法校验当前群权限: %w", err)
	}
	role := normalizeQQGroupRole(member.Role)
	if !qqGroupRoleCanConfigure(role) {
		return role, fmt.Errorf("只有机器人主人、群主或群管理员可以配置本群")
	}
	return role, nil
}

func (r *Runtime) shouldIgnoreGroupReplyByMemberLevel(ctx context.Context, event MessageEvent) (bool, groupReplyLevelDecision) {
	groupCfg, ok := r.groupConfigForEvent(event)
	if !ok || groupCfg.MinimumReplyMemberLevel <= 0 {
		return false, groupReplyLevelDecision{}
	}
	decision := groupReplyLevelDecision{Minimum: groupCfg.MinimumReplyMemberLevel}
	cfg := r.effectiveConfigForEvent(event)
	if eventDirectlyMentionsBot(event, cfg) {
		decision.Reason = "direct_mention"
		return false, decision
	}
	if ownerID := strings.TrimSpace(cfg.OwnerID); ownerID != "" && ownerID == strings.TrimSpace(event.UserID) {
		decision.Role = "bot_owner"
		decision.Reason = "privileged_role"
		return false, decision
	}

	decision.Role = normalizeQQGroupRole(event.SenderRole)
	decision.Level, decision.LevelSet = parseQQGroupLevel(event.SenderLevel)
	if qqGroupRoleCanConfigure(decision.Role) {
		decision.Reason = "privileged_role"
		return false, decision
	}
	if decision.Role == "" || !decision.LevelSet {
		member, err := r.GetGroupMemberInfo(ctx, event.GroupID, event.UserID)
		if err != nil {
			decision.LookupErr = err
			decision.Reason = "member_lookup_failed"
			return true, decision
		}
		decision.Role = normalizeQQGroupRole(member.Role)
		decision.Level, decision.LevelSet = parseQQGroupLevel(member.Level)
		if qqGroupRoleCanConfigure(decision.Role) {
			decision.Reason = "privileged_role"
			return false, decision
		}
	}
	if !decision.LevelSet {
		decision.Reason = "member_level_unavailable"
		return true, decision
	}
	if decision.Level < decision.Minimum {
		decision.Reason = "member_level_below_minimum"
		return true, decision
	}
	decision.Reason = "member_level_allowed"
	return false, decision
}

func (r *Runtime) saveGroupConfig(cfg GroupConfig) (GroupConfig, error) {
	r.mu.RLock()
	store := r.groupConfigs
	base := r.cfg
	r.mu.RUnlock()
	writer, ok := store.(GroupConfigWriter)
	if !ok || writer == nil {
		return GroupConfig{}, fmt.Errorf("当前未接入可写的群配置存储")
	}
	return writer.SaveGroupConfig(cfg, base)
}

func (r *Runtime) recordGroupReplyLevelIgnored(ctx context.Context, event MessageEvent, decision groupReplyLevelDecision) {
	writer := r.appLogWriter()
	if writer == nil {
		return
	}
	detail := ""
	if decision.LookupErr != nil {
		detail = decision.LookupErr.Error()
	}
	_ = writer.AppendLog(ctx, applog.Entry{
		Kind:    applog.KindOperation,
		Level:   applog.LevelInfo,
		Action:  "qqbot.group_reply_level_filter",
		Message: "成员未达到本群回复等级要求，已跳过回复判断",
		Detail:  detail,
		Actor:   qqEventActor(event),
		Target:  event.MessageID,
		Metadata: map[string]any{
			"group_id":      event.GroupID,
			"user_id":       event.UserID,
			"sender_role":   decision.Role,
			"sender_level":  decision.Level,
			"level_known":   decision.LevelSet,
			"minimum_level": decision.Minimum,
			"reason":        decision.Reason,
		},
	})
}

func (r *Runtime) recordGroupReplyPolicyChanged(ctx context.Context, event MessageEvent, role string, cfg GroupConfig) {
	writer := r.appLogWriter()
	if writer == nil {
		return
	}
	_ = writer.AppendLog(ctx, applog.Entry{
		Kind:    applog.KindOperation,
		Level:   applog.LevelInfo,
		Action:  "qqbot.group_reply_policy_config",
		Message: "群级回复策略已通过聊天更新",
		Actor:   qqEventActor(event),
		Target:  event.GroupID,
		Metadata: map[string]any{
			"group_id":                   event.GroupID,
			"user_id":                    event.UserID,
			"operator_role":              role,
			"passive_reply_chance":       cfg.PassiveReplyChance,
			"passive_reply_threshold":    cfg.PassiveReplyThreshold,
			"minimum_reply_member_level": cfg.MinimumReplyMemberLevel,
		},
	})
}
