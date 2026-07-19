package updater

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestGitUpdaterStatusRejectsNonRepository(t *testing.T) {
	u, err := NewGitUpdater(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, err = u.Status(context.Background())
	if !errors.Is(err, ErrRepositoryNotFound) {
		t.Fatalf("Status() error = %v, want ErrRepositoryNotFound", err)
	}
}

func TestGitUpdaterCheckFetchesRemoteState(t *testing.T) {
	repo := newUpdaterTestRepo(t)
	u, err := NewGitUpdater(repo.work)
	if err != nil {
		t.Fatal(err)
	}
	repo.commitRemote(t, "remote update")

	before, err := u.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if before.Behind != 0 {
		t.Fatalf("status before fetch behind = %d, want 0", before.Behind)
	}

	after, err := u.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if after.Behind != 1 || !after.UpdateAvailable {
		t.Fatalf("status after fetch = %#v, want one available update", after)
	}
	if after.LastFetchedAt.IsZero() || after.Updating {
		t.Fatalf("status after fetch = %#v", after)
	}
}

func TestGitUpdaterFastForwardAndAlreadyCurrent(t *testing.T) {
	repo := newUpdaterTestRepo(t)
	target := repo.commitRemote(t, "remote update")
	u, err := NewGitUpdater(repo.work)
	if err != nil {
		t.Fatal(err)
	}

	result, err := u.Update(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Fetched || !result.Updated || !result.SourceUpdated || result.Applied || result.RestartRequired {
		t.Fatalf("first Update() = %#v", result)
	}
	if result.TargetCommit != target || gitOutputForTest(t, repo.work, "rev-parse", "HEAD") != target {
		t.Fatalf("target commit = %q, result = %#v", target, result)
	}

	current, err := u.Update(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if current.Updated || current.SourceUpdated || current.Applied {
		t.Fatalf("second Update() = %#v, want no update", current)
	}
	if !strings.Contains(current.Output, "Already up to date") {
		t.Fatalf("second Update() output = %q", current.Output)
	}
}

func TestGitUpdaterRejectsUnsafeRepositoryStates(t *testing.T) {
	t.Run("dirty work tree", func(t *testing.T) {
		repo := newUpdaterTestRepo(t)
		if err := os.WriteFile(filepath.Join(repo.work, "untracked.txt"), []byte("local"), 0o600); err != nil {
			t.Fatal(err)
		}
		u, _ := NewGitUpdater(repo.work)
		_, err := u.Update(context.Background())
		if !errors.Is(err, ErrWorkingTreeDirty) {
			t.Fatalf("Update() error = %v, want ErrWorkingTreeDirty", err)
		}
	})

	t.Run("detached head", func(t *testing.T) {
		repo := newUpdaterTestRepo(t)
		gitRunForTest(t, repo.work, "checkout", "--detach")
		u, _ := NewGitUpdater(repo.work)
		_, err := u.Update(context.Background())
		if !errors.Is(err, ErrDetachedHead) {
			t.Fatalf("Update() error = %v, want ErrDetachedHead", err)
		}
	})

	t.Run("diverged branch", func(t *testing.T) {
		repo := newUpdaterTestRepo(t)
		repo.commitRemote(t, "remote update")
		writeAndCommitForTest(t, repo.work, "state.txt", "local update", "local update")
		u, _ := NewGitUpdater(repo.work)
		_, err := u.Update(context.Background())
		if !errors.Is(err, ErrNonFastForward) {
			t.Fatalf("Update() error = %v, want ErrNonFastForward", err)
		}
	})
}

func TestGitUpdaterRunsApplyCommandAndRequiresRestart(t *testing.T) {
	repo := newUpdaterTestRepo(t)
	marker := filepath.Join(t.TempDir(), "apply.txt")
	t.Setenv("DIANA_UPDATER_HELPER", "1")
	t.Setenv("DIANA_UPDATER_HELPER_MODE", "success")
	t.Setenv("DIANA_UPDATER_HELPER_MARKER", marker)
	u := newHelperUpdater(t, repo.work)

	result, err := u.Update(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Applied || !result.Updated || !result.RestartRequired || !result.Status.RestartRequired {
		t.Fatalf("Update() = %#v", result)
	}
	if !strings.Contains(result.Output, "apply complete") {
		t.Fatalf("Update() output = %q", result.Output)
	}
	markerText, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(markerText), repo.work) || !strings.Contains(string(markerText), result.TargetCommit) {
		t.Fatalf("apply environment marker = %q", markerText)
	}
}

func TestGitUpdaterApplyFailureCanBeRetried(t *testing.T) {
	repo := newUpdaterTestRepo(t)
	failOnce := filepath.Join(t.TempDir(), "fail-once")
	t.Setenv("DIANA_UPDATER_HELPER", "1")
	t.Setenv("DIANA_UPDATER_HELPER_MODE", "fail-once")
	t.Setenv("DIANA_UPDATER_HELPER_MARKER", failOnce)
	u := newHelperUpdater(t, repo.work)

	_, err := u.Update(context.Background())
	if !errors.Is(err, ErrApplyFailed) {
		t.Fatalf("first Update() error = %v, want ErrApplyFailed", err)
	}
	status, statusErr := u.Status(context.Background())
	if statusErr != nil {
		t.Fatal(statusErr)
	}
	if status.RestartRequired {
		t.Fatalf("status after failed apply = %#v", status)
	}

	result, err := u.Update(context.Background())
	if err != nil {
		t.Fatalf("retry Update() error = %v", err)
	}
	if !result.Applied || !result.RestartRequired || !strings.Contains(result.Output, "retry complete") {
		t.Fatalf("retry Update() = %#v", result)
	}
}

func TestGitUpdaterRejectsConcurrentUpdate(t *testing.T) {
	repo := newUpdaterTestRepo(t)
	temp := t.TempDir()
	started := filepath.Join(temp, "started")
	release := filepath.Join(temp, "release")
	t.Setenv("DIANA_UPDATER_HELPER", "1")
	t.Setenv("DIANA_UPDATER_HELPER_MODE", "block")
	t.Setenv("DIANA_UPDATER_HELPER_MARKER", started)
	t.Setenv("DIANA_UPDATER_HELPER_RELEASE", release)
	u := newHelperUpdater(t, repo.work)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	firstDone := make(chan error, 1)
	go func() {
		_, err := u.Update(ctx)
		firstDone <- err
	}()
	waitForUpdaterTestFile(t, started, 5*time.Second)

	status, err := u.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Updating {
		t.Fatalf("Status().Updating = false while apply command is blocked")
	}
	if _, err := u.Update(context.Background()); !errors.Is(err, ErrUpdateInProgress) {
		t.Fatalf("concurrent Update() error = %v, want ErrUpdateInProgress", err)
	}
	if err := os.WriteFile(release, []byte("release"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := <-firstDone; err != nil {
		t.Fatalf("first Update() error = %v", err)
	}
}

func TestApplyUpdateScriptReplacesArtifactsAndKeepsBackups(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("apply-update.sh requires a POSIX shell")
	}
	fixture := newApplyScriptFixture(t)

	output, err := fixture.run(t, false)
	if err != nil {
		t.Fatalf("apply-update.sh error = %v\n%s", err, output)
	}
	assertUpdaterTestFileContent(t, fixture.executable, "new-binary")
	assertUpdaterTestFileContent(t, fixture.executable+".backup", "old-binary")
	assertUpdaterTestFileContent(t, filepath.Join(fixture.frontend, "index.html"), "new-frontend")
	assertUpdaterTestFileContent(t, filepath.Join(fixture.frontend+".backup", "index.html"), "old-frontend")
	if !strings.Contains(output, "Restart Diana QQ Bot") {
		t.Fatalf("apply-update.sh output = %q", output)
	}
}

func TestApplyUpdateScriptReplacesMacAppBundle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("apply-update.sh requires a POSIX shell")
	}
	fixture := newApplyScriptFixture(t)
	appPath := configureMacApplyScriptFixture(t, &fixture)

	output, err := fixture.run(t, false)
	if err != nil {
		t.Fatalf("apply-update.sh error = %v\n%s", err, output)
	}
	assertUpdaterTestFileContent(t, fixture.executable, "new-app")
	backupExecutable := filepath.Join(appPath+".backup", "Contents", "MacOS", "diana-qq-bot-webui")
	assertUpdaterTestFileContent(t, backupExecutable, "old-app")
}

func TestApplyUpdateScriptRollsBackMacAppWhenFrontendSwapFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("apply-update.sh requires a POSIX shell")
	}
	fixture := newApplyScriptFixture(t)
	configureMacApplyScriptFixture(t, &fixture)

	output, err := fixture.run(t, true)
	if err == nil {
		t.Fatalf("apply-update.sh unexpectedly succeeded\n%s", output)
	}
	assertUpdaterTestFileContent(t, fixture.executable, "old-app")
	assertUpdaterTestFileContent(t, filepath.Join(fixture.frontend, "index.html"), "old-frontend")
}

