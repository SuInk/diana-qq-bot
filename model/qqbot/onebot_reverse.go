package qqbot

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type OneBotReverseServer struct {
	mu      sync.RWMutex
	cfg     OneBotConfig
	handler EventHandler
	ctx     context.Context

	connMu   sync.RWMutex
	writeMu  sync.Mutex
	conn     *websocket.Conn
	status   ChannelStatus
	pending  sync.Map
	upgrader websocket.Upgrader
}

func (s *OneBotReverseServer) OutboundBackoffEnabled() bool { return true }

// NewOneBotReverseServer 创建反向 OneBot WebSocket server。
func NewOneBotReverseServer(cfg OneBotConfig) *OneBotReverseServer {
	return &OneBotReverseServer{
		cfg: cfg,
		status: ChannelStatus{
			Endpoint:  cfg.Endpoint,
			UpdatedAt: time.Now(),
		},
		upgrader: websocket.Upgrader{
			// 反向 WS 通常来自本机或局域网 NapCat，跨 origin 由 access token 控制。
			CheckOrigin: func(*http.Request) bool { return true },
		},
	}
}

// SetConfig 更新反向 OneBot server 的连接配置。
func (s *OneBotReverseServer) SetConfig(cfg OneBotConfig) {
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
	s.connMu.Lock()
	s.status.Endpoint = cfg.Endpoint
	s.status.UpdatedAt = time.Now()
	s.connMu.Unlock()
}

// Connect 在反向模式下登记事件处理器并等待关闭。
func (s *OneBotReverseServer) Connect(ctx context.Context, handler EventHandler) error {
	s.mu.Lock()
	// 反向模式下 Connect 不主动拨号，只登记 handler 等待 NapCat 连进来。
	s.ctx = ctx
	s.handler = handler
	s.mu.Unlock()
	s.setStatus(false, s.Status().SelfID, "")
	<-ctx.Done()
	_ = s.Close()
	return ctx.Err()
}

// ServeHTTP 接受 NapCat 反向 WebSocket 连接。
func (s *OneBotReverseServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.setStatus(false, s.Status().SelfID, err.Error())
		return
	}

	s.connMu.Lock()
	if s.conn != nil {
		// 新 NapCat 连接进来时替换旧连接，避免 API 调用写到过期 socket。
		_ = s.conn.Close()
	}
	s.conn = conn
	s.connMu.Unlock()
	s.setStatus(true, "", "")

	go s.readLoop(conn)
}

// Send 通过反向 OneBot 连接发送消息。
func (s *OneBotReverseServer) Send(ctx context.Context, msg OutgoingMessage) error {
	_, err := s.SendWithResult(ctx, msg)
	return err
}

// SendWithResult sends a message and preserves the OneBot response message_id.
func (s *OneBotReverseServer) SendWithResult(ctx context.Context, msg OutgoingMessage) (map[string]any, error) {
	if strings.TrimSpace(msg.Text) == "" && len(msg.ImageURLs) == 0 && len(msg.VideoURLs) == 0 {
		return nil, nil
	}
	params := map[string]any{"message": buildOutgoingSegments(msg)}
	action := "send_private_msg"
	if msg.GroupID != "" {
		action = "send_group_msg"
		groupID, err := strconv.ParseInt(msg.GroupID, 10, 64)
		if err != nil {
			return nil, err
		}
		params["group_id"] = groupID
	} else {
		userID, err := strconv.ParseInt(msg.UserID, 10, 64)
		if err != nil {
			return nil, err
		}
		params["user_id"] = userID
	}
	return s.CallAPI(ctx, action, params)
}

// CallAPI 通过反向连接发送 OneBot action 并等待响应。
func (s *OneBotReverseServer) CallAPI(ctx context.Context, action string, params map[string]any) (map[string]any, error) {
	s.connMu.RLock()
	conn := s.conn
	s.connMu.RUnlock()
	if conn == nil {
		return nil, errors.New("qqbot: onebot reverse websocket is not connected")
	}

	echo := time.Now().Format("20060102150405.000000000")
	resultCh := make(chan callResult, 1)
	// 与正向模式一样，所有 API 调用通过 echo 等待读循环返回结果。
	s.pending.Store(echo, resultCh)
	defer s.pending.Delete(echo)

	req := map[string]any{
		"action": action,
		"params": params,
		"echo":   echo,
	}
	s.writeMu.Lock()
	err := conn.WriteJSON(req)
	s.writeMu.Unlock()
	if err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-resultCh:
		return result.data, result.err
	}
}

