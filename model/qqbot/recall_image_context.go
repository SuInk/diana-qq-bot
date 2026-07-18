package qqbot

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"diana-qq-bot/model/llm"
)

const (
	recallImageDescriptionKey         = "cached_description"
	recallImageDescriptionSourceKey   = "cached_description_source"
	recallImageAttachmentIndexKey     = "recall_attachment_index"
	recallImageDescriptionVersion     = "recall-image-v1"
	recallImageDescriptionMaxRunes    = 1600
	recallImageDescriptionConcurrency = 3
)

type recallImagePosition struct {
	eventIndex   int
	segmentIndex int
}

type recallImageTarget struct {
	key               string
	contentSHA256     string
	imageSource       string
	sourceMessageIDs  []string
	positions         []recallImagePosition
	description       string
	descriptionSource string
}

func prepareRecallImageAttachments(recalls []MessageEvent) ([]MessageEvent, []string) {
	out := cloneRecallEvents(recalls)
	attachmentByImage := make(map[string]int)
	var imageURLs []string
	for eventIndex := range out {
		for segmentIndex := range out[eventIndex].Segments {
			segment := &out[eventIndex].Segments[segmentIndex]
			if !recallStillImageSegment(*segment) {
				continue
			}
			delete(segment.Data, recallImageAttachmentIndexKey)
			if strings.TrimSpace(segment.Data[recallImageDescriptionKey]) != "" {
				continue
			}
			source := firstImageSource(*segment)
			if source == "" {
				continue
			}
			key := source
			if hash, ok := imageSegmentContentSHA256(*segment); ok {
				key = hash
				segment.Data[imageContentSHA256Key] = hash
			}
			attachmentIndex, ok := attachmentByImage[key]
			if !ok {
				imageURLs = append(imageURLs, source)
				attachmentIndex = len(imageURLs)
				attachmentByImage[key] = attachmentIndex
			}
			segment.Data[recallImageAttachmentIndexKey] = strconv.Itoa(attachmentIndex)
		}
	}
	return out, imageURLs
}

func recallImageFactLines(segments []MessageSegment) []string {
	var lines []string
	imageIndex := 0
	for _, segment := range segments {
		if !recallStillImageSegment(segment) {
			continue
		}
		imageIndex++
		description := compactRecallImageDescription(segment.Data[recallImageDescriptionKey])
		switch {
		case description != "":
			lines = append(lines, fmt.Sprintf("图片%d内容描述=%s", imageIndex, description))
		case strings.TrimSpace(segment.Data[recallImageAttachmentIndexKey]) != "":
			lines = append(lines, fmt.Sprintf("图片%d内容描述=此前没有可复用描述，请查看多模态附件%s后客观描述", imageIndex, segment.Data[recallImageAttachmentIndexKey]))
		default:
			lines = append(lines, fmt.Sprintf("图片%d内容描述=没有缓存描述，原图片文件当前也不可读取", imageIndex))
		}
	}
	return lines
}

func recallStillImageSegment(segment MessageSegment) bool {
	return segment.Type == "image" && strings.TrimSpace(segment.Data["source_type"]) != "video_frame"
}

func cloneRecallEvents(events []MessageEvent) []MessageEvent {
	out := make([]MessageEvent, len(events))
	for eventIndex, event := range events {
		out[eventIndex] = event
		out[eventIndex].Segments = make([]MessageSegment, len(event.Segments))
		for segmentIndex, segment := range event.Segments {
			out[eventIndex].Segments[segmentIndex] = segment
			out[eventIndex].Segments[segmentIndex].Data = cloneSegmentData(segment.Data)
		}
	}
	return out
}

func compactRecallImageDescription(value string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	runes := []rune(value)
	if len(runes) > recallImageDescriptionMaxRunes {
		value = string(runes[:recallImageDescriptionMaxRunes]) + "..."
	}
	return value
}

func (r *Runtime) enrichRecallImageDescriptions(ctx context.Context, event MessageEvent, recalls []MessageEvent) []MessageEvent {
	out := cloneRecallEvents(recalls)
	targets := collectRecallImageTargets(out)
	if len(targets) == 0 {
		return out
	}

	store := r.recallImageDescriptionStore()
	for _, target := range targets {
		if target.contentSHA256 == "" || store == nil {
			continue
		}
		record, ok, err := store.GetImageDescription(ctx, target.contentSHA256)
		if err != nil {
			log.Printf("qqbot recall image description cache load failed: %v", err)
			continue
		}
		if ok && strings.TrimSpace(record.Description) != "" {
			target.description = compactRecallImageDescription(record.Description)
			target.descriptionSource = firstNonEmpty(record.Source, "cache")
		}
	}

	historical := r.historicalRecallImageDescriptions(ctx, event, targets)
	for _, target := range targets {
		if target.description != "" {
			continue
		}
		if description := compactRecallImageDescription(historical[target.key]); description != "" {
			target.description = description
			target.descriptionSource = "history"
			r.saveRecallImageDescription(target, event)
		}
	}

	r.describeMissingRecallImages(ctx, event, targets)
	for _, target := range targets {
		if target.description == "" {
			continue
		}
		for _, position := range target.positions {
			segment := &out[position.eventIndex].Segments[position.segmentIndex]
			segment.Data[recallImageDescriptionKey] = target.description
			segment.Data[recallImageDescriptionSourceKey] = target.descriptionSource
			if target.contentSHA256 != "" {
				segment.Data[imageContentSHA256Key] = target.contentSHA256
			}
		}
	}
	return out
}

