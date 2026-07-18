package qqbot

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"diana-qq-bot/model/agent"
)

const capabilityKnowledgePluginID = "official.capability-knowledge-rag"

type CapabilityKnowledgePlugin struct {
	mu            sync.RWMutex
	stateProvider func() []PluginState
}

type dianaCapabilitiesTool struct {
	plugin *CapabilityKnowledgePlugin
}

type capabilityDocument struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Content  string `json:"content"`
	Source   string `json:"source"`
	Enabled  bool   `json:"enabled"`
	Required string `json:"required_relationship,omitempty"`
}

type capabilitySearchHit struct {
	capabilityDocument
	Score float64 `json:"score"`
}

func NewCapabilityKnowledgePlugin() *CapabilityKnowledgePlugin {
	return &CapabilityKnowledgePlugin{}
}

func (p *CapabilityKnowledgePlugin) Manifest() PluginManifest {
	return PluginManifest{
		ID:          capabilityKnowledgePluginID,
		Name:        "Diana 能力知识库 RAG",
		Version:     "0.1.0",
		Description: "索引 Diana 核心能力和实时插件清单，通过本地稀疏检索向 Agent 提供与问题相关的能力说明。",
		Official:    true,
		BuiltIn:     true,
		Permissions: []string{"agent:tool", "knowledge:read", "plugin:list"},
	}
}

func (p *CapabilityKnowledgePlugin) Handle(context.Context, PluginRequest) (*PluginResponse, error) {
	return nil, nil
}

func (p *CapabilityKnowledgePlugin) AgentTools() []agent.Tool {
	return []agent.Tool{&dianaCapabilitiesTool{plugin: p}}
}

func (p *CapabilityKnowledgePlugin) setPluginStateProvider(provider func() []PluginState) {
	p.mu.Lock()
	p.stateProvider = provider
	p.mu.Unlock()
}

func (p *CapabilityKnowledgePlugin) documents() []capabilityDocument {
	documents := append([]capabilityDocument(nil), coreCapabilityDocuments...)
	p.mu.RLock()
	provider := p.stateProvider
	p.mu.RUnlock()
	if provider == nil {
		return documents
	}
	for _, state := range provider() {
		documents = append(documents, capabilityDocument{
			ID:      "plugin:" + state.Manifest.ID,
			Title:   state.Manifest.Name,
			Content: fmt.Sprintf("插件 %s，版本 %s。%s。权限：%s。安装=%t，启用=%t。", state.Manifest.ID, state.Manifest.Version, state.Manifest.Description, strings.Join(state.Manifest.Permissions, "、"), state.Installed, state.Enabled),
			Source:  "plugin_manifest",
			Enabled: state.Installed && state.Enabled,
		})
	}
	return documents
}

func (t *dianaCapabilitiesTool) Name() string {
	return "diana.capabilities"
}

func (t *dianaCapabilitiesTool) Description() string {
	return `从 Diana 自身能力知识库检索相关能力、工具、权限门槛和实时插件状态。用户询问“你会什么”“能否处理某事”“哪个插件负责某功能”或质疑机器人能力时必须先调用本工具，不要凭提示词记忆猜测。input: {"query":"用户关于能力的问题","limit":"可选，默认 5，最大 8"}`
}

