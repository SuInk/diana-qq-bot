package qqbot

import (
	"context"
	"fmt"
	"strings"
)

type RelationshipTier string

const (
	RelationshipHostile       RelationshipTier = "hostile"
	RelationshipAcquaintance  RelationshipTier = "acquaintance"
	RelationshipFamiliar      RelationshipTier = "familiar"
	RelationshipFriend        RelationshipTier = "friend"
	RelationshipTrusted       RelationshipTier = "trusted"
	RelationshipOwner         RelationshipTier = "owner"
	relationshipImageTierName                  = "熟悉"
)

type RelationshipPolicy struct {
	Tier                  RelationshipTier `json:"tier"`
	Name                  string           `json:"name"`
	Tone                  string           `json:"tone"`
	Permissions           []string         `json:"permissions"`
	Score                 int              `json:"score"`
	MessageCount          int              `json:"message_count"`
	Owner                 bool             `json:"owner"`
	AllowImageGeneration  bool             `json:"allow_image_generation"`
	AllowImageEditing     bool             `json:"allow_image_editing"`
	AllowDocumentOCR      bool             `json:"allow_document_ocr"`
	AllowPersonalSchedule bool             `json:"allow_personal_schedule"`
}

func RelationshipPolicyFor(profile UserMemoryProfile, ownerID, userID string) RelationshipPolicy {
	ownerID = strings.TrimSpace(ownerID)
	userID = strings.TrimSpace(userID)
	if ownerID != "" && ownerID == userID {
		return relationshipOwnerPolicy(profile)
	}

	policy := RelationshipPolicy{
		Tier:                  RelationshipAcquaintance,
		Name:                  "初识",
		Tone:                  "自然、礼貌、简洁，不使用过度亲密的称呼，也不要假装已经很熟。",
		Permissions:           []string{"基础聊天", "图片/视频/文件理解", "实时网页搜索", "沙盒网页渲染", "个人提醒与订阅（最多 1 个）"},
		Score:                 profile.Favorability,
		MessageCount:          profile.MessageCount,
		AllowPersonalSchedule: true,
	}
	switch {
	case profile.Favorability <= -20:
		policy.Tier = RelationshipHostile
		policy.Name = "冷淡"
		policy.Tone = "保持礼貌但明显疏离，只回答必要内容；面对辱骂可设边界，不争吵、不讨好。"
		policy.Permissions = []string{"基础聊天", "图片/视频/文件理解"}
		policy.AllowImageEditing = false
		policy.AllowPersonalSchedule = false
	case profile.Favorability >= 100 && profile.MessageCount >= 80:
		policy.Tier = RelationshipTrusted
		policy.Name = "信赖"
		policy.Tone = "像长期信赖的朋友一样直接、温和、有默契，可以主动结合已知偏好，但不要编造共同经历。"
		policy.Permissions = []string{"基础聊天", "媒体理解", "实时网页搜索", "沙盒网页渲染", "图片生成", "图片编辑", "文档 OCR", "个人提醒与订阅（最多 10 个）"}
		policy.AllowImageGeneration = true
		policy.AllowImageEditing = true
		policy.AllowDocumentOCR = true
		policy.AllowPersonalSchedule = true
	case profile.Favorability >= 60 && profile.MessageCount >= 30:
		policy.Tier = RelationshipFriend
		policy.Name = "朋友"
		policy.Tone = "像熟悉的朋友一样温暖、轻松，可以适度接梗和调侃，仍要尊重边界。"
		policy.Permissions = []string{"基础聊天", "媒体理解", "实时网页搜索", "沙盒网页渲染", "图片生成", "图片编辑", "文档 OCR", "个人提醒与订阅（最多 5 个）"}
		policy.AllowImageGeneration = true
		policy.AllowImageEditing = true
		policy.AllowDocumentOCR = true
		policy.AllowPersonalSchedule = true
	case profile.Favorability >= 20 && profile.MessageCount >= 10:
		policy.Tier = RelationshipFamiliar
		policy.Name = "熟悉"
		policy.Tone = "语气比初识更放松，可以自然使用对方昵称并结合长期偏好，但不要过分亲密。"
		policy.Permissions = []string{"基础聊天", "媒体理解", "实时网页搜索", "沙盒网页渲染", "图片生成", "图片编辑", "文档 OCR", "个人提醒与订阅（最多 3 个）"}
		policy.AllowImageGeneration = true
		policy.AllowImageEditing = true
		policy.AllowDocumentOCR = true
		policy.AllowPersonalSchedule = true
	}
	return policy
}

