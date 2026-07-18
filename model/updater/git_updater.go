package updater

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var ErrRemoteNotConfigured = errors.New("updater: git remote origin is not configured")

type Status struct {
	Root           string    `json:"root"`
	Branch         string    `json:"branch,omitempty"`
	RemoteName     string    `json:"remote_name,omitempty"`
	RemoteURL      string    `json:"remote_url,omitempty"`
	HeadCommit     string    `json:"head_commit,omitempty"`
	HeadSubject    string    `json:"head_subject,omitempty"`
	Dirty          bool      `json:"dirty"`
	Ahead          int       `json:"ahead,omitempty"`
	Behind         int       `json:"behind,omitempty"`
	Upstream       string    `json:"upstream,omitempty"`
	LastFetchedAt  time.Time `json:"last_fetched_at,omitempty"`
	LastUpdateAt   time.Time `json:"last_update_at,omitempty"`
	LastUpdateText string    `json:"last_update_text,omitempty"`
}

type Result struct {
	Status  Status    `json:"status"`
	Fetched bool      `json:"fetched"`
	Updated bool      `json:"updated"`
	Output  string    `json:"output,omitempty"`
	At      time.Time `json:"at"`
}

type GitUpdater struct {
	root          string
	lastFetchedAt time.Time
	lastUpdateAt  time.Time
}

// NewGitUpdater 创建 Git 更新器。
func NewGitUpdater(root string) (*GitUpdater, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return &GitUpdater{root: absRoot}, nil
}

// Status 读取当前 Git 仓库更新状态。
func (u *GitUpdater) Status(ctx context.Context) (Status, error) {
	status := Status{Root: u.root}

	// 状态接口尽量容错：单个 Git 信息读不到时保留空值，只有非退出类错误才返回。
	if branch, err := u.gitOutput(ctx, "branch", "--show-current"); err == nil {
		status.Branch = branch
	}
	if head, err := u.gitOutput(ctx, "rev-parse", "--short", "HEAD"); err == nil {
		status.HeadCommit = head
	}
	if subject, err := u.gitOutput(ctx, "log", "-1", "--pretty=%s"); err == nil {
		status.HeadSubject = subject
	}
	if dirty, err := u.gitOutput(ctx, "status", "--porcelain"); err == nil {
		status.Dirty = strings.TrimSpace(dirty) != ""
	}
	if remoteURL, err := u.gitOutput(ctx, "remote", "get-url", "origin"); err == nil {
		status.RemoteName = "origin"
		status.RemoteURL = remoteURL
	}
	if upstream, err := u.gitOutput(ctx, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}"); err == nil {
		status.Upstream = upstream
	}
	if status.Upstream != "" {
		if aheadBehind, err := u.gitOutput(ctx, "rev-list", "--left-right", "--count", status.Upstream+"...HEAD"); err == nil {
			// rev-list 输出顺序是 behind ahead，因为左边是 upstream，右边是 HEAD。
			fmt.Sscanf(aheadBehind, "%d %d", &status.Behind, &status.Ahead)
		}
	}
	status.LastFetchedAt = u.lastFetchedAt
	status.LastUpdateAt = u.lastUpdateAt
	return status, nil
}

// Update 执行 fetch 和 ff-only pull 更新。
func (u *GitUpdater) Update(ctx context.Context) (Result, error) {
	status, err := u.Status(ctx)
	if err != nil {
		return Result{}, err
	}
	if status.RemoteURL == "" {
		return Result{}, ErrRemoteNotConfigured
	}

	outputs := make([]string, 0, 2)
	// 先 fetch 再 ff-only pull，避免在 WebUI 更新时产生本地 merge commit。
	fetchOut, err := u.gitCombined(ctx, "fetch", "--prune", "origin")
	if err != nil {
		return Result{}, err
	}
	u.lastFetchedAt = time.Now()
	if trimmed := strings.TrimSpace(fetchOut); trimmed != "" {
		outputs = append(outputs, trimmed)
	}

	updateOut, err := u.gitCombined(ctx, "pull", "--ff-only", "origin", status.Branch)
	if err != nil {
		return Result{}, err
	}
	u.lastUpdateAt = time.Now()
	if trimmed := strings.TrimSpace(updateOut); trimmed != "" {
		outputs = append(outputs, trimmed)
	}

	nextStatus, err := u.Status(ctx)
	if err != nil {
		return Result{}, err
	}
	nextStatus.LastFetchedAt = u.lastFetchedAt
	nextStatus.LastUpdateAt = u.lastUpdateAt
	result := Result{
		Status:  nextStatus,
		Fetched: true,
		Updated: !strings.Contains(updateOut, "Already up to date."),
		Output:  strings.TrimSpace(strings.Join(outputs, "\n\n")),
		At:      time.Now(),
	}
	nextStatus.LastUpdateText = result.Output
	result.Status = nextStatus
	return result, nil
}

// gitOutput 执行 Git 命令并返回去空白输出。
func (u *GitUpdater) gitOutput(ctx context.Context, args ...string) (string, error) {
	out, err := u.gitCombined(ctx, args...)
	return strings.TrimSpace(out), err
}

// gitCombined 执行 Git 命令并合并 stdout/stderr。
func (u *GitUpdater) gitCombined(ctx context.Context, args ...string) (string, error) {
	// 所有 Git 命令都绑定仓库根目录运行，stdout/stderr 合并后返回给前端展示。
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = u.root
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		text := strings.TrimSpace(out.String())
		if text == "" {
			return "", err
		}
		// 用 %w 保留底层退出错误，这样状态查询可以继续把“无远端/无上游”当成可忽略分支处理。
		return text, fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err, text)
	}
	return out.String(), nil
}
