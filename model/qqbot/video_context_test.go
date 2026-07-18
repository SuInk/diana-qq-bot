package qqbot

import (
	"context"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractVideoContextFrames(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}
	videoPath := filepath.Join(t.TempDir(), "sample.mp4")
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=10:duration=3", "-pix_fmt", "yuv420p", videoPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create sample video: %v: %s", err, output)
	}

	frames := extractVideoContextFrames(context.Background(), []string{videoPath})
	defer cleanupVideoContextFrames(frames)
	if len(frames) != 4 {
		t.Fatalf("frame count = %d, want 4: %#v", len(frames), frames)
	}
	for _, frame := range frames {
		info, err := os.Stat(frame)
		if err != nil || info.Size() == 0 {
			t.Fatalf("invalid frame %q: info=%v err=%v", frame, info, err)
		}
	}
}

func TestExtractVideoContextFramesSamplesTimeline(t *testing.T) {
	videoPath := createTimelineVideo(t)
	frames := extractVideoContextFrames(context.Background(), []string{videoPath})
	defer cleanupVideoContextFrames(frames)
	if len(frames) != 4 {
		t.Fatalf("frame count = %d, want 4: %#v", len(frames), frames)
	}

	tests := []struct {
		name  string
		match func(r, g, b uint8) bool
	}{
		{name: "red", match: func(r, g, b uint8) bool { return r > 180 && g < 100 && b < 100 }},
		{name: "green", match: func(r, g, b uint8) bool { return g > 90 && g > r+60 && g > b+60 }},
		{name: "blue", match: func(r, g, b uint8) bool { return b > 180 && r < 100 && g < 100 }},
		{name: "white", match: func(r, g, b uint8) bool { return r > 200 && g > 200 && b > 200 }},
	}
	for index, test := range tests {
		r, g, b := centerPixelRGB(t, frames[index])
		if !test.match(r, g, b) {
			t.Fatalf("frame %d sampled RGB(%d,%d,%d), want %s scene", index+1, r, g, b, test.name)
		}
	}
}

func TestVideoFrameCountGrowsWithDuration(t *testing.T) {
	tests := []struct {
		name     string
		duration float64
		want     int
	}{
		{name: "unknown_duration", duration: 0, want: 4},
		{name: "short", duration: 3, want: 4},
		{name: "thirty_seconds", duration: 30, want: 4},
		{name: "one_minute", duration: 60, want: 5},
		{name: "two_minutes", duration: 120, want: 7},
		{name: "five_minutes", duration: 300, want: 13},
		{name: "long_video_cap", duration: 600, want: 16},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := desiredVideoFrameCount(test.duration); got != test.want {
				t.Fatalf("desiredVideoFrameCount(%v) = %d, want %d", test.duration, got, test.want)
			}
			timestamps := videoFrameTimestamps(test.duration, maxVideoContextFrames)
			if len(timestamps) != test.want {
				t.Fatalf("timestamp count = %d, want %d: %#v", len(timestamps), test.want, timestamps)
			}
			for index, timestamp := range timestamps {
				if test.duration <= 0 {
					if timestamp != float64(index) {
						t.Fatalf("fallback timestamp %d = %v", index, timestamp)
					}
					continue
				}
				if timestamp <= 0 || timestamp >= test.duration {
					t.Fatalf("timestamp %d out of range: %v", index, timestamp)
				}
				if index > 0 && timestamp <= timestamps[index-1] {
					t.Fatalf("timestamps are not increasing: %#v", timestamps)
				}
			}
		})
	}
}

func TestVideoContextPathAllowedForNapCatMacQQStorage(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "Library", "Application Support", "QQ", "nt_qq_test", "nt_data", "Video", "2026-07", "Ori", "video.mp4")
	if !videoContextPathAllowed(path) {
		t.Fatalf("expected NapCat macOS QQ video path to be allowed: %s", path)
	}
}

