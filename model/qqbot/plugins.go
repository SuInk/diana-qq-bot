package qqbot

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"diana-qq-bot/model/agent"
	"diana-qq-bot/model/applog"
	"diana-qq-bot/model/llm"
)

type PluginManifest struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	Official    bool     `json:"official"`
	BuiltIn     bool     `json:"built_in"`
	Permissions []string `json:"permissions,omitempty"`
}

type PluginState struct {
	Manifest  PluginManifest `json:"manifest"`
	Installed bool           `json:"installed"`
	Enabled   bool           `json:"enabled"`
}

type PluginRequest struct {
	Event                   MessageEvent    `json:"event"`
	RecentEvents            []MessageEvent  `json:"recent_events,omitempty"`
	RecallEvents            []MessageEvent  `json:"recall_events,omitempty"`
	Text                    string          `json:"text"`
	OwnerID                 string          `json:"owner_id,omitempty"`
	SandboxedBrowserEnabled bool            `json:"sandboxed_browser_enabled,omitempty"`
	Channel                 Channel         `json:"-"`
	LLMStore                LLMProfileStore `json:"-"`
	LLMModelLister          LLMModelLister  `json:"-"`
	AppLogs                 applog.Writer   `json:"-"`
}

type PluginResponse struct {
	Handled             bool              `json:"handled"`
	Context             string            `json:"context,omitempty"`
	Reply               string            `json:"reply,omitempty"`
	ImageURLs           []string          `json:"image_urls,omitempty"`
	ContextImageURLs    []string          `json:"-"`
	VideoURLs           []string          `json:"video_urls,omitempty"`
	Forward             bool              `json:"forward,omitempty"`
	NestedForward       bool              `json:"-"`
	ForwardMessages     []OutgoingMessage `json:"-"`
	Tasks               []PluginTask      `json:"-"`
	RecallDisclosure    bool              `json:"-"`
	RecallEvents        []MessageEvent    `json:"-"`
	RecallReferenceTime int64             `json:"-"`
}

// PluginTask describes work that should outlive the incoming message request.
// A task may call the configured LLM repeatedly and report progress to QQ.
type PluginTask struct {
	Kind           string
	Name           string
	Key            string
	StartedMessage string
	Timeout        time.Duration
	Run            func(context.Context, PluginTaskServices) (PluginTaskResult, error)
}

type PluginTaskResult struct {
	Reply    string
	Messages []OutgoingMessage
}

type PluginTaskProgress struct {
	Phase     string
	Message   string
	Completed int
	Total     int
}

type PluginTaskServices struct {
	Generate func(context.Context, llm.GenerateRequest) (string, error)
	Report   func(PluginTaskProgress)
}

type Plugin interface {
	Manifest() PluginManifest
	Handle(ctx context.Context, req PluginRequest) (*PluginResponse, error)
}

type DirectTriggerPlugin interface {
	ShouldHandle(event MessageEvent, text string) bool
}

type EventObserverPlugin interface {
	Observe(ctx context.Context, event MessageEvent) MessageEvent
}

type PluginManager struct {
	mu      sync.RWMutex
	catalog map[string]Plugin
	states  map[string]PluginState
}

type pluginRuntimeEntry struct {
	id     string
	plugin Plugin
}

var ErrPluginNotFound = errors.New("qqbot: plugin not found")

const (
	resolverPluginID         = "official.nonebot-plugin-resolver-go"
	messageHistoryPluginID   = "official.message-history"
	sandboxedBrowserPluginID = "official.sandboxed-browser-renderer"
)

// NewPluginManager 创建插件管理器并登记插件目录。
func NewPluginManager(plugins ...Plugin) *PluginManager {
	manager := &PluginManager{
		catalog: map[string]Plugin{},
		states:  map[string]PluginState{},
	}
	for _, plugin := range plugins {
		manifest := plugin.Manifest()
		manager.catalog[manifest.ID] = plugin
		// 内置插件默认安装并启用，普通插件后续可以通过安装接口改变状态。
		manager.states[manifest.ID] = PluginState{
			Manifest:  manifest,
			Installed: manifest.BuiltIn,
			Enabled:   manifest.BuiltIn,
		}
	}
	return manager
}

// NewDefaultPluginManager 创建包含官方内置插件的默认插件管理器。
func NewDefaultPluginManager() *PluginManager {
	capabilities := NewCapabilityKnowledgePlugin()
	manager := NewPluginManager(NewMessageHistoryPlugin(), NewResolverPlugin(nil), NewFileParserPlugin(nil), NewLLMConfigPlugin(), NewSandboxedBrowserRenderPlugin(), NewVoiceTTSPlugin(nil), capabilities)
	capabilities.setPluginStateProvider(manager.List)
	return manager
}

func (m *PluginManager) ShouldHandleWithOverrides(event MessageEvent, text string, overrides map[string]bool) bool {
	if m == nil {
		return false
	}
	m.mu.RLock()
	plugins := make([]DirectTriggerPlugin, 0)
	for id, plugin := range m.catalog {
		state := m.states[id]
		enabled := state.Enabled
		if override, ok := overrides[id]; ok {
			enabled = override
		}
		trigger, ok := plugin.(DirectTriggerPlugin)
		if state.Installed && enabled && ok {
			plugins = append(plugins, trigger)
		}
	}
	m.mu.RUnlock()
	for _, plugin := range plugins {
		if plugin.ShouldHandle(event, text) {
			return true
		}
	}
	return false
}

