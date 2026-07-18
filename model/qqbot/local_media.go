package qqbot

import (
	"mime"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type LocalMediaSharer interface {
	Share(path string, ttl time.Duration) (string, bool)
}

type LocalMediaStore struct {
	mu      sync.RWMutex
	baseURL string
	items   map[string]localMediaItem
	now     func() time.Time
}

type localMediaItem struct {
	Path      string
	Name      string
	ExpiresAt time.Time
}

func NewLocalMediaStore(baseURL string) *LocalMediaStore {
	return &LocalMediaStore{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		items:   map[string]localMediaItem{},
		now:     time.Now,
	}
}

func (s *LocalMediaStore) Share(path string, ttl time.Duration) (string, bool) {
	if s == nil || s.baseURL == "" {
		return "", false
	}
	path = strings.TrimSpace(strings.TrimPrefix(path, "file://"))
	if path == "" || !filepath.IsAbs(path) {
		return "", false
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() == 0 {
		return "", false
	}
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	now := s.now()
	token := uuid.NewString()

	s.mu.Lock()
	s.cleanupExpiredLocked(now)
	s.items[token] = localMediaItem{
		Path:      path,
		Name:      filepath.Base(path),
		ExpiresAt: now.Add(ttl),
	}
	s.mu.Unlock()

	return s.baseURL + "/" + neturl.PathEscape(token), true
}

func (s *LocalMediaStore) ServeToken(w http.ResponseWriter, r *http.Request, token string) {
	item, ok := s.lookup(token)
	if !ok {
		http.NotFound(w, r)
		return
	}
	file, err := os.Open(item.Path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.IsDir() || info.Size() == 0 {
		http.NotFound(w, r)
		return
	}
	if disposition := mime.FormatMediaType("inline", map[string]string{"filename": item.Name}); disposition != "" {
		w.Header().Set("Content-Disposition", disposition)
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, item.Name, info.ModTime(), file)
}

func (s *LocalMediaStore) lookup(token string) (localMediaItem, bool) {
	if s == nil {
		return localMediaItem{}, false
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return localMediaItem{}, false
	}
	now := s.now()
	s.mu.RLock()
	item, ok := s.items[token]
	s.mu.RUnlock()
	if !ok {
		return localMediaItem{}, false
	}
	if now.After(item.ExpiresAt) {
		s.mu.Lock()
		if current, ok := s.items[token]; ok && now.After(current.ExpiresAt) {
			delete(s.items, token)
		}
		s.mu.Unlock()
		return localMediaItem{}, false
	}
	return item, true
}

func (s *LocalMediaStore) cleanupExpiredLocked(now time.Time) {
	for token, item := range s.items {
		if now.After(item.ExpiresAt) {
			delete(s.items, token)
		}
	}
}
