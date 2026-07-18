package qqbot

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"testing"
	"time"
)

func TestLocalMediaStoreServesSharedFile(t *testing.T) {
	tempDir := t.TempDir()
	videoPath := filepath.Join(tempDir, "video.mp4")
	if err := os.WriteFile(videoPath, []byte("fake video"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	store := NewLocalMediaStore("http://127.0.0.1:18080/api/qqbot/media")
	sharedURL, ok := store.Share(videoPath, time.Minute)
	if !ok {
		t.Fatal("Share() returned false")
	}
	parsed, err := url.Parse(sharedURL)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	token := path.Base(parsed.Path)
	req := httptest.NewRequest(http.MethodGet, "/api/qqbot/media/"+token, nil)
	rec := httptest.NewRecorder()
	store.ServeToken(rec, req, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q", rec.Code, rec.Body.String())
	}
	body, err := io.ReadAll(rec.Result().Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if string(body) != "fake video" {
		t.Fatalf("body = %q", body)
	}
}

func TestLocalMediaStoreExpiresSharedFile(t *testing.T) {
	tempDir := t.TempDir()
	videoPath := filepath.Join(tempDir, "video.mp4")
	if err := os.WriteFile(videoPath, []byte("fake video"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	now := time.Now()
	store := NewLocalMediaStore("http://127.0.0.1:18080/api/qqbot/media")
	store.now = func() time.Time { return now }
	sharedURL, ok := store.Share(videoPath, time.Second)
	if !ok {
		t.Fatal("Share() returned false")
	}
	parsed, err := url.Parse(sharedURL)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	token := path.Base(parsed.Path)
	now = now.Add(2 * time.Second)

	req := httptest.NewRequest(http.MethodGet, "/api/qqbot/media/"+token, nil)
	rec := httptest.NewRecorder()
	store.ServeToken(rec, req, token)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}
