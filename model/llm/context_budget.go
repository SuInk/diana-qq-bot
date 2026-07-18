package llm

import (
	"sort"
	"strings"
	"unicode"
)

const (
	messageTokenOverhead       int64 = 8
	contextBudgetSafetyReserve int64 = 128
	minimumRequiredMessageCost int64 = messageTokenOverhead + 8
	truncationMarker                 = "\n...[上下文已按 token 预算裁剪]...\n"
)

type tokenBudgetCandidate struct {
	index    int
	priority MessagePriority
	cost     int64
}

// applyContextBudget keeps every request below the configured total context
// budget. The estimator is deliberately conservative across providers: CJK and
// non-ASCII text cost more than ASCII, and image parts reserve vision tokens.
func applyContextBudget(req GenerateRequest, cfg ProviderConfig) GenerateRequest {
	limit := cfg.MaxContextTokensWithDefault()
	if limit <= 0 || len(req.Messages) == 0 {
		return req
	}
	outputReserve := req.MaxOutputTokens
	if outputReserve <= 0 {
		outputReserve = DefaultMaxOutputTokens
	}
	inputBudget := limit - outputReserve
	if inputBudget > contextBudgetSafetyReserve {
		inputBudget -= contextBudgetSafetyReserve
	}
	if inputBudget < 1 {
		inputBudget = 1
	}
	req.Messages = fitMessagesToTokenBudget(req.Messages, inputBudget)
	return req
}

func fitMessagesToTokenBudget(messages []Message, budget int64) []Message {
	if len(messages) == 0 || budget <= 0 {
		return nil
	}
	if estimateMessagesTokens(messages) <= budget {
		return append([]Message(nil), messages...)
	}

	candidates := make([]tokenBudgetCandidate, 0, len(messages))
	lastIndex := len(messages) - 1
	for index, message := range messages {
		priority := effectiveMessagePriority(message, index == lastIndex)
		candidates = append(candidates, tokenBudgetCandidate{index: index, priority: priority, cost: estimateMessageTokens(message)})
	}
	sort.SliceStable(candidates, func(left, right int) bool {
		if candidates[left].priority == candidates[right].priority {
			return candidates[left].index > candidates[right].index
		}
		return candidates[left].priority > candidates[right].priority
	})

	selected := make(map[int]Message, len(messages))
	remaining := budget
	remaining = selectRequiredMessages(messages, candidates, selected, remaining)
	for _, item := range candidates {
		if remaining <= 0 {
			break
		}
		if _, ok := selected[item.index]; ok || item.priority >= MessagePrioritySystem {
			continue
		}
		message := messages[item.index]
		if item.cost <= remaining {
			selected[item.index] = message
			remaining -= item.cost
			continue
		}
		if item.priority < MessagePrioritySummary {
			continue
		}
		trimmed, ok := trimMessageToTokenBudget(message, remaining)
		if !ok {
			continue
		}
		selected[item.index] = trimmed
		remaining -= estimateMessageTokens(trimmed)
	}

	if len(selected) == 0 {
		if trimmed, ok := trimMessageToTokenBudget(messages[lastIndex], budget); ok {
			selected[lastIndex] = trimmed
		}
	}
	out := make([]Message, 0, len(selected))
	for index := range messages {
		if message, ok := selected[index]; ok {
			out = append(out, message)
		}
	}
	return out
}

