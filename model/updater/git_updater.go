package updater

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	ErrRepositoryNotFound  = errors.New("updater: update root is not a Git work tree")
	ErrRemoteNotConfigured = errors.New("updater: git remote origin is not configured")
	ErrRemoteBranchMissing = errors.New("updater: matching branch does not exist on origin")
	ErrDetachedHead        = errors.New("updater: detached HEAD cannot be updated")
	ErrWorkingTreeDirty    = errors.New("updater: working tree has uncommitted changes")
	ErrNonFastForward      = errors.New("updater: local and remote branches have diverged")
	ErrUpdateInProgress    = errors.New("updater: another update operation is already running")
	ErrFetchFailed         = errors.New("updater: failed to fetch origin")
	ErrApplyFailed         = errors.New("updater: failed to build or apply the update")
)

type Status struct {
	Root            string    `json:"root"`
	Branch          string    `json:"branch,omitempty"`
	RemoteName      string    `json:"remote_name,omitempty"`
	RemoteURL       string    `json:"remote_url,omitempty"`
	HeadCommit      string    `json:"head_commit,omitempty"`
	HeadSubject     string    `json:"head_subject,omitempty"`
	RunningCommit   string    `json:"running_commit,omitempty"`
	Dirty           bool      `json:"dirty"`
	Ahead           int       `json:"ahead,omitempty"`
	Behind          int       `json:"behind,omitempty"`
	Upstream        string    `json:"upstream,omitempty"`
	Updating        bool      `json:"updating"`
	ApplySupported  bool      `json:"apply_supported"`
	UpdateAvailable bool      `json:"update_available"`
	RestartRequired bool      `json:"restart_required"`
	LastFetchedAt   time.Time `json:"last_fetched_at,omitempty"`
	LastUpdateAt    time.Time `json:"last_update_at,omitempty"`
	LastUpdateText  string    `json:"last_update_text,omitempty"`
}

type Result struct {
	Status          Status    `json:"status"`
	Fetched         bool      `json:"fetched"`
	Updated         bool      `json:"updated"`
	SourceUpdated   bool      `json:"source_updated"`
	Applied         bool      `json:"applied"`
	RestartRequired bool      `json:"restart_required"`
	PreviousCommit  string    `json:"previous_commit,omitempty"`
	TargetCommit    string    `json:"target_commit,omitempty"`
	Output          string    `json:"output,omitempty"`
	At              time.Time `json:"at"`
}

// Options configures the fixed local command used to build and apply an update.
// ApplyCommand is never accepted from an HTTP request.
type Options struct {
	ApplyCommand      []string
	RunningCommit     string
	RunningExecutable string
}

type GitUpdater struct {
	root              string
	applyCommand      []string
	runningCommit     string
	runningExecutable string

	operationMu     sync.Mutex
	stateMu         sync.RWMutex
	updating        bool
	lastFetchedAt   time.Time
	lastUpdateAt    time.Time
	lastUpdateText  string
	restartRequired bool
}

// NewGitUpdater creates a source updater without an automatic apply command.
func NewGitUpdater(root string) (*GitUpdater, error) {
	return NewGitUpdaterWithOptions(root, Options{})
}

// NewGitUpdaterWithOptions creates a source updater with a fixed apply command.
func NewGitUpdaterWithOptions(root string, options Options) (*GitUpdater, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	applyCommand := append([]string(nil), options.ApplyCommand...)
	if len(applyCommand) > 0 && !filepath.IsAbs(applyCommand[0]) && strings.ContainsRune(applyCommand[0], filepath.Separator) {
		applyCommand[0] = filepath.Join(absRoot, applyCommand[0])
	}
	return &GitUpdater{
		root:              absRoot,
		applyCommand:      applyCommand,
		runningCommit:     strings.TrimSpace(options.RunningCommit),
		runningExecutable: strings.TrimSpace(options.RunningExecutable),
	}, nil
}