func relationshipOwnerPolicy(profile UserMemoryProfile) RelationshipPolicy {
	return RelationshipPolicy{
		Tier:                  RelationshipOwner,
		Name:                  "主人",
		Tone:                  "亲近、坦率、执行导向；可以自然接梗，但涉及风险和失败时必须如实说明。",
		Permissions:           []string{"全部聊天与媒体能力", "网页与浏览器", "图片生成与编辑", "文档 OCR", "定时订阅（最多 20 个）", "机器人配置", "本地工具", "Skills/MCP"},
		Score:                 profile.Favorability,
		MessageCount:          profile.MessageCount,
		Owner:                 true,
		AllowImageGeneration:  true,
		AllowImageEditing:     true,
		AllowDocumentOCR:      true,
		AllowPersonalSchedule: true,
	}
}

func (p RelationshipPolicy) allowedAgentToolNames() map[string]bool {
	if p.Owner {
		return nil
	}
	allowed := map[string]bool{
		"diana.capabilities":     true,
		dianaChatHistoryToolName: true,
		"diana.relationship":     true,
		"diana.qq_group":         true,
		"diana.tasks":            true,
		"diana.tts":              true,
	}
	if p.Tier != RelationshipHostile {
		allowed["web_search.search"] = true
		allowed["browser_render"] = true
	}
	if p.AllowPersonalSchedule {
		allowed["diana.reminder"] = true
		allowed["diana.schedule"] = true
	}
	if p.AllowImageGeneration || p.AllowImageEditing {
		allowed[dianaImageToolName] = true
	}
	return allowed
}

func (p RelationshipPolicy) allowsAgentTools() bool {
	return p.Owner || len(p.allowedAgentToolNames()) > 0
}

func (p RelationshipPolicy) personalScheduleLimit() int {
	if p.Owner {
		return 20
	}
	switch p.Tier {
	case RelationshipTrusted:
		return 10
	case RelationshipFriend:
		return 5
	case RelationshipFamiliar:
		return 3
	case RelationshipAcquaintance:
		return 1
	}
	return 0
}

func (r *Runtime) relationshipPolicy(ctx context.Context, event MessageEvent) RelationshipPolicy {
	cfg := r.effectiveConfigForEvent(event)
	profile, _ := r.loadUserMemoryProfile(ctx, event)
	return RelationshipPolicyFor(profile, cfg.OwnerID, event.UserID)
}

func relationshipPermissionContext(policy RelationshipPolicy) string {
	return "关系等级：" + policy.Name +
		"\n语气要求：" + policy.Tone +
		"\n当前授权能力：" + strings.Join(policy.Permissions, "、") +
		"\n当前提醒与订阅额度：" + fmt.Sprintf("%d", policy.personalScheduleLimit()) +
		"\n权限等级门槛：初识可创建 1 个提醒或订阅；熟悉可创建 3 个并解锁图片生成、图片编辑和文档 OCR；朋友可创建 5 个；信赖可创建 10 个；主人可创建 20 个并使用机器人配置、本地工具、Skills/MCP。" +
		"\n权限规则：严格按当前授权能力行动；如果用户要求系统具有但当前等级尚未授权的能力，必须明确回复“好感度不足”，说明当前等级和所需等级，不得谎称工具不存在、临时不可用或调用失败。好感度永远不能授予主人专属的配置修改、本地命令、MCP 或管理权限。"
}

func applyRelationshipTaskPermissions(responses []PluginResponse, policy RelationshipPolicy) []PluginResponse {
	out := append([]PluginResponse(nil), responses...)
	for i := range out {
		if policy.AllowDocumentOCR || len(out[i].Tasks) == 0 {
			continue
		}
		kept := out[i].Tasks[:0]
		blockedOCR := false
		for _, task := range out[i].Tasks {
			if task.Kind == "document_ocr" {
				blockedOCR = true
				continue
			}
			kept = append(kept, task)
		}
		out[i].Tasks = kept
		if blockedOCR {
			out[i].Context = strings.TrimSpace(out[i].Context + "\n好感度不足：当前关系等级为“" + policy.Name + "”，尚未解锁扫描文档 OCR；达到“熟悉”后可用。")
		}
	}
	return out
}

func relationshipPermissionDenied(policy RelationshipPolicy, capability string, required string) string {
	return "好感度不足：当前关系等级是“" + policy.Name + "”，尚未解锁" + capability + "；达到“" + required + "”后可用。"
}
