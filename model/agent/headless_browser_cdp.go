package agent

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	cdppage "github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

const (
	chromeDevToolsPrefix       = "DevTools listening on "
	browserStartupTimeout      = 10 * time.Second
	browserProbeTimeout        = 3 * time.Second
	browserCaptureTimeout      = 5 * time.Second
	browserLoadingStableWindow = 5 * time.Second
	previousPageTextLimit      = 1200
	maxPreviousPageSnapshots   = 3
	semanticNetworkGraceFactor = 3
)

type browserDOMProbe struct {
	URL               string `json:"url"`
	ReadyState        string `json:"ready_state"`
	Title             string `json:"title"`
	Description       string `json:"description"`
	TextLength        int    `json:"text_length"`
	SemanticSignature string `json:"semantic_signature"`
	DOMChanges        int64  `json:"dom_changes"`
}

func (p browserDOMProbe) meaningful() bool {
	return strings.TrimSpace(p.Title) != "" || strings.TrimSpace(p.Description) != "" || p.TextLength > 0
}

type browserActivitySnapshot struct {
	NavigationChain []string
	LastNavigation  time.Time
	LastNetwork     time.Time
	Loading         bool
	PendingRequests int
	BlockedError    error
}

type browserActivityTracker struct {
	mu              sync.Mutex
	mainFrameID     cdp.FrameID
	navigationChain []string
	lastNavigation  time.Time
	lastNetwork     time.Time
	loading         bool
	pending         map[network.RequestID]struct{}
	blockedError    error
}

func newBrowserActivityTracker(rawURL string, now time.Time) *browserActivityTracker {
	return &browserActivityTracker{
		navigationChain: []string{rawURL},
		lastNavigation:  now,
		lastNetwork:     now,
		loading:         true,
		pending:         map[network.RequestID]struct{}{},
	}
}

func (t *browserActivityTracker) observe(event any, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()

	switch value := event.(type) {
	case *cdppage.EventFrameStartedNavigating:
		if t.mainFrameID == "" || value.FrameID == t.mainFrameID {
			t.appendNavigationLocked(value.URL)
			t.lastNavigation = now
			t.loading = true
			t.pending = map[network.RequestID]struct{}{}
		}
	case *cdppage.EventFrameNavigated:
		if value.Frame != nil && value.Frame.ParentID == "" {
			t.mainFrameID = value.Frame.ID
			t.appendNavigationLocked(value.Frame.URL)
			t.lastNavigation = now
			t.loading = true
			t.pending = map[network.RequestID]struct{}{}
		}
	case *cdppage.EventFrameStartedLoading:
		if value.FrameID == t.mainFrameID {
			t.loading = true
		}
	case *cdppage.EventFrameStoppedLoading:
		if value.FrameID == t.mainFrameID {
			t.loading = false
		}
	case *network.EventRequestWillBeSent:
		if !importantBrowserResource(value.Type) || (t.mainFrameID != "" && value.FrameID != "" && value.FrameID != t.mainFrameID) {
			return
		}
		t.pending[value.RequestID] = struct{}{}
		t.lastNetwork = now
		if value.Type == network.ResourceTypeDocument && value.Request != nil {
			t.appendNavigationLocked(value.Request.URL)
		}
	case *network.EventLoadingFinished:
		if _, ok := t.pending[value.RequestID]; ok {
			delete(t.pending, value.RequestID)
			t.lastNetwork = now
		}
	case *network.EventLoadingFailed:
		if _, ok := t.pending[value.RequestID]; ok {
			delete(t.pending, value.RequestID)
			t.lastNetwork = now
		}
	}
}

func (t *browserActivityTracker) appendNavigationLocked(rawURL string) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" || rawURL == "about:blank" {
		return
	}
	if len(t.navigationChain) == 0 || t.navigationChain[len(t.navigationChain)-1] != rawURL {
		t.navigationChain = append(t.navigationChain, rawURL)
	}
}

func (t *browserActivityTracker) block(err error) {
	if err == nil {
		return
	}
	t.mu.Lock()
	if t.blockedError == nil {
		t.blockedError = err
	}
	t.mu.Unlock()
}

