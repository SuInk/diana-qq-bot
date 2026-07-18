package qqbot

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

const (
	defaultQQGroupMemberLimit = 50
	maximumQQGroupMemberLimit = 100
)

type dianaQQGroupTool struct {
	runtime *Runtime
	event   MessageEvent
}

type dianaQQGroupResult struct {
	OK           bool                     `json:"ok"`
	Action       string                   `json:"action"`
	Message      string                   `json:"message,omitempty"`
	Group        *QQGroupInfo             `json:"group,omitempty"`
	Members      []dianaQQGroupMemberItem `json:"members,omitempty"`
	ReplyPolicy  *dianaQQGroupReplyPolicy `json:"reply_policy,omitempty"`
	OperatorRole string                   `json:"operator_role,omitempty"`
	Total        int                      `json:"total,omitempty"`
	Limited      bool                     `json:"limited,omitempty"`
}

type dianaQQGroupReplyPolicy struct {
	PassiveReplyChance      float64 `json:"passive_reply_chance"`
	PassiveReplyThreshold   float64 `json:"passive_reply_threshold"`
	MinimumReplyMemberLevel int     `json:"minimum_reply_member_level"`
}

type dianaQQGroupMemberItem struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
	Nickname    string `json:"nickname,omitempty"`
	Card        string `json:"card,omitempty"`
	Role        string `json:"role,omitempty"`
	Title       string `json:"title,omitempty"`
	AvatarURL   string `json:"avatar_url,omitempty"`
	MentionCQ   string `json:"mention_cq"`
}

func newDianaQQGroupTool(runtime *Runtime, event MessageEvent) *dianaQQGroupTool {
	return &dianaQQGroupTool{runtime: runtime, event: event}
}

func (t *dianaQQGroupTool) Name() string {
	return "diana.qq_group"
}

func (t *dianaQQGroupTool) Description() string {
	return `读取当前 QQ 群的真实群信息、成员和回复策略。用户要求查群名、成员、群名片、昵称、QQ号、头像，或要求真正 @ 某位/多位/其他所有成员时必须调用；不要要求用户先手动 @。operation=info 读取群资料；operation=members 获取或检索成员；operation=reply_policy 读取本群插话概率、判断阈值和最低回复群等级；operation=set_reply_policy 修改这些设置。只有机器人主人、群主或群管理员可读取或修改 reply policy，工具会实时校验权限。set_reply_policy 支持局部更新，passive_reply_chance 范围 0.05~1，passive_reply_threshold 范围 0.5~1，minimum_reply_member_level 范围 0~1000；低于最低等级的成员仅在主动 @ 机器人时可回复。members 支持 query 按群名片/昵称/QQ号筛选，exclude_current_sender 排除当前发言者，exclude_user_ids 排除指定 QQ，limit 默认 50、最大 100。结果中的 mention_cq 可直接用于最终回复，提及多人时依次原样输出。input: {"operation":"info|members|reply_policy|set_reply_policy","query":"可选","exclude_current_sender":false,"exclude_user_ids":["QQ号"],"limit":50,"passive_reply_chance":0.5,"passive_reply_threshold":0.9,"minimum_reply_member_level":10}`
}

func (t *dianaQQGroupTool) Run(ctx context.Context, input map[string]any) (string, error) {
	if t == nil || t.runtime == nil {
		return "", fmt.Errorf("diana qq group: runtime is not configured")
	}
	if t.event.Kind != EventKindGroup || strings.TrimSpace(t.event.GroupID) == "" {
		return "", fmt.Errorf("群信息工具只能在 QQ 群聊中使用")
	}
	operation := strings.ToLower(strings.TrimSpace(configToolString(input, "operation")))
	if operation == "" {
		operation = "members"
	}
	switch operation {
	case "info", "group":
		group, err := t.runtime.GetGroupInfo(ctx, t.event.GroupID)
		if err != nil {
			return "", fmt.Errorf("读取群信息失败: %w", err)
		}
		return marshalDianaQQGroupResult(dianaQQGroupResult{
			OK:      true,
			Action:  "info",
			Message: "已从 NapCat 读取当前群资料。",
			Group:   &group,
		})
	case "members", "list", "search", "resolve":
		return t.listMembers(ctx, input)
	case "reply_policy", "policy":
		return t.replyPolicy(ctx, input, false)
	case "set_reply_policy", "update_reply_policy":
		return t.replyPolicy(ctx, input, true)
	default:
		return "", fmt.Errorf("operation 必须是 info、members、reply_policy 或 set_reply_policy")
	}
}

