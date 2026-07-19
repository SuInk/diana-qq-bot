package qqbot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const historyMediaReadyTimeout = 5 * time.Second

const imageContentSHA256Key = "content_sha256"

func cacheMessageEventImages(ctx context.Context, event MessageEvent) MessageEvent {
	event.Segments = cacheImageSegments(ctx, string(event.Kind), event.GroupID, event.UserID, event.MessageID, event.Segments)
	if event.Quoted != nil {
		quoted := *event.Quoted
		quoted.Segments = cacheImageSegments(ctx, "quoted", firstNonEmpty(quoted.GroupID, event.GroupID), firstNonEmpty(quoted.UserID, event.UserID), quoted.MessageID, quoted.Segments)
		event.Quoted = &quoted
	}
	return event
}

func cacheMessageEventVideos(ctx context.Context, event MessageEvent) MessageEvent {
	event.Segments = cacheVideoFrames(ctx, string(event.Kind), event.GroupID, event.UserID, event.MessageID, event.Segments)
	if event.Quoted != nil {
		quoted := *event.Quoted
		quoted.Segments = cacheVideoFrames(ctx, "quoted", firstNonEmpty(quoted.GroupID, event.GroupID), firstNonEmpty(quoted.UserID, event.UserID), quoted.MessageID, quoted.Segments)
		event.Quoted = &quoted
	}
	return event
}

func cacheVideoFrames(ctx context.Context, targetKind, groupID, userID, messageID string, segments []MessageSegment) []MessageSegment {
	if len(segments) == 0 || hasCachedVideoFrames(segments) {
		return segments
	}
	videoURLs := videoSourceCandidates(segments)
	if len(videoURLs) == 0 {
		return segments
	}
	frames := extractVideoContextFramesAfterReady(ctx, videoURLs, historyMediaReadyTimeout)
	defer cleanupVideoContextFrames(frames)
	if len(frames) == 0 {
		log.Printf("qqbot video history cache produced no frames: message_id=%s", messageID)
		return segments
	}
	out := append([]MessageSegment(nil), segments...)
	for i, frame := range frames {
		body, err := os.ReadFile(frame)
		if err != nil {
			continue
		}
		source := fmt.Sprintf("video-frame:%d:%s", i, firstNonEmpty(videoURLs...))
		path, err := writeHistoryImage(targetKind, groupID, userID, messageID, source, body, "image/jpeg")
		if err != nil {
			continue
		}
		out = append(out, MessageSegment{
			Type: "image",
			Data: map[string]string{
				"cached_file":         path,
				"cached_mime":         "image/jpeg",
				"cached_size":         fmt.Sprint(len(body)),
				imageContentSHA256Key: imageBytesSHA256(body),
				"source_type":         "video_frame",
				"frame_index":         fmt.Sprint(i),
			},
		})
	}
	log.Printf("qqbot video history cached: message_id=%s frames=%d", messageID, len(cachedVideoFrameURLs(out)))
	return out
}

func hasCachedVideoFrames(segments []MessageSegment) bool {
	for _, segment := range segments {
		if segment.Type == "image" && segment.Data["source_type"] == "video_frame" && strings.TrimSpace(segment.Data["cached_file"]) != "" {
			return true
		}
	}
	return false
}

func cachedVideoFrameURLs(segments []MessageSegment) []string {
	out := make([]string, 0, 4)
	for _, segment := range segments {
		if segment.Type != "image" || segment.Data["source_type"] != "video_frame" {
			continue
		}
		if path := normalizedImageURL(segment.Data["cached_file"]); path != "" {
			out = append(out, path)
		}
	}
	return out
}

func cacheImageSegments(ctx context.Context, targetKind, groupID, userID, messageID string, segments []MessageSegment) []MessageSegment {
	if len(segments) == 0 {
		return segments
	}
	out := make([]MessageSegment, len(segments))
	copy(out, segments)
	for i, segment := range out {
		if segment.Type != "image" {
			continue
		}
		data := cloneSegmentData(segment.Data)
		if cached := normalizedLocalImagePath(segment.Data["cached_file"]); cached != "" {
			if data[imageContentSHA256Key] == "" {
				if hash, ok := imageSegmentContentSHA256(segment); ok {
					data[imageContentSHA256Key] = hash
				}
			}
			out[i].Data = data
			continue
		}
		source := firstImageSource(segment)
		if source == "" {
			continue
		}
		body, contentType, err := readHistoryImageSource(ctx, source, historyMediaReadyTimeout)
		if err != nil {
			continue
		}
		path, err := writeHistoryImage(targetKind, groupID, userID, messageID, source, body, contentType)
		if err != nil {
			continue
		}
		data["cached_file"] = path
		data["cached_mime"] = contentType
		data["cached_size"] = fmt.Sprint(len(body))
		data[imageContentSHA256Key] = imageBytesSHA256(body)
		out[i].Data = data
	}
	return out
}

