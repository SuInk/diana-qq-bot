package qqbot

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	dianaChatHistoryToolName       = "diana.chat_history"
	defaultChatHistoryRecentLimit  = 20
	maximumChatHistoryResultLimit  = 50
	defaultChatHistoryBefore       = 4
	defaultChatHistoryAfter        = 2
	maximumChatHistoryAroundRadius = 10
	defaultChatHistorySearchHours  = 24
	maximumChatHistorySearchHours  = 72
	maximumChatHistoryOutputRunes  = 7600
	chatHistoryLookupTimeout       = 3 * time.Second
)

type dianaChatHistoryTool struct {
	runtime *Runtime
	event   MessageEvent
}

type dianaChatHistoryResult struct {
	OK              bool                   `json:"ok"`
	Action          string                 `json:"action"`
	Message         string                 `json:"message"`
	AnchorMessageID string                 `json:"anchor_message_id,omitempty"`
	Query           string                 `json:"query,omitempty"`
	Items           []dianaChatHistoryItem `json:"items"`
	Total           int                    `json:"total"`
	Limited         bool                   `json:"limited,omitempty"`
}

type dianaChatHistoryItem struct {
	MessageID       string   `json:"message_id,omitempty"`
	Time            int64    `json:"event_time,omitempty"`
	LocalTime       string   `json:"local_time,omitempty"`
	Sender          string   `json:"sender"`
	Text            string   `json:"text,omitempty"`
	ContentTypes    []string `json:"content_types,omitempty"`
	ImageCount      int      `json:"image_count,omitempty"`
	VideoCount      int      `json:"video_count,omitempty"`
	FileCount       int      `json:"file_count,omitempty"`
	QuotedMessageID string   `json:"quoted_message_id,omitempty"`
	QuotedSender    string   `json:"quoted_sender,omitempty"`
	QuotedText      string   `json:"quoted_text,omitempty"`
}

func newDianaChatHistoryTool(runtime *Runtime, event MessageEvent) *dianaChatHistoryTool {
	return &dianaChatHistoryTool{runtime: runtime, event: event}
}

func (t *dianaChatHistoryTool) Name() string {
	return dianaChatHistoryToolName
}

func (t *dianaChatHistoryTool) Description() string {
	return `按需读取当前 QQ 会话的本地持久化聊天记录。当引用消息里的“这/那个/是的”等指代需要更早上文、当前 20 条上下文不足，或用户明确询问较早消息时，必须先调用，不要直接声称看不到。around 读取某条消息前后记录，message_id 省略时默认使用当前 QQ 引用；recent 读取最近记录；search 按模型给出的 query 检索。严格限定当前群聊或私聊。input: {"operation":"around|recent|search","message_id":"around 可选","before":4,"after":2,"query":"search 必填","hours":24,"limit":20}`
}

func (t *dianaChatHistoryTool) Run(ctx context.Context, input map[string]any) (string, error) {
	if t == nil || t.runtime == nil {
		return "", fmt.Errorf("diana chat history: runtime is not configured")
	}
	operation := strings.ToLower(strings.TrimSpace(configToolString(input, "operation")))
	if operation == "" {
		if t.event.Quoted != nil && strings.TrimSpace(t.event.Quoted.MessageID) != "" {
			operation = "around"
		} else {
			operation = "recent"
		}
	}

	var result dianaChatHistoryResult
	var err error
	switch operation {
	case "around", "context":
		result, err = t.around(ctx, input)
	case "recent", "list":
		result, err = t.recent(ctx, input)
	case "search", "find":
		result, err = t.search(ctx, input)
	default:
		return "", fmt.Errorf("operation 必须是 around、recent 或 search")
	}
	if err != nil {
		return "", err
	}
	return marshalDianaChatHistoryResult(result)
}

