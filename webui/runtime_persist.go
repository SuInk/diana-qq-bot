package webui

import (
	"diana-qq-bot/model/qqbot"
)

type RuntimePersistor struct {
	store QQBotProfileStore
}

// NewRuntimePersistor 创建机器人运行态配置持久化器。
func NewRuntimePersistor(store QQBotProfileStore) *RuntimePersistor {
	return &RuntimePersistor{store: store}
}

// SaveBotConfig 保存BotConfig数据。
func (p *RuntimePersistor) SaveBotConfig(cfg qqbot.BotConfig) {
	if p == nil || p.store == nil {
		return
	}
	// 这是机器人 owner 指令的轻量落盘通道，失败不阻塞聊天响应。
	p.store.SaveCurrentConfig(cfg)
}