func imageBytesSHA256(body []byte) string {
	hash := sha256.Sum256(body)
	return hex.EncodeToString(hash[:])
}

func imageSegmentContentSHA256(segment MessageSegment) (string, bool) {
	if segment.Type != "image" {
		return "", false
	}
	if value := strings.ToLower(strings.TrimSpace(segment.Data[imageContentSHA256Key])); validSHA256(value) {
		return value, true
	}
	path := normalizedLocalImagePath(segment.Data["cached_file"])
	if path == "" {
		return "", false
	}
	body, err := os.ReadFile(path)
	if err != nil || len(body) == 0 {
		return "", false
	}
	return imageBytesSHA256(body), true
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func firstRemoteImageURL(segment MessageSegment) string {
	for _, key := range []string{"url", "image_url", "src", "file"} {
		if value := normalizedHTTPURL(segment.Data[key]); value != "" {
			return value
		}
	}
	return ""
}

func firstImageSource(segment MessageSegment) string {
	for _, key := range []string{"cached_file", "url", "image_url", "src", "file", "path"} {
		value := strings.TrimSpace(segment.Data[key])
		if normalizedHTTPURL(value) != "" || rawAbsoluteMediaPath(value) != "" {
			return value
		}
	}
	return ""
}

func readHistoryImageSource(ctx context.Context, source string, wait time.Duration) ([]byte, string, error) {
	if remote := normalizedHTTPURL(source); remote != "" {
		return downloadImageBytes(ctx, remote)
	}
	path := waitForLocalMediaPath(ctx, source, wait, maxLLMImageBytes)
	if path == "" {
		return nil, "", fmt.Errorf("image source is unavailable")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, maxLLMImageBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(body) == 0 || len(body) > maxLLMImageBytes {
		return nil, "", fmt.Errorf("image size is invalid")
	}
	contentType := imageContentType(http.DetectContentType(body), body)
	if !strings.HasPrefix(contentType, "image/") {
		return nil, "", fmt.Errorf("local content is not an image")
	}
	return body, contentType, nil
}

func writeHistoryImage(targetKind, groupID, userID, messageID, source string, body []byte, contentType string) (string, error) {
	baseDir, err := historyMediaDir()
	if err != nil {
		return "", err
	}
	session := historyMediaSession(targetKind, groupID, userID)
	messageID = safeHistoryPart(firstNonEmpty(messageID, "no-message"))
	hash := sha256.Sum256([]byte(session + ":" + messageID + ":" + source))
	name := hex.EncodeToString(hash[:])[:16] + imageExtension(contentType, body)
	dir := filepath.Join(baseDir, session, messageID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func historyMediaDir() (string, error) {
	if value := strings.TrimSpace(os.Getenv("DIANA_HISTORY_MEDIA_DIR")); value != "" {
		return value, nil
	}
	if dbPath := strings.TrimSpace(os.Getenv("APP_DB_PATH")); dbPath != "" {
		return filepath.Join(filepath.Dir(dbPath), "history-media"), nil
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cacheDir, "diana-qq-bot", "history-media"), nil
}

func historyMediaSession(targetKind, groupID, userID string) string {
	if targetKind == string(EventKindGroup) || groupID != "" {
		return "group_" + safeHistoryPart(firstNonEmpty(groupID, "unknown"))
	}
	return "private_" + safeHistoryPart(firstNonEmpty(userID, "unknown"))
}

func safeHistoryPart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	var builder strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			builder.WriteRune(r)
		} else {
			builder.WriteByte('_')
		}
	}
	if builder.Len() == 0 {
		return "unknown"
	}
	return builder.String()
}

func imageExtension(contentType string, body []byte) string {
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	}
	if len(body) >= 12 && string(body[:4]) == "RIFF" && string(body[8:12]) == "WEBP" {
		return ".webp"
	}
	return ".img"
}