func (t *dianaChatHistoryTool) around(ctx context.Context, input map[string]any) (dianaChatHistoryResult, error) {
	messageID := strings.TrimSpace(configToolString(input, "message_id"))
	if messageID == "" && t.event.Quoted != nil {
		messageID = strings.TrimSpace(t.event.Quoted.MessageID)
	}
	if messageID == "" {
		messageID = strings.TrimSpace(t.event.SemanticSourceMessageID)
	}
	if messageID == "" {
		return dianaChatHistoryResult{}, fmt.Errorf("around 需要 message_id；当前消息也没有 QQ 引用可作为默认锚点")
	}
	anchor, found := t.runtime.findSemanticReferenceEvent(ctx, t.event, messageID)
	if !found {
		return dianaChatHistoryResult{}, fmt.Errorf("当前会话中找不到消息 %s", messageID)
	}
	before := chatHistoryBoundedInt(input, "before", defaultChatHistoryBefore, maximumChatHistoryAroundRadius)
	after := chatHistoryBoundedInt(input, "after", defaultChatHistoryAfter, maximumChatHistoryAroundRadius)
	timeline, err := t.timeline(
		ctx,
		anchor.Time-int64(semanticReferenceQuotedLookback/time.Second),
		anchor.Time+int64(semanticReferenceQuotedLookahead/time.Second),
	)
	if err != nil {
		return dianaChatHistoryResult{}, err
	}
	timeline = mergeSemanticReferenceHistory(timeline, []MessageEvent{anchor})
	anchorIndex := -1
	for index := range timeline {
		if strings.TrimSpace(timeline[index].MessageID) == messageID {
			anchorIndex = index
			break
		}
	}
	if anchorIndex < 0 {
		return dianaChatHistoryResult{}, fmt.Errorf("当前会话中无法定位消息 %s 的相邻记录", messageID)
	}
	left := anchorIndex - before
	if left < 0 {
		left = 0
	}
	right := anchorIndex + after + 1
	if right > len(timeline) {
		right = len(timeline)
	}
	items := chatHistoryItems(timeline[left:right])
	return dianaChatHistoryResult{
		OK:              true,
		Action:          "around",
		Message:         "已从当前会话的本地持久化记录读取引用消息前后文。",
		AnchorMessageID: messageID,
		Items:           items,
		Total:           len(items),
	}, nil
}

func (t *dianaChatHistoryTool) recent(ctx context.Context, input map[string]any) (dianaChatHistoryResult, error) {
	limit := chatHistoryPositiveInt(input, "limit", defaultChatHistoryRecentLimit, maximumChatHistoryResultLimit)
	t.runtime.mu.RLock()
	store := t.runtime.messageStore
	memory := append([]MessageEvent(nil), t.runtime.history[sessionKey(t.event)]...)
	t.runtime.mu.RUnlock()
	events := memory
	if store != nil {
		loadCtx, cancel := context.WithTimeout(ctx, chatHistoryLookupTimeout)
		stored, err := store.ListRecentMessageEvents(loadCtx, sessionKey(t.event), limit)
		cancel()
		if err != nil {
			return dianaChatHistoryResult{}, fmt.Errorf("读取当前会话最近记录失败: %w", err)
		}
		events = mergeMessageHistory(memory, stored, limit)
	} else if len(events) > limit {
		events = events[len(events)-limit:]
	}
	items := chatHistoryItems(events)
	return dianaChatHistoryResult{
		OK:      true,
		Action:  "recent",
		Message: "已读取当前会话最近的本地聊天记录。",
		Items:   items,
		Total:   len(items),
		Limited: len(items) >= limit,
	}, nil
}