func TestNapCatVideoFromEnvironmentReachesLLMImages(t *testing.T) {
	videoPath := strings.TrimSpace(os.Getenv("DIANA_TEST_NAPCAT_VIDEO"))
	if videoPath == "" {
		t.Skip("DIANA_TEST_NAPCAT_VIDEO is not set")
	}
	msg := llmMessageFromEventWithVideoFrames(context.Background(), MessageEvent{
		RawMessage: "这个视频是什么内容",
		Quoted: &QuotedMessage{Segments: []MessageSegment{{
			Type: "video",
			Data: map[string]string{"url": videoPath},
		}}},
	}, "这个视频是什么内容", nil)
	imageParts := 0
	for _, part := range msg.Parts {
		if part.Type == "image_url" {
			imageParts++
		}
	}
	if imageParts < minVideoContextFrames {
		t.Fatalf("real NapCat video produced %d LLM image parts, want at least %d", imageParts, minVideoContextFrames)
	}
}

func TestLLMMessageIncludesQuotedVideoFrames(t *testing.T) {
	videoPath := createTimelineVideo(t)

	msg := llmMessageFromEventWithVideoFrames(context.Background(), MessageEvent{
		RawMessage: "这是什么",
		Quoted: &QuotedMessage{Segments: []MessageSegment{{
			Type: "video",
			Data: map[string]string{"url": videoPath},
		}}},
	}, "这是什么", nil)
	imageParts := 0
	for _, part := range msg.Parts {
		if part.Type == "image_url" {
			imageParts++
			if len(part.ImageURL) < 16 || part.ImageURL[:16] != "data:image/jpeg;" {
				t.Fatalf("frame is not a JPEG data URL: %.32q", part.ImageURL)
			}
		}
	}
	if imageParts != 4 {
		t.Fatalf("message contains %d video frames, want 4: %#v", imageParts, msg)
	}
	if !strings.Contains(msg.Content, "当前引用视频的关键帧") || !strings.Contains(msg.Content, "不要把历史消息") {
		t.Fatalf("message does not distinguish current quoted video: %q", msg.Content)
	}
}

func TestLLMMessageDoesNotGuessWhenQuotedVideoCannotBeRead(t *testing.T) {
	invalidVideo := filepath.Join(t.TempDir(), "invalid.mp4")
	if err := os.WriteFile(invalidVideo, []byte("not a video"), 0o600); err != nil {
		t.Fatal(err)
	}
	msg := llmMessageFromEventWithVideoFrames(context.Background(), MessageEvent{
		Quoted: &QuotedMessage{Segments: []MessageSegment{{
			Type: "video",
			Data: map[string]string{"url": invalidVideo},
		}}},
	}, "这是什么", nil)
	if !strings.Contains(msg.Content, "不得使用历史消息") {
		t.Fatalf("missing anti-hallucination instruction: %q", msg.Content)
	}
}

func createTimelineVideo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}
	videoPath := filepath.Join(t.TempDir(), "timeline.mp4")
	cmd := exec.Command(
		"ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "color=c=red:size=160x90:rate=10:duration=1",
		"-f", "lavfi", "-i", "color=c=green:size=160x90:rate=10:duration=1",
		"-f", "lavfi", "-i", "color=c=blue:size=160x90:rate=10:duration=1",
		"-f", "lavfi", "-i", "color=c=white:size=160x90:rate=10:duration=1",
		"-filter_complex", "[0:v][1:v][2:v][3:v]concat=n=4:v=1:a=0,format=yuv420p[v]",
		"-map", "[v]", videoPath,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create timeline video: %v: %s", err, output)
	}
	return videoPath
}

func centerPixelRGB(t *testing.T, path string) (uint8, uint8, uint8) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	img, err := jpeg.Decode(file)
	if err != nil {
		t.Fatalf("decode frame %q: %v", path, err)
	}
	bounds := img.Bounds()
	r, g, b, _ := img.At(bounds.Min.X+bounds.Dx()/2, bounds.Min.Y+bounds.Dy()/2).RGBA()
	return uint8(r >> 8), uint8(g >> 8), uint8(b >> 8)
}
