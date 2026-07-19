package webui

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const adminSessionStoreVersion = 2

type persistedAdminSessionStore struct {
	Version  int            `json:"version"`
	KeyID    string         `json:"key_id"`
	Sessions []adminSession `json:"sessions"`
}

type adminSession struct {
	ID          string    `json:"id"`
	RefreshHash string    `json:"refresh_hash"`
	AccessID    string    `json:"access_id"`
	AccountID   string    `json:"account_id"`
	DeviceName  string    `json:"device_name"`
	UserAgent   string    `json:"user_agent,omitempty"`
	IPAddress   string    `json:"ip_address,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

func adminSessionIDHash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func adminSessionKeyID(secret string) string {
	digest := sha256.Sum256([]byte("diana-admin-session-v2\x00" + secret))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func loadAdminSessions(path, keyID string, now time.Time) (map[string]adminSession, error) {
	sessions := make(map[string]adminSession)
	if strings.TrimSpace(path) == "" {
		return sessions, nil
	}
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return sessions, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read admin sessions: %w", err)
	}
	var version struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(body, &version); err != nil {
		return nil, fmt.Errorf("decode admin sessions: %w", err)
	}
	// Version 1 sessions used opaque browser cookies and cannot be upgraded to
	// refresh tokens. They are intentionally invalidated during migration.
	if version.Version == 1 {
		return sessions, nil
	}
	if version.Version != adminSessionStoreVersion {
		return nil, fmt.Errorf("unsupported admin session store version %d", version.Version)
	}
	var store persistedAdminSessionStore
	if err := json.Unmarshal(body, &store); err != nil {
		return nil, fmt.Errorf("decode admin sessions: %w", err)
	}
	if !secureEqual(store.KeyID, keyID) {
		return sessions, nil
	}
	for _, session := range store.Sessions {
		if strings.TrimSpace(session.ID) == "" || strings.TrimSpace(session.RefreshHash) == "" || strings.TrimSpace(session.AccessID) == "" || strings.TrimSpace(session.AccountID) == "" || !now.Before(session.ExpiresAt) {
			continue
		}
		session.CreatedAt = session.CreatedAt.UTC()
		session.LastSeenAt = session.LastSeenAt.UTC()
		session.ExpiresAt = session.ExpiresAt.UTC()
		sessions[session.ID] = session
	}
	return sessions, nil
}

func persistAdminSessions(path, keyID string, sessions map[string]adminSession) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	items := make([]adminSession, 0, len(sessions))
	for _, session := range sessions {
		items = append(items, session)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].LastSeenAt.Equal(items[j].LastSeenAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].LastSeenAt.After(items[j].LastSeenAt)
	})
	store := persistedAdminSessionStore{Version: adminSessionStoreVersion, KeyID: keyID, Sessions: items}

	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create admin session directory: %w", err)
	}
	file, err := os.CreateTemp(directory, ".admin-sessions-*.json")
	if err != nil {
		return fmt.Errorf("create admin session store: %w", err)
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("secure admin session store: %w", err)
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(store); err != nil {
		_ = file.Close()
		return fmt.Errorf("encode admin sessions: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close admin session store: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace admin session store: %w", err)
	}
	return nil
}