func (t *dianaCapabilitiesTool) Run(_ context.Context, input map[string]any) (string, error) {
	if t == nil || t.plugin == nil {
		return "", fmt.Errorf("能力知识库未配置")
	}
	query := strings.TrimSpace(configToolString(input, "query"))
	if query == "" {
		return "", fmt.Errorf("query 不能为空")
	}
	limit := 5
	if raw := strings.TrimSpace(configToolString(input, "limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if limit > 8 {
		limit = 8
	}
	hits := retrieveCapabilityDocuments(query, t.plugin.documents(), limit)
	body, err := json.MarshalIndent(map[string]any{
		"ok":      true,
		"action":  "retrieved",
		"query":   query,
		"message": fmt.Sprintf("能力知识库检索到 %d 条相关结果。请结合当前用户关系权限回答，不要把未解锁能力说成可直接使用。", len(hits)),
		"items":   hits,
	}, "", "  ")
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func retrieveCapabilityDocuments(query string, documents []capabilityDocument, limit int) []capabilitySearchHit {
	queryTerms := capabilityTerms(query)
	hits := make([]capabilitySearchHit, 0, len(documents))
	for _, document := range documents {
		titleTerms := capabilityTerms(document.Title)
		contentTerms := capabilityTerms(document.Content)
		score := 0.0
		for term, queryWeight := range queryTerms {
			if weight := titleTerms[term]; weight > 0 {
				score += queryWeight * weight * 3
			}
			if weight := contentTerms[term]; weight > 0 {
				score += queryWeight * weight
			}
		}
		if score > 0 {
			hits = append(hits, capabilitySearchHit{capabilityDocument: document, Score: score})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].ID < hits[j].ID
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

var capabilityASCIIToken = regexp.MustCompile(`[a-z0-9._:/-]+`)

func capabilityTerms(text string) map[string]float64 {
	text = strings.ToLower(text)
	terms := map[string]float64{}
	for _, token := range capabilityASCIIToken.FindAllString(text, -1) {
		if len(token) >= 2 {
			terms[token] = 2
		}
	}
	runes := []rune(text)
	for index, current := range runes {
		if !unicode.Is(unicode.Han, current) {
			continue
		}
		terms[string(current)] = 0.2
		if index+1 < len(runes) && unicode.Is(unicode.Han, runes[index+1]) {
			terms[string(runes[index:index+2])] = 1
		}
		if index+2 < len(runes) && unicode.Is(unicode.Han, runes[index+1]) && unicode.Is(unicode.Han, runes[index+2]) {
			terms[string(runes[index:index+3])] = 1.5
		}
	}
	return terms
}

var coreCapabilityDocuments = []capabilityDocument{
	{ID: "core:web-search", Title: "实时联网搜索", Content: "可使用 web_search.search 通过多配置失败回退检索实时新闻、IPO 时间、价格和网页资料，并打开结果继续核验。", Source: "core", Enabled: true},
	{ID: "core:browser", Title: "网页浏览与渲染", Content: "可用沙盒无头浏览器执行 JavaScript、跟随跳转、读取动态网页；主人还可使用浏览器和本地工具。", Source: "core", Enabled: true},
	{ID: "core:media", Title: "图片视频与链接解析", Content: "能理解 QQ 图片上下文，下载并抽取视频多帧；链接解析插件支持 B站、YouTube、X、小红书、抖音等平台并发送解析结果。", Source: "core", Enabled: true},
	{ID: "core:image", Title: "图片生成与编辑", Content: "熟悉等级可生成和编辑图片；可结合群成员头像、用户提供的图片以及 Agent 联网搜索或网页核验后的结果。", Source: "core", Enabled: true, Required: "熟悉"},
	{ID: "core:voice", Title: "配置音色语音回复", Content: "用户明确要求语音回复、朗读或念出文字时，可调用 diana.tts 通过语音合成插件生成已配置音色并直接发送 QQ 语音；普通文字回复不会自动转语音。", Source: "core", Enabled: true},
	{ID: "core:ocr", Title: "文件与 OCR", Content: "能解析 PDF 和文件；macOS 使用 PDFKit/Vision，本地原生路径不可用时回退 PDFium 与视觉 LLM。", Source: "core", Enabled: true, Required: "熟悉"},
	{ID: "core:group", Title: "QQ群资料与成员", Content: "通过 diana.qq_group 获取群名、群成员列表、成员昵称、群头像和成员头像，可查找并真实 @ 一名或多名成员。", Source: "core", Enabled: true},
	{ID: "core:relationship", Title: "记忆好感度与权限", Content: "通过 diana.relationship 查询用户长期互动、好感度、关系等级和权限；主人可设置或增减其他人的好感度。", Source: "core", Enabled: true},
	{ID: "core:tasks", Title: "提醒与周期订阅", Content: "通过 diana.reminder、diana.schedule、diana.tasks 创建、批量查询、修改、取消和删除提醒或周期订阅；主人可管理其他用户任务。", Source: "core", Enabled: true},
	{ID: "core:history", Title: "聊天历史引用与撤回", Content: "持久保存 QQ 消息、引用、图片和视频关键帧，重启后不丢；可读取合并转发和撤回记录并结合上下文回复。", Source: "core", Enabled: true},
	{ID: "core:config", Title: "机器人配置与模型配置", Content: "diana.config 可读取脱敏运行配置、LLM、plugins 和 skills；仅主人可用 diana.llm_config 修改 Diana 自己当前的 provider/model。", Source: "core", Enabled: true, Required: "主人"},
	{ID: "core:llm-qq-privacy", Title: "LLM QQ 标识脱敏", Content: "默认在本地 LLM 边界把 QQ 号和群号替换为带角色语义的稳定别名；模型回复和 Agent 工具参数会在本地执行前还原。真实标识仍保留在本地数据库，不影响 QQ 发送、群工具和长期记忆。", Source: "core", Enabled: true},
	{ID: "core:capabilities", Title: "自身能力知识库 RAG", Content: "diana.capabilities 使用本地稀疏检索，从核心能力和实时插件清单召回相关条目后交给 LLM 回答。", Source: "core", Enabled: true},
}
