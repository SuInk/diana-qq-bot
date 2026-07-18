package qqbot

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const (
	defaultRelationshipListLimit = 20
	maximumRelationshipListLimit = 50
)

type dianaRelationshipTool struct {
	runtime *Runtime
	event   MessageEvent
}

type dianaRelationshipResult struct {
	OK      bool                        `json:"ok"`
	Action  string                      `json:"action"`
	Message string                      `json:"message,omitempty"`
	Target  *dianaRelationshipSnapshot  `json:"target,omitempty"`
	Items   []dianaRelationshipSnapshot `json:"items,omitempty"`
}

type dianaRelationshipSnapshot struct {
	UserID           string           `json:"user_id"`
	DisplayName      string           `json:"display_name"`
	MentionCQ        string           `json:"mention_cq"`
	Favorability     int              `json:"favorability"`
	MessageCount     int              `json:"message_count"`
	RelationshipTier RelationshipTier `json:"relationship_tier"`
	RelationshipName string           `json:"relationship_name"`
	Permissions      []string         `json:"permissions"`
	ScheduleLimit    int              `json:"reminder_schedule_limit"`
	CanGenerateImage bool             `json:"can_generate_image"`
	CanEditImage     bool             `json:"can_edit_image"`
	CanDocumentOCR   bool             `json:"can_document_ocr"`
	Owner            bool             `json:"owner"`
	HasHistory       bool             `json:"has_history"`
}

func newDianaRelationshipTool(runtime *Runtime, event MessageEvent) *dianaRelationshipTool {
	return &dianaRelationshipTool{runtime: runtime, event: event}
}

func (t *dianaRelationshipTool) Name() string {
	return "diana.relationship"
}

func (t *dianaRelationshipTool) Description() string {
	return `查询 Diana 对 QQ 用户的好感度、关系等级、互动次数、当前权限和提醒/订阅额度。用户询问自己、被 @ 成员或指定群成员的好感度/关系/权限时必须调用本工具，不要根据“当前发言者”上下文猜测，也不要声称无法查询隐藏数据。最终回复必须简明列出结果中的 permissions 和 reminder_schedule_limit，不能只报好感度数字。operation=get 时 target_user_id 可省略：消息里有被 @ 成员就自动查询该成员，否则查询当前发言者；operation=list 查询当前群内已有互动记录的成员并按好感度排序，仅主人可用。主人还可使用 set 直接设置或 adjust 增减任意非主人用户的好感度，不增加互动次数：{"operation":"set","target_user_id":"QQ号","value":80} 或 {"operation":"adjust","target_user_id":"QQ号","delta":10}。若最终回复需要真正 @ 目标，请原样使用结果中的 mention_cq，不要写普通文本 @QQ号。`
}

func (t *dianaRelationshipTool) Run(ctx context.Context, input map[string]any) (string, error) {
	if t == nil || t.runtime == nil {
		return "", fmt.Errorf("diana relationship: runtime is not configured")
	}
	operation := strings.ToLower(strings.TrimSpace(configToolString(input, "operation")))
	if operation == "" {
		operation = "get"
	}
	switch operation {
	case "get", "show":
		targetID := normalizeRelationshipUserID(configToolString(input, "target_user_id"))
		if targetID == "" {
			targetID = t.defaultTargetUserID()
		}
		if targetID == "" {
			return "", fmt.Errorf("没有找到要查询的 QQ 用户")
		}
		member, err := t.resolveTargetMember(ctx, targetID)
		if err != nil {
			return "", err
		}
		snapshot, err := t.relationshipSnapshot(ctx, targetID, member.DisplayName())
		if err != nil {
			return "", err
		}
		return marshalDianaRelationshipResult(dianaRelationshipResult{
			OK:      true,
			Action:  "retrieved",
			Message: "已读取目标用户的关系数据；只包含关系统计，不包含长期记忆正文。",
			Target:  &snapshot,
		})
	case "list", "rank":
		requester := t.runtime.relationshipPolicy(ctx, t.event)
		if !requester.Owner {
			return "", fmt.Errorf("只有主人可以查询群内关系榜单")
		}
		items, err := t.listGroupRelationships(ctx, relationshipListLimit(input))
		if err != nil {
			return "", err
		}
		return marshalDianaRelationshipResult(dianaRelationshipResult{
			OK:      true,
			Action:  "listed",
			Message: fmt.Sprintf("已读取当前群内 %d 位有互动记录成员的关系数据。", len(items)),
			Items:   items,
		})
	case "set", "adjust":
		requester := t.runtime.relationshipPolicy(ctx, t.event)
		if !requester.Owner {
			return "", fmt.Errorf("只有主人可以修改其他用户的好感度")
		}
		targetID := normalizeRelationshipUserID(configToolString(input, "target_user_id"))
		if targetID == "" {
			targetID = t.defaultTargetUserID()
		}
		if targetID == "" {
			return "", fmt.Errorf("修改好感度时必须提供有效的 target_user_id 或 @ 目标用户")
		}
		ownerID := strings.TrimSpace(t.runtime.effectiveConfigForEvent(t.event).OwnerID)
		if targetID == ownerID {
			return "", fmt.Errorf("主人的关系等级固定，不能修改主人自己的好感度")
		}
		value, err := t.updatedFavorability(ctx, operation, targetID, input)
		if err != nil {
			return "", err
		}
		snapshot, err := t.relationshipSnapshot(ctx, targetID, "")
		if err != nil {
			return "", err
		}
		return marshalDianaRelationshipResult(dianaRelationshipResult{
			OK:      true,
			Action:  "updated",
			Message: fmt.Sprintf("已由主人将目标用户好感度更新为 %d；未增加互动次数。", value),
			Target:  &snapshot,
		})
	default:
		return "", fmt.Errorf("operation 必须是 get、list、set 或 adjust")
	}
}