func TestApplyUpdateScriptRollsBackExecutableWhenFrontendSwapFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("apply-update.sh requires a POSIX shell")
	}
	fixture := newApplyScriptFixture(t)

	output, err := fixture.run(t, true)
	if err == nil {
		t.Fatalf("apply-update.sh unexpectedly succeeded\n%s", output)
	}
	assertUpdaterTestFileContent(t, fixture.executable, "old-binary")
	assertUpdaterTestFileContent(t, filepath.Join(fixture.frontend, "index.html"), "old-frontend")
}

// TestGitUpdaterApplyHelper is re-executed as a child process by apply tests.
func TestGitUpdaterApplyHelper(t *testing.T) {
	if os.Getenv("DIANA_UPDATER_HELPER") != "1" {
		return
	}
	marker := os.Getenv("DIANA_UPDATER_HELPER_MARKER")
	switch os.Getenv("DIANA_UPDATER_HELPER_MODE") {
	case "success":
		content := os.Getenv("DIANA_UPDATE_ROOT") + "\n" + os.Getenv("DIANA_UPDATE_TARGET_COMMIT") + "\n" + os.Getenv("DIANA_RUNNING_EXECUTABLE")
		if err := os.WriteFile(marker, []byte(content), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		fmt.Println("apply complete")
		os.Exit(0)
	case "fail-once":
		if _, err := os.Stat(marker); errors.Is(err, os.ErrNotExist) {
			_ = os.WriteFile(marker, []byte("failed"), 0o600)
			fmt.Fprintln(os.Stderr, "intentional apply failure")
			os.Exit(3)
		}
		fmt.Println("retry complete")
		os.Exit(0)
	case "block":
		if err := os.WriteFile(marker, []byte("started"), 0o600); err != nil {
			os.Exit(2)
		}
		release := os.Getenv("DIANA_UPDATER_HELPER_RELEASE")
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(release); err == nil {
				fmt.Println("block released")
				os.Exit(0)
			}
			time.Sleep(20 * time.Millisecond)
		}
		fmt.Fprintln(os.Stderr, "timed out waiting for release")
		os.Exit(4)
	default:
		fmt.Fprintln(os.Stderr, "unknown helper mode")
		os.Exit(5)
	}
}