func (t *dianaQQGroupTool) replyPolicy(ctx context.Context, input map[string]any, update bool) (string, error) {
	role, err := t.runtime.canConfigureGroup(ctx, t.event)
	if err != nil {
		return "", err
	}
	cfg, ok := t.runtime.groupConfigForEvent(t.event)
	if !ok {
		cfg = DefaultGroupConfig(t.event.GroupID, t.runtime.effectiveConfigForEvent(t.event))
	}
	if !update {
		policy := dianaQQGroupReplyPolicyFromConfig(cfg)
		return marshalDianaQQGroupResult(dianaQQGroupResult{
			OK:           true,
			Action:       "reply_policy",
			Message:      "已读取本群回复策略。",
			ReplyPolicy:  &policy,
			OperatorRole: role,
		})
	}

	changed := false
	if chance, present, err := groupToolFloat(input, "passive_reply_chance"); err != nil {
		return "", err
	} else if present {
		if chance < 0.05 || chance > 1 {
			return "", fmt.Errorf("passive_reply_chance 必须在 0.05 到 1 之间")
		}
		cfg.PassiveReplyChance = chance
		changed = true
	}
	if threshold, present, err := groupToolFloat(input, "passive_reply_threshold"); err != nil {
		return "", err
	} else if present {
		if threshold < 0.5 || threshold > 1 {
			return "", fmt.Errorf("passive_reply_threshold 必须在 0.5 到 1 之间")
		}
		cfg.PassiveReplyThreshold = threshold
		changed = true
	}
	if value, present := input["minimum_reply_member_level"]; present {
		level, err := groupToolInteger(value)
		if err != nil || level < 0 || level > maximumReplyMemberLevel {
			return "", fmt.Errorf("minimum_reply_member_level 必须是 0 到 %d 的整数", maximumReplyMemberLevel)
		}
		cfg.MinimumReplyMemberLevel = level
		changed = true
	}
	if !changed {
		return "", fmt.Errorf("至少提供一项要修改的回复策略")
	}
	saved, err := t.runtime.saveGroupConfig(cfg)
	if err != nil {
		return "", err
	}
	t.runtime.cancelPassiveReplyBatch(t.event)
	saved = saved.WithDefaults(t.event.GroupID, t.runtime.effectiveConfigForEvent(t.event))
	t.runtime.recordGroupReplyPolicyChanged(ctx, t.event, role, saved)
	policy := dianaQQGroupReplyPolicyFromConfig(saved)
	return marshalDianaQQGroupResult(dianaQQGroupResult{
		OK:           true,
		Action:       "set_reply_policy",
		Message:      "已更新本群回复策略。",
		ReplyPolicy:  &policy,
		OperatorRole: role,
	})
}

func dianaQQGroupReplyPolicyFromConfig(cfg GroupConfig) dianaQQGroupReplyPolicy {
	return dianaQQGroupReplyPolicy{
		PassiveReplyChance:      cfg.PassiveReplyChance,
		PassiveReplyThreshold:   cfg.PassiveReplyThreshold,
		MinimumReplyMemberLevel: cfg.MinimumReplyMemberLevel,
	}
}

