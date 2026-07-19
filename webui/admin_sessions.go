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

const adminSessionStoreVersion = 1

type persistedAdminSessionStore struct {
	Version  int                     `json:"version"`
	KeyID    string                  `json:"key_id"`
	Sessions []persistedAdminSession `json:"sessions"`
}

type persistedAdminSession struct {
	IDHash    string    `json:"id_hash"`
	ExpiresAt time.Time `json:"expires_at"`
}

func adminSessionIDHash(sessionID string) string {
	digest := sha256.Sum256([]byte(sessionID))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func adminSessionKeyID(token string) string {
	digest := sha256.Sum256([]byte("diana-admin-session-v1\x00" + token))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func loadAdminSessions(path, keyID string, now time.Time) (map[string]time.Time, error) {
	sessions := make(map[string]time.Time)
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
	var store persistedAdminSessionStore
	if err := json.Unmarshal(body, &store); err != nil {
		return nil, fmt.Errorf("decode admin sessions: %w", err)
	}
	if store.Version != adminSessionStoreVersion {
		return nil, fmt.Errorf("unsupported admin session store version %d", store.Version)
	}
	if !secureEqual(store.KeyID, keyID) {
		return sessions, nil
	}
	for _, session := range store.Sessions {
		if strings.TrimSpace(session.IDHash) != "" && now.Before(session.ExpiresAt) {
			sessions[session.IDHash] = session.ExpiresAt
		}
	}
	return sessions, nil
}

func persistAdminSessions(path, keyID string, sessions map[string]time.Time) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	items := make([]persistedAdminSession, 0, len(sessions))
	for idHash, expiresAt := range sessions {
		items = append(items, persistedAdminSession{IDHash: idHash, ExpiresAt: expiresAt.UTC()})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].ExpiresAt.Equal(items[j].ExpiresAt) {
			return items[i].IDHash < items[j].IDHash
		}
		return items[i].ExpiresAt.Before(items[j].ExpiresAt)
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