func (t *browserActivityTracker) snapshot() browserActivitySnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	return browserActivitySnapshot{
		NavigationChain: append([]string(nil), t.navigationChain...),
		LastNavigation:  t.lastNavigation,
		LastNetwork:     t.lastNetwork,
		Loading:         t.loading,
		PendingRequests: len(t.pending),
		BlockedError:    t.blockedError,
	}
}

func importantBrowserResource(resourceType network.ResourceType) bool {
	switch resourceType {
	case network.ResourceTypeMedia, network.ResourceTypeWebSocket, network.ResourceTypeEventSource,
		network.ResourceTypePing, network.ResourceTypeImage:
		return false
	default:
		return true
	}
}

type renderReadiness struct {
	started             time.Time
	lastURL             string
	lastSignature       string
	lastReadyState      string
	signatureSince      time.Time
	mutationURL         string
	lastMutationCount   int64
	totalDOMChanges     int64
	semanticChangeCount int64
}

type renderDecision struct {
	ContentStable  bool
	Complete       bool
	Reason         string
	StableFor      time.Duration
	DOMChanges     int64
	ContentChanges int64
}

func newRenderReadiness(started time.Time) *renderReadiness {
	return &renderReadiness{started: started, signatureSince: started}
}

func (r *renderReadiness) observe(now time.Time, probe browserDOMProbe, activity browserActivitySnapshot, cfg SandboxedBrowserConfig) renderDecision {
	if probe.URL != r.mutationURL {
		r.mutationURL = probe.URL
		r.lastMutationCount = probe.DOMChanges
		r.totalDOMChanges += max(int64(0), probe.DOMChanges)
	} else if probe.DOMChanges >= r.lastMutationCount {
		r.totalDOMChanges += probe.DOMChanges - r.lastMutationCount
		r.lastMutationCount = probe.DOMChanges
	}

	changed := r.lastURL == "" || probe.URL != r.lastURL || probe.SemanticSignature != r.lastSignature || probe.ReadyState != r.lastReadyState
	if changed {
		if r.lastURL != "" {
			r.semanticChangeCount++
		}
		r.lastURL = probe.URL
		r.lastSignature = probe.SemanticSignature
		r.lastReadyState = probe.ReadyState
		r.signatureSince = now
	}

	stableFor := now.Sub(r.signatureSince)
	navigationQuiet := now.Sub(activity.LastNavigation) >= cfg.StabilityWindow
	networkQuiet := activity.PendingRequests == 0 && now.Sub(activity.LastNetwork) >= cfg.NetworkIdleWindow
	semanticOverride := stableFor >= time.Duration(semanticNetworkGraceFactor)*cfg.StabilityWindow
	loadingStableWindow := max(browserLoadingStableWindow, 2*time.Duration(semanticNetworkGraceFactor)*cfg.StabilityWindow)
	documentReady := probe.ReadyState == "complete" ||
		(probe.ReadyState == "interactive" && semanticOverride) ||
		(probe.ReadyState == "loading" && stableFor >= loadingStableWindow)
	contentStable := documentReady && probe.meaningful() && navigationQuiet && stableFor >= cfg.StabilityWindow
	settled := contentStable && ((!activity.Loading && networkQuiet) || semanticOverride)
	minimumObserved := now.Sub(r.started) >= cfg.VirtualTimeBudget

	reason := "waiting_for_dom"
	switch {
	case !minimumObserved:
		reason = "observing_for_delayed_changes"
	case !contentStable:
		reason = "dom_or_navigation_still_changing"
	case !settled:
		reason = "network_still_active"
	default:
		reason = "dom_and_network_stable"
	}
	return renderDecision{
		ContentStable:  contentStable,
		Complete:       minimumObserved && settled,
		Reason:         reason,
		StableFor:      stableFor,
		DOMChanges:     r.totalDOMChanges,
		ContentChanges: r.semanticChangeCount,
	}
}

type capturedBrowserPage struct {
	signature string
	page      RenderedPage
}

type chromeDiagnosticBuffer struct {
	mu     sync.Mutex
	buffer cappedBuffer
}

