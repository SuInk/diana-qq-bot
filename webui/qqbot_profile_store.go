package webui

import (
	"context"
	"sync"

	"diana-qq-bot/model/qqbot"
	"diana-qq-bot/model/storage"
)

type QQBotProfileStore interface {
	Current() qqbot.BotConfig
	Profiles() qqbot.ProfileSet
	SaveProfiles(qqbot.ProfileSet)
	SaveCurrentConfig(qqbot.BotConfig)
}

type MemoryQQBotProfileStore struct {
	mu   sync.RWMutex
	data qqbot.ProfileSet
}

// NewMemoryQQBotProfileStore 创建内存版 QQ 机器人配置集存储。
func NewMemoryQQBotProfileStore(cfg qqbot.BotConfig) *MemoryQQBotProfileStore {
	return &MemoryQQBotProfileStore{data: qqbot.NewProfileSet(cfg)}
}

// Current 返回内存存储中的当前机器人配置。
func (s *MemoryQQBotProfileStore) Current() qqbot.BotConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if profile, ok := s.data.Current(); ok {
		return profile.WithDefaults()
	}
	return qqbot.BotConfig{}
}

// Profiles 返回内存存储中的机器人配置集。
func (s *MemoryQQBotProfileStore) Profiles() qqbot.ProfileSet {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.WithDefaults()
}

// SaveProfiles 更新内存中的机器人配置集。
func (s *MemoryQQBotProfileStore) SaveProfiles(set qqbot.ProfileSet) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = set.WithDefaults()
}

// SaveCurrentConfig 把运行时当前配置写回当前激活的机器人档案。
func (s *MemoryQQBotProfileStore) SaveCurrentConfig(cfg qqbot.BotConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = upsertCurrentQQBotProfileSet(s.data, cfg)
}

type PersistentQQBotProfileStore struct {
	mu    sync.RWMutex
	data  qqbot.ProfileSet
	store *storage.SQLiteStore
	ctx   context.Context
}

// NewPersistentQQBotProfileStore 创建 SQLite 持久化版 QQ 机器人配置集存储。
func NewPersistentQQBotProfileStore(ctx context.Context, store *storage.SQLiteStore, fallback qqbot.BotConfig) (*PersistentQQBotProfileStore, error) {
	data := qqbot.NewProfileSet(fallback)
	if saved, ok, err := store.LoadQQBotProfiles(ctx); err != nil {
		return nil, err
	} else if ok && len(saved.Profiles) > 0 {
		data = saved.WithDefaults()
	} else if savedCfg, ok, err := store.LoadQQBotConfig(ctx); err != nil {
		return nil, err
	} else if ok {
		// 兼容旧版只有单个 qqbot_config 的数据库，首次启动时自动升级为配置集。
		data = qqbot.NewProfileSet(savedCfg)
	}
	return &PersistentQQBotProfileStore{
		data:  data.WithDefaults(),
		store: store,
		ctx:   ctx,
	}, nil
}

// Current 返回持久化存储中的当前机器人配置。
func (s *PersistentQQBotProfileStore) Current() qqbot.BotConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if profile, ok := s.data.Current(); ok {
		return profile.WithDefaults()
	}
	return qqbot.BotConfig{}
}

// Profiles 返回持久化存储中的机器人配置集。
func (s *PersistentQQBotProfileStore) Profiles() qqbot.ProfileSet {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.WithDefaults()
}

// SaveProfiles 保存机器人配置集并同步旧版 flat 配置。
func (s *PersistentQQBotProfileStore) SaveProfiles(set qqbot.ProfileSet) {
	set = set.WithDefaults()
	s.mu.Lock()
	s.data = set
	s.mu.Unlock()
	if s.store != nil {
		_ = s.store.SaveQQBotProfiles(s.ctx, set)
		if profile, ok := set.Current(); ok {
			_ = s.store.SaveQQBotConfig(s.ctx, profile)
		}
	}
}

// SaveCurrentConfig 把运行时当前配置回写到激活中的机器人配置档。
func (s *PersistentQQBotProfileStore) SaveCurrentConfig(cfg qqbot.BotConfig) {
	s.mu.Lock()
	s.data = upsertCurrentQQBotProfileSet(s.data, cfg)
	set := s.data
	s.mu.Unlock()
	if s.store != nil {
		_ = s.store.SaveQQBotProfiles(s.ctx, set)
		if profile, ok := set.Current(); ok {
			_ = s.store.SaveQQBotConfig(s.ctx, profile)
		}
	}
}

// upsertCurrentQQBotProfileSet 用最新运行态覆盖当前激活的机器人配置档。
func upsertCurrentQQBotProfileSet(set qqbot.ProfileSet, cfg qqbot.BotConfig) qqbot.ProfileSet {
	set = set.WithDefaults()
	current, ok := set.Current()
	if cfg.ID == "" && ok {
		cfg.ID = current.ID
	}
	if cfg.Name == "" && ok {
		cfg.Name = current.Name
	}
	if cfg.Platform == "" && ok {
		cfg.Platform = current.Platform
	}
	if cfg.AvatarURL == "" && ok {
		cfg.AvatarURL = current.AvatarURL
	}
	cfg = cfg.WithDefaults()
	for i := range set.Profiles {
		if set.Profiles[i].ID != cfg.ID {
			continue
		}
		set.Profiles[i] = cfg
		set.ActiveID = cfg.ID
		return set.WithDefaults()
	}
	set.Profiles = append(set.Profiles, cfg)
	set.ActiveID = cfg.ID
	return set.WithDefaults()
}
