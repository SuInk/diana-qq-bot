package webui

import (
	"context"
	"sync"

	"diana-qq-bot/model/llm"
	"diana-qq-bot/model/storage"
)

type LLMProfileStore interface {
	Current() llm.ProviderConfig
	Profiles() llm.ProfileSet
	SaveProfiles(llm.ProfileSet)
}

type MemoryLLMProfileStore struct {
	mu   sync.RWMutex
	data llm.ProfileSet
}

// NewMemoryLLMProfileStore 创建内存版 LLM 配置集存储。
func NewMemoryLLMProfileStore(cfg llm.ProviderConfig) *MemoryLLMProfileStore {
	return &MemoryLLMProfileStore{data: llm.NewProfileSet(cfg)}
}

// Current 返回内存存储中的当前 LLM 配置。
func (s *MemoryLLMProfileStore) Current() llm.ProviderConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if profile, ok := s.data.Current(); ok {
		return profile.Config.WithDefaults()
	}
	return llm.ProviderConfig{}
}

// Profiles 返回内存存储中的 LLM 配置集。
func (s *MemoryLLMProfileStore) Profiles() llm.ProfileSet {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.WithDefaults()
}

// SaveProfiles 更新内存中的 LLM 配置集。
func (s *MemoryLLMProfileStore) SaveProfiles(set llm.ProfileSet) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = set.WithDefaults()
}

type PersistentLLMProfileStore struct {
	mu    sync.RWMutex
	data  llm.ProfileSet
	store *storage.SQLiteStore
	ctx   context.Context
}

// NewPersistentLLMProfileStore 创建 SQLite 持久化版 LLM 配置集存储。
func NewPersistentLLMProfileStore(ctx context.Context, store *storage.SQLiteStore, fallback llm.ProviderConfig) (*PersistentLLMProfileStore, error) {
	data := llm.NewProfileSet(fallback)
	if saved, ok, err := store.LoadLLMProfiles(ctx); err != nil {
		return nil, err
	} else if ok && len(saved.Profiles) > 0 {
		data = saved.WithDefaults()
	} else if savedCfg, ok, err := store.LoadLLMConfig(ctx); err != nil {
		return nil, err
	} else if ok {
		// 兼容旧版本只有单个 llm_config 的数据库，首次启动时自动升级为配置集。
		data = llm.NewProfileSet(savedCfg)
	}
	return &PersistentLLMProfileStore{
		data:  data,
		store: store,
		ctx:   ctx,
	}, nil
}

// Current 返回持久化存储中的当前 LLM 配置。
func (s *PersistentLLMProfileStore) Current() llm.ProviderConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if profile, ok := s.data.Current(); ok {
		return profile.Config.WithDefaults()
	}
	return llm.ProviderConfig{}
}

// Profiles 返回持久化存储中的 LLM 配置集。
func (s *PersistentLLMProfileStore) Profiles() llm.ProfileSet {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.WithDefaults()
}

// SaveProfiles 保存 LLM 配置集并同步当前 flat 配置。
func (s *PersistentLLMProfileStore) SaveProfiles(set llm.ProfileSet) {
	set = set.WithDefaults()
	s.mu.Lock()
	s.data = set
	s.mu.Unlock()
	if s.store != nil {
		// 同时写 profile set 和旧 flat config，旧代码/测试读取 llm_config 时仍能拿到当前配置。
		_ = s.store.SaveLLMProfiles(s.ctx, set)
		if profile, ok := set.Current(); ok {
			_ = s.store.SaveLLMConfig(s.ctx, profile.Config)
		}
	}
}