func newChromeDiagnosticBuffer(limit int) *chromeDiagnosticBuffer {
	return &chromeDiagnosticBuffer{buffer: cappedBuffer{limit: limit}}
}

func (b *chromeDiagnosticBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(value)
}

func (b *chromeDiagnosticBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

type sandboxedChromeProcess struct {
	wsURL       string
	diagnostics *chromeDiagnosticBuffer
	cancel      context.CancelFunc
	done        <-chan error
}

func (p *sandboxedChromeProcess) stop() {
	if p == nil {
		return
	}
	p.cancel()
	select {
	case <-p.done:
	case <-time.After(2 * time.Second):
	}
}

func launchSandboxedChrome(ctx context.Context, executable, root, profileDir, cacheDir, crashDir string, cfg SandboxedBrowserConfig) (*sandboxedChromeProcess, error) {
	processCtx, cancel := context.WithCancel(ctx)
	args := sandboxedChromeArgs(profileDir, cacheDir, crashDir, cfg)
	args = append(args, "about:blank")
	cmd := exec.CommandContext(processCtx, executable, args...)
	cmd.Env = sandboxedBrowserEnvironment(os.Environ(), root)
	cmd.Stdin = nil
	cmd.Stdout = io.Discard
	cmd.WaitDelay = 2 * time.Second
	diagnostics := newChromeDiagnosticBuffer(32 * 1024)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}

	wsURL := make(chan string, 1)
	go scanChromeDiagnostics(stderr, diagnostics, wsURL)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	timer := time.NewTimer(browserStartupTimeout)
	defer timer.Stop()
	select {
	case value := <-wsURL:
		return &sandboxedChromeProcess{wsURL: value, diagnostics: diagnostics, cancel: cancel, done: done}, nil
	case err := <-done:
		cancel()
		return nil, fmt.Errorf("headless browser exited before CDP startup: %w: %s", err, compactBrowserError(diagnostics.String()))
	case <-timer.C:
		cancel()
		return nil, fmt.Errorf("headless browser CDP startup timeout: %s", compactBrowserError(diagnostics.String()))
	case <-ctx.Done():
		cancel()
		return nil, ctx.Err()
	}
}

func scanChromeDiagnostics(reader io.Reader, diagnostics *chromeDiagnosticBuffer, wsURL chan<- string) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		_, _ = diagnostics.Write(append([]byte(line), '\n'))
		if index := strings.Index(line, chromeDevToolsPrefix); index >= 0 {
			value := strings.TrimSpace(line[index+len(chromeDevToolsPrefix):])
			select {
			case wsURL <- value:
			default:
			}
		}
	}
}