type AgentToolProviderPlugin interface {
	AgentTools() []agent.Tool
}

type LocalMediaSharerAwarePlugin interface {
	SetLocalMediaSharer(LocalMediaSharer)
}

// List 返回所有插件状态。
func (m *PluginManager) List() []PluginState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]PluginState, 0, len(m.states))
	for _, state := range m.states {
		out = append(out, state)
	}
	return out
}

// Get 按 ID 返回单个插件状态。
func (m *PluginManager) Get(id string) (PluginState, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state, ok := m.states[id]
	return state, ok
}

// Enabled 判断指定插件当前是否已安装且启用。
func (m *PluginManager) Enabled(id string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state, ok := m.states[id]
	return ok && state.Installed && state.Enabled
}

func (m *PluginManager) EnabledWithOverrides(id string, overrides map[string]bool) bool {
	if m == nil {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	state, ok := m.states[id]
	if !ok || !state.Installed {
		return false
	}
	enabled := state.Enabled
	if override, ok := overrides[id]; ok {
		enabled = override
	}
	return enabled
}

// Snapshot 返回插件状态快照用于持久化。
func (m *PluginManager) Snapshot() map[string]PluginState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	// 返回副本，避免外部持久化逻辑反向修改 manager 内部状态。
	out := make(map[string]PluginState, len(m.states))
	for id, state := range m.states {
		out[id] = state
	}
	return out
}

// Restore 从持久化状态恢复插件开关。
func (m *PluginManager) Restore(states map[string]PluginState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, plugin := range m.catalog {
		current := m.states[id]
		current.Manifest = plugin.Manifest()
		if saved, ok := states[id]; ok {
			current.Installed = saved.Installed
			current.Enabled = saved.Enabled
		}
		if current.Manifest.BuiltIn {
			// 内置插件不能被彻底卸载，但允许用户在 WebUI 里关闭启用状态。
			current.Installed = true
			if !savedStateDisabled(states, id) {
				current.Enabled = true
			}
		}
		m.states[id] = current
	}
}

// Install 安装并启用指定插件。
func (m *PluginManager) Install(id string) (PluginState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	plugin, ok := m.catalog[id]
	if !ok {
		return PluginState{}, ErrPluginNotFound
	}
	state := m.states[id]
	state.Manifest = plugin.Manifest()
	state.Installed = true
	state.Enabled = true
	m.states[id] = state
	return state, nil
}

// Uninstall 卸载并关闭指定插件。
func (m *PluginManager) Uninstall(id string) (PluginState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	plugin, ok := m.catalog[id]
	if !ok {
		return PluginState{}, ErrPluginNotFound
	}
	state := m.states[id]
	state.Manifest = plugin.Manifest()
	state.Installed = false
	state.Enabled = false
	m.states[id] = state
	return state, nil
}

// SetEnabled 更新指定插件启用状态。
func (m *PluginManager) SetEnabled(id string, enabled bool) (PluginState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	plugin, ok := m.catalog[id]
	if !ok {
		return PluginState{}, ErrPluginNotFound
	}
	state := m.states[id]
	state.Manifest = plugin.Manifest()
	if !state.Installed {
		return state, fmt.Errorf("qqbot: plugin %q is not installed", id)
	}
	state.Enabled = enabled
	m.states[id] = state
	return state, nil
}

// Run 依次执行已安装且启用的插件。
func (m *PluginManager) Run(ctx context.Context, req PluginRequest) []PluginResponse {
	return m.RunWithOverrides(ctx, req, nil)
}

// RunWithOverrides 依次执行插件，并允许调用方按会话覆盖已安装插件的启用状态。
func (m *PluginManager) RunWithOverrides(ctx context.Context, req PluginRequest, overrides map[string]bool) []PluginResponse {
	m.mu.RLock()
	plugins := make([]pluginRuntimeEntry, 0, len(m.catalog))
	for id, plugin := range m.catalog {
		state := m.states[id]
		enabled := state.Enabled
		if override, ok := overrides[id]; ok {
			enabled = override
		}
		if state.Installed && enabled {
			plugins = append(plugins, pluginRuntimeEntry{id: id, plugin: plugin})
		}
	}
	m.mu.RUnlock()

	responses := make([]PluginResponse, 0, len(plugins))
	for _, entry := range plugins {
		resp, err := safeHandlePlugin(ctx, entry, req)
		if err != nil || resp == nil || !resp.Handled {
			// 插件失败或未处理不打断主回复链路，运行时会继续调用其它插件/LLM。
			continue
		}
		responses = append(responses, *resp)
	}
	return responses
}

func (m *PluginManager) RunOneWithOverrides(ctx context.Context, id string, req PluginRequest, overrides map[string]bool) (*PluginResponse, error) {
	if m == nil {
		return nil, nil
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, nil
	}
	m.mu.RLock()
	plugin, ok := m.catalog[id]
	state := m.states[id]
	enabled := state.Enabled
	if override, ok := overrides[id]; ok {
		enabled = override
	}
	if !ok || !state.Installed || !enabled {
		m.mu.RUnlock()
		return nil, nil
	}
	m.mu.RUnlock()

	resp, err := safeHandlePlugin(ctx, pluginRuntimeEntry{id: id, plugin: plugin}, req)
	if err != nil || resp == nil || !resp.Handled {
		return nil, err
	}
	return resp, nil
}

