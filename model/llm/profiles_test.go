package llm

import "testing"

// TestProfileSetActiveGroupProfilesRotatesWithinGroup 验证只在当前分组内按顺序轮换。
func TestProfileSetActiveGroupProfilesRotatesWithinGroup(t *testing.T) {
	set := ProfileSet{
		ActiveID: "b",
		Profiles: []Profile{
			{ID: "a", Name: "A", Group: "chat", Config: ProviderConfig{Provider: ProviderOpenAICompatible, APIKey: "key-a", Model: "example-chat-model"}},
			{ID: "b", Name: "B", Group: "chat", Config: ProviderConfig{Provider: ProviderOpenAICompatible, APIKey: "key-b", Model: "example-chat-model"}},
			{ID: "c", Name: "C", Group: "vision", Config: ProviderConfig{Provider: ProviderOpenAICompatible, APIKey: "key-c", Model: "example-chat-model"}},
			{ID: "d", Name: "D", Group: "chat", Config: ProviderConfig{Provider: ProviderOpenAICompatible, APIKey: "key-d", Model: "example-chat-model"}},
		},
	}

	got := set.ActiveGroupProfiles()
	want := []string{"b", "d", "a"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].ID != want[i] || got[i].Group != "chat" {
			t.Fatalf("got[%d] = %#v, want id=%q group=chat", i, got[i], want[i])
		}
	}
}

// TestProfileSetWithDefaultsAssignsDefaultGroup 验证旧配置会进入默认分组。
func TestProfileSetWithDefaultsAssignsDefaultGroup(t *testing.T) {
	set := ProfileSet{
		ActiveID: "a",
		Profiles: []Profile{
			{ID: "a", Name: "A", Config: ProviderConfig{Provider: ProviderOpenAICompatible, APIKey: "key-a", Model: "example-chat-model"}},
		},
	}.WithDefaults()

	if set.Profiles[0].Group != DefaultProfileGroup {
		t.Fatalf("group = %q, want %q", set.Profiles[0].Group, DefaultProfileGroup)
	}
}