func (b *SandboxedHeadlessBrowser) renderObservable(ctx context.Context, executable, root, profileDir, cacheDir, crashDir, rawURL string) (RenderedPage, error) {
	renderStarted := time.Now()
	process, err := launchSandboxedChrome(ctx, executable, root, profileDir, cacheDir, crashDir, b.cfg)
	if err != nil {
		return RenderedPage{}, err
	}
	defer process.stop()

	allocatorCtx, cancelAllocator := chromedp.NewRemoteAllocator(ctx, process.wsURL, chromedp.NoModifyURL)
	defer cancelAllocator()
	browserCtx, cancelBrowser := chromedp.NewContext(allocatorCtx)
	defer cancelBrowser()

	tracker := newBrowserActivityTracker(rawURL, time.Now())
	chromedp.ListenTarget(browserCtx, func(event any) {
		tracker.observe(event, time.Now())
		if paused, ok := event.(*fetch.EventRequestPaused); ok {
			handlePausedBrowserDocument(browserCtx, tracker, paused)
		}
	})

	// The first Run owns the remote browser lifecycle, so it must use the
	// long-lived browser context rather than a short setup timeout.
	if err := chromedp.Run(browserCtx); err != nil {
		return RenderedPage{}, fmt.Errorf("connect to headless browser CDP: %w", err)
	}
	setupCtx, cancelSetup := context.WithTimeout(browserCtx, min(b.cfg.Timeout, browserStartupTimeout))
	err = chromedp.Run(setupCtx,
		network.Enable(),
		cdppage.Enable(),
		cdppage.SetLifecycleEventsEnabled(true),
		chromedp.ActionFunc(func(actionCtx context.Context) error {
			_, err := cdppage.AddScriptToEvaluateOnNewDocument(browserMutationObserverScript).Do(actionCtx)
			return err
		}),
		fetch.Enable().WithPatterns([]*fetch.RequestPattern{{
			URLPattern:   "*",
			ResourceType: network.ResourceTypeDocument,
			RequestStage: fetch.RequestStageRequest,
		}}),
		chromedp.ActionFunc(func(actionCtx context.Context) error {
			_, _, errorText, isDownload, err := cdppage.Navigate(rawURL).Do(actionCtx)
			if err != nil {
				return err
			}
			if isDownload {
				return errors.New("headless browser navigation became a download")
			}
			if errorText != "" {
				return errors.New(errorText)
			}
			return nil
		}),
	)
	cancelSetup()
	if err != nil {
		if blocked := tracker.snapshot().BlockedError; blocked != nil {
			return RenderedPage{}, blocked
		}
		return RenderedPage{}, fmt.Errorf("headless browser CDP setup failed: %w: %s", err, compactBrowserError(process.diagnostics.String()))
	}

	observationStarted := time.Now()
	readiness := newRenderReadiness(observationStarted)
	hardDeadline := renderStarted.Add(time.Duration(MaxAllowedBrowserTimeoutMS) * time.Millisecond)
	deadline := renderStarted.Add(b.cfg.Timeout)
	if deadline.After(hardDeadline) {
		deadline = hardDeadline
	}
	var deadlineNavigation time.Time
	ticker := time.NewTicker(b.cfg.PollInterval)
	defer ticker.Stop()
	var (
		captures              []capturedBrowserPage
		lastCapturedSignature string
		lastProbe             browserDOMProbe
		lastDecision          renderDecision
	)

	for {
		activity := tracker.snapshot()
		if activity.LastNavigation.After(deadlineNavigation) {
			deadlineNavigation = activity.LastNavigation
			candidate := activity.LastNavigation.Add(b.cfg.Timeout)
			if candidate.After(deadline) {
				deadline = candidate
			}
			if deadline.After(hardDeadline) {
				deadline = hardDeadline
			}
		}
		now := time.Now()
		if !now.Before(deadline) {
			return b.finishObservableRender(browserCtx, executable, rawURL, renderStarted, lastProbe, lastDecision, activity, captures, false, "render_timeout_returning_last_non_empty_snapshot")
		}
		select {
		case <-ctx.Done():
			if len(captures) > 0 {
				return b.finishObservableRender(browserCtx, executable, rawURL, renderStarted, lastProbe, lastDecision, tracker.snapshot(), captures, false, "request_cancelled_returning_last_non_empty_snapshot")
			}
			return RenderedPage{}, ctx.Err()
		case <-ticker.C:
		}

		activity = tracker.snapshot()
		if activity.BlockedError != nil {
			return RenderedPage{}, activity.BlockedError
		}
		probe, err := evaluateBrowserDOMProbe(browserCtx)
		if err != nil {
			if transientBrowserEvaluationError(err) {
				continue
			}
			return RenderedPage{}, fmt.Errorf("inspect rendered DOM: %w", err)
		}
		lastProbe = probe
		lastDecision = readiness.observe(time.Now(), probe, activity, b.cfg)
		if lastDecision.ContentStable && probe.SemanticSignature != "" && probe.SemanticSignature != lastCapturedSignature {
			if page, captureErr := b.captureObservablePage(browserCtx, rawURL); captureErr == nil {
				captures = appendOrReplaceCapturedPage(captures, capturedBrowserPage{signature: probe.SemanticSignature, page: page})
				lastCapturedSignature = probe.SemanticSignature
			}
		}
		if lastDecision.Complete {
			return b.finishObservableRender(browserCtx, executable, rawURL, renderStarted, probe, lastDecision, tracker.snapshot(), captures, true, lastDecision.Reason)
		}
	}
}