// AgentToolsWithOverrides returns tools supplied by enabled plugins for this conversation.
func (m *PluginManager) AgentToolsWithOverrides(overrides map[string]bool) []agent.Tool {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	providers := make([]AgentToolProviderPlugin, 0)
	for id, plugin := range m.catalog {
		state := m.states[id]
		enabled := state.Enabled
		if override, ok := overrides[id]; ok {
			enabled = override
		}
		provider, ok := plugin.(AgentToolProviderPlugin)
		if state.Installed && enabled && ok {
			providers = append(providers, provider)
		}
	}
	m.mu.RUnlock()
	var tools []agent.Tool
	for _, provider := range providers {
		tools = append(tools, provider.AgentTools()...)
	}
	return tools
}

func (m *PluginManager) SetLocalMediaSharer(sharer LocalMediaSharer) {
	if m == nil {
		return
	}
	m.mu.RLock()
	plugins := make([]LocalMediaSharerAwarePlugin, 0)
	for _, plugin := range m.catalog {
		if aware, ok := plugin.(LocalMediaSharerAwarePlugin); ok {
			plugins = append(plugins, aware)
		}
	}
	m.mu.RUnlock()
	for _, plugin := range plugins {
		plugin.SetLocalMediaSharer(sharer)
	}
}

func (m *PluginManager) ObserveEvent(ctx context.Context, event MessageEvent) MessageEvent {
	if m == nil {
		return event
	}
	m.mu.RLock()
	plugins := make([]pluginRuntimeEntry, 0, len(m.catalog))
	for id, plugin := range m.catalog {
		state := m.states[id]
		if state.Installed && state.Enabled {
			plugins = append(plugins, pluginRuntimeEntry{id: id, plugin: plugin})
		}
	}
	m.mu.RUnlock()

	for _, entry := range plugins {
		event = safeObservePlugin(ctx, entry, event)
	}
	return event
}

func safeHandlePlugin(ctx context.Context, entry pluginRuntimeEntry, req PluginRequest) (resp *PluginResponse, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			resp = nil
			err = fmt.Errorf("qqbot: plugin %q panicked: %v", entry.id, recovered)
		}
	}()
	return entry.plugin.Handle(ctx, req)
}

func safeObservePlugin(ctx context.Context, entry pluginRuntimeEntry, event MessageEvent) (out MessageEvent) {
	out = event
	defer func() {
		if recover() != nil {
			out = event
		}
	}()
	observer, ok := entry.plugin.(EventObserverPlugin)
	if !ok {
		return event
	}
	return observer.Observe(ctx, event)
}

// savedStateDisabled 判断保存状态里插件是否被显式关闭。
func savedStateDisabled(states map[string]PluginState, id string) bool {
	state, ok := states[id]
	return ok && !state.Enabled
}

type ResolverPlugin struct {
	client          *http.Client
	videoDownloader func(context.Context, string) string
}

// NewResolverPlugin 创建官方内置链接解析插件。
func NewResolverPlugin(client *http.Client) *ResolverPlugin {
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	return &ResolverPlugin{client: client, videoDownloader: downloadPlatformVideoFile}
}

// Manifest 返回链接解析插件清单。
func (p *ResolverPlugin) Manifest() PluginManifest {
	return PluginManifest{
		ID:          resolverPluginID,
		Name:        "nonebot-plugin-resolver Go",
		Version:     "0.1.0",
		Description: "官方内置 Go 版链接解析插件，兼容 QQ/NapCat 场景下常见 B 站、YouTube、X、小红书、抖音等链接上下文。",
		Official:    true,
		BuiltIn:     true,
		Permissions: []string{"network:http", "message:read"},
	}
}

// Handle 解析消息中的链接并生成上下文。
func (p *ResolverPlugin) Handle(ctx context.Context, req PluginRequest) (*PluginResponse, error) {
	urls := extractResolverRequestURLs(req)
	if len(urls) == 0 {
		return nil, nil
	}

	parts := make([]string, 0, len(urls))
	forwardMessages := make([]OutgoingMessage, 0, len(urls)*2)
	imageURLs := make([]string, 0)
	videoURLs := make([]string, 0, 1)
	for _, raw := range urls {
		if isKnownResolverPlatformURL(raw) {
			result := p.resolveKnownPlatform(ctx, req, raw)
			if result.Context != "" {
				parts = append(parts, result.Context)
			}
			forwardMessages = append(forwardMessages, result.ForwardMessages...)
			imageURLs = append(imageURLs, result.ImageURLs...)
			videoURLs = append(videoURLs, result.VideoURLs...)
			continue
		}
		if req.SandboxedBrowserEnabled {
			// 普通网页交给沙盒浏览器，避免同一 URL 再走一次无 JavaScript 的 HTTP 抓取。
			continue
		}
		info := resolveURL(ctx, p.client, raw)
		parts = append(parts, info)
	}
	contextText := strings.Join(nonEmptyStrings(parts), "\n")
	if contextText == "" {
		contextText = resolverForwardMessagesContext(forwardMessages)
	}
	return &PluginResponse{
		Handled:         true,
		Context:         contextText,
		ImageURLs:       imageURLs,
		VideoURLs:       videoURLs,
		Forward:         len(forwardMessages) > 0,
		ForwardMessages: forwardMessages,
	}, nil
}

