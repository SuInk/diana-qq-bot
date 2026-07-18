package qqbot

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const messageHistoryLimit = 100

const recallDefaultWindow = 24 * time.Hour

const maxRecallIdentityLookups = 8

type groupMemberIdentity struct {
	Name string
	Role string
}

type MessageHistoryPlugin struct {
	mu               sync.RWMutex
	group            map[string]map[string]MessageEvent
	private          map[string][]MessageEvent
	recalls          map[string][]MessageEvent
	inserted         map[string][]string
	memberIdentities map[string]groupMemberIdentity
}

func NewMessageHistoryPlugin() *MessageHistoryPlugin {
	return &MessageHistoryPlugin{
		group:            map[string]map[string]MessageEvent{},
		private:          map[string][]MessageEvent{},
		recalls:          map[string][]MessageEvent{},
		inserted:         map[string][]string{},
		memberIdentities: map[string]groupMemberIdentity{},
	}
}

func (p *MessageHistoryPlugin) Manifest() PluginManifest {
	return PluginManifest{
		ID:          messageHistoryPluginID,
		Name:        "Diana message history",
		Version:     "0.1.0",
		Description: "缓存最近群聊/私聊消息、引用消息和撤回消息，并把图片与视频关键帧保存到本地历史目录。",
		Official:    true,
		BuiltIn:     true,
		Permissions: []string{"message:read", "notice:read", "file:write"},
	}
}

func (p *MessageHistoryPlugin) Handle(ctx context.Context, req PluginRequest) (*PluginResponse, error) {
	if req.Event.Kind != EventKindGroup || req.Event.GroupID == "" {
		return nil, nil
	}
	if !recallHistoryQuery(req.Text) {
		return nil, nil
	}
	p.mu.RLock()
	memoryRecalls := append([]MessageEvent(nil), p.recalls[req.Event.GroupID]...)
	p.mu.RUnlock()
	recalls := mergeRecallRecords(recallRecordsForGroup(req.RecallEvents, req.Event.GroupID), memoryRecalls)
	referenceTime := recallReferenceTime(req.Event.Time, recalls)
	recalls = recentRecallRecords(recalls, referenceTime, recallDefaultWindow)
	if len(recalls) == 0 {
		return &PluginResponse{
			Handled:          true,
			Reply:            "最近24小时没有记录到群消息撤回。",
			RecallDisclosure: true,
		}, nil
	}
	recalls = p.enrichRecallIdentities(ctx, req.Channel, req.Event, req.RecentEvents, recalls)
	return buildRecallPluginResponse(recalls, referenceTime, recallHistoryQuery(req.Text)), nil
}

func buildRecallPluginResponse(recalls []MessageEvent, referenceTime int64, disclosure bool) *PluginResponse {
	prepared, imageURLs := prepareRecallImageAttachments(recalls)
	return &PluginResponse{
		Handled:             true,
		Context:             formatRecallRecords(prepared, referenceTime),
		ContextImageURLs:    imageURLs,
		Forward:             true,
		NestedForward:       true,
		ForwardMessages:     recallForwardMessages(prepared),
		RecallDisclosure:    disclosure,
		RecallEvents:        prepared,
		RecallReferenceTime: referenceTime,
	}
}

func refreshRecallPluginResponse(resp *PluginResponse, recalls []MessageEvent) {
	if resp == nil {
		return
	}
	refreshed := buildRecallPluginResponse(recalls, resp.RecallReferenceTime, resp.RecallDisclosure)
	resp.Context = refreshed.Context
	resp.ContextImageURLs = refreshed.ContextImageURLs
	resp.ForwardMessages = refreshed.ForwardMessages
	resp.RecallEvents = refreshed.RecallEvents
}

func applyRecallReplyMode(responses []PluginResponse, mode RecallReplyMode) []PluginResponse {
	if normalizeRecallReplyMode(mode) == RecallReplyModeOriginalForward {
		return responses
	}
	out := append([]PluginResponse(nil), responses...)
	for index := range out {
		if !out[index].RecallDisclosure {
			continue
		}
		if strings.TrimSpace(out[index].Context) == "" {
			out[index].Context = strings.TrimSpace(out[index].Reply)
		}
		out[index].Reply = ""
		out[index].Forward = false
		out[index].NestedForward = false
		out[index].ForwardMessages = nil
	}
	return out
}