func (t *dianaChatHistoryTool) search(ctx context.Context, input map[string]any) (dianaChatHistoryResult, error) {
	query := strings.TrimSpace(configToolString(input, "query"))
	if query == "" {
		return dianaChatHistoryResult{}, fmt.Errorf("search 的 query 不能为空")
	}
	limit := chatHistoryPositiveInt(input, "limit", defaultChatHistoryRecentLimit, maximumChatHistoryResultLimit)
	hours := chatHistoryPositiveInt(input, "hours", defaultChatHistorySearchHours, maximumChatHistorySearchHours)
	throughTime := t.event.Time
	if throughTime <= 0 {
		throughTime = time.Now().Unix()
	}
	timeline, err := t.timeline(ctx, throughTime-int64(time.Duration(hours)*time.Hour/time.Second), throughTime)
	if err != nil {
		return dianaChatHistoryResult{}, err
	}
	normalizedQuery := strings.ToLower(query)
	matched := make([]MessageEvent, 0, min(limit, len(timeline)))
	total := 0
	for index := len(timeline) - 1; index >= 0; index-- {
		item := timeline[index]
		searchable := strings.ToLower(strings.Join([]string{
			item.MessageID,
			item.SenderNameOrID(),
			historyToolEventText(item),
			quotedPlainText(item.Quoted),
		}, "\n"))
		if !strings.Contains(searchable, normalizedQuery) {
			continue
		}
		total++
		if len(matched) < limit {
			matched = append(matched, item)
		}
	}
	return dianaChatHistoryResult{
		OK:      true,
		Action:  "search",
		Message: "已在当前会话的本地持久化记录中完成检索，结果按时间从新到旧排列。",
		Query:   query,
		Items:   chatHistoryItems(matched),
		Total:   total,
		Limited: total > len(matched),
	}, nil
}

func (t *dianaChatHistoryTool) timeline(ctx context.Context, fromTime, throughTime int64) ([]MessageEvent, error) {
	if fromTime < 0 {
		fromTime = 0
	}
	t.runtime.mu.RLock()
	store := t.runtime.messageStore
	memory := append([]MessageEvent(nil), t.runtime.history[sessionKey(t.event)]...)
	t.runtime.mu.RUnlock()
	filteredMemory := make([]MessageEvent, 0, len(memory))
	for _, event := range memory {
		if event.Kind == EventKindNotice || (event.Time > 0 && (event.Time < fromTime || event.Time > throughTime)) {
			continue
		}
		filteredMemory = append(filteredMemory, event)
	}
	if timelineStore, ok := store.(MessageTimelineStore); ok {
		loadCtx, cancel := context.WithTimeout(ctx, chatHistoryLookupTimeout)
		stored, err := timelineStore.ListMessageEventsBetween(loadCtx, sessionKey(t.event), fromTime, throughTime)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("读取当前会话持久化时间线失败: %w", err)
		}
		return mergeSemanticReferenceHistory(stored, filteredMemory), nil
	}
	return mergeSemanticReferenceHistory(filteredMemory), nil
}

func chatHistoryBoundedInt(input map[string]any, key string, fallback, maximum int) int {
	raw, exists := input[key]
	if !exists {
		return fallback
	}
	value := intFromAny(raw)
	if value < 0 {
		value = 0
	}
	if value > maximum {
		value = maximum
	}
	return value
}

func chatHistoryPositiveInt(input map[string]any, key string, fallback, maximum int) int {
	value := chatHistoryBoundedInt(input, key, fallback, maximum)
	if value <= 0 {
		return fallback
	}
	return value
}

func chatHistoryReferenceOutsideContext(event MessageEvent, history []MessageEvent) bool {
	references := append([]string(nil), replyReferenceIDs(event.Segments)...)
	references = append(references, event.SemanticSourceMessageID)
	if event.Quoted != nil {
		references = append(references, event.Quoted.MessageID, event.Quoted.SemanticSourceMessageID)
	}
	for _, messageID := range dedupeStrings(references) {
		messageID = strings.TrimSpace(messageID)
		if messageID == "" {
			continue
		}
		found := false
		for _, item := range history {
			if strings.TrimSpace(item.MessageID) == messageID {
				found = true
				break
			}
		}
		if !found {
			return true
		}
	}
	return false
}