type resolverPlatformResult struct {
	Context         string
	ImageURLs       []string
	VideoURLs       []string
	ForwardMessages []OutgoingMessage
}

func (p *ResolverPlugin) resolveKnownPlatform(ctx context.Context, req PluginRequest, raw string) resolverPlatformResult {
	raw = normalizeResolverURL(raw)
	switch {
	case isBilibiliURL(raw):
		return p.resolveBilibili(ctx, req, raw)
	case isDouyinURL(raw):
		return p.resolveDouyin(ctx, req, raw)
	case isXiaohongshuURL(raw):
		return p.resolveXiaohongshu(ctx, req, raw)
	case isTwitterURL(raw):
		return p.resolveTwitter(ctx, req, raw)
	case isYouTubeURL(raw):
		return p.resolveYouTube(ctx, req, raw)
	default:
		info := resolveURL(ctx, p.client, raw)
		return resolverPlatformTextResult(info)
	}
}

func resolverPlatformTextResult(text string) resolverPlatformResult {
	text = strings.TrimSpace(text)
	if text == "" {
		return resolverPlatformResult{}
	}
	return resolverPlatformResult{
		Context:         text,
		ForwardMessages: []OutgoingMessage{{Text: text}},
	}
}

func (p *ResolverPlugin) resolveBilibili(ctx context.Context, req PluginRequest, raw string) resolverPlatformResult {
	nickname := resolverNickname()
	view, ok := fetchBilibiliView(ctx, raw)
	if !ok {
		text := fmt.Sprintf("%s识别：B站，出错，无法获取数据！", nickname)
		return resolverPlatformTextResult(text)
	}
	videoTitle := deleteResolverBoringCharacters(firstNonEmpty(view.Data.Title, fetchHTMLTitle(ctx, p.client, raw)))
	if videoTitle == "" {
		videoTitle = "出错，无法获取数据！"
	}
	videoDesc := strings.TrimSpace(view.Data.Desc)
	text := fmt.Sprintf("\n%s识别：B站，%s", nickname, videoTitle)
	if extra := extraBiliInfoText(view); extra != "" {
		text += "\n" + extra
	}
	if videoDesc != "" {
		text += "\n📝 简介：" + videoDesc
	}
	duration := view.Data.Duration
	if duration > resolverVideoMaxDuration() {
		text += fmt.Sprintf("\n---------\n⚠️ 当前视频时长 %d 分钟，超过管理员设置的最长时间 %d 分钟！", duration/60, resolverVideoMaxDuration()/60)
		return resolverPlatformResult{
			Context:         strings.TrimSpace(text),
			ImageURLs:       singleNonEmptyString(view.Data.Pic),
			ForwardMessages: []OutgoingMessage{{Text: text, ImageURLs: singleNonEmptyString(view.Data.Pic), ImagesFirst: true}},
		}
	}
	videoPath := p.downloadResolverVideo(ctx, req, raw)
	nodes := []OutgoingMessage{{Text: text, ImageURLs: singleNonEmptyString(view.Data.Pic), ImagesFirst: true}}
	if videoPath == "" {
		nodes = append(nodes, OutgoingMessage{Text: fmt.Sprintf("%s识别：B站，媒体下载失败", nickname)})
		return resolverPlatformResult{
			Context:         strings.TrimSpace(text),
			ImageURLs:       singleNonEmptyString(view.Data.Pic),
			ForwardMessages: nodes,
		}
	}
	nodes = append(nodes, OutgoingMessage{VideoURLs: []string{videoPath}})
	return resolverPlatformResult{
		Context:         strings.TrimSpace(text),
		ImageURLs:       singleNonEmptyString(view.Data.Pic),
		VideoURLs:       []string{videoPath},
		ForwardMessages: nodes,
	}
}

