package agent

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseRenderedPageExtractsVisibleContent(t *testing.T) {
	raw := []byte(`<!doctype html><html><head><title>测试站点</title><meta name="description" content="站点描述"><link rel="canonical" href="/home"></head><body><nav>导航</nav><main><h1>欢迎</h1><script>secret()</script><p>动态正文</p><p hidden>隐藏内容</p></main></body></html>`)
	page, err := parseRenderedPage(raw, "https://example.com/start", 1000, false)
	if err != nil {
		t.Fatal(err)
	}
	if page.Title != "测试站点" || page.Description != "站点描述" || page.URL != "https://example.com/home" {
		t.Fatalf("page = %#v", page)
	}
	if !strings.Contains(page.Text, "欢迎") || !strings.Contains(page.Text, "动态正文") || strings.Contains(page.Text, "secret") || strings.Contains(page.Text, "隐藏内容") {
		t.Fatalf("text = %q", page.Text)
	}
}

func TestSandboxedBrowserURLRejectsLocalTargets(t *testing.T) {
	for _, rawURL := range []string{
		"http://127.0.0.1:8080",
		"http://[::1]/",
		"http://192.168.1.1/",
		"http://localhost/",
		"http://host.docker.internal/",
		"https://" + "user:pass@" + "example.com/",
	} {
		if err := validateSandboxedBrowserURL(context.Background(), rawURL); err == nil {
			t.Fatalf("validateSandboxedBrowserURL(%q) error = nil", rawURL)
		}
	}
	if err := validateSandboxedBrowserURL(context.Background(), "https://1.1.1.1/"); err != nil {
		t.Fatalf("public address rejected: %v", err)
	}
}

func TestSandboxedChromeArgsKeepChromeSandboxEnabled(t *testing.T) {
	args := sandboxedChromeArgs("/tmp/profile", "/tmp/cache", "/tmp/crash", sandboxedBrowserConfigWithDefaults(SandboxedBrowserConfig{}))
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--no-sandbox") {
		t.Fatalf("args disable Chrome sandbox: %s", joined)
	}
	for _, want := range []string{"--headless=new", "--remote-debugging-port=0", "--user-data-dir=/tmp/profile", "--disable-extensions"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args missing %q: %s", want, joined)
		}
	}
}