// System instructions, plugin evidence, and the current user turn are required.
// Preserve plugin evidence and current input whole when feasible, then allocate
// the remaining space across generic system instructions.
func selectRequiredMessages(messages []Message, candidates []tokenBudgetCandidate, selected map[int]Message, budget int64) int64 {
	required := make([]struct {
		index    int
		cost     int64
		priority MessagePriority
	}, 0, 2)
	var totalCost int64
	for _, item := range candidates {
		if item.priority < MessagePrioritySystem {
			continue
		}
		required = append(required, struct {
			index    int
			cost     int64
			priority MessagePriority
		}{index: item.index, cost: item.cost, priority: item.priority})
		totalCost += item.cost
	}
	if len(required) == 0 || budget <= 0 {
		return budget
	}
	sort.Slice(required, func(left, right int) bool {
		return required[left].index < required[right].index
	})
	if totalCost <= budget {
		for _, item := range required {
			selected[item.index] = messages[item.index]
			budget -= item.cost
		}
		return budget
	}

	// Current input and authoritative plugin evidence must remain intact whenever
	// they can fit. Generic system instructions are the expendable part: trim
	// those before silently removing facts returned by a plugin.
	protectedCost := int64(0)
	flexibleCount := int64(0)
	for _, item := range required {
		if item.priority >= MessagePriorityPlugin {
			protectedCost += item.cost
		} else {
			flexibleCount++
		}
	}
	minimumForFlexible := flexibleCount * minimumRequiredMessageCost
	if protectedCost+minimumForFlexible <= budget {
		for _, item := range required {
			if item.priority < MessagePriorityPlugin {
				continue
			}
			selected[item.index] = messages[item.index]
			budget -= item.cost
		}
		flexible := required[:0]
		for _, item := range required {
			if item.priority < MessagePriorityPlugin {
				flexible = append(flexible, item)
			}
		}
		return selectRequiredMessagesProportionally(messages, flexible, selected, budget)
	}

	return selectRequiredMessagesProportionally(messages, required, selected, budget)
}

func selectRequiredMessagesProportionally(messages []Message, required []struct {
	index    int
	cost     int64
	priority MessagePriority
}, selected map[int]Message, budget int64) int64 {
	var totalCost int64
	for _, item := range required {
		totalCost += item.cost
	}

	remainingCost := totalCost
	for index, item := range required {
		slotsAfter := int64(len(required) - index - 1)
		if budget <= messageTokenOverhead {
			break
		}
		allocation := budget
		if remainingCost > 0 {
			allocation = budget * item.cost / remainingCost
		}
		minimumForOthers := slotsAfter * minimumRequiredMessageCost
		if maximum := budget - minimumForOthers; allocation > maximum {
			allocation = maximum
		}
		if allocation < minimumRequiredMessageCost {
			allocation = minimumRequiredMessageCost
		}
		if allocation > budget {
			allocation = budget
		}
		trimmed, ok := trimMessageToTokenBudget(messages[item.index], allocation)
		if ok {
			selected[item.index] = trimmed
			budget -= estimateMessageTokens(trimmed)
		}
		remainingCost -= item.cost
	}
	return budget
}

func effectiveMessagePriority(message Message, current bool) MessagePriority {
	priority := message.Priority
	if priority == MessagePriorityDefault {
		priority = MessagePriorityHistory
	}
	if message.Role == RoleSystem && priority < MessagePrioritySystem {
		priority = MessagePrioritySystem
	}
	if current && priority < MessagePriorityCurrent {
		priority = MessagePriorityCurrent
	}
	return priority
}

func estimateMessagesTokens(messages []Message) int64 {
	var total int64
	for _, message := range messages {
		total += estimateMessageTokens(message)
	}
	return total
}

func estimateMessageTokens(message Message) int64 {
	total := messageTokenOverhead
	if len(message.Parts) == 0 {
		return total + estimateTextTokens(message.Content)
	}
	hasText := false
	for _, part := range message.Parts {
		switch part.Type {
		case ContentPartText:
			if strings.TrimSpace(part.Text) != "" {
				hasText = true
				total += estimateTextTokens(part.Text) + 2
			}
		case ContentPartImageURL:
			if strings.TrimSpace(part.ImageURL) != "" {
				total += estimatedImageTokens(part.Detail)
			}
		}
	}
	if !hasText {
		total += estimateTextTokens(message.Content)
	}
	return total
}