func (p *ResolverPlugin) resolveDouyin(ctx context.Context, req PluginRequest, raw string) resolverPlatformResult {
	nickname := resolverNickname()
	detail, ok, status := fetchDouyinDetail(ctx, raw)
	if status == "missing_cookie" {
		return resolverPlatformTextResult(fmt.Sprintf("%s识别：抖音，无法获取到管理员设置的抖音ck！", nickname))
	}
	if !ok {
		return resolverPlatformTextResult(fmt.Sprintf("%s识别：抖音，解析失败！", nickname))
	}
	desc := strings.TrimSpace(detail.Desc)
	text := fmt.Sprintf("%s识别：抖音，%s", nickname, desc)
	if strings.TrimSpace(text) == nickname+"识别：抖音，" {
		text = fmt.Sprintf("%s识别：抖音", nickname)
	}
	if resolverDouyinURLType(detail.AwemeType) == "image" {
		imageURLs := douyinImageURLs(detail)
		nodes := []OutgoingMessage{{Text: text}}
		for _, imageURL := range imageURLs {
			nodes = append(nodes, OutgoingMessage{ImageURLs: []string{imageURL}})
		}
		return resolverPlatformResult{
			Context:         strings.TrimSpace(text),
			ImageURLs:       imageURLs,
			ForwardMessages: nodes,
		}
	}
	cover := firstNonEmptyString(detail.Video.Cover.URLList)
	metaText := "\n" + text
	nodes := []OutgoingMessage{{Text: metaText, ImageURLs: singleNonEmptyString(cover), ImagesFirst: true}}
	videoPath := p.downloadResolverVideo(ctx, req, raw)
	if videoPath == "" {
		nodes = append(nodes, OutgoingMessage{Text: fmt.Sprintf("%s识别：抖音，视频下载失败，已停止转发。", nickname)})
		return resolverPlatformResult{
			Context:         strings.TrimSpace(metaText),
			ImageURLs:       singleNonEmptyString(cover),
			ForwardMessages: nodes,
		}
	}
	nodes = append(nodes, OutgoingMessage{VideoURLs: []string{videoPath}})
	return resolverPlatformResult{
		Context:         strings.TrimSpace(metaText),
		ImageURLs:       singleNonEmptyString(cover),
		VideoURLs:       []string{videoPath},
		ForwardMessages: nodes,
	}
}

func (p *ResolverPlugin) resolveXiaohongshu(ctx context.Context, req PluginRequest, raw string) resolverPlatformResult {
	nickname := resolverNickname()
	note, status := fetchXiaohongshuNote(ctx, raw)
	switch status {
	case "missing_cookie":
		return resolverPlatformTextResult(fmt.Sprintf("%s识别内容来自：【小红书】\n无法获取到管理员设置的小红书ck！", nickname))
	case "expired_link":
		return resolverPlatformTextResult(fmt.Sprintf("%s识别内容来自：【小红书】\n分享链接已失效，或者对应直播已经结束。", nickname))
	case "live_link":
		return resolverPlatformTextResult(fmt.Sprintf("%s识别内容来自：【小红书】\n这是小红书直播链接，不是普通笔记；将继续尝试用沙盒浏览器读取直播页面。", nickname))
	case "unsupported_link":
		return resolverPlatformTextResult(fmt.Sprintf("%s识别内容来自：【小红书】\n该链接不是可识别的普通笔记链接。", nickname))
	case "note_unavailable":
		return resolverPlatformTextResult(fmt.Sprintf("%s识别内容来自：【小红书】\n笔记不存在、已删除，或当前分享参数已经过期。", nickname))
	case "page_unavailable", "request_failed":
		return resolverPlatformTextResult(fmt.Sprintf("%s识别内容来自：【小红书】\n页面暂时无法读取，不能据此判断ck已经失效。", nickname))
	}
	if len(note) == 0 {
		return resolverPlatformTextResult(fmt.Sprintf("%s识别内容来自：【小红书】\n没有读取到笔记内容，但不能据此判断ck已经失效。", nickname))
	}
	metaText := xiaohongshuMetaText(nickname, note)
	if strings.TrimSpace(anyString(note["type"])) == "normal" {
		imageURLs := xiaohongshuImageURLs(note)
		nodes := []OutgoingMessage{{Text: metaText}}
		for _, imageURL := range imageURLs {
			nodes = append(nodes, OutgoingMessage{ImageURLs: []string{imageURL}})
		}
		return resolverPlatformResult{
			Context:         metaText,
			ImageURLs:       imageURLs,
			ForwardMessages: nodes,
		}
	}
	if strings.TrimSpace(anyString(note["type"])) == "video" {
		cover := firstNonEmptyString(xiaohongshuImageURLs(note))
		videoPath := p.downloadResolverVideo(ctx, req, raw)
		if videoPath == "" {
			return resolverPlatformTextResult(fmt.Sprintf("%s识别内容来自：【小红书】\n视频直链均不可用，暂时无法发送视频。", nickname))
		}
		nodes := []OutgoingMessage{
			{Text: "\n" + metaText, ImageURLs: singleNonEmptyString(cover), ImagesFirst: true},
			{VideoURLs: []string{videoPath}},
		}
		return resolverPlatformResult{
			Context:         metaText,
			ImageURLs:       singleNonEmptyString(cover),
			VideoURLs:       []string{videoPath},
			ForwardMessages: nodes,
		}
	}
	return resolverPlatformTextResult(metaText)
}

func (p *ResolverPlugin) resolveTwitter(ctx context.Context, req PluginRequest, raw string) resolverPlatformResult {
	nickname := resolverNickname()
	metaText := fmt.Sprintf("%s识别：小蓝鸟学习版", nickname)
	videoPath := p.downloadResolverVideo(ctx, req, raw)
	if videoPath != "" {
		return resolverPlatformResult{
			Context:         metaText,
			VideoURLs:       []string{videoPath},
			ForwardMessages: []OutgoingMessage{{Text: metaText}, {VideoURLs: []string{videoPath}}},
		}
	}
	mediaURL := fetchTwitterMediaURL(ctx, raw)
	if resolverMediaURLIsImage(mediaURL) {
		return resolverPlatformResult{
			Context:         metaText,
			ImageURLs:       []string{mediaURL},
			ForwardMessages: []OutgoingMessage{{Text: metaText}, {ImageURLs: []string{mediaURL}}},
		}
	}
	return resolverPlatformTextResult(fmt.Sprintf("%s识别：小蓝鸟学习版\n媒体下载失败，可能是代理不可用、解析源失效或媒体链接被限制。", nickname))
}