// Status 返回反向 OneBot server 状态。
func (s *OneBotReverseServer) Status() ChannelStatus {
	s.connMu.RLock()
	defer s.connMu.RUnlock()
	return s.status
}

// Close 关闭当前反向 WebSocket 连接。
func (s *OneBotReverseServer) Close() error {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	if s.conn == nil {
		s.status.Connected = false
		s.status.UpdatedAt = time.Now()
		return nil
	}
	err := s.conn.Close()
	s.conn = nil
	s.status.Connected = false
	s.status.UpdatedAt = time.Now()
	return err
}

// readLoop 持续读取反向 WebSocket 事件帧。
func (s *OneBotReverseServer) readLoop(conn *websocket.Conn) {
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			s.disconnectIfCurrent(conn, err.Error())
			return
		}
		if err := s.handleFrame(data); err != nil {
			s.setStatus(s.Status().Connected, s.Status().SelfID, err.Error())
		}
	}
}

func (s *OneBotReverseServer) disconnectIfCurrent(conn *websocket.Conn, lastError string) {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	if s.conn != conn {
		return
	}
	s.conn = nil
	s.status.Connected = false
	s.status.LastError = lastError
	s.status.UpdatedAt = time.Now()
}

// handleFrame 解析反向 OneBot 帧并分发事件。
func (s *OneBotReverseServer) handleFrame(data []byte) error {
	var envelope oneBotEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return err
	}
	if envelope.Echo != "" {
		// echo 响应只唤醒对应 CallAPI，不进入消息 handler。
		s.resolveCall(envelope)
		return nil
	}
	if envelope.PostType == "meta_event" {
		if selfID := stringifyID(envelope.SelfID); selfID != "" {
			s.setStatus(true, selfID, "")
		}
		return nil
	}
	if envelope.PostType != "message" && envelope.PostType != "notice" {
		// request/meta 等其它事件目前不触发机器人回复。
		return nil
	}

	event := messageEventFromEnvelope(envelope)
	if event.Kind == "" {
		return nil
	}
	if event.SelfID != "" {
		s.setStatus(true, event.SelfID, "")
	}

	s.mu.RLock()
	handler := s.handler
	ctx := s.ctx
	s.mu.RUnlock()
	if handler == nil {
		return nil
	}
	if ctx == nil {
		// 单元测试或异常初始化路径可能没有 Connect context，兜底避免 nil context。
		ctx = context.Background()
	}
	go func() {
		if err := handler(ctx, event); err != nil {
			s.setStatus(s.Status().Connected, s.Status().SelfID, err.Error())
		}
	}()
	return nil
}

// resolveCall 根据 echo 处理反向 API 调用结果。
func (s *OneBotReverseServer) resolveCall(envelope oneBotEnvelope) {
	value, ok := s.pending.Load(envelope.Echo)
	if !ok {
		return
	}
	resultCh, ok := value.(chan callResult)
	if !ok {
		return
	}
	if envelopeStatusOK(envelope) {
		resultCh <- callResult{data: oneBotDataMap(envelope.Data)}
		return
	}
	resultCh <- callResult{err: errors.New(oneBotErrorMessage(envelope))}
}

// setStatus 更新反向 OneBot server 状态。
func (s *OneBotReverseServer) setStatus(connected bool, selfID string, lastError string) {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	s.status = ChannelStatus{
		Connected: connected,
		Endpoint:  s.cfg.Endpoint,
		SelfID:    selfID,
		LastError: lastError,
		UpdatedAt: time.Now(),
	}
}

// authorized 校验反向 WebSocket 请求鉴权。
func (s *OneBotReverseServer) authorized(r *http.Request) bool {
	s.mu.RLock()
	token := s.cfg.AccessToken
	s.mu.RUnlock()
	if token == "" {
		return true
	}
	// 兼容 Authorization Bearer 和 access_token 查询参数两种 NapCat 常见鉴权方式。
	if got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "); got == token {
		return true
	}
	return r.URL.Query().Get("access_token") == token
}