func chatHistoryItems(events []MessageEvent) []dianaChatHistoryItem {
	items := make([]dianaChatHistoryItem, 0, len(events))
	for _, event := range events {
		if event.Kind == EventKindNotice {
			continue
		}
		items = append(items, chatHistoryItem(event))
	}
	return items
}

func chatHistoryItem(event MessageEvent) dianaChatHistoryItem {
	item := dianaChatHistoryItem{
		MessageID: event.MessageID,
		Time:      event.Time,
		Sender:    event.SenderNameOrID(),
		Text:      truncateChatHistoryText(historyToolEventText(event), 420),
	}
	if event.Time > 0 {
		item.LocalTime = time.Unix(event.Time, 0).Local().Format("2006-01-02 15:04:05 -07:00")
	}
	for _, segment := range event.Segments {
		switch segment.Type {
		case "text":
			if strings.TrimSpace(segment.Data["text"]) != "" {
				item.ContentTypes = appendUniqueStrings(item.ContentTypes, "text")
			}
		case "image":
			if segment.Data["source_type"] != "video_frame" {
				item.ImageCount++
				item.ContentTypes = appendUniqueStrings(item.ContentTypes, "image")
			}
		case "video":
			item.VideoCount++
			item.ContentTypes = appendUniqueStrings(item.ContentTypes, "video")
		case "file":
			if videoFileSegment(segment) {
				item.VideoCount++
				item.ContentTypes = appendUniqueStrings(item.ContentTypes, "video")
			} else {
				item.FileCount++
				item.ContentTypes = appendUniqueStrings(item.ContentTypes, "file")
			}
		case "forward":
			item.ContentTypes = appendUniqueStrings(item.ContentTypes, "forward")
		}
	}
	if event.Quoted != nil {
		item.QuotedMessageID = strings.TrimSpace(event.Quoted.MessageID)
		item.QuotedSender = strings.TrimSpace(firstNonEmpty(event.Quoted.SenderName, event.Quoted.UserID))
		item.QuotedText = truncateChatHistoryText(historyToolQuotedText(event.Quoted), 280)
	}
	sort.Strings(item.ContentTypes)
	return item
}

func historyToolEventText(event MessageEvent) string {
	text := strings.TrimSpace(PlainText(event.Segments))
	if text != "" {
		return text
	}
	labels := make([]string, 0, 4)
	for _, segment := range event.Segments {
		switch segment.Type {
		case "image":
			labels = appendUniqueStrings(labels, "[图片]")
		case "video":
			labels = appendUniqueStrings(labels, "[视频]")
		case "file":
			labels = appendUniqueStrings(labels, "[文件]")
		case "forward":
			labels = appendUniqueStrings(labels, "[合并转发]")
		}
	}
	if len(labels) > 0 {
		return strings.Join(labels, " ")
	}
	return strings.TrimSpace(event.RawMessage)
}

func historyToolQuotedText(quoted *QuotedMessage) string {
	if quoted == nil {
		return ""
	}
	text := strings.TrimSpace(PlainText(quoted.Segments))
	if text != "" {
		return text
	}
	for _, segment := range quoted.Segments {
		switch segment.Type {
		case "image":
			return "[图片]"
		case "video":
			return "[视频]"
		case "file":
			return "[文件]"
		case "forward":
			return "[合并转发]"
		}
	}
	return strings.TrimSpace(quoted.RawMessage)
}

func truncateChatHistoryText(text string, limit int) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if limit <= 0 || len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "..."
}

func marshalDianaChatHistoryResult(result dianaChatHistoryResult) (string, error) {
	for {
		body, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return "", err
		}
		if len([]rune(string(body))) <= maximumChatHistoryOutputRunes || len(result.Items) == 0 {
			return string(body), nil
		}
		result.Items = result.Items[:len(result.Items)-1]
		result.Limited = true
	}
}
