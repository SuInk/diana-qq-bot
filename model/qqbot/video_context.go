package qqbot

import (
	"context"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	maxVideoContextBytes     = 100 << 20
	minVideoContextFrames    = 4
	maxVideoContextFrames    = 16
	videoFrameGrowthInterval = 30.0
)

func localVideoPath(value string) string {
	path := strings.TrimSpace(strings.TrimPrefix(value, "file://"))
	if path == "" || !filepath.IsAbs(path) {
		return ""
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() <= 0 || info.Size() > maxVideoContextBytes {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || !videoContextPathAllowed(resolved) {
		return ""
	}
	return resolved
}

func videoContextPathAllowed(path string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	roots := []string{
		filepath.Join(home, "Library", "Containers", "com.tencent.qq"),
		filepath.Join(home, "Library", "Application Support", "QQ"),
		filepath.Join(home, "Library", "Application Support", "diana-qq-bot"),
		os.TempDir(),
	}
	clean := filepath.Clean(path)
	for _, root := range roots {
		root = filepath.Clean(root)
		if resolvedRoot, resolveErr := filepath.EvalSymlinks(root); resolveErr == nil {
			root = resolvedRoot
		}
		if clean == root || strings.HasPrefix(clean, root+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

func extractVideoContextFrames(ctx context.Context, sources []string) []string {
	return extractVideoContextFramesAfterReady(ctx, sources, 0)
}

func extractVideoContextFramesAfterReady(ctx context.Context, sources []string, wait time.Duration) []string {
	if len(sources) == 0 {
		return nil
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		log.Printf("qqbot video context unavailable: ffmpeg not found")
		return nil
	}
	out := make([]string, 0, maxVideoContextFrames)
	seen := map[string]bool{}
	for _, source := range sources {
		path, cleanup := materializeVideoContextSource(ctx, source, wait)
		if path == "" || seen[path] {
			cleanup()
			continue
		}
		seen[path] = true
		frames := extractLocalVideoFrames(ctx, path, maxVideoContextFrames-len(out))
		cleanup()
		out = append(out, frames...)
		if len(out) >= maxVideoContextFrames {
			break
		}
	}
	return out
}

func materializeVideoContextSource(ctx context.Context, source string, wait time.Duration) (string, func()) {
	if remote := normalizedHTTPURL(source); remote != "" {
		path, dir, err := downloadVideoContextSource(ctx, remote)
		if err != nil {
			log.Printf("qqbot video download failed: %v", err)
			return "", func() {}
		}
		return path, func() { _ = os.RemoveAll(dir) }
	}
	path := waitForLocalMediaPath(ctx, source, wait, maxVideoContextBytes)
	if path == "" {
		return "", func() {}
	}
	path = localVideoPath(path)
	return path, func() {}
}

func downloadVideoContextSource(ctx context.Context, source string) (string, string, error) {
	workDir, err := os.MkdirTemp("", "diana-video-download-*")
	if err != nil {
		return "", "", err
	}
	cleanup := func(err error) (string, string, error) {
		_ = os.RemoveAll(workDir)
		return "", "", err
	}
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, source, nil)
	if err != nil {
		return cleanup(err)
	}
	req.Header.Set("User-Agent", "DianaQQBot/0.1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return cleanup(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return cleanup(fmt.Errorf("HTTP %d", resp.StatusCode))
	}
	path := filepath.Join(workDir, "source.mp4")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return cleanup(err)
	}
	written, copyErr := io.Copy(file, io.LimitReader(resp.Body, maxVideoContextBytes+1))
	closeErr := file.Close()
	if copyErr != nil {
		return cleanup(copyErr)
	}
	if closeErr != nil {
		return cleanup(closeErr)
	}
	if written <= 0 || written > maxVideoContextBytes {
		return cleanup(fmt.Errorf("video size is invalid: %d", written))
	}
	return path, workDir, nil
}

func waitForLocalMediaPath(ctx context.Context, source string, wait time.Duration, maxBytes int64) string {
	path := rawAbsoluteMediaPath(source)
	if path == "" {
		return ""
	}
	deadline := time.Now().Add(wait)
	for {
		info, err := os.Stat(path)
		if err == nil && !info.IsDir() && info.Size() > 0 && info.Size() <= maxBytes {
			return path
		}
		if wait <= 0 || time.Now().After(deadline) {
			return ""
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ""
		case <-timer.C:
		}
	}
}

func rawAbsoluteMediaPath(value string) string {
	value = strings.TrimSpace(strings.TrimPrefix(value, "file://"))
	if value == "" || !filepath.IsAbs(value) {
		return ""
	}
	return filepath.Clean(value)
}

func extractLocalVideoFrames(ctx context.Context, videoPath string, limit int) []string {
	if limit <= 0 {
		return nil
	}
	workDir, err := os.MkdirTemp("", "diana-video-context-*")
	if err != nil {
		return nil
	}
	stagedPath, err := stageVideoForContext(videoPath, workDir)
	if err != nil {
		log.Printf("qqbot video staging failed for %s: %v", filepath.Base(videoPath), err)
		_ = os.RemoveAll(workDir)
		return nil
	}
	duration := probeVideoDuration(ctx, stagedPath)
	timestamps := videoFrameTimestamps(duration, limit)
	frames := make([]string, 0, len(timestamps))
	for i, timestamp := range timestamps {
		framePath := filepath.Join(workDir, fmt.Sprintf("frame-%02d.jpg", i+1))
		callCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
		cmd := exec.CommandContext(callCtx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-ss", strconv.FormatFloat(timestamp, 'f', 3, 64), "-i", stagedPath, "-frames:v", "1", "-vf", "scale='min(1280,iw)':-2", "-q:v", "3", framePath)
		output, runErr := cmd.CombinedOutput()
		cancel()
		if runErr != nil {
			log.Printf("qqbot video frame extraction failed: %v: %s", runErr, strings.TrimSpace(string(output)))
			continue
		}
		if info, statErr := os.Stat(framePath); statErr == nil && info.Size() > 0 {
			frames = append(frames, framePath)
		}
	}
	if len(frames) == 0 {
		_ = os.RemoveAll(workDir)
	}
	return frames
}

func stageVideoForContext(sourcePath, workDir string) (string, error) {
	source, err := os.Open(sourcePath)
	if err != nil {
		return "", err
	}
	defer source.Close()
	destinationPath := filepath.Join(workDir, "source"+filepath.Ext(sourcePath))
	destination, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	written, copyErr := io.Copy(destination, io.LimitReader(source, maxVideoContextBytes+1))
	closeErr := destination.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if written <= 0 || written > maxVideoContextBytes {
		return "", fmt.Errorf("video size is invalid: %d", written)
	}
	return destinationPath, nil
}

func probeVideoDuration(ctx context.Context, path string) float64 {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		return 0
	}
	callCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	output, err := exec.CommandContext(callCtx, "ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", path).Output()
	if err != nil {
		return 0
	}
	duration, _ := strconv.ParseFloat(strings.TrimSpace(string(output)), 64)
	return duration
}

func videoFrameTimestamps(duration float64, limit int) []float64 {
	if limit <= 0 {
		return nil
	}
	if duration <= 0 {
		count := minVideoContextFrames
		if count > limit {
			count = limit
		}
		out := make([]float64, 0, count)
		for index := 0; index < count; index++ {
			out = append(out, float64(index))
		}
		return out
	}
	count := desiredVideoFrameCount(duration)
	if count > limit {
		count = limit
	}
	out := make([]float64, 0, count)
	for i := 0; i < count; i++ {
		out = append(out, duration*float64(i+1)/float64(count+1))
	}
	return out
}

func desiredVideoFrameCount(duration float64) int {
	if duration <= 0 {
		return minVideoContextFrames
	}
	count := minVideoContextFrames
	if duration > videoFrameGrowthInterval {
		count += int(math.Ceil(duration/videoFrameGrowthInterval)) - 1
	}
	if count > maxVideoContextFrames {
		return maxVideoContextFrames
	}
	return count
}

func cleanupVideoContextFrames(frames []string) {
	seen := map[string]bool{}
	for _, frame := range frames {
		dir := filepath.Dir(frame)
		if seen[dir] || !strings.HasPrefix(filepath.Base(dir), "diana-video-context-") {
			continue
		}
		seen[dir] = true
		_ = os.RemoveAll(dir)
	}
}