func collectRecallImageTargets(recalls []MessageEvent) []*recallImageTarget {
	targetByKey := make(map[string]*recallImageTarget)
	var targets []*recallImageTarget
	for eventIndex := range recalls {
		for segmentIndex := range recalls[eventIndex].Segments {
			segment := &recalls[eventIndex].Segments[segmentIndex]
			if !recallStillImageSegment(*segment) {
				continue
			}
			hash, _ := imageSegmentContentSHA256(*segment)
			if hash != "" {
				segment.Data[imageContentSHA256Key] = hash
			}
			source := firstImageSource(*segment)
			key := hash
			if key == "" {
				key = source
			}
			if key == "" {
				key = fmt.Sprintf("message:%s:image:%d", recalls[eventIndex].MessageID, segmentIndex)
			}
			target := targetByKey[key]
			if target == nil {
				target = &recallImageTarget{
					key:               key,
					contentSHA256:     hash,
					imageSource:       source,
					description:       compactRecallImageDescription(segment.Data[recallImageDescriptionKey]),
					descriptionSource: strings.TrimSpace(segment.Data[recallImageDescriptionSourceKey]),
				}
				targetByKey[key] = target
				targets = append(targets, target)
			}
			target.sourceMessageIDs = appendUniqueStrings(target.sourceMessageIDs, recalls[eventIndex].MessageID)
			target.positions = append(target.positions, recallImagePosition{eventIndex: eventIndex, segmentIndex: segmentIndex})
		}
	}
	return targets
}

func (r *Runtime) recallImageDescriptionStore() ImageDescriptionStore {
	r.mu.RLock()
	defer r.mu.RUnlock()
	store, _ := r.messageStore.(ImageDescriptionStore)
	return store
}

func (r *Runtime) historicalRecallImageDescriptions(ctx context.Context, event MessageEvent, targets []*recallImageTarget) map[string]string {
	result := make(map[string]string)
	if len(targets) == 0 {
		return result
	}
	targetByHash := make(map[string]*recallImageTarget)
	targetByMessageID := make(map[string]*recallImageTarget)
	for _, target := range targets {
		if target.contentSHA256 != "" {
			targetByHash[target.contentSHA256] = target
		}
		for _, messageID := range target.sourceMessageIDs {
			targetByMessageID[messageID] = target
		}
	}

	timeline := r.recallDescriptionTimeline(ctx, event)
	for _, item := range timeline {
		for _, segment := range item.Segments {
			if !recallStillImageSegment(segment) {
				continue
			}
			hash, ok := imageSegmentContentSHA256(segment)
			if !ok {
				continue
			}
			if target := targetByHash[hash]; target != nil && strings.TrimSpace(item.MessageID) != "" {
				targetByMessageID[item.MessageID] = target
			}
		}
	}

	cfg := r.effectiveConfigForEvent(event)
	bestLength := make(map[string]int)
	for _, item := range timeline {
		if !recallDescriptionBotMessage(item, cfg) {
			continue
		}
		description := recallDescriptionMessageText(item)
		if description == "" || semanticErrorWrapperText(description) {
			continue
		}
		sourceIDs := append([]string{strings.TrimSpace(item.SemanticSourceMessageID)}, replyReferenceIDs(item.Segments)...)
		for _, sourceID := range dedupeStrings(sourceIDs) {
			target := targetByMessageID[sourceID]
			if target == nil {
				continue
			}
			length := len([]rune(description))
			if length > bestLength[target.key] {
				result[target.key] = description
				bestLength[target.key] = length
			}
		}
	}
	return result
}

func (r *Runtime) recallDescriptionTimeline(ctx context.Context, event MessageEvent) []MessageEvent {
	throughTime := event.Time
	if throughTime <= 0 {
		throughTime = time.Now().Unix()
	}
	fromTime := throughTime - int64((recallDefaultWindow+time.Hour)/time.Second)
	r.mu.RLock()
	store := r.messageStore
	inMemory := append([]MessageEvent(nil), r.history[sessionKey(event)]...)
	r.mu.RUnlock()
	timelineStore, ok := store.(MessageTimelineStore)
	if !ok {
		return inMemory
	}
	loadCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	loaded, err := timelineStore.ListMessageEventsBetween(loadCtx, sessionKey(event), fromTime, throughTime)
	if err != nil {
		log.Printf("qqbot recall image description timeline load failed: %v", err)
		return inMemory
	}
	return mergeMessageTimelines(loaded, inMemory)
}