// Status reads the current local repository state without contacting the remote.
func (u *GitUpdater) Status(ctx context.Context) (Status, error) {
	status := Status{Root: u.root, RunningCommit: u.runningCommit, ApplySupported: len(u.applyCommand) > 0}

	inside, err := u.gitOutput(ctx, "rev-parse", "--is-inside-work-tree")
	if err != nil || inside != "true" {
		return status, fmt.Errorf("%w: %s", ErrRepositoryNotFound, u.root)
	}

	if branch, err := u.gitOutput(ctx, "branch", "--show-current"); err == nil {
		status.Branch = branch
	}
	if head, err := u.gitOutput(ctx, "rev-parse", "HEAD"); err == nil {
		status.HeadCommit = head
	}
	if subject, err := u.gitOutput(ctx, "log", "-1", "--pretty=%s"); err == nil {
		status.HeadSubject = subject
	}
	if dirty, err := u.gitOutput(ctx, "status", "--porcelain", "--untracked-files=normal"); err == nil {
		status.Dirty = strings.TrimSpace(dirty) != ""
	}
	if remoteURL, err := u.gitOutput(ctx, "remote", "get-url", "origin"); err == nil {
		status.RemoteName = "origin"
		status.RemoteURL = remoteURL
	}
	if upstream, err := u.gitOutput(ctx, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}"); err == nil {
		status.Upstream = upstream
	}
	if status.Upstream == "" && status.Branch != "" && u.refExists(ctx, remoteBranchRef(status.Branch)) {
		status.Upstream = "origin/" + status.Branch
	}
	if status.Upstream != "" {
		if aheadBehind, err := u.gitOutput(ctx, "rev-list", "--left-right", "--count", status.Upstream+"...HEAD"); err == nil {
			// The left side is upstream, so rev-list reports behind before ahead.
			_, _ = fmt.Sscanf(aheadBehind, "%d %d", &status.Behind, &status.Ahead)
		}
	}

	u.stateMu.RLock()
	status.Updating = u.updating
	status.LastFetchedAt = u.lastFetchedAt
	status.LastUpdateAt = u.lastUpdateAt
	status.LastUpdateText = u.lastUpdateText
	status.RestartRequired = u.restartRequired
	u.stateMu.RUnlock()

	status.UpdateAvailable = status.Behind > 0
	if status.ApplySupported && status.RunningCommit != "" && status.HeadCommit != "" && !sameCommit(status.RunningCommit, status.HeadCommit) {
		status.UpdateAvailable = true
	}
	return status, nil
}

// Check refreshes origin and returns the resulting repository state.
func (u *GitUpdater) Check(ctx context.Context) (Status, error) {
	if !u.beginOperation() {
		return Status{}, ErrUpdateInProgress
	}
	defer u.endOperation()

	status, err := u.Status(ctx)
	if err != nil {
		return Status{}, err
	}
	if status.RemoteURL == "" {
		return Status{}, ErrRemoteNotConfigured
	}
	if status.Branch == "" {
		return Status{}, ErrDetachedHead
	}

	output, err := u.gitCombined(ctx, "fetch", "--prune", "origin")
	if err != nil {
		return Status{}, fmt.Errorf("%w: %v", ErrFetchFailed, err)
	}
	u.recordFetch(time.Now())
	if strings.TrimSpace(output) != "" {
		u.recordUpdateText(strings.TrimSpace(output))
	}

	next, err := u.Status(ctx)
	if err != nil {
		return Status{}, err
	}
	next.Updating = false
	return next, nil
}

// Update fetches origin, performs a fast-forward-only update, then runs the
// configured build/apply command. The running process is never restarted here.
func (u *GitUpdater) Update(ctx context.Context) (Result, error) {
	if !u.beginOperation() {
		return Result{}, ErrUpdateInProgress
	}
	defer u.endOperation()

	status, err := u.Status(ctx)
	if err != nil {
		return Result{}, err
	}
	if status.RemoteURL == "" {
		return Result{}, ErrRemoteNotConfigured
	}
	if status.Branch == "" {
		return Result{}, ErrDetachedHead
	}
	if status.Dirty {
		return Result{}, ErrWorkingTreeDirty
	}

	outputs := make([]string, 0, 3)
	fetchOut, err := u.gitCombined(ctx, "fetch", "--prune", "origin")
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrFetchFailed, err)
	}
	u.recordFetch(time.Now())
	appendOutput(&outputs, fetchOut)

	previousCommit, err := u.gitOutput(ctx, "rev-parse", "HEAD")
	if err != nil {
		return Result{}, err
	}
	remoteRef := remoteBranchRef(status.Branch)
	targetCommit, err := u.gitOutput(ctx, "rev-parse", "--verify", remoteRef)
	if err != nil {
		return Result{}, fmt.Errorf("%w: origin/%s", ErrRemoteBranchMissing, status.Branch)
	}

	sourceUpdated := !sameCommit(previousCommit, targetCommit)
	if sourceUpdated {
		ancestor, err := u.isAncestor(ctx, previousCommit, targetCommit)
		if err != nil {
			return Result{}, err
		}
		if !ancestor {
			return Result{}, ErrNonFastForward
		}
		mergeOut, err := u.gitCombined(ctx, "merge", "--ff-only", remoteRef)
		if err != nil {
			return Result{}, fmt.Errorf("updater: fast-forward origin/%s: %w", status.Branch, err)
		}
		appendOutput(&outputs, mergeOut)
	}

	applied := false
	restartRequired := false
	// POST /update is also an explicit repair/rebuild action when source is current.
	applyNeeded := len(u.applyCommand) > 0
	if applyNeeded {
		applyOut, err := u.runApply(ctx, targetCommit)
		appendOutput(&outputs, applyOut)
		if err != nil {
			message := strings.TrimSpace(strings.Join(outputs, "\n\n"))
			if message == "" {
				message = err.Error()
			}
			u.recordUpdate(time.Now(), message, false)
			return Result{}, fmt.Errorf("%w: %v", ErrApplyFailed, err)
		}
		applied = true
		restartRequired = true
	}

	if len(outputs) == 0 {
		outputs = append(outputs, "Already up to date.")
	}
	output := strings.TrimSpace(strings.Join(outputs, "\n\n"))
	now := time.Now()
	u.recordUpdate(now, output, restartRequired)

	nextStatus, err := u.Status(ctx)
	if err != nil {
		return Result{}, err
	}
	nextStatus.Updating = false
	result := Result{
		Status:          nextStatus,
		Fetched:         true,
		Updated:         sourceUpdated || applied,
		SourceUpdated:   sourceUpdated,
		Applied:         applied,
		RestartRequired: restartRequired,
		PreviousCommit:  previousCommit,
		TargetCommit:    targetCommit,
		Output:          output,
		At:              now,
	}
	return result, nil
}

