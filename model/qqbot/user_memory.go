package qqbot

import "time"

type UserMemoryUpdate struct {
	OwnerID           string `json:"owner_id,omitempty"`
	FavorabilityDelta int    `json:"favorability_delta,omitempty"`
	SetFavorability   *int   `json:"set_favorability,omitempty"`
	Administrative    bool   `json:"administrative,omitempty"`
}

type UserMemoryProfile struct {
	UserID       string           `json:"user_id"`
	DisplayName  string           `json:"display_name,omitempty"`
	Favorability int              `json:"favorability"`
	MessageCount int              `json:"message_count"`
	Memories     []UserMemoryItem `json:"memories,omitempty"`
	LastSeenAt   time.Time        `json:"last_seen_at,omitempty"`
	UpdatedAt    time.Time        `json:"updated_at,omitempty"`
}

type UserMemoryItem struct {
	Text      string    `json:"text"`
	Source    string    `json:"source,omitempty"`
	GroupID   string    `json:"group_id,omitempty"`
	MessageID string    `json:"message_id,omitempty"`
	At        time.Time `json:"at,omitempty"`
}