func mergeMessageTimelines(primary, secondary []MessageEvent) []MessageEvent {
	byID := make(map[string]MessageEvent, len(primary)+len(secondary))
	var withoutID []MessageEvent
	for _, events := range [][]MessageEvent{primary, secondary} {
		for _, event := range events {
			if strings.TrimSpace(event.MessageID) == "" {
				withoutID = append(withoutID, event)
				continue
			}
			byID[event.MessageID] = event
		}
	}
	out := make([]MessageEvent, 0, len(byID)+len(withoutID))
	for _, event := range byID {
		out = append(out, event)
	}
	out = append(out, withoutID...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time < out[j].Time })
	return out
}

func recallDescriptionBotMessage(event MessageEvent, cfg BotConfig) bool {
	botQQ := strings.TrimSpace(cfg.BotQQ)
	if botQQ != "" && strings.TrimSpace(event.UserID) == botQQ {
		return true
	}
	return strings.TrimSpace(cfg.Name) != "" && strings.TrimSpace(event.SenderName) == strings.TrimSpace(cfg.Name)
}

func recallDescriptionMessageText(event MessageEvent) string {
	var builder strings.Builder
	for _, segment := range event.Segments {
		if segment.Type == "text" {
			builder.WriteString(segment.Data["text"])
		}
	}
	text := strings.TrimSpace(builder.String())
	if text == "" {
		text = strings.TrimSpace(event.RawMessage)
	}
	text = compactRecallImageDescription(text)
	if len([]rune(text)) < 4 || text == "[图片]" || text == "改好了。[图片]" {
		return ""
	}
	return text
}

func (r *Runtime) describeMissingRecallImages(ctx context.Context, event MessageEvent, targets []*recallImageTarget) {
	var pending []*recallImageTarget
	for _, target := range targets {
		if target.description == "" && target.imageSource != "" {
			pending = append(pending, target)
		}
	}
	if len(pending) == 0 {
		return
	}

	type descriptionResult struct {
		target      *recallImageTarget
		description string
		err         error
	}
	jobs := make(chan *recallImageTarget)
	results := make(chan descriptionResult, len(pending))
	workerCount := recallImageDescriptionConcurrency
	if len(pending) < workerCount {
		workerCount = len(pending)
	}
	var workers sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for target := range jobs {
				description, err := r.describeRecallImage(ctx, event, target.imageSource)
				results <- descriptionResult{target: target, description: description, err: err}
			}
		}()
	}
	go func() {
		for _, target := range pending {
			jobs <- target
		}
		close(jobs)
		workers.Wait()
		close(results)
	}()

	for result := range results {
		if result.err != nil {
			log.Printf("qqbot recall image description failed: message_id=%s err=%v", firstNonEmpty(result.target.sourceMessageIDs...), result.err)
			continue
		}
		result.target.description = compactRecallImageDescription(result.description)
		result.target.descriptionSource = "vision"
		r.saveRecallImageDescription(result.target, event)
	}
}

func (r *Runtime) describeRecallImage(ctx context.Context, event MessageEvent, source string) (string, error) {
	readyImages := llmReadyImageURLs(ctx, []string{source})
	if len(readyImages) == 0 || !strings.HasPrefix(readyImages[0], "data:image/") {
		return "", fmt.Errorf("cached image is unavailable")
	}
	const instruction = "请为这张图片生成可复用的客观中文描述。说明主要人物、物体、场景、界面结构，并完整记录清晰可辨的文字、数字和关键细节。不要回答任何聊天问题，不要推测看不清的内容，不要使用 Markdown，控制在1200字以内。"
	request := llm.GenerateRequest{
		MaxOutputTokens: 1200,
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "你是 Diana 的图片内容缓存子代理。输出将作为后续聊天和撤回记录的可靠视觉事实。"},
			{
				Role:    llm.RoleUser,
				Content: instruction,
				Parts: []llm.ContentPart{
					{Type: llm.ContentPartText, Text: instruction},
					{Type: llm.ContentPartImageURL, ImageURL: readyImages[0], Detail: "auto"},
				},
			},
		},
	}
	callCtx := r.withQQPrivacyContext(ctx, event, nil)
	return r.runLLMProvider(callCtx, func(client LLMProvider) (string, error) {
		response, err := client.Generate(callCtx, request)
		if err != nil {
			return "", err
		}
		description := compactRecallImageDescription(response.Text)
		if description == "" {
			return "", fmt.Errorf("vision model returned an empty description")
		}
		return description, nil
	})
}

func (r *Runtime) saveRecallImageDescription(target *recallImageTarget, event MessageEvent) {
	if target == nil || target.contentSHA256 == "" || strings.TrimSpace(target.description) == "" {
		return
	}
	store := r.recallImageDescriptionStore()
	if store == nil {
		return
	}
	saveCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := store.SaveImageDescription(saveCtx, ImageDescriptionRecord{
		ContentSHA256:   target.contentSHA256,
		Description:     target.description,
		SourceSession:   sessionKey(event),
		SourceMessageID: firstNonEmpty(target.sourceMessageIDs...),
		Source:          target.descriptionSource,
		Version:         recallImageDescriptionVersion,
	}); err != nil {
		log.Printf("qqbot recall image description cache save failed: %v", err)
	}
}