type updaterTestRepo struct {
	remote string
	seed   string
	work   string
}

type applyScriptFixture struct {
	root       string
	script     string
	binDir     string
	executable string
	frontend   string
}

func newApplyScriptFixture(t *testing.T) applyScriptFixture {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	fixtureRoot := t.TempDir()
	fixture := applyScriptFixture{
		root:       filepath.Join(fixtureRoot, "source"),
		script:     filepath.Join(repositoryRoot, "scripts", "apply-update.sh"),
		binDir:     filepath.Join(fixtureRoot, "bin"),
		executable: filepath.Join(fixtureRoot, "application", "diana-qq-bot-webui"),
		frontend:   filepath.Join(fixtureRoot, "web", "dist"),
	}
	for _, path := range []string{
		filepath.Join(fixture.root, ".git"),
		filepath.Join(fixture.root, "frontend", "node_modules", ".bin"),
		filepath.Dir(fixture.executable),
		fixture.frontend,
		fixture.binDir,
	} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(fixture.root, "frontend", "package-lock.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.executable, []byte("old-binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.frontend, "index.html"), []byte("old-frontend"), 0o600); err != nil {
		t.Fatal(err)
	}

	writeExecutableForUpdaterTest(t, filepath.Join(fixture.binDir, "npm"), "#!/bin/sh\nexit 0\n")
	writeExecutableForUpdaterTest(t, filepath.Join(fixture.binDir, "uname"), "#!/bin/sh\nprintf 'Linux\\n'\n")
	writeExecutableForUpdaterTest(t, filepath.Join(fixture.binDir, "mv"), `#!/bin/sh
case "$1" in
  "${DIANA_UPDATER_TEST_FAIL_FRONTEND_MOVE:-}/.diana-frontend.new."*)
    echo "intentional frontend move failure" >&2
    exit 9
    ;;
esac
exec /bin/mv "$@"
`)
	writeExecutableForUpdaterTest(t, filepath.Join(fixture.root, "frontend", "node_modules", ".bin", "vue-tsc"), "#!/bin/sh\nexit 0\n")
	writeExecutableForUpdaterTest(t, filepath.Join(fixture.root, "frontend", "node_modules", ".bin", "vite"), `#!/bin/sh
out=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--outDir" ]; then
    shift
    out="$1"
  fi
  shift
done
mkdir -p "$out"
printf 'new-frontend' > "$out/index.html"
`)
	writeExecutableForUpdaterTest(t, filepath.Join(fixture.binDir, "go"), `#!/bin/sh
out=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-o" ]; then
    shift
    out="$1"
  fi
  shift
done
printf 'new-binary' > "$out"
chmod 700 "$out"
`)
	return fixture
}