func (t *dianaRelationshipTool) updatedFavorability(ctx context.Context, operation string, targetID string, input map[string]any) (int, error) {
	t.runtime.mu.RLock()
	store := t.runtime.userMemory
	t.runtime.mu.RUnlock()
	if store == nil {
		return 0, fmt.Errorf("当前未启用用户关系存储")
	}
	profile, _, err := store.GetUserMemory(ctx, targetID)
	if err != nil {
		return 0, fmt.Errorf("读取用户关系失败: %w", err)
	}
	valueKey := "value"
	value := 0
	if operation == "adjust" {
		valueKey = "delta"
		value = profile.Favorability
	}
	change, err := strconv.Atoi(strings.TrimSpace(configToolString(input, valueKey)))
	if err != nil {
		return 0, fmt.Errorf("%s 必须是整数", valueKey)
	}
	value += change
	if value < -100 || value > 200 {
		return 0, fmt.Errorf("好感度必须在 -100 到 200 之间")
	}
	updated, err := store.UpdateUserMemory(ctx, MessageEvent{
		Kind:       t.event.Kind,
		GroupID:    t.event.GroupID,
		UserID:     targetID,
		SenderName: profile.DisplayName,
	}, UserMemoryUpdate{
		OwnerID:         t.runtime.effectiveConfigForEvent(t.event).OwnerID,
		SetFavorability: &value,
		Administrative:  true,
	})
	if err != nil {
		return 0, fmt.Errorf("保存用户好感度失败: %w", err)
	}
	return updated.Favorability, nil
}

func (t *dianaRelationshipTool) defaultTargetUserID() string {
	cfg := t.runtime.effectiveConfigForEvent(t.event)
	botIDs := map[string]bool{}
	for _, id := range []string{t.event.SelfID, cfg.BotQQ} {
		if id = strings.TrimSpace(id); id != "" {
			botIDs[id] = true
		}
	}
	for _, id := range mentionedUserIDs(t.event.Segments) {
		if !botIDs[id] {
			return id
		}
	}
	return strings.TrimSpace(t.event.UserID)
}

func (t *dianaRelationshipTool) resolveTargetMember(ctx context.Context, targetID string) (QQGroupMemberInfo, error) {
	if t.event.Kind != EventKindGroup || strings.TrimSpace(t.event.GroupID) == "" {
		if targetID != strings.TrimSpace(t.event.UserID) && !t.runtime.relationshipPolicy(ctx, t.event).Owner {
			return QQGroupMemberInfo{}, fmt.Errorf("私聊中只能查询自己的关系数据")
		}
		return QQGroupMemberInfo{UserID: targetID}, nil
	}

	directlyMentioned := false
	for _, id := range mentionedUserIDs(t.event.Segments) {
		if id == targetID {
			directlyMentioned = true
			break
		}
	}
	member, err := t.runtime.GetGroupMemberInfo(ctx, t.event.GroupID, targetID)
	if err == nil && member.UserID != "" {
		return member, nil
	}
	if directlyMentioned || targetID == strings.TrimSpace(t.event.UserID) {
		return QQGroupMemberInfo{GroupID: t.event.GroupID, UserID: targetID}, nil
	}
	if err != nil {
		return QQGroupMemberInfo{}, fmt.Errorf("无法确认 QQ %s 是当前群成员: %w", targetID, err)
	}
	return QQGroupMemberInfo{}, fmt.Errorf("QQ %s 不是当前群成员", targetID)
}