func handlePausedBrowserDocument(ctx context.Context, tracker *browserActivityTracker, event *fetch.EventRequestPaused) {
	if event == nil || event.Request == nil {
		return
	}
	requestID := event.RequestID
	rawURL := event.Request.URL
	go func() {
		browserContext := chromedp.FromContext(ctx)
		if browserContext == nil || browserContext.Target == nil {
			return
		}
		executorCtx := cdp.WithExecutor(ctx, browserContext.Target)
		if err := validateSandboxedBrowserURL(ctx, rawURL); err != nil {
			tracker.block(fmt.Errorf("browser redirect blocked for %q: %w", rawURL, err))
			_ = fetch.FailRequest(requestID, network.ErrorReasonBlockedByClient).Do(executorCtx)
			return
		}
		if err := fetch.ContinueRequest(requestID).Do(executorCtx); err != nil && ctx.Err() == nil {
			tracker.block(fmt.Errorf("continue browser navigation %q: %w", rawURL, err))
		}
	}()
}

func evaluateBrowserDOMProbe(ctx context.Context) (browserDOMProbe, error) {
	probeCtx, cancel := context.WithTimeout(ctx, browserProbeTimeout)
	defer cancel()
	var probe browserDOMProbe
	err := chromedp.Run(probeCtx, chromedp.Evaluate(browserDOMProbeScript, &probe))
	return probe, err
}

func (b *SandboxedHeadlessBrowser) captureObservablePage(ctx context.Context, requestedURL string) (RenderedPage, error) {
	captureCtx, cancel := context.WithTimeout(ctx, browserCaptureTimeout)
	defer cancel()
	var snapshot struct {
		URL        string `json:"url"`
		ReadyState string `json:"ready_state"`
		HTML       string `json:"html"`
		Truncated  bool   `json:"truncated"`
		DOMChanges int64  `json:"dom_changes"`
	}
	expression := fmt.Sprintf(browserDOMCaptureScript, b.cfg.MaxHTMLBytes, b.cfg.MaxHTMLBytes)
	if err := chromedp.Run(captureCtx, chromedp.Evaluate(expression, &snapshot)); err != nil {
		return RenderedPage{}, err
	}
	if strings.TrimSpace(snapshot.HTML) == "" {
		return RenderedPage{}, errors.New("headless browser returned an empty DOM snapshot")
	}
	page, err := parseRenderedPage([]byte(snapshot.HTML), requestedURL, b.cfg.MaxTextChars, snapshot.Truncated)
	if err != nil {
		return RenderedPage{}, err
	}
	if snapshot.URL != "" {
		page.URL = snapshot.URL
	}
	page.ReadyState = snapshot.ReadyState
	page.DOMChanges = snapshot.DOMChanges
	return page, nil
}

func (b *SandboxedHeadlessBrowser) finishObservableRender(ctx context.Context, executable, requestedURL string, started time.Time, probe browserDOMProbe, decision renderDecision, activity browserActivitySnapshot, captures []capturedBrowserPage, stable bool, reason string) (RenderedPage, error) {
	page, err := b.captureObservablePage(ctx, requestedURL)
	if err != nil {
		if len(captures) == 0 {
			return RenderedPage{}, fmt.Errorf("headless browser did not produce a readable DOM: %w", err)
		}
		page = captures[len(captures)-1].page
	}
	page.RequestedURL = requestedURL
	page.Sandboxed = true
	page.BrowserEngine = filepath.Base(executable)
	page.ReadyState = firstNonEmptyString(probe.ReadyState, page.ReadyState)
	page.Stable = stable
	page.StabilityReason = reason
	page.WaitedMS = time.Since(started).Milliseconds()
	page.DOMChanges = max(page.DOMChanges, decision.DOMChanges)
	page.ContentChanges = decision.ContentChanges
	page.PendingRequests = activity.PendingRequests
	page.NavigationChain = dedupeConsecutiveStrings(activity.NavigationChain)
	page.PreviousPages = previousPageSnapshots(captures, page.URL)
	return page, nil
}

