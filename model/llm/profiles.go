package llm

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	DefaultProfileName  = "默认配置"
	DefaultProfileGroup = "default"
)

type Profile struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Group       string         `json:"group,omitempty"`
	Description string         `json:"description,omitempty"`
	UpdatedAt   time.Time      `json:"updated_at,omitempty"`
	Config      ProviderConfig `json:"config"`
}

type ProfileSet struct {
	ActiveID string    `json:"active_id"`
	Profiles []Profile `json:"profiles"`
}

// NewProfileSet 基于单个 provider 配置创建配置集。
func NewProfileSet(cfg ProviderConfig) ProfileSet {
	profile := Profile{
		ID:        uuid.NewString(),
		Name:      DefaultProfileName,
		Group:     DefaultProfileGroup,
		UpdatedAt: time.Now(),
		Config:    cfg.WithDefaults(),
	}
	return ProfileSet{
		ActiveID: profile.ID,
		Profiles: []Profile{profile},
	}
}

// NormalizeProfileName 规范化配置档名称。
func NormalizeProfileName(name string) string {
	if trimmed := strings.TrimSpace(name); trimmed != "" {
		return trimmed
	}
	return DefaultProfileName
}

// NormalizeProfileGroup 规范化配置档分组，空分组归入默认轮换组。
func NormalizeProfileGroup(group string) string {
	if trimmed := strings.TrimSpace(group); trimmed != "" {
		return trimmed
	}
	return DefaultProfileGroup
}

// Current 返回配置集当前激活的 profile。
func (s ProfileSet) Current() (Profile, bool) {
	for _, profile := range s.Profiles {
		if profile.ID == s.ActiveID {
			profile.Config = profile.Config.WithDefaults()
			profile.Group = NormalizeProfileGroup(profile.Group)
			return profile, true
		}
	}
	if len(s.Profiles) == 0 {
		return Profile{}, false
	}
	// active_id 丢失或失效时退回第一个配置，避免旧/坏数据导致页面完全不可用。
	profile := s.Profiles[0]
	profile.Config = profile.Config.WithDefaults()
	profile.Group = NormalizeProfileGroup(profile.Group)
	return profile, true
}

// ActiveGroupProfiles 返回从当前配置开始、同分组内按顺序轮换的配置列表。
func (s ProfileSet) ActiveGroupProfiles() []Profile {
	s = s.WithDefaults()
	current, ok := s.Current()
	if !ok {
		return nil
	}
	group := NormalizeProfileGroup(current.Group)
	currentIndex := 0
	for i, profile := range s.Profiles {
		if profile.ID == current.ID {
			currentIndex = i
			break
		}
	}
	out := make([]Profile, 0, len(s.Profiles))
	for offset := 0; offset < len(s.Profiles); offset++ {
		profile := s.Profiles[(currentIndex+offset)%len(s.Profiles)]
		if NormalizeProfileGroup(profile.Group) != group {
			continue
		}
		profile.Group = NormalizeProfileGroup(profile.Group)
		profile.Config = profile.Config.WithDefaults()
		out = append(out, profile)
	}
	return out
}

// WithActive 返回切换 active_id 后的配置集。
func (s ProfileSet) WithActive(id string) ProfileSet {
	id = strings.TrimSpace(id)
	for _, profile := range s.Profiles {
		if profile.ID == id {
			s.ActiveID = id
			return s
		}
	}
	return s
}

// Delete 从配置集中删除指定 profile。
func (s ProfileSet) Delete(id string) ProfileSet {
	id = strings.TrimSpace(id)
	if len(s.Profiles) == 0 {
		return s
	}
	next := make([]Profile, 0, len(s.Profiles))
	for _, profile := range s.Profiles {
		if profile.ID == id {
			continue
		}
		next = append(next, profile)
	}
	s.Profiles = next
	if len(s.Profiles) == 0 {
		s.ActiveID = ""
		return s
	}
	if s.ActiveID == id {
		s.ActiveID = s.Profiles[0].ID
	}
	return s
}

// WithDefaults 补齐配置集 profile ID、名称和默认配置。
func (s ProfileSet) WithDefaults() ProfileSet {
	if len(s.Profiles) > 0 {
		// 复制 slice 后再标准化，避免调用方持有的 ProfileSet 被意外原地修改。
		profiles := make([]Profile, len(s.Profiles))
		copy(profiles, s.Profiles)
		s.Profiles = profiles
	}
	seen := make(map[string]struct{}, len(s.Profiles))
	for i := range s.Profiles {
		id := strings.TrimSpace(s.Profiles[i].ID)
		if id == "" {
			id = uuid.NewString()
		}
		if _, ok := seen[id]; ok {
			// 导入配置可能出现重复 ID，重新生成可以保证后续切换/删除定位唯一。
			id = uuid.NewString()
		}
		seen[id] = struct{}{}
		s.Profiles[i].ID = id
		s.Profiles[i].Name = NormalizeProfileName(s.Profiles[i].Name)
		s.Profiles[i].Group = NormalizeProfileGroup(s.Profiles[i].Group)
		s.Profiles[i].Description = strings.TrimSpace(s.Profiles[i].Description)
		s.Profiles[i].Config = s.Profiles[i].Config.WithDefaults()
	}
	if len(s.Profiles) == 0 {
		s.ActiveID = ""
		return s
	}
	s.ActiveID = strings.TrimSpace(s.ActiveID)
	for _, profile := range s.Profiles {
		if profile.ID == s.ActiveID {
			return s
		}
	}
	// active_id 找不到时自动激活第一个配置，保证配置集始终有可用当前项。
	s.ActiveID = s.Profiles[0].ID
	return s
}