func (p *ResolverPlugin) resolveYouTube(ctx context.Context, req PluginRequest, raw string) resolverPlatformResult {
	nickname := resolverNickname()
	title := ""
	if info, ok := ytdlpDumpInfo(ctx, raw); ok {
		title = strings.TrimSpace(info.Title)
	}
	if title == "" {
		title = fetchHTMLTitle(ctx, p.client, raw)
	}
	text := fmt.Sprintf("%s识别：油管，%s\n", nickname, title)
	nodes := []OutgoingMessage{{Text: text}}
	videoPath := p.downloadResolverVideo(ctx, req, raw)
	if videoPath != "" {
		nodes = append(nodes, OutgoingMessage{VideoURLs: []string{videoPath}})
		return resolverPlatformResult{
			Context:         strings.TrimSpace(text),
			VideoURLs:       []string{videoPath},
			ForwardMessages: nodes,
		}
	}
	return resolverPlatformResult{
		Context:         strings.TrimSpace(text),
		ForwardMessages: nodes,
	}
}

func (p *ResolverPlugin) downloadResolverVideo(ctx context.Context, req PluginRequest, raw string) string {
	downloadVideo := p.videoDownloader
	if downloadVideo == nil {
		downloadVideo = downloadPlatformVideoFile
	}
	videoPath := downloadVideo(ctx, raw)
	recordResolverVideoLog(ctx, req, raw, videoPath)
	return videoPath
}

type douyinAwemeDetail struct {
	Desc      string `json:"desc"`
	AwemeType int    `json:"aweme_type"`
	Video     struct {
		Cover    douyinURLSet `json:"cover"`
		PlayAddr douyinURLSet `json:"play_addr"`
	} `json:"video"`
	Images []struct {
		URLList []string `json:"url_list"`
	} `json:"images"`
}

type douyinURLSet struct {
	URI     string   `json:"uri"`
	URLList []string `json:"url_list"`
}

func fetchDouyinDetail(ctx context.Context, raw string) (douyinAwemeDetail, bool, string) {
	cookie := strings.TrimSpace(firstNonEmpty(os.Getenv("DIANA_DOUYIN_CK"), os.Getenv("DOUYIN_CK"), os.Getenv("douyin_ck")))
	if cookie == "" {
		return douyinAwemeDetail{}, false, "missing_cookie"
	}
	pageURL := fetchFinalURL(ctx, raw, resolverCommonHeaders())
	if pageURL == "" {
		pageURL = raw
	}
	match := douyinIDPattern.FindStringSubmatch(pageURL)
	if len(match) < 2 {
		return douyinAwemeDetail{}, false, ""
	}
	awemeID := match[1]
	headers := resolverCommonHeaders()
	headers["Accept-Language"] = "zh-CN,zh;q=0.8,zh-TW;q=0.7,zh-HK;q=0.5,en-US;q=0.3,en;q=0.2"
	headers["Referer"] = "https://www.douyin.com/video/" + awemeID
	headers["Cookie"] = cookie
	apiURL := fmt.Sprintf(douyinVideoAPI, awemeID)
	if bogus := generateDouyinABogus(ctx, apiURL, headers["User-Agent"]); bogus != "" {
		apiURL += "&a_bogus=" + url.QueryEscape(bogus)
	}
	var resp struct {
		AwemeDetail douyinAwemeDetail `json:"aweme_detail"`
	}
	if !fetchResolverJSON(ctx, apiURL, headers, &resp) {
		return douyinAwemeDetail{}, false, ""
	}
	return resp.AwemeDetail, true, ""
}

func resolverDouyinURLType(code int) string {
	switch code {
	case 2, 68, 150:
		return "image"
	default:
		return "video"
	}
}

func douyinImageURLs(detail douyinAwemeDetail) []string {
	out := make([]string, 0, len(detail.Images))
	seen := map[string]bool{}
	for _, image := range detail.Images {
		imageURL := firstNonEmptyString(image.URLList)
		if imageURL == "" || seen[imageURL] {
			continue
		}
		seen[imageURL] = true
		out = append(out, imageURL)
	}
	return out
}