func (t *dianaQQGroupTool) listMembers(ctx context.Context, input map[string]any) (string, error) {
	members, err := t.runtime.GetGroupMemberList(ctx, t.event.GroupID)
	if err != nil {
		return "", fmt.Errorf("读取群成员列表失败: %w", err)
	}
	query := strings.ToLower(strings.TrimSpace(configToolString(input, "query")))
	excluded := make(map[string]bool)
	if groupToolBool(input, "exclude_current_sender") {
		excluded[strings.TrimSpace(t.event.UserID)] = true
	}
	for _, userID := range groupToolStringList(input["exclude_user_ids"]) {
		excluded[userID] = true
	}
	cfg := t.runtime.effectiveConfigForEvent(t.event)
	for _, userID := range []string{t.event.SelfID, cfg.BotQQ} {
		if userID = strings.TrimSpace(userID); userID != "" {
			excluded[userID] = true
		}
	}

	limit := groupToolLimit(input)
	items := make([]dianaQQGroupMemberItem, 0, min(limit, len(members)))
	matched := 0
	for _, member := range members {
		if member.UserID == "" || excluded[member.UserID] || !qqGroupMemberMatches(member, query) {
			continue
		}
		matched++
		if len(items) >= limit {
			continue
		}
		items = append(items, dianaQQGroupMemberItem{
			UserID:      member.UserID,
			DisplayName: member.DisplayName(),
			Nickname:    member.Nickname,
			Card:        member.Card,
			Role:        member.Role,
			Title:       member.Title,
			AvatarURL:   member.AvatarURL,
			MentionCQ:   "[CQ:at,qq=" + member.UserID + "]",
		})
	}
	return marshalDianaQQGroupResult(dianaQQGroupResult{
		OK:      true,
		Action:  "members",
		Message: fmt.Sprintf("已从 NapCat 读取当前群成员，匹配 %d 人，返回 %d 人。", matched, len(items)),
		Members: items,
		Total:   matched,
		Limited: matched > len(items),
	})
}

func qqGroupMemberMatches(member QQGroupMemberInfo, query string) bool {
	if query == "" {
		return true
	}
	for _, value := range []string{member.UserID, member.Card, member.Nickname, member.DisplayName()} {
		if strings.Contains(strings.ToLower(strings.TrimSpace(value)), query) {
			return true
		}
	}
	return false
}

func groupToolBool(input map[string]any, key string) bool {
	switch value := input[key].(type) {
	case bool:
		return value
	case string:
		parsed, _ := strconv.ParseBool(strings.TrimSpace(value))
		return parsed
	default:
		return false
	}
}

func groupToolFloat(input map[string]any, key string) (float64, bool, error) {
	value, present := input[key]
	if !present {
		return 0, false, nil
	}
	parsed, err := strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(value)), 64)
	if err != nil {
		return 0, true, fmt.Errorf("%s 必须是数字", key)
	}
	return parsed, true, nil
}

func groupToolInteger(value any) (int, error) {
	raw := strings.TrimSpace(fmt.Sprint(value))
	parsed, err := strconv.Atoi(raw)
	if err == nil {
		return parsed, nil
	}
	decimal, floatErr := strconv.ParseFloat(raw, 64)
	if floatErr != nil || decimal != float64(int(decimal)) {
		return 0, fmt.Errorf("not an integer")
	}
	return int(decimal), nil
}

func groupToolStringList(value any) []string {
	var raw []string
	switch items := value.(type) {
	case []any:
		for _, item := range items {
			raw = append(raw, stringFromAny(item))
		}
	case []string:
		raw = append(raw, items...)
	case string:
		raw = strings.FieldsFunc(items, func(r rune) bool { return r == ',' || r == '，' || r == ' ' })
	}
	var out []string
	for _, item := range raw {
		if userID := normalizeRelationshipUserID(item); userID != "" {
			out = appendUniqueStrings(out, userID)
		}
	}
	return out
}

func groupToolLimit(input map[string]any) int {
	limit := intFromAny(input["limit"])
	if limit <= 0 {
		limit = defaultQQGroupMemberLimit
	}
	if limit > maximumQQGroupMemberLimit {
		limit = maximumQQGroupMemberLimit
	}
	return limit
}

func marshalDianaQQGroupResult(result dianaQQGroupResult) (string, error) {
	body, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}
	return string(body), nil
}