func (t *dianaRelationshipTool) relationshipSnapshot(ctx context.Context, userID string, fallbackName string) (dianaRelationshipSnapshot, error) {
	t.runtime.mu.RLock()
	store := t.runtime.userMemory
	t.runtime.mu.RUnlock()
	if store == nil {
		return dianaRelationshipSnapshot{}, fmt.Errorf("当前未启用用户关系存储")
	}
	profile, found, err := store.GetUserMemory(ctx, userID)
	if err != nil {
		return dianaRelationshipSnapshot{}, fmt.Errorf("读取用户关系失败: %w", err)
	}
	if !found {
		profile = UserMemoryProfile{UserID: userID}
	}
	profile.UserID = userID
	if strings.TrimSpace(profile.DisplayName) == "" {
		profile.DisplayName = firstNonEmpty(strings.TrimSpace(fallbackName), relationshipEventDisplayName(t.event, userID), userID)
	}
	policy := RelationshipPolicyFor(profile, t.runtime.effectiveConfigForEvent(t.event).OwnerID, userID)
	return dianaRelationshipSnapshot{
		UserID:           userID,
		DisplayName:      profile.DisplayName,
		MentionCQ:        "[CQ:at,qq=" + userID + "]",
		Favorability:     profile.Favorability,
		MessageCount:     profile.MessageCount,
		RelationshipTier: policy.Tier,
		RelationshipName: policy.Name,
		Permissions:      append([]string(nil), policy.Permissions...),
		ScheduleLimit:    policy.personalScheduleLimit(),
		CanGenerateImage: policy.AllowImageGeneration,
		CanEditImage:     policy.AllowImageEditing,
		CanDocumentOCR:   policy.AllowDocumentOCR,
		Owner:            policy.Owner,
		HasHistory:       found,
	}, nil
}

func (t *dianaRelationshipTool) listGroupRelationships(ctx context.Context, limit int) ([]dianaRelationshipSnapshot, error) {
	if t.event.Kind != EventKindGroup || strings.TrimSpace(t.event.GroupID) == "" {
		return nil, fmt.Errorf("关系榜单只能在群聊中查询")
	}
	members, err := t.runtime.GetGroupMemberList(ctx, t.event.GroupID)
	if err != nil {
		return nil, fmt.Errorf("读取群成员列表失败: %w", err)
	}
	items := make([]dianaRelationshipSnapshot, 0, len(members))
	for _, member := range members {
		item, err := t.relationshipSnapshot(ctx, member.UserID, member.DisplayName())
		if err != nil {
			return nil, err
		}
		if !item.HasHistory {
			continue
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Favorability != items[j].Favorability {
			return items[i].Favorability > items[j].Favorability
		}
		if items[i].MessageCount != items[j].MessageCount {
			return items[i].MessageCount > items[j].MessageCount
		}
		return items[i].UserID < items[j].UserID
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func relationshipListLimit(input map[string]any) int {
	limit, err := strconv.Atoi(strings.TrimSpace(configToolString(input, "limit")))
	if err != nil || limit <= 0 {
		return defaultRelationshipListLimit
	}
	if limit > maximumRelationshipListLimit {
		return maximumRelationshipListLimit
	}
	return limit
}

func normalizeRelationshipUserID(raw string) string {
	raw = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "@"))
	if strings.HasPrefix(raw, "[CQ:at,qq=") && strings.HasSuffix(raw, "]") {
		raw = strings.TrimSuffix(strings.TrimPrefix(raw, "[CQ:at,qq="), "]")
	}
	if raw == "" {
		return ""
	}
	for _, char := range raw {
		if char < '0' || char > '9' {
			return ""
		}
	}
	return raw
}

func relationshipEventDisplayName(event MessageEvent, userID string) string {
	if strings.TrimSpace(event.UserID) == userID {
		return event.SenderNameOrID()
	}
	return ""
}

func marshalDianaRelationshipResult(result dianaRelationshipResult) (string, error) {
	body, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}
	return string(body), nil
}
