package webui

import (
	"context"
	"sync"

	"diana-qq-bot/model/qqbot"
	"diana-qq-bot/model/storage"
)

type QQBotGroupConfigStore interface {
	ConfigForGroup(groupID string) (qqbot.GroupConfig, bool)
	Groups() qqbot.GroupConfigSet
	SaveGroupConfig(qqbot.GroupConfig, qqbot.BotConfig) (qqbot.GroupConfig, error)
}

type MemoryQQBotGroupConfigStore struct {
	mu   sync.RWMutex
	data qqbot.GroupConfigSet
}

func NewMemoryQQBotGroupConfigStore() *MemoryQQBotGroupConfigStore {
	return &MemoryQQBotGroupConfigStore{data: qqbot.GroupConfigSet{}}
}

func (s *MemoryQQBotGroupConfigStore) ConfigForGroup(groupID string) (qqbot.GroupConfig, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.ConfigForGroup(groupID)
}

func (s *MemoryQQBotGroupConfigStore) Groups() qqbot.GroupConfigSet {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data
}

func (s *MemoryQQBotGroupConfigStore) SaveGroupConfig(cfg qqbot.GroupConfig, base qqbot.BotConfig) (qqbot.GroupConfig, error) {
	cfg = cfg.WithDefaults(cfg.GroupID, base)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = s.data.Upsert(cfg, base)
	saved, _ := s.data.ConfigForGroup(cfg.GroupID)
	return saved, nil
}

type PersistentQQBotGroupConfigStore struct {
	mu    sync.RWMutex
	data  qqbot.GroupConfigSet
	store *storage.SQLiteStore
	ctx   context.Context
}

func NewPersistentQQBotGroupConfigStore(ctx context.Context, store *storage.SQLiteStore) (*PersistentQQBotGroupConfigStore, error) {
	data := qqbot.GroupConfigSet{}
	if saved, ok, err := store.LoadQQBotGroupConfigs(ctx); err != nil {
		return nil, err
	} else if ok {
		data = saved
	}
	return &PersistentQQBotGroupConfigStore{
		data:  data,
		store: store,
		ctx:   ctx,
	}, nil
}

func (s *PersistentQQBotGroupConfigStore) ConfigForGroup(groupID string) (qqbot.GroupConfig, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.ConfigForGroup(groupID)
}

func (s *PersistentQQBotGroupConfigStore) Groups() qqbot.GroupConfigSet {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data
}

func (s *PersistentQQBotGroupConfigStore) SaveGroupConfig(cfg qqbot.GroupConfig, base qqbot.BotConfig) (qqbot.GroupConfig, error) {
	cfg = cfg.WithDefaults(cfg.GroupID, base)
	s.mu.Lock()
	s.data = s.data.Upsert(cfg, base)
	set := s.data
	saved, _ := set.ConfigForGroup(cfg.GroupID)
	s.mu.Unlock()
	if s.store != nil {
		if err := s.store.SaveQQBotGroupConfigs(s.ctx, set); err != nil {
			return saved, err
		}
	}
	return saved, nil
}