func (u *GitUpdater) beginOperation() bool {
	if !u.operationMu.TryLock() {
		return false
	}
	u.stateMu.Lock()
	u.updating = true
	u.stateMu.Unlock()
	return true
}

func (u *GitUpdater) endOperation() {
	u.stateMu.Lock()
	u.updating = false
	u.stateMu.Unlock()
	u.operationMu.Unlock()
}

func (u *GitUpdater) recordFetch(at time.Time) {
	u.stateMu.Lock()
	u.lastFetchedAt = at
	u.stateMu.Unlock()
}

func (u *GitUpdater) recordUpdateText(output string) {
	u.stateMu.Lock()
	u.lastUpdateText = output
	u.stateMu.Unlock()
}

func (u *GitUpdater) recordUpdate(at time.Time, output string, restartRequired bool) {
	u.stateMu.Lock()
	u.lastUpdateAt = at
	u.lastUpdateText = output
	if restartRequired {
		u.restartRequired = true
	}
	u.stateMu.Unlock()
}

func (u *GitUpdater) runApply(ctx context.Context, targetCommit string) (string, error) {
	cmd := exec.CommandContext(ctx, u.applyCommand[0], u.applyCommand[1:]...)
	cmd.Dir = u.root
	cmd.Env = environmentWithOverrides(os.Environ(),
		"DIANA_UPDATE_ROOT="+u.root,
		"DIANA_UPDATE_TARGET_COMMIT="+targetCommit,
		"DIANA_RUNNING_COMMIT="+u.runningCommit,
		"DIANA_RUNNING_EXECUTABLE="+u.runningExecutable,
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		text := strings.TrimSpace(out.String())
		if text == "" {
			return "", err
		}
		return text, fmt.Errorf("%w (%s)", err, text)
	}
	return out.String(), nil
}

func environmentWithOverrides(base []string, overrides ...string) []string {
	keys := make(map[string]struct{}, len(overrides))
	for _, value := range overrides {
		if index := strings.IndexByte(value, '='); index > 0 {
			keys[value[:index]] = struct{}{}
		}
	}
	result := make([]string, 0, len(base)+len(overrides))
	for _, value := range base {
		index := strings.IndexByte(value, '=')
		if index <= 0 {
			result = append(result, value)
			continue
		}
		if _, replaced := keys[value[:index]]; !replaced {
			result = append(result, value)
		}
	}
	return append(result, overrides...)
}

func (u *GitUpdater) isAncestor(ctx context.Context, ancestor, descendant string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "merge-base", "--is-ancestor", ancestor, descendant)
	cmd.Dir = u.root
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	text := strings.TrimSpace(out.String())
	if text == "" {
		return false, err
	}
	return false, fmt.Errorf("git merge-base --is-ancestor: %w (%s)", err, text)
}

func (u *GitUpdater) refExists(ctx context.Context, ref string) bool {
	cmd := exec.CommandContext(ctx, "git", "show-ref", "--verify", "--quiet", ref)
	cmd.Dir = u.root
	return cmd.Run() == nil
}

func remoteBranchRef(branch string) string {
	return "refs/remotes/origin/" + branch
}

func sameCommit(left, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" || right == "" {
		return false
	}
	return left == right || strings.HasPrefix(left, right) || strings.HasPrefix(right, left)
}

func appendOutput(outputs *[]string, output string) {
	if trimmed := strings.TrimSpace(output); trimmed != "" {
		*outputs = append(*outputs, trimmed)
	}
}

func (u *GitUpdater) gitOutput(ctx context.Context, args ...string) (string, error) {
	out, err := u.gitCombined(ctx, args...)
	return strings.TrimSpace(out), err
}

func (u *GitUpdater) gitCombined(ctx context.Context, args ...string) (string, error) {
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
		return text, fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err, text)
	}
	return out.String(), nil
}