func configureMacApplyScriptFixture(t *testing.T, fixture *applyScriptFixture) string {
	t.Helper()
	appPath := filepath.Join(filepath.Dir(fixture.executable), "Diana QQ Bot.app")
	fixture.executable = filepath.Join(appPath, "Contents", "MacOS", "diana-qq-bot-webui")
	if err := os.MkdirAll(filepath.Dir(fixture.executable), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.executable, []byte("old-app"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeExecutableForUpdaterTest(t, filepath.Join(fixture.binDir, "uname"), "#!/bin/sh\nprintf 'Darwin\\n'\n")
	if err := os.MkdirAll(filepath.Join(fixture.root, "scripts"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeExecutableForUpdaterTest(t, filepath.Join(fixture.root, "scripts", "build-local-mac.sh"), `#!/bin/sh
case "$1" in
  *.app) ;;
  *) echo "staged app must end in .app" >&2; exit 8 ;;
esac
mkdir -p "$1/Contents/MacOS"
printf 'new-app' > "$1/Contents/MacOS/diana-qq-bot-webui"
chmod 700 "$1/Contents/MacOS/diana-qq-bot-webui"
`)
	return appPath
}

func (f applyScriptFixture) run(t *testing.T, failFrontendMove bool) (string, error) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is unavailable")
	}
	failMoveDir := ""
	if failFrontendMove {
		failMoveDir = filepath.Dir(f.frontend)
	}
	cmd := exec.Command("bash", f.script)
	cmd.Env = environmentWithOverrides(os.Environ(),
		"PATH="+f.binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"DIANA_UPDATE_ROOT="+f.root,
		"DIANA_UPDATE_TARGET_COMMIT=0123456789abcdef",
		"DIANA_RUNNING_EXECUTABLE="+f.executable,
		"FRONTEND_DIST="+f.frontend,
		"GO="+filepath.Join(f.binDir, "go"),
		"NPM="+filepath.Join(f.binDir, "npm"),
		"DIANA_UPDATER_TEST_FAIL_FRONTEND_MOVE="+failMoveDir,
	)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func writeExecutableForUpdaterTest(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
}

func assertUpdaterTestFileContent(t *testing.T, path, expected string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}
	if string(content) != expected {
		t.Fatalf("%s = %q, want %q", path, content, expected)
	}
}

func newUpdaterTestRepo(t *testing.T) updaterTestRepo {
	t.Helper()
	root := t.TempDir()
	repo := updaterTestRepo{
		remote: filepath.Join(root, "remote.git"),
		seed:   filepath.Join(root, "seed"),
		work:   filepath.Join(root, "work"),
	}
	gitRunForTest(t, root, "init", "--bare", repo.remote)
	gitRunForTest(t, root, "init", repo.seed)
	gitRunForTest(t, repo.seed, "checkout", "-b", "main")
	configureGitIdentityForTest(t, repo.seed)
	writeAndCommitForTest(t, repo.seed, "state.txt", "initial", "initial")
	gitRunForTest(t, repo.seed, "remote", "add", "origin", repo.remote)
	gitRunForTest(t, repo.seed, "push", "-u", "origin", "main")
	gitRunForTest(t, repo.remote, "symbolic-ref", "HEAD", "refs/heads/main")
	gitRunForTest(t, root, "clone", repo.remote, repo.work)
	configureGitIdentityForTest(t, repo.work)
	return repo
}

func (r updaterTestRepo) commitRemote(t *testing.T, content string) string {
	t.Helper()
	writeAndCommitForTest(t, r.seed, "state.txt", content, content)
	gitRunForTest(t, r.seed, "push", "origin", "main")
	return gitOutputForTest(t, r.seed, "rev-parse", "HEAD")
}

func newHelperUpdater(t *testing.T, root string) *GitUpdater {
	t.Helper()
	u, err := NewGitUpdaterWithOptions(root, Options{
		ApplyCommand:      []string{os.Args[0], "-test.run=^TestGitUpdaterApplyHelper$"},
		RunningCommit:     "0000000",
		RunningExecutable: filepath.Join(root, "diana-test"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func configureGitIdentityForTest(t *testing.T, dir string) {
	t.Helper()
	gitRunForTest(t, dir, "config", "user.name", "Diana Updater Test")
	gitRunForTest(t, dir, "config", "user.email", "updater@example.test")
}

func writeAndCommitForTest(t *testing.T, dir, name, content, message string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRunForTest(t, dir, "add", name)
	gitRunForTest(t, dir, "commit", "-m", message)
}

func gitRunForTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	_ = gitOutputForTest(t, dir, args...)
}

func gitOutputForTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func waitForUpdaterTestFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}