func fetchTwitterMediaURL(ctx context.Context, raw string) string {
	apiURL := fmt.Sprintf(twitterResolverAPI, url.QueryEscape(raw))
	headers := resolverCommonHeaders()
	headers["Accept"] = "ext/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"
	headers["Accept-Encoding"] = "gzip, deflate"
	headers["Accept-Language"] = "zh-CN,zh;q=0.9"
	headers["Host"] = "47.99.158.118"
	headers["Proxy-Connection"] = "keep-alive"
	headers["Upgrade-Insecure-Requests"] = "1"
	headers["Sec-Fetch-User"] = "?1"
	var resp struct {
		Data struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	if !fetchResolverJSON(ctx, apiURL, headers, &resp) {
		return ""
	}
	return strings.TrimSpace(resp.Data.URL)
}

func resolverMediaURLIsImage(raw string) bool {
	lower := strings.ToLower(strings.TrimSpace(raw))
	return strings.HasSuffix(lower, ".jpg") ||
		strings.HasSuffix(lower, ".jpeg") ||
		strings.HasSuffix(lower, ".png") ||
		strings.HasSuffix(lower, ".webp")
}

func xiaohongshuMetaText(nickname string, note map[string]any) string {
	user, _ := note["user"].(map[string]any)
	return fmt.Sprintf("%s识别内容来自：【小红书】\n作者：%s\n标题：%s\n内容：%s",
		nickname,
		anyString(user["nickname"]),
		anyString(note["title"]),
		anyString(note["desc"]),
	)
}

func xiaohongshuImageURLs(note map[string]any) []string {
	items, _ := note["imageList"].([]any)
	out := make([]string, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		image, _ := item.(map[string]any)
		imageURL := strings.TrimSpace(anyString(image["urlDefault"]))
		if imageURL == "" || seen[imageURL] {
			continue
		}
		seen[imageURL] = true
		out = append(out, imageURL)
	}
	return out
}

func extraBiliInfoText(view bilibiliViewResponse) string {
	lines := make([]string, 0, 3)
	if owner := strings.TrimSpace(view.Data.Owner.Name); owner != "" {
		lines = append(lines, "UP主："+owner)
	}
	stats := make([]string, 0, 4)
	if view.Data.Stat.View > 0 {
		stats = append(stats, fmt.Sprintf("播放：%d", view.Data.Stat.View))
	}
	if view.Data.Stat.Like > 0 {
		stats = append(stats, fmt.Sprintf("点赞：%d", view.Data.Stat.Like))
	}
	if view.Data.Stat.Coin > 0 {
		stats = append(stats, fmt.Sprintf("投币：%d", view.Data.Stat.Coin))
	}
	if view.Data.Stat.Favorite > 0 {
		stats = append(stats, fmt.Sprintf("收藏：%d", view.Data.Stat.Favorite))
	}
	if len(stats) > 0 {
		lines = append(lines, strings.Join(stats, "，"))
	}
	return strings.Join(lines, "\n")
}

func deleteResolverBoringCharacters(text string) string {
	replacer := strings.NewReplacer("/", " ", "\\", " ", ":", " ", "*", " ", "?", " ", "\"", " ", "<", " ", ">", " ", "|", " ")
	return compactWhitespace(replacer.Replace(text))
}

func resolverNickname() string {
	return firstNonEmpty(
		strings.TrimSpace(os.Getenv("DIANA_RESOLVER_NICKNAME")),
		strings.TrimSpace(os.Getenv("R_GLOBAL_NICKNAME")),
	)
}

func resolverForwardMessagesContext(messages []OutgoingMessage) string {
	parts := make([]string, 0, len(messages))
	for _, msg := range messages {
		text := strings.TrimSpace(msg.Text)
		if text != "" {
			parts = append(parts, text)
			continue
		}
		if len(msg.ImageURLs) > 0 {
			parts = append(parts, "[图片]")
			continue
		}
		if len(msg.VideoURLs) > 0 {
			parts = append(parts, "[视频]")
		}
	}
	return strings.Join(nonEmptyStrings(parts), "\n")
}

func nonEmptyStrings(values []string) []string {
	out := values[:0]
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func singleNonEmptyString(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return []string{value}
}

func firstNonEmptyString(values []string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func recordResolverVideoLog(ctx context.Context, req PluginRequest, raw string, videoPath string) {
	if req.AppLogs == nil || strings.TrimSpace(videoPath) == "" {
		return
	}
	metadata := map[string]any{
		"user_id":    req.Event.UserID,
		"kind":       string(req.Event.Kind),
		"url":        raw,
		"video_path": videoPath,
	}
	if req.Event.GroupID != "" {
		metadata["group_id"] = req.Event.GroupID
	}
	_ = req.AppLogs.AppendLog(ctx, applog.Entry{
		Kind:     applog.KindOperation,
		Level:    applog.LevelInfo,
		Action:   "qqbot.resolver.video_download",
		Message:  "链接解析插件已下载视频",
		Actor:    qqEventActor(req.Event),
		Target:   raw,
		Metadata: metadata,
	})
}

var urlPattern = regexp.MustCompile(`https?://[^\s<>"'，。！？、]+`)

func extractResolverRequestURLs(req PluginRequest) []string {
	return dedupeURLs(append(append(
		extractURLs(req.Text),
		extractURLs(req.Event.RawMessage)...,
	), extractURLs(PlainText(req.Event.Segments))...))
}

// extractURLs 从消息文本中提取并去重 URL。
func extractURLs(text string) []string {
	var out []string
	for _, candidate := range resolverURLTextVariants(text) {
		matches := urlPattern.FindAllString(candidate, -1)
		for _, match := range matches {
			// QQ 消息里的链接常贴着中文标点，解析前去掉尾部标点并做去重。
			match = strings.TrimRight(match, ".,;:!?)]}\\")
			match = normalizeResolverURL(match)
			if match == "" {
				continue
			}
			out = append(out, match)
		}
	}
	return dedupeURLs(out)
}

func resolverURLTextVariants(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	var variants []string
	appendVariant := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		for _, existing := range variants {
			if existing == value {
				return
			}
		}
		variants = append(variants, value)
	}

	appendVariant(text)
	current := text
	for i := 0; i < 3; i++ {
		next := html.UnescapeString(current)
		if next == current {
			break
		}
		appendVariant(next)
		current = next
	}
	baseLen := len(variants)
	for i := 0; i < baseLen; i++ {
		appendVariant(decodeResolverEscapedURLText(variants[i]))
	}
	return variants
}

func decodeResolverEscapedURLText(text string) string {
	return strings.NewReplacer(
		`\/`, `/`,
		`\u002f`, `/`,
		`\u002F`, `/`,
		`\\u002f`, `/`,
		`\\u002F`, `/`,
	).Replace(text)
}

func dedupeURLs(urls []string) []string {
	out := make([]string, 0, len(urls))
	seen := map[string]struct{}{}
	for _, raw := range urls {
		raw = normalizeResolverURL(raw)
		if raw == "" {
			continue
		}
		key := resolverURLDedupeKey(raw)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, raw)
	}
	return out
}

func normalizeResolverURL(raw string) string {
	raw = strings.TrimSpace(raw)
	for i := 0; i < 3; i++ {
		next := html.UnescapeString(raw)
		if next == raw {
			break
		}
		raw = next
	}
	raw = decodeResolverEscapedURLText(raw)
	raw = strings.TrimRight(raw, ".,;:!?)]}\\")
	return raw
}

func resolverURLDedupeKey(raw string) string {
	raw = normalizeResolverURL(raw)
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	parsed.Fragment = ""
	return parsed.String()
}

func knownResolverPlatformURLs(text string) []string {
	urls := extractURLs(text)
	out := make([]string, 0, len(urls))
	for _, raw := range urls {
		if isKnownResolverPlatformURL(raw) {
			out = append(out, raw)
		}
	}
	return out
}

func isKnownResolverPlatformURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return isKnownResolverPlatformHost(parsed.Hostname())
}