func appendOrReplaceCapturedPage(captures []capturedBrowserPage, capture capturedBrowserPage) []capturedBrowserPage {
	if len(captures) > 0 && captures[len(captures)-1].page.URL == capture.page.URL {
		captures[len(captures)-1] = capture
		return captures
	}
	return append(captures, capture)
}

func previousPageSnapshots(captures []capturedBrowserPage, finalURL string) []RenderedPageSnapshot {
	start := max(0, len(captures)-maxPreviousPageSnapshots)
	out := make([]RenderedPageSnapshot, 0, len(captures)-start)
	for _, capture := range captures[start:] {
		page := capture.page
		if page.URL == "" || page.URL == finalURL {
			continue
		}
		out = append(out, RenderedPageSnapshot{
			URL:         page.URL,
			Title:       page.Title,
			Description: page.Description,
			Text:        truncateText(page.Text, previousPageTextLimit),
		})
	}
	return out
}

func dedupeConsecutiveStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || value == "about:blank" || (len(out) > 0 && out[len(out)-1] == value) {
			continue
		}
		out = append(out, value)
	}
	return out
}

func transientBrowserEvaluationError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "context was destroyed") ||
		strings.Contains(text, "cannot find context") ||
		strings.Contains(text, "execution context") ||
		strings.Contains(text, "target closed")
}

const browserMutationObserverScript = `(function () {
  const state = { mutations: 0, lastMutationAt: Date.now() };
  Object.defineProperty(globalThis, "__dianaRenderState", {
    configurable: false,
    enumerable: false,
    value: state
  });
  const install = () => {
    if (!document.documentElement) return;
    const observer = new MutationObserver((records) => {
      state.mutations += records.length;
      state.lastMutationAt = Date.now();
    });
    observer.observe(document.documentElement, {
      subtree: true,
      childList: true,
      characterData: true,
      attributes: true
    });
  };
  if (document.documentElement) install();
  else document.addEventListener("DOMContentLoaded", install, { once: true });
})();`

const browserDOMProbeScript = `(() => {
  const normalize = (value) => String(value || "").replace(/\s+/g, " ").trim();

  let state = globalThis.__dianaRenderState;
  if (!state) {
    state = { mutations: 0, lastMutationAt: Date.now() };
    try {
      Object.defineProperty(globalThis, "__dianaRenderState", {
        configurable: false,
        enumerable: false,
        value: state
      });
    } catch (_) {
      globalThis.__dianaRenderState = state;
    }
    if (document.documentElement) {
      const observer = new MutationObserver((records) => {
        state.mutations += records.length;
        state.lastMutationAt = Date.now();
      });
      observer.observe(document.documentElement, {
        subtree: true,
        childList: true,
        characterData: true,
        attributes: true
      });
    }
  }

  const descriptionNode = document.querySelector('meta[name="description"],meta[property="og:description"]');
  const description = normalize(descriptionNode && descriptionNode.content);
  const text = normalize(document.body ? document.body.innerText : "");
  // Stability follows the content that can actually fit in the LLM context.
  // Infinite feeds may keep appending recommendations without changing the
  // primary page information near the start of the visible text.
  const semanticText = text.slice(0, 12000);
  const semantic = [location.href, document.title, description, semanticText].join("\n");
  let hash = 2166136261;
  for (let index = 0; index < semantic.length; index++) {
    hash ^= semantic.charCodeAt(index);
    hash = Math.imul(hash, 16777619);
  }
  return {
    url: location.href,
    ready_state: document.readyState,
    title: normalize(document.title),
    description,
    text_length: text.length,
    semantic_signature: (hash >>> 0).toString(16) + ":" + semantic.length,
    dom_changes: Number(state.mutations || 0)
  };
})()`

const browserDOMCaptureScript = `(() => {
  const html = document.documentElement ? document.documentElement.outerHTML : "";
  const state = globalThis.__dianaRenderState || {};
  return {
    url: location.href,
    ready_state: document.readyState,
    html: html.slice(0, %d),
    truncated: html.length > %d,
    dom_changes: Number(state.mutations || 0)
  };
})()`