func recallHistoryQuery(text string) bool {
	text = strings.TrimSpace(text)
	if !strings.Contains(text, "撤回") {
		return false
	}
	for _, marker := range []string{"什么", "消息", "内容", "记录", "查看", "看看", "看下", "看一下", "刚才", "最近", "所有", "谁", "怎么", "如何"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func (p *MessageHistoryPlugin) Observe(_ context.Context, event MessageEvent) MessageEvent {
	if event.Kind == EventKindNotice {
		return p.observeNotice(event)
	}
	event = p.enrichQuotedFromCache(event)
	p.store(event)
	return event
}

func (p *MessageHistoryPlugin) Lookup(groupID, userID, messageID string) (*QuotedMessage, bool) {
	record, ok := p.lookupEvent(groupID, userID, messageID)
	if !ok {
		return nil, false
	}
	return quotedMessageFromHistory(record), true
}

func (p *MessageHistoryPlugin) observeNotice(event MessageEvent) MessageEvent {
	if event.SubType != "group_recall" && event.SubType != "friend_recall" {
		return event
	}
	messageID := firstNonEmpty(event.MessageID, event.RawMessage, event.SegmentsData("message_id"), event.SegmentsData("target_id"))
	if messageID == "" {
		return event
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensureMapsLocked()
	if event.GroupID != "" {
		if p.group[event.GroupID] == nil {
			p.group[event.GroupID] = map[string]MessageEvent{}
		}
		record, ok := p.group[event.GroupID][messageID]
		if !ok {
			record = recallRecordFromNotice(event)
		} else if recallEventHasContent(event) {
			record = recallRecordFromNotice(event)
		} else {
			if record.OriginalTime == 0 {
				record.OriginalTime = record.Time
			}
			if event.Time > 0 {
				record.Time = event.Time
			}
		}
		record.SubType = event.SubType
		record.OperatorID = event.OperatorID
		if record.SelfID == "" {
			record.SelfID = event.SelfID
		}
		record.RawMessage = strings.TrimSpace(record.RawMessage)
		if record.RawMessage == "" {
			record.RawMessage = "[已撤回消息]"
		}
		p.group[event.GroupID][messageID] = record
		p.recalls[event.GroupID] = appendLimit(p.recalls[event.GroupID], record, messageHistoryLimit)
		return noticeWithRecallRecord(event, record)
	}
	if event.UserID != "" {
		for i := len(p.private[event.UserID]) - 1; i >= 0; i-- {
			if p.private[event.UserID][i].MessageID == messageID {
				p.private[event.UserID][i].SubType = event.SubType
				p.private[event.UserID][i].OperatorID = event.OperatorID
				if p.private[event.UserID][i].SelfID == "" {
					p.private[event.UserID][i].SelfID = event.SelfID
				}
				return noticeWithRecallRecord(event, p.private[event.UserID][i])
			}
		}
	}
	return event
}

func (p *MessageHistoryPlugin) enrichQuotedFromCache(event MessageEvent) MessageEvent {
	if event.Quoted != nil {
		return event
	}
	for _, id := range replyReferenceIDs(event.Segments) {
		if quoted, ok := p.Lookup(event.GroupID, event.UserID, id); ok {
			event.Quoted = quoted
			return event
		}
	}
	return event
}

func (p *MessageHistoryPlugin) store(event MessageEvent) {
	if event.MessageID == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensureMapsLocked()
	switch event.Kind {
	case EventKindGroup:
		if event.GroupID == "" {
			return
		}
		if p.group[event.GroupID] == nil {
			p.group[event.GroupID] = map[string]MessageEvent{}
		}
		p.group[event.GroupID][event.MessageID] = event
		p.inserted[event.GroupID] = append(p.inserted[event.GroupID], event.MessageID)
		for len(p.inserted[event.GroupID]) > messageHistoryLimit {
			oldest := p.inserted[event.GroupID][0]
			p.inserted[event.GroupID] = p.inserted[event.GroupID][1:]
			delete(p.group[event.GroupID], oldest)
		}
	case EventKindPrivate:
		if event.UserID == "" {
			return
		}
		p.private[event.UserID] = appendLimit(p.private[event.UserID], event, messageHistoryLimit)
	}
}

func (p *MessageHistoryPlugin) ensureMapsLocked() {
	if p.group == nil {
		p.group = map[string]map[string]MessageEvent{}
	}
	if p.private == nil {
		p.private = map[string][]MessageEvent{}
	}
	if p.recalls == nil {
		p.recalls = map[string][]MessageEvent{}
	}
	if p.inserted == nil {
		p.inserted = map[string][]string{}
	}
	if p.memberIdentities == nil {
		p.memberIdentities = map[string]groupMemberIdentity{}
	}
}

func (p *MessageHistoryPlugin) groupHistory(groupID string) []MessageEvent {
	p.mu.RLock()
	defer p.mu.RUnlock()
	messageIDs := p.inserted[groupID]
	out := make([]MessageEvent, 0, len(messageIDs))
	for _, messageID := range messageIDs {
		if event, ok := p.group[groupID][messageID]; ok {
			out = append(out, event)
		}
	}
	return out
}

func (p *MessageHistoryPlugin) lookupEvent(groupID, userID, messageID string) (MessageEvent, bool) {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return MessageEvent{}, false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if groupID != "" {
		record, ok := p.group[groupID][messageID]
		return record, ok
	}
	if userID != "" {
		for i := len(p.private[userID]) - 1; i >= 0; i-- {
			record := p.private[userID][i]
			if record.MessageID == messageID {
				return record, true
			}
		}
	}
	return MessageEvent{}, false
}

func quotedMessageFromHistory(event MessageEvent) *QuotedMessage {
	return &QuotedMessage{
		MessageID:               event.MessageID,
		UserID:                  event.UserID,
		GroupID:                 event.GroupID,
		SenderName:              event.SenderName,
		RawMessage:              event.RawMessage,
		Segments:                event.Segments,
		SemanticSourceMessageID: event.SemanticSourceMessageID,
	}
}

func appendLimit[T any](items []T, item T, limit int) []T {
	items = append(items, item)
	if limit > 0 && len(items) > limit {
		items = items[len(items)-limit:]
	}
	return items
}

func (event MessageEvent) SegmentsData(key string) string {
	for _, segment := range event.Segments {
		if value := strings.TrimSpace(segment.Data[key]); value != "" {
			return value
		}
	}
	return ""
}

func formatRecallRecords(recalls []MessageEvent, referenceTime int64) string {
	blocks := make([]string, 0, len(recalls)+1)
	windowStart := referenceTime - int64(recallDefaultWindow/time.Second)
	blocks = append(blocks, strings.Join([]string{
		"最近24小时群撤回消息时间线（结构化事实，仅包含该时间窗口；按撤回时间从旧到新）：",
		"时间窗口=" + formatRecallTime(windowStart) + " 至 " + formatRecallTime(referenceTime),
		fmt.Sprintf("记录总数=%d", len(recalls)),
		"回复要求=根据当前用户的问题直接生成最终QQ回复，不得猜测，也不要声称看不到撤回内容。用户要求完整、全部或逐条查看时，必须按以下旧到新顺序逐条说明发送时间、发送者、撤回时间、撤回者和原消息内容；用户只要求概括时才可以精简。",
		"字段顺序=序号|撤回时间|原消息发送时间|原消息ID|原消息发送者|被@对象|执行撤回者|执行者身份|结论",
		"每条数据的下一行“原消息完整内容=”保存对应原文；字段名只声明一次以避免重复消耗上下文。",
		"图片说明=优先使用已缓存的图片内容描述；标记为附件的图片按附件编号与下方多模态图片从1开始一一对应，必须查看对应图片后再描述。",
	}, "\n"))
	for i, record := range recalls {
		text := strings.TrimSpace(PlainText(record.Segments))
		if text == "" {
			text = strings.TrimSpace(record.RawMessage)
		}
		if text == "" {
			text = "(无纯文本)"
		}
		senderName := firstNonEmpty(record.SenderName, record.UserID, "未知用户")
		sender := formatRecallIdentity(senderName, record.UserID)
		operatorName := strings.TrimSpace(record.OperatorName)
		if operatorName == "" && record.OperatorID == record.UserID {
			operatorName = senderName
		}
		if operatorName == "" && record.OperatorID == record.SelfID {
			operatorName = "机器人"
		}
		operator := formatRecallIdentity(operatorName, record.OperatorID)
		messageID := strings.TrimSpace(record.MessageID)
		mentions := firstNonEmpty(recallMentionIdentityText(record), "无")
		lines := []string{
			strings.Join([]string{
				fmt.Sprintf("%d", i+1),
				formatRecallTime(record.Time),
				formatRecallTime(record.OriginalTime),
				firstNonEmpty(messageID, "未知"),
				sender,
				mentions,
				operator,
				recallOperatorRoleText(record),
				recallConclusionText(record),
			}, "|"),
			"原消息完整内容=" + text,
		}
		lines = append(lines, recallImageFactLines(record.Segments)...)
		blocks = append(blocks, strings.Join(lines, "\n"))
	}
	return strings.Join(blocks, "\n\n")
}

func recallMentionIdentityText(record MessageEvent) string {
	identities := make([]string, 0)
	seen := make(map[string]struct{})
	for _, segment := range record.Segments {
		if segment.Type != "at" {
			continue
		}
		userID := strings.TrimSpace(segment.Data["qq"])
		if userID == "" {
			continue
		}
		identity := "全体成员"
		if userID != "all" {
			identity = formatRecallIdentity(segment.Data["display_name"], userID)
		}
		if _, ok := seen[identity]; ok {
			continue
		}
		seen[identity] = struct{}{}
		identities = append(identities, identity)
	}
	return strings.Join(identities, "、")
}

func formatRecallTime(timestamp int64) string {
	if timestamp <= 0 {
		return "未知"
	}
	return time.Unix(timestamp, 0).In(time.Local).Format("2006-01-02 15:04:05 -07:00")
}

func recallForwardMessages(recalls []MessageEvent) []OutgoingMessage {
	messages := make([]OutgoingMessage, 0, len(recalls))
	for _, record := range recalls {
		text := strings.TrimSpace(record.RawMessage)
		segments := append([]MessageSegment(nil), record.Segments...)
		if len(segments) == 0 {
			text = firstNonEmpty(text, "[已撤回消息]")
		} else {
			text = ""
		}
		messages = append(messages, OutgoingMessage{
			Text:        text,
			Segments:    segments,
			ForwardName: firstNonEmpty(record.SenderName, record.UserID, "未知用户"),
			ForwardUIN:  firstNonEmpty(record.UserID, "0"),
			ForwardTime: firstNonZeroInt64(record.OriginalTime, record.Time),
		})
	}
	return messages
}

func firstNonZeroInt64(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func formatRecallIdentity(name, id string) string {
	name = strings.TrimSpace(name)
	id = strings.TrimSpace(id)
	if id == "" {
		return firstNonEmpty(name, "未知")
	}
	if name == "" || name == id {
		return id
	}
	return name + "(" + id + ")"
}

func recallConclusionText(record MessageEvent) string {
	operatorID := strings.TrimSpace(record.OperatorID)
	if operatorID == "" {
		return "撤回通知未提供操作者，无法判断是自行撤回还是管理员撤回"
	}
	userID := strings.TrimSpace(record.UserID)
	selfID := strings.TrimSpace(record.SelfID)
	switch {
	case operatorID == selfID && userID == selfID:
		return "机器人自行撤回"
	case operatorID == userID:
		return "发送者自行撤回"
	case operatorID == selfID:
		return "机器人以管理员身份撤回，绝不是发送者自行撤回"
	default:
		return "管理员撤回，绝不是发送者自行撤回"
	}
}

func recallOperatorRoleText(record MessageEvent) string {
	switch strings.ToLower(strings.TrimSpace(record.OperatorRole)) {
	case "owner":
		return "群主"
	case "admin":
		return "管理员"
	case "member":
		return "群成员"
	}
	if record.OperatorID == "" {
		return "未知"
	}
	if record.OperatorID != record.UserID {
		return "管理员或群主"
	}
	return "原消息发送者"
}

func (p *MessageHistoryPlugin) enrichRecallIdentities(ctx context.Context, channel Channel, current MessageEvent, recentEvents, recalls []MessageEvent) []MessageEvent {
	out := make([]MessageEvent, len(recalls))
	for index, record := range recalls {
		out[index] = record
		out[index].Segments = make([]MessageSegment, len(record.Segments))
		for segmentIndex, segment := range record.Segments {
			out[index].Segments[segmentIndex] = MessageSegment{Type: segment.Type, Data: cloneSegmentData(segment.Data)}
		}
	}

	identityEvents := make([]MessageEvent, 0, messageHistoryLimit+len(recentEvents)+len(out)+1)
	identityEvents = append(identityEvents, current)
	for index := len(recentEvents) - 1; index >= 0; index-- {
		identityEvents = append(identityEvents, recentEvents[index])
	}
	history := p.groupHistory(current.GroupID)
	for index := len(history) - 1; index >= 0; index-- {
		identityEvents = append(identityEvents, history[index])
	}
	for index := len(out) - 1; index >= 0; index-- {
		identityEvents = append(identityEvents, out[index])
	}
	localNames := messageParticipantDisplayNames(identityEvents...)
	selfIDs := make(map[string]struct{})
	for _, event := range identityEvents {
		if selfID := strings.TrimSpace(event.SelfID); selfID != "" {
			selfIDs[selfID] = struct{}{}
		}
	}

	groupID := strings.TrimSpace(current.GroupID)
	memberIDs := make([]string, 0)
	operatorRoleNeeded := make(map[string]bool)
	addMemberID := func(userID string) {
		userID = strings.TrimSpace(userID)
		if userID == "" || userID == "all" {
			return
		}
		memberIDs = appendUniqueStrings(memberIDs, userID)
	}
	for _, record := range out {
		groupID = firstNonEmpty(groupID, strings.TrimSpace(record.GroupID))
		for _, segment := range record.Segments {
			if segment.Type != "at" {
				continue
			}
			userID := strings.TrimSpace(segment.Data["qq"])
			addMemberID(userID)
			if localNames[userID] == "" {
				localNames[userID] = firstNonEmpty(strings.TrimSpace(segment.Data["display_name"]), strings.TrimSpace(segment.Data["name"]))
			}
		}
	}
	for _, record := range out {
		operatorID := strings.TrimSpace(record.OperatorID)
		if operatorID != "" {
			addMemberID(operatorID)
			operatorRoleNeeded[operatorID] = operatorRoleNeeded[operatorID] || strings.TrimSpace(record.OperatorRole) == ""
			if localNames[operatorID] == "" && strings.TrimSpace(record.OperatorName) != "" {
				localNames[operatorID] = strings.TrimSpace(record.OperatorName)
			}
		}
	}

	identities := make(map[string]groupMemberIdentity, len(memberIDs))
	lookups := 0
	for _, userID := range memberIDs {
		identity, _ := p.cachedGroupMemberIdentity(groupID, userID)
		if name := strings.TrimSpace(localNames[userID]); name != "" {
			identity.Name = name
		}
		if _, isSelf := selfIDs[userID]; isSelf && identity.Name == "" {
			identity.Name = "机器人"
		}
		needsLookup := identity.Name == "" || (operatorRoleNeeded[userID] && identity.Role == "")
		if needsLookup && groupID != "" && channel != nil && lookups < maxRecallIdentityLookups {
			lookups++
			callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			data, err := channel.CallAPI(callCtx, "get_group_member_info", map[string]any{
				"group_id": oneBotIDParam(groupID),
				"user_id":  oneBotIDParam(userID),
				"no_cache": false,
			})
			cancel()
			if err == nil {
				member := qqGroupMemberInfoFromData(groupID, data)
				if name := strings.TrimSpace(member.DisplayName()); name != "" {
					identity.Name = name
				}
				if role := strings.TrimSpace(member.Role); role != "" {
					identity.Role = role
				}
			}
		}
		if identity.Name != "" || identity.Role != "" {
			p.cacheGroupMemberIdentity(groupID, userID, identity)
		}
		identities[userID] = identity
	}

	for index := range out {
		record := &out[index]
		if identity := identities[strings.TrimSpace(record.OperatorID)]; record.OperatorID != "" {
			if record.OperatorName == "" {
				record.OperatorName = identity.Name
			}
			if record.OperatorRole == "" {
				record.OperatorRole = identity.Role
			}
		}
		for segmentIndex := range record.Segments {
			segment := &record.Segments[segmentIndex]
			if segment.Type != "at" {
				continue
			}
			if identity := identities[strings.TrimSpace(segment.Data["qq"])]; identity.Name != "" {
				segment.Data["display_name"] = identity.Name
			}
		}
	}
	return out
}

func (p *MessageHistoryPlugin) cachedGroupMemberIdentity(groupID, userID string) (groupMemberIdentity, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	identity, ok := p.memberIdentities[groupMemberIdentityKey(groupID, userID)]
	return identity, ok
}

func (p *MessageHistoryPlugin) cacheGroupMemberIdentity(groupID, userID string, identity groupMemberIdentity) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ensureMapsLocked()
	p.memberIdentities[groupMemberIdentityKey(groupID, userID)] = identity
}

func groupMemberIdentityKey(groupID, userID string) string {
	return strings.TrimSpace(groupID) + "|" + strings.TrimSpace(userID)
}

func recallRecordsForGroup(events []MessageEvent, groupID string) []MessageEvent {
	out := make([]MessageEvent, 0, len(events))
	for _, event := range events {
		if event.SubType != "group_recall" || event.GroupID != groupID {
			continue
		}
		out = append(out, recallRecordFromNotice(event))
	}
	return out
}

func mergeRecallRecords(groups ...[]MessageEvent) []MessageEvent {
	merged := make([]MessageEvent, 0)
	indexes := map[string]int{}
	for _, records := range groups {
		for _, record := range records {
			key := recallRecordKey(record)
			if index, ok := indexes[key]; ok {
				merged[index] = record
				continue
			}
			indexes[key] = len(merged)
			merged = append(merged, record)
		}
	}
	return merged
}

func recallReferenceTime(currentTime int64, recalls []MessageEvent) int64 {
	if currentTime > 0 {
		return currentTime
	}
	for _, record := range recalls {
		if record.Time > currentTime {
			currentTime = record.Time
		}
	}
	if currentTime > 0 {
		return currentTime
	}
	return time.Now().Unix()
}

func recentRecallRecords(recalls []MessageEvent, referenceTime int64, window time.Duration) []MessageEvent {
	if len(recalls) == 0 {
		return nil
	}
	cutoff := referenceTime - int64(window/time.Second)
	out := make([]MessageEvent, 0, len(recalls))
	for _, record := range recalls {
		if record.Time > 0 && (record.Time < cutoff || record.Time > referenceTime) {
			continue
		}
		out = append(out, record)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Time < out[j].Time
	})
	return out
}

func recallRecordKey(event MessageEvent) string {
	if messageID := strings.TrimSpace(event.MessageID); messageID != "" {
		return event.GroupID + "|" + messageID
	}
	return fmt.Sprintf("%s|%s|%d|%s", event.GroupID, event.UserID, event.Time, strings.TrimSpace(event.RawMessage))
}

func recallRecordFromNotice(event MessageEvent) MessageEvent {
	record := event
	if event.GroupID != "" {
		record.Kind = EventKindGroup
		record.MessageType = "group"
	} else {
		record.Kind = EventKindPrivate
		record.MessageType = "private"
	}
	if !recallEventHasContent(event) {
		record.RawMessage = "[已撤回消息]"
		record.Segments = nil
		record.Quoted = nil
	}
	return record
}

func noticeWithRecallRecord(notice MessageEvent, record MessageEvent) MessageEvent {
	notice.RawMessage = record.RawMessage
	notice.Segments = append([]MessageSegment(nil), record.Segments...)
	notice.SenderName = record.SenderName
	notice.Quoted = record.Quoted
	if notice.UserID == "" {
		notice.UserID = record.UserID
	}
	return notice
}

func recallEventHasContent(event MessageEvent) bool {
	if strings.TrimSpace(event.RawMessage) != "" {
		return true
	}
	for _, segment := range event.Segments {
		if segment.Type != "notice" && segment.Type != "" {
			return true
		}
	}
	return false
}
