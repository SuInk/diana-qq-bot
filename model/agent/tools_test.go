package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSafePathRejectsEscapes 验证对应功能场景。
func TestSafePathRejectsEscapes(t *testing.T) {
	root := t.TempDir()
	if _, err := safePath(root, "../secret.txt"); err == nil {
		t.Fatal("expected escape to be rejected")
	}
	if _, err := safePath(root, "/etc/passwd"); err == nil {
		t.Fatal("expected absolute path to be rejected")
	}
	got, err := safePath(root, "sub/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "sub", "file.txt")
	if got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
}

// TestReadFileToolLimitsContent 验证对应功能场景。
func TestReadFileToolLimitsContent(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "a.txt", "abcdef")
	tool := &ReadFileTool{root: root, maxBytes: 3}
	got, err := tool.Run(context.Background(), map[string]any{"path": "a.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `"truncated": true`) || !strings.Contains(got, `"content": "abc"`) {
		t.Fatalf("unexpected output: %s", got)
	}
}

// TestRunCommandToolRunsAllowedCommand 验证命令工具执行白名单命令并返回结构化结果。
func TestRunCommandToolRunsAllowedCommand(t *testing.T) {
	root := t.TempDir()
	tool := &RunCommandTool{
		root:      root,
		allowlist: map[string]bool{"go": true},
		timeout:   time.Duration(DefaultCommandTimeoutMS) * time.Millisecond,
		maxBytes:  DefaultMaxToolOutputChars,
	}
	got, err := tool.Run(context.Background(), map[string]any{"command": "go", "args": []any{"version"}})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Command  string `json:"command"`
		ExitCode int    `json:"exit_code"`
		Output   string `json:"output"`
	}
	if err := json.Unmarshal([]byte(got), &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v, json = %s", err, got)
	}
	if payload.Command != "go" || payload.ExitCode != 0 || !strings.Contains(payload.Output, "go version") {
		t.Fatalf("payload = %#v", payload)
	}
}

// TestRunCommandToolRejectsBlockedCommand 验证命令工具拒绝非白名单命令和路径命令。
func TestRunCommandToolRejectsBlockedCommand(t *testing.T) {
	root := t.TempDir()
	tool := &RunCommandTool{
		root:      root,
		allowlist: map[string]bool{"go": true},
		timeout:   time.Duration(DefaultCommandTimeoutMS) * time.Millisecond,
		maxBytes:  DefaultMaxToolOutputChars,
	}
	if _, err := tool.Run(context.Background(), map[string]any{"command": "python3"}); err == nil {
		t.Fatal("expected blocked command error")
	}
	if _, err := tool.Run(context.Background(), map[string]any{"command": "../go"}); err == nil {
		t.Fatal("expected path command error")
	}
}

// TestValidateBrowserURL 验证浏览器工具只接受 http/https URL。
func TestValidateBrowserURL(t *testing.T) {
	for _, value := range []string{"http://127.0.0.1:5173", "https://example.com"} {
		if err := validateBrowserURL(value); err != nil {
			t.Fatalf("validateBrowserURL(%q) error = %v", value, err)
		}
	}
	for _, value := range []string{"file:///etc/passwd", "javascript:alert(1)", "https://"} {
		if err := validateBrowserURL(value); err == nil {
			t.Fatalf("validateBrowserURL(%q) error = nil", value)
		}
	}
}

// writeTestFile 封装当前模块的 writeTestFile 逻辑。
func writeTestFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