func estimateTextTokens(text string) int64 {
	var total int64
	var asciiRun int64
	flushASCII := func() {
		if asciiRun > 0 {
			// Three ASCII characters per token is conservative for prose, JSON,
			// URLs, and tool output without treating every byte as a full token.
			total += (asciiRun + 2) / 3
			asciiRun = 0
		}
	}
	for _, value := range text {
		if value <= unicode.MaxASCII {
			asciiRun++
			continue
		}
		flushASCII()
		if value > 0xffff {
			total += 4
		} else {
			total += 2
		}
	}
	flushASCII()
	return total
}

func estimatedImageTokens(detail string) int64 {
	switch strings.ToLower(strings.TrimSpace(detail)) {
	case "low":
		return 1024
	case "high":
		return 8192
	case "original":
		return 16384
	default:
		return 4096
	}
}

func trimMessageToTokenBudget(message Message, budget int64) (Message, bool) {
	if budget <= messageTokenOverhead {
		return Message{}, false
	}
	textBudget := budget - messageTokenOverhead
	trimmed := message
	if len(message.Parts) == 0 {
		trimmed.Content = trimTextToTokenBudget(message.Content, textBudget, preserveMessagePrefix(message))
		return trimmed, strings.TrimSpace(trimmed.Content) != ""
	}

	trimmed.Parts = nil
	remaining := textBudget
	hasText := false
	for _, part := range message.Parts {
		switch part.Type {
		case ContentPartText:
			if remaining <= 2 {
				continue
			}
			text := strings.TrimSpace(part.Text)
			if text == "" {
				continue
			}
			partBudget := remaining - 2
			if cost := estimateTextTokens(text); cost > partBudget {
				text = trimTextToTokenBudget(text, partBudget, preserveMessagePrefix(message))
			}
			if text == "" {
				continue
			}
			part.Text = text
			trimmed.Parts = append(trimmed.Parts, part)
			hasText = true
			remaining -= estimateTextTokens(text) + 2
		case ContentPartImageURL:
			cost := estimatedImageTokens(part.Detail)
			if strings.TrimSpace(part.ImageURL) == "" || cost > remaining {
				continue
			}
			trimmed.Parts = append(trimmed.Parts, part)
			remaining -= cost
		}
	}
	if !hasText && remaining > 0 {
		text := trimTextToTokenBudget(message.Content, remaining, preserveMessagePrefix(message))
		if text != "" {
			trimmed.Content = text
			trimmed.Parts = append([]ContentPart{{Type: ContentPartText, Text: text}}, trimmed.Parts...)
		}
	}
	return trimmed, len(trimmed.Parts) > 0
}

func preserveMessagePrefix(message Message) bool {
	return message.Priority == MessagePriorityMemory || message.Priority == MessagePrioritySummary
}

func trimTextToTokenBudget(text string, budget int64, prefixOnly bool) string {
	text = strings.TrimSpace(text)
	if text == "" || budget <= 0 {
		return ""
	}
	if estimateTextTokens(text) <= budget {
		return text
	}
	runes := []rune(text)
	low, high := 1, len(runes)
	best := ""
	for low <= high {
		mid := low + (high-low)/2
		candidate := clippedText(runes, mid, prefixOnly)
		if estimateTextTokens(candidate) <= budget {
			best = candidate
			low = mid + 1
		} else {
			high = mid - 1
		}
	}
	return strings.TrimSpace(best)
}

func clippedText(runes []rune, keep int, prefixOnly bool) string {
	if keep >= len(runes) {
		return string(runes)
	}
	marker := []rune(truncationMarker)
	if keep <= len(marker)+2 {
		if prefixOnly {
			return string(runes[:keep])
		}
		return string(runes[len(runes)-keep:])
	}
	contentRunes := keep - len(marker)
	if prefixOnly {
		return string(runes[:contentRunes]) + truncationMarker
	}
	head := (contentRunes + 1) / 2
	tail := contentRunes - head
	return string(runes[:head]) + truncationMarker + string(runes[len(runes)-tail:])
}
