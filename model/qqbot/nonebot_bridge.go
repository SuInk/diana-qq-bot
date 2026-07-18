package qqbot

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type NoneBotBridgeConfig struct {
	Enabled     bool
	Endpoint    string
	AccessToken string
}

type NoneBotBridgeStatus struct {
	Enabled   bool      `json:"enabled"`
	Connected bool      `json:"connected"`
	Endpoint  string    `json:"endpoint,omitempty"`
	LastError string    `json:"last_error,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

type NoneBotBridge struct {
	mu      sync.RWMutex
	cfg     NoneBotBridgeConfig
	channel Channel
	conn    *websocket.Conn
	status  NoneBotBridgeStatus
	cancel  context.CancelFunc
	writeMu sync.Mutex
	dialer  *websocket.Dialer
}

// NewNoneBotBridge 创建 NoneBot 桥接器。
func NewNoneBotBridge(cfg NoneBotBridgeConfig, channel Channel) *NoneBotBridge {
	return &NoneBotBridge{
		cfg:     cfg,
		channel: channel,
		dialer:  websocket.DefaultDialer,
		status: NoneBotBridgeStatus{
			Enabled:   cfg.Enabled,
			Endpoint:  cfg.Endpoint,
			UpdatedAt: time.Now(),
		},
	}
}

// UpdateConfig 更新 NoneBot bridge 配置和主 channel。
func (b *NoneBotBridge) UpdateConfig(cfg NoneBotBridgeConfig, channel Channel) {
	b.mu.Lock()
	b.cfg = cfg
	if channel != nil {
		b.channel = channel
	}
	b.status.Enabled = cfg.Enabled
	b.status.Endpoint = cfg.Endpoint
	b.status.UpdatedAt = time.Now()
	b.mu.Unlock()
}

// Start 启动 NoneBot bridge 连接循环。
func (b *NoneBotBridge) Start(parent context.Context) {
	b.Stop()
	b.mu.RLock()
	cfg := b.cfg
	b.mu.RUnlock()
	if !cfg.Enabled || cfg.Endpoint == "" {
		b.setStatus(false, "")
		return
	}
	// 桥接连接独立于主 OneBot channel，可单独重连和停止。
	ctx, cancel := context.WithCancel(parent)
	b.mu.Lock()
	b.cancel = cancel
	b.mu.Unlock()
	go b.run(ctx)
}

// Stop 停止 NoneBot bridge 并关闭连接。
func (b *NoneBotBridge) Stop() {
	b.mu.Lock()
	cancel := b.cancel
	b.cancel = nil
	conn := b.conn
	b.conn = nil
	b.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if conn != nil {
		_ = conn.Close()
	}
	b.setStatus(false, "")
}

// Status 返回 NoneBot bridge 状态。
func (b *NoneBotBridge) Status() NoneBotBridgeStatus {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.status
}

// ForwardEvent 将 QQ 事件转发给 NoneBot bridge。
func (b *NoneBotBridge) ForwardEvent(event MessageEvent) {
	payload := oneBotEventPayload(event)
	b.mu.RLock()
	conn := b.conn
	b.mu.RUnlock()
	if conn == nil {
		return
	}
	// 转发失败只更新桥接状态，不能影响机器人本地回复链路。
	b.writeMu.Lock()
	err := conn.WriteJSON(payload)
	b.writeMu.Unlock()
	if err != nil {
		b.setStatus(false, err.Error())
	}
}

// run 持续连接 NoneBot 并处理重连。
func (b *NoneBotBridge) run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		b.mu.RLock()
		cfg := b.cfg
		b.mu.RUnlock()
		header := http.Header{}
		if cfg.AccessToken != "" {
			header.Set("Authorization", "Bearer "+cfg.AccessToken)
		}
		conn, _, err := b.dialer.DialContext(ctx, cfg.Endpoint, header)
		if err != nil {
			b.setStatus(false, err.Error())
			log.Printf("nonebot bridge connect failed: %v", err)
			// 固定短间隔重试，避免 NoneBot 暂时未启动时需要重启主服务。
			if !sleepContext(ctx, 3*time.Second) {
				return
			}
			continue
		}
		b.mu.Lock()
		if b.conn != nil {
			_ = b.conn.Close()
		}
		b.conn = conn
		b.mu.Unlock()
		b.setStatus(true, "")

		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				if ctx.Err() == nil {
					b.setStatus(false, err.Error())
					log.Printf("nonebot bridge stopped: %v", err)
				}
				_ = conn.Close()
				break
			}
			b.handleFrame(ctx, data)
		}
	}
}

// handleFrame 处理 NoneBot 发来的 OneBot API 请求。
func (b *NoneBotBridge) handleFrame(ctx context.Context, data []byte) {
	var req struct {
		Action string         `json:"action"`
		Params map[string]any `json:"params"`
		Echo   any            `json:"echo,omitempty"`
	}
	if err := json.Unmarshal(data, &req); err != nil {
		b.setStatus(b.Status().Connected, err.Error())
		return
	}
	if req.Action == "" {
		return
	}

	b.mu.RLock()
	channel := b.channel
	conn := b.conn
	b.mu.RUnlock()
	if channel == nil || conn == nil {
		return
	}
	// NoneBot 发来的 action 直接转发给主 OneBot channel，相当于一个受控 API 代理。
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	result, err := channel.CallAPI(callCtx, req.Action, req.Params)
	resp := map[string]any{
		"status":  "ok",
		"retcode": 0,
		"data":    result,
		"echo":    req.Echo,
	}
	if err != nil {
		resp["status"] = "failed"
		resp["retcode"] = 1
		resp["wording"] = err.Error()
	}
	b.writeMu.Lock()
	_ = conn.WriteJSON(resp)
	b.writeMu.Unlock()
}

// setStatus 更新 NoneBot bridge 状态。
func (b *NoneBotBridge) setStatus(connected bool, lastError string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.status = NoneBotBridgeStatus{
		Enabled:   b.cfg.Enabled,
		Connected: connected,
		Endpoint:  b.cfg.Endpoint,
		LastError: lastError,
		UpdatedAt: time.Now(),
	}
}

// oneBotEventPayload 将内部消息事件转换为 OneBot 事件 payload。
func oneBotEventPayload(event MessageEvent) map[string]any {
	// 尽量还原 OneBot 事件字段，让 NoneBot 插件无需了解本项目内部 MessageEvent。
	payload := map[string]any{
		"time":         event.Time,
		"self_id":      numberOrString(event.SelfID),
		"post_type":    "message",
		"message_type": event.MessageType,
		"sub_type":     "normal",
		"user_id":      numberOrString(event.UserID),
		"message_id":   numberOrString(event.MessageID),
		"message":      event.Segments,
		"raw_message":  event.RawMessage,
		"sender": map[string]any{
			"nickname": event.SenderName,
			"card":     event.SenderName,
		},
	}
	if event.Kind == EventKindGroup {
		payload["group_id"] = numberOrString(event.GroupID)
	}
	return payload
}

// numberOrString 将 QQ ID 转为 OneBot 需要的数字或字符串。
func numberOrString(value string) any {
	if value == "" {
		return int64(0)
	}
	// QQ/群号能转数字就按 OneBot 常见 number 输出，非数字 ID 保持字符串。
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err == nil {
		return parsed
	}
	return value
}

// bridgeConfigFromBotConfig 从机器人配置中提取 NoneBot bridge 配置。
func bridgeConfigFromBotConfig(cfg BotConfig) NoneBotBridgeConfig {
	return NoneBotBridgeConfig{
		Enabled:     cfg.NoneBotBridgeEnabled,
		Endpoint:    cfg.NoneBotBridgeEndpoint,
		AccessToken: cfg.NoneBotBridgeToken,
	}
}

// sleepContext 执行可被 context 取消的等待。
func sleepContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	// 可取消 sleep，服务停止时不会被重连等待卡住。
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// String 返回 NoneBot bridge 状态摘要。
func (s NoneBotBridgeStatus) String() string {
	return fmt.Sprintf("enabled=%v connected=%v endpoint=%s", s.Enabled, s.Connected, s.Endpoint)
}
