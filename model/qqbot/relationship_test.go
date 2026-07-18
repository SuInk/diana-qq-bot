package qqbot

import (
	"context"
	"strings"
	"testing"
)

func TestRelationshipPolicyTiersRequireScoreAndInteractionCount(t *testing.T) {
	tests := []struct {
		name     string
		score    int
		messages int
		ownerID  string
		userID   string
		want     RelationshipTier
	}{
		{name: "hostile", score: -20, messages: 50, want: RelationshipHostile},
		{name: "score alone cannot unlock familiar", score: 20, messages: 9, want: RelationshipAcquaintance},
		{name: "familiar", score: 20, messages: 10, want: RelationshipFamiliar},
		{name: "friend", score: 60, messages: 30, want: RelationshipFriend},
		{name: "trusted still needs history", score: 100, messages: 79, want: RelationshipFriend},
		{name: "trusted", score: 100, messages: 80, want: RelationshipTrusted},
		{name: "owner bypasses score", score: -100, messages: 0, ownerID: "42", userID: "42", want: RelationshipOwner},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := RelationshipPolicyFor(UserMemoryProfile{Favorability: test.score, MessageCount: test.messages}, test.ownerID, test.userID)
			if policy.Tier != test.want {
				t.Fatalf("tier = %q, want %q: %#v", policy.Tier, test.want, policy)
			}
		})
	}
}

func TestRelationshipPolicySeparatesCapabilitiesFromOwnerAdministration(t *testing.T) {
	initial := RelationshipPolicyFor(UserMemoryProfile{}, "owner", "user")
	if !initial.allowedAgentToolNames()["web_search.search"] || !initial.allowedAgentToolNames()[dianaChatHistoryToolName] || !initial.allowedAgentToolNames()["diana.relationship"] || !initial.allowedAgentToolNames()["diana.tts"] || initial.allowedAgentToolNames()[dianaImageToolName] || initial.AllowImageGeneration || initial.AllowImageEditing || !initial.AllowPersonalSchedule || initial.allowedAgentToolNames()["run_command"] {
		t.Fatalf("initial tools = %#v", initial.allowedAgentToolNames())
	}
	familiar := RelationshipPolicyFor(UserMemoryProfile{Favorability: 20, MessageCount: 10}, "owner", "user")
	if !familiar.AllowImageGeneration || !familiar.AllowImageEditing || !familiar.AllowDocumentOCR {
		t.Fatalf("familiar policy = %#v", familiar)
	}
	hostile := RelationshipPolicyFor(UserMemoryProfile{Favorability: -20, MessageCount: 10}, "owner", "user")
	if hostile.AllowImageEditing {
		t.Fatalf("hostile policy = %#v", hostile)
	}
	friend := RelationshipPolicyFor(UserMemoryProfile{Favorability: 60, MessageCount: 30}, "owner", "user")
	if !friend.AllowImageEditing || !friend.AllowPersonalSchedule || friend.allowedAgentToolNames()["diana.config"] {
		t.Fatalf("friend policy = %#v tools=%#v", friend, friend.allowedAgentToolNames())
	}
	owner := RelationshipPolicyFor(UserMemoryProfile{}, "owner", "owner")
	if !owner.Owner || owner.allowedAgentToolNames() != nil {
		t.Fatalf("owner policy = %#v", owner)
	}
}

func TestRelationshipImagePermissionsRequireFamiliarThreshold(t *testing.T) {
	tests := []struct {
		name    string
		profile UserMemoryProfile
		ownerID string
		userID  string
		want    bool
	}{
		{name: "score below threshold", profile: UserMemoryProfile{Favorability: 19, MessageCount: 10}, userID: "user"},
		{name: "messages below threshold", profile: UserMemoryProfile{Favorability: 20, MessageCount: 9}, userID: "user"},
		{name: "familiar threshold", profile: UserMemoryProfile{Favorability: 20, MessageCount: 10}, userID: "user", want: true},
		{name: "owner bypass", ownerID: "owner", userID: "owner", want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := RelationshipPolicyFor(test.profile, test.ownerID, test.userID)
			if policy.AllowImageGeneration != test.want || policy.AllowImageEditing != test.want {
				t.Fatalf("image permissions = generate:%v edit:%v, want both %v: %#v", policy.AllowImageGeneration, policy.AllowImageEditing, test.want, policy)
			}
		})
	}
}

func TestRelationshipScheduleLimitsIncreaseByTier(t *testing.T) {
	tests := []struct {
		profile UserMemoryProfile
		ownerID string
		userID  string
		want    int
	}{
		{profile: UserMemoryProfile{Favorability: -20}, want: 0},
		{profile: UserMemoryProfile{}, want: 1},
		{profile: UserMemoryProfile{Favorability: 20, MessageCount: 10}, want: 3},
		{profile: UserMemoryProfile{Favorability: 60, MessageCount: 30}, want: 5},
		{profile: UserMemoryProfile{Favorability: 100, MessageCount: 80}, want: 10},
		{ownerID: "owner", userID: "owner", want: 20},
	}
	for _, test := range tests {
		policy := RelationshipPolicyFor(test.profile, test.ownerID, test.userID)
		if got := policy.personalScheduleLimit(); got != test.want {
			t.Fatalf("tier=%s limit=%d want=%d", policy.Name, got, test.want)
		}
	}
}

func TestRelationshipContextDrivesToneAndHardPermissionMessage(t *testing.T) {
	policy := RelationshipPolicyFor(UserMemoryProfile{Favorability: 20, MessageCount: 10}, "owner", "user")
	contextText := relationshipPermissionContext(policy)
	for _, want := range []string{"关系等级：熟悉", "语气要求", "图片生成", "好感度永远不能授予主人"} {
		if !strings.Contains(contextText, want) {
			t.Fatalf("context = %q, missing %q", contextText, want)
		}
	}
	denied := relationshipPermissionDenied(RelationshipPolicyFor(UserMemoryProfile{Favorability: -20}, "owner", "user"), "图片编辑", relationshipImageTierName)
	if !strings.Contains(denied, "好感度不足") || !strings.Contains(denied, "冷淡") || !strings.Contains(denied, relationshipImageTierName) {
		t.Fatalf("denied = %q", denied)
	}
}

func TestRelationshipBlocksOCRTasksBelowFamiliar(t *testing.T) {
	responses := []PluginResponse{{
		Handled: true,
		Tasks: []PluginTask{{
			Kind: "document_ocr",
			Name: "OCR",
			Run: func(context.Context, PluginTaskServices) (PluginTaskResult, error) {
				return PluginTaskResult{}, nil
			},
		}},
	}}
	initial := RelationshipPolicyFor(UserMemoryProfile{}, "owner", "user")
	blocked := applyRelationshipTaskPermissions(responses, initial)
	if len(blocked[0].Tasks) != 0 || !strings.Contains(blocked[0].Context, "尚未解锁扫描文档 OCR") {
		t.Fatalf("blocked responses = %#v", blocked)
	}
	familiar := RelationshipPolicyFor(UserMemoryProfile{Favorability: 20, MessageCount: 10}, "owner", "user")
	allowed := applyRelationshipTaskPermissions(responses, familiar)
	if len(allowed[0].Tasks) != 1 {
		t.Fatalf("allowed responses = %#v", allowed)
	}
}