func isKnownResolverPlatformHost(host string) bool {
	host = strings.ToLower(strings.TrimPrefix(host, "www."))
	switch {
	case strings.Contains(host, "bilibili.com") || host == "b23.tv" || host == "bili2233.cn":
		return true
	case strings.Contains(host, "youtube.com") || host == "youtu.be":
		return true
	case strings.Contains(host, "x.com") || strings.Contains(host, "twitter.com"):
		return true
	case strings.Contains(host, "xiaohongshu.com") || host == "xhslink.com":
		return true
	case strings.Contains(host, "douyin.com"):
		return true
	default:
		return false
	}
}

func isYouTubeURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return strings.Contains(raw, "youtube.com") || strings.Contains(raw, "youtu.be")
	}
	host := strings.ToLower(strings.TrimPrefix(parsed.Hostname(), "www."))
	return strings.Contains(host, "youtube.com") || host == "youtu.be"
}

// resolveURL 获取链接平台和标题摘要。
func resolveURL(ctx context.Context, client *http.Client, raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "- " + raw
	}
	platform := platformName(parsed.Hostname())
	title := fetchHTMLTitle(ctx, client, raw)
	if title == "" {
		return fmt.Sprintf("- [%s] %s", platform, raw)
	}
	return fmt.Sprintf("- [%s] %s\n  标题：%s", platform, raw, title)
}

// platformName 根据域名识别常见平台名称。
func platformName(host string) string {
	host = strings.ToLower(strings.TrimPrefix(host, "www."))
	switch {
	case strings.Contains(host, "bilibili.com") || host == "b23.tv" || host == "bili2233.cn":
		return "Bilibili"
	case strings.Contains(host, "youtube.com") || host == "youtu.be":
		return "YouTube"
	case strings.Contains(host, "x.com") || strings.Contains(host, "twitter.com"):
		return "X"
	case strings.Contains(host, "xiaohongshu.com") || host == "xhslink.com":
		return "小红书"
	case strings.Contains(host, "douyin.com"):
		return "抖音"
	case strings.Contains(host, "github.com"):
		return "GitHub"
	default:
		return host
	}
}

// fetchHTMLTitle 读取网页标题用于链接解析上下文。
func fetchHTMLTitle(ctx context.Context, client *http.Client, raw string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "DianaQQBot/0.1 (+https://github.com)")
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return ""
	}
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if contentType != "" && !strings.Contains(contentType, "html") && !strings.Contains(contentType, "text") {
		return ""
	}
	// 只读前 256KB 足够拿 title，避免链接解析下载大页面或大文件。
	buf := make([]byte, 256*1024)
	n, _ := resp.Body.Read(buf)
	title := extractHTMLTitle(string(buf[:n]))
	return compactWhitespace(title)
}

var titlePattern = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

// extractHTMLTitle 从 HTML 片段中提取 title。
func extractHTMLTitle(html string) string {
	match := titlePattern.FindStringSubmatch(html)
	if len(match) < 2 {
		return ""
	}
	title := match[1]
	replacer := strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'")
	return replacer.Replace(title)
}

// compactWhitespace 压缩文本中的连续空白。
func compactWhitespace(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