func TestBrowserRenderToolUsesRenderer(t *testing.T) {
	tool := NewBrowserRenderTool(PageRendererFunc(func(_ context.Context, rawURL string) (RenderedPage, error) {
		return RenderedPage{RequestedURL: rawURL, URL: rawURL, Title: "Rendered", Text: "hello", Sandboxed: true}, nil
	}))
	output, err := tool.Run(context.Background(), map[string]any{"url": "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, `"title": "Rendered"`) || !strings.Contains(output, `"sandboxed": true`) {
		t.Fatalf("output = %s", output)
	}
}

func TestRenderReadinessWaitsForObservationWindow(t *testing.T) {
	started := time.Unix(1_700_000_000, 0)
	cfg := sandboxedBrowserConfigWithDefaults(SandboxedBrowserConfig{
		VirtualTimeBudget: 8 * time.Second,
		StabilityWindow:   time.Second,
		NetworkIdleWindow: 500 * time.Millisecond,
	})
	readiness := newRenderReadiness(started)
	activity := browserActivitySnapshot{
		LastNavigation: started,
		LastNetwork:    started,
	}
	probe := browserDOMProbe{
		URL:               "https://example.com",
		ReadyState:        "complete",
		Title:             "Example",
		TextLength:        100,
		SemanticSignature: "first",
		DOMChanges:        3,
	}
	_ = readiness.observe(started, probe, activity, cfg)
	before := readiness.observe(started.Add(2*time.Second), probe, activity, cfg)
	if !before.ContentStable || before.Complete || before.Reason != "observing_for_delayed_changes" {
		t.Fatalf("before = %#v", before)
	}
	after := readiness.observe(started.Add(9*time.Second), probe, activity, cfg)
	if !after.Complete || after.Reason != "dom_and_network_stable" || after.DOMChanges != 3 {
		t.Fatalf("after = %#v", after)
	}
}

func TestRenderReadinessResetsWhenPageChanges(t *testing.T) {
	started := time.Unix(1_700_000_000, 0)
	cfg := sandboxedBrowserConfigWithDefaults(SandboxedBrowserConfig{
		VirtualTimeBudget: time.Second,
		StabilityWindow:   time.Second,
		NetworkIdleWindow: 500 * time.Millisecond,
	})
	readiness := newRenderReadiness(started)
	activity := browserActivitySnapshot{LastNavigation: started, LastNetwork: started}
	probe := browserDOMProbe{URL: "https://example.com", ReadyState: "complete", Title: "Before", TextLength: 20, SemanticSignature: "before", DOMChanges: 2}
	_ = readiness.observe(started, probe, activity, cfg)
	if decision := readiness.observe(started.Add(2*time.Second), probe, activity, cfg); !decision.Complete {
		t.Fatalf("initial decision = %#v", decision)
	}

	changedAt := started.Add(3 * time.Second)
	activity.LastNavigation = changedAt
	activity.LastNetwork = changedAt
	probe.URL = "https://redirect.example/final"
	probe.Title = "After"
	probe.SemanticSignature = "after"
	probe.DOMChanges = 5
	changed := readiness.observe(changedAt, probe, activity, cfg)
	if changed.ContentStable || changed.Complete {
		t.Fatalf("changed = %#v", changed)
	}
	settled := readiness.observe(changedAt.Add(2*time.Second), probe, activity, cfg)
	if !settled.Complete || settled.DOMChanges != 7 {
		t.Fatalf("settled = %#v", settled)
	}
}

func TestRenderReadinessAcceptsStableInteractiveSPA(t *testing.T) {
	started := time.Unix(1_700_000_000, 0)
	cfg := sandboxedBrowserConfigWithDefaults(SandboxedBrowserConfig{
		VirtualTimeBudget: time.Second,
		StabilityWindow:   time.Second,
		NetworkIdleWindow: 500 * time.Millisecond,
	})
	readiness := newRenderReadiness(started)
	activity := browserActivitySnapshot{
		LastNavigation:  started,
		LastNetwork:     started.Add(3500 * time.Millisecond),
		Loading:         true,
		PendingRequests: 8,
	}
	probe := browserDOMProbe{
		URL:               "https://spa.example/app",
		ReadyState:        "interactive",
		Title:             "Loaded app",
		TextLength:        12_000,
		SemanticSignature: "stable-primary-content",
		DOMChanges:        500,
	}
	_ = readiness.observe(started, probe, activity, cfg)
	decision := readiness.observe(started.Add(4*time.Second), probe, activity, cfg)
	if !decision.ContentStable || !decision.Complete {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestRenderReadinessAcceptsStableLoadingDocumentAfterGrace(t *testing.T) {
	started := time.Unix(1_700_000_000, 0)
	cfg := sandboxedBrowserConfigWithDefaults(SandboxedBrowserConfig{
		VirtualTimeBudget: time.Second,
		StabilityWindow:   800 * time.Millisecond,
		NetworkIdleWindow: 500 * time.Millisecond,
	})
	readiness := newRenderReadiness(started)
	activity := browserActivitySnapshot{
		LastNavigation:  started,
		LastNetwork:     started.Add(5500 * time.Millisecond),
		Loading:         true,
		PendingRequests: 6,
	}
	probe := browserDOMProbe{
		URL:               "https://video.example/watch",
		ReadyState:        "loading",
		Title:             "Video title",
		Description:       "Primary metadata is available",
		TextLength:        3000,
		SemanticSignature: "stable-loading-content",
		DOMChanges:        900,
	}
	_ = readiness.observe(started, probe, activity, cfg)
	decision := readiness.observe(started.Add(6*time.Second), probe, activity, cfg)
	if !decision.ContentStable || !decision.Complete {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestSandboxedHeadlessBrowserIntegration(t *testing.T) {
	if os.Getenv("DIANA_HEADLESS_BROWSER_INTEGRATION") != "1" {
		t.Skip("set DIANA_HEADLESS_BROWSER_INTEGRATION=1 to run Chrome integration")
	}
	targetURL := strings.TrimSpace(os.Getenv("DIANA_HEADLESS_BROWSER_TEST_URL"))
	if targetURL == "" {
		targetURL = "https://example.com"
	}
	renderer := NewSandboxedHeadlessBrowser(SandboxedBrowserConfig{
		Timeout:           30 * time.Second,
		VirtualTimeBudget: 3 * time.Second,
	})
	page, err := renderer.Render(context.Background(), targetURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("rendered title=%q url=%q text=%q", page.Title, page.URL, truncateText(page.Text, 500))
	if !page.Sandboxed || (page.Title == "" && page.Text == "") {
		t.Fatalf("page = %#v", page)
	}
	if targetURL == "https://example.com" && (page.Title != "Example Domain" || !strings.Contains(page.Text, "documentation examples")) {
		t.Fatalf("page = %#v", page)
	}
}

func TestSandboxedHeadlessBrowserDelayedRedirectIntegration(t *testing.T) {
	if os.Getenv("DIANA_HEADLESS_BROWSER_REDIRECT_INTEGRATION") != "1" {
		t.Skip("set DIANA_HEADLESS_BROWSER_REDIRECT_INTEGRATION=1 to run delayed redirect integration")
	}
	renderer := NewSandboxedHeadlessBrowser(SandboxedBrowserConfig{})
	page, err := renderer.Render(context.Background(), "https://67movies.xyz")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("rendered stable=%v waited=%d url=%q title=%q chain=%#v previous=%#v", page.Stable, page.WaitedMS, page.URL, page.Title, page.NavigationChain, page.PreviousPages)
	if !page.Stable || !strings.Contains(page.URL, "youtube.com/watch") {
		t.Fatalf("final page = %#v", page)
	}
	if len(page.NavigationChain) < 2 || len(page.PreviousPages) == 0 || !strings.Contains(page.PreviousPages[0].Text, "Welcome to 67movies") {
		t.Fatalf("navigation evidence missing: %#v", page)
	}
}
