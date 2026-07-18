package qqbot

import "strings"

// messageParticipantDisplayNames builds a reusable QQ identity map from
// message senders and quoted senders. Events must be passed in priority order.
func messageParticipantDisplayNames(events ...MessageEvent) map[string]string {
	names := make(map[string]string)
	add := func(userID, displayName string) {
		userID = strings.TrimSpace(userID)
		displayName = strings.TrimSpace(displayName)
		if userID == "" || displayName == "" || names[userID] != "" {
			return
		}
		names[userID] = displayName
	}
	for _, event := range events {
		add(event.UserID, event.SenderName)
		if event.Quoted != nil {
			add(event.Quoted.UserID, event.Quoted.SenderName)
		}
	}
	return names
}
