package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"diana-qq-bot/model/qqbot"
)

const (
	defaultUserFavorability = 0
	ownerUserFavorability   = 100
	minUserFavorability     = -100
	maxUserFavorability     = 200
	maxUserMemoryItems      = 20
	maxUserMemoryTextRunes  = 180
)

// UpdateUserMemory updates one QQ user's long-term profile without calling the LLM.
func (s *SQLiteStore) UpdateUserMemory(ctx context.Context, event qqbot.MessageEvent, update qqbot.UserMemoryUpdate) (qqbot.UserMemoryProfile, error) {
	var profile qqbot.UserMemoryProfile
	if s == nil || s.db == nil {
		return profile, nil
	}
	s.userMemoryMu.Lock()
	defer s.userMemoryMu.Unlock()
	userID := strings.TrimSpace(event.UserID)
	if userID == "" {
		return profile, nil
	}

	profile, ok, err := s.GetUserMemory(ctx, userID)
	if err != nil {
		return qqbot.UserMemoryProfile{}, err
	}
	if !ok {
		profile = qqbot.UserMemoryProfile{
			UserID:       userID,
			Favorability: defaultUserFavorability,
			Memories:     []qqbot.UserMemoryItem{},
		}
	}

	ownerID := strings.TrimSpace(update.OwnerID)
	if ownerID != "" && ownerID == userID && profile.Favorability < ownerUserFavorability {
		profile.Favorability = ownerUserFavorability
	}
	if name := strings.TrimSpace(event.SenderName); name != "" {
		profile.DisplayName = name
	}
	if update.SetFavorability != nil {
		profile.Favorability = clampUserFavorability(*update.SetFavorability, ownerID, userID)
	} else {
		profile.Favorability = clampUserFavorability(profile.Favorability+clampUserFavorabilityDelta(update.FavorabilityDelta), ownerID, userID)
	}
	if !update.Administrative {
		profile.MessageCount++
		profile.LastSeenAt = userMemoryEventTime(event)
		if item, ok := userMemoryItemFromEvent(event); ok {
			profile.Memories = appendUserMemory(profile.Memories, item)
		}
	}
	profile.UpdatedAt = time.Now().UTC()

	memories, err := json.Marshal(profile.Memories)
	if err != nil {
		return qqbot.UserMemoryProfile{}, err
	}
	lastSeen := ""
	if !profile.LastSeenAt.IsZero() {
		lastSeen = profile.LastSeenAt.UTC().Format(time.RFC3339Nano)
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO user_profiles (user_id, display_name, favorability, message_count, memories, last_seen_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(user_id) DO UPDATE SET
  display_name=excluded.display_name,
  favorability=excluded.favorability,
  message_count=excluded.message_count,
  memories=excluded.memories,
  last_seen_at=excluded.last_seen_at,
  updated_at=excluded.updated_at
`, profile.UserID, profile.DisplayName, profile.Favorability, profile.MessageCount, string(memories), lastSeen, profile.UpdatedAt.Format(time.RFC3339Nano))
	return profile, err
}

// GetUserMemory loads one QQ user's long-term profile.
func (s *SQLiteStore) GetUserMemory(ctx context.Context, userID string) (qqbot.UserMemoryProfile, bool, error) {
	var profile qqbot.UserMemoryProfile
	if s == nil || s.db == nil {
		return profile, false, nil
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return profile, false, nil
	}
	var displayName sql.NullString
	var memoriesRaw string
	var lastSeenRaw sql.NullString
	var updatedRaw sql.NullString
	err := s.db.QueryRowContext(ctx, `
SELECT user_id, display_name, favorability, message_count, memories, last_seen_at, updated_at
FROM user_profiles
WHERE user_id = ?
`, userID).Scan(&profile.UserID, &displayName, &profile.Favorability, &profile.MessageCount, &memoriesRaw, &lastSeenRaw, &updatedRaw)
	if err == sql.ErrNoRows {
		return qqbot.UserMemoryProfile{}, false, nil
	}
	if err != nil {
		return qqbot.UserMemoryProfile{}, false, err
	}
	profile.DisplayName = displayName.String
	if strings.TrimSpace(memoriesRaw) != "" {
		if err := json.Unmarshal([]byte(memoriesRaw), &profile.Memories); err != nil {
			return qqbot.UserMemoryProfile{}, false, err
		}
	}
	profile.LastSeenAt = parseUserProfileTime(lastSeenRaw)
	profile.UpdatedAt = parseUserProfileTime(updatedRaw)
	return profile, true, nil
}

func userMemoryEventTime(event qqbot.MessageEvent) time.Time {
	if event.Time > 0 {
		return time.Unix(event.Time, 0).UTC()
	}
	return time.Now().UTC()
}

func userMemoryItemFromEvent(event qqbot.MessageEvent) (qqbot.UserMemoryItem, bool) {
	text := userMemoryEventText(event)
	if !usefulUserMemoryText(text) {
		return qqbot.UserMemoryItem{}, false
	}
	return qqbot.UserMemoryItem{
		Text:      truncateUserMemoryText(text),
		Source:    string(event.Kind),
		GroupID:   event.GroupID,
		MessageID: event.MessageID,
		At:        userMemoryEventTime(event),
	}, true
}

func userMemoryEventText(event qqbot.MessageEvent) string {
	text := strings.TrimSpace(qqbot.PlainText(event.Segments))
	if text == "" {
		text = strings.TrimSpace(event.RawMessage)
	}
	return strings.Join(strings.Fields(text), " ")
}

func usefulUserMemoryText(text string) bool {
	text = strings.TrimSpace(text)
	if len([]rune(text)) < 2 {
		return false
	}
	lower := strings.ToLower(text)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return strings.Contains(lower, " ")
	}
	return true
}

func appendUserMemory(memories []qqbot.UserMemoryItem, item qqbot.UserMemoryItem) []qqbot.UserMemoryItem {
	for _, existing := range memories {
		if existing.Text == item.Text && existing.GroupID == item.GroupID {
			return memories
		}
	}
	memories = append(memories, item)
	if len(memories) > maxUserMemoryItems {
		memories = memories[len(memories)-maxUserMemoryItems:]
	}
	return memories
}

func clampUserFavorabilityDelta(delta int) int {
	if delta < -3 {
		return -3
	}
	if delta > 3 {
		return 3
	}
	return delta
}

func clampUserFavorability(value int, ownerID string, userID string) int {
	minValue := minUserFavorability
	if ownerID != "" && ownerID == userID {
		minValue = ownerUserFavorability
	}
	if value < minValue {
		return minValue
	}
	if value > maxUserFavorability {
		return maxUserFavorability
	}
	return value
}

func truncateUserMemoryText(text string) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= maxUserMemoryTextRunes {
		return string(runes)
	}
	return string(runes[:maxUserMemoryTextRunes]) + "..."
}

func parseUserProfileTime(value sql.NullString) time.Time {
	if !value.Valid || strings.TrimSpace(value.String) == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, value.String)
	if err != nil {
		return time.Time{}
	}
	return parsed
}
