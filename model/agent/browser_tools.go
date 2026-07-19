package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const defaultScreenshotPath = ".agent-browser/screenshot.png"

type browserToolBase struct {
	root     string
	cdpURL   string
	timeout  time.Duration
	maxChars int
}

type BrowserOpenTool struct {
	base browserToolBase
}

func (t *BrowserOpenTool) Name() string {
	return "browser_open"
}

func (t *BrowserOpenTool) Description() string {
	return `通过 Chrome DevTools Protocol 打开网页。需要 Chrome 启用 remote debugging。input: {"url":"https://example.com","new_tab":true}`
}

func (t *BrowserOpenTool) Run(ctx context.Context, input map[string]any) (string, error) {
	pageURL := stringFromInput(input, "url")
	if err := validateBrowserURL(pageURL); err != nil {
		return "", err
	}
	newTab := boolFromInput(input, "new_tab", true)
	client, err := t.base.pageClient(ctx, pageURL, newTab)
	if err != nil {
		return "", err
	}
	defer client.Close()
	if !newTab {
		if err := client.call(ctx, "Page.navigate", map[string]any{"url": pageURL}, nil); err != nil {
			return "", err
		}
	}
	_ = client.waitReady(ctx)
	return t.base.pageSnapshot(ctx, client, "")
}

type BrowserTextTool struct {
	base browserToolBase
}

func (t *BrowserTextTool) Name() string {
	return "browser_text"
}

func (t *BrowserTextTool) Description() string {
	return `读取当前浏览器页面文本。input: {"selector":"CSS 选择器，可选"}`
}

func (t *BrowserTextTool) Run(ctx context.Context, input map[string]any) (string, error) {
	client, err := t.base.pageClient(ctx, "", false)
	if err != nil {
		return "", err
	}
	defer client.Close()
	return t.base.pageSnapshot(ctx, client, stringFromInput(input, "selector"))
}

type BrowserClickTool struct {
	base browserToolBase
}

func (t *BrowserClickTool) Name() string {
	return "browser_click"
}

func (t *BrowserClickTool) Description() string {
	return `点击当前页面中的元素。input: {"selector":"CSS 选择器"}`
}

func (t *BrowserClickTool) Run(ctx context.Context, input map[string]any) (string, error) {
	selector := stringFromInput(input, "selector")
	if selector == "" {
		return "", errors.New("selector is required")
	}
	client, err := t.base.pageClient(ctx, "", false)
	if err != nil {
		return "", err
	}
	defer client.Close()
	expr := fmt.Sprintf(`(() => {
const el = document.querySelector(%s);
if (!el) return {ok:false, error:"selector not found"};
el.scrollIntoView({block:"center", inline:"center"});
el.click();
return {ok:true, url: location.href, title: document.title};
})()`, jsString(selector))
	raw, err := client.evaluate(ctx, expr)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

type BrowserTypeTool struct {
	base browserToolBase
}

func (t *BrowserTypeTool) Name() string {
	return "browser_type"
}

func (t *BrowserTypeTool) Description() string {
	return `向当前页面元素输入文本。input: {"selector":"CSS 选择器","text":"文本","clear":true,"press_enter":false}`
}

func (t *BrowserTypeTool) Run(ctx context.Context, input map[string]any) (string, error) {
	selector := stringFromInput(input, "selector")
	if selector == "" {
		return "", errors.New("selector is required")
	}
	text := rawStringFromInput(input, "text")
	client, err := t.base.pageClient(ctx, "", false)
	if err != nil {
		return "", err
	}
	defer client.Close()
	expr := fmt.Sprintf(`(() => {
const el = document.querySelector(%s);
if (!el) return {ok:false, error:"selector not found"};
el.scrollIntoView({block:"center", inline:"center"});
el.focus();
const text = %s;
const clear = %t;
if (el.isContentEditable) {
  el.textContent = clear ? text : (el.textContent || "") + text;
} else if ("value" in el) {
  el.value = clear ? text : (el.value || "") + text;
} else {
  el.textContent = clear ? text : (el.textContent || "") + text;
}
el.dispatchEvent(new Event("input", {bubbles:true}));
el.dispatchEvent(new Event("change", {bubbles:true}));
if (%t) {
  el.dispatchEvent(new KeyboardEvent("keydown", {key:"Enter", code:"Enter", bubbles:true}));
  el.dispatchEvent(new KeyboardEvent("keyup", {key:"Enter", code:"Enter", bubbles:true}));
  if (el.form) el.form.requestSubmit ? el.form.requestSubmit() : el.form.submit();
}
return {ok:true, url: location.href, title: document.title};
})()`, jsString(selector), jsString(text), boolFromInput(input, "clear", true), boolFromInput(input, "press_enter", false))
	raw, err := client.evaluate(ctx, expr)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

type BrowserScreenshotTool struct {
	base browserToolBase
}

func (t *BrowserScreenshotTool) Name() string {
	return "browser_screenshot"
}

func (t *BrowserScreenshotTool) Description() string {
	return `保存当前页面截图到 Agent 工作目录。input: {"path":".agent-browser/screenshot.png"}`
}

func (t *BrowserScreenshotTool) Run(ctx context.Context, input map[string]any) (string, error) {
	outPath := stringFromInput(input, "path")
	if outPath == "" {
		outPath = defaultScreenshotPath
	}
	path, err := safePath(t.base.root, outPath)
	if err != nil {
		return "", err
	}
	client, err := t.base.pageClient(ctx, "", false)
	if err != nil {
		return "", err
	}
	defer client.Close()
	var result struct {
		Data string `json:"data"`
	}
	if err := client.call(ctx, "Page.captureScreenshot", map[string]any{"format": "png", "fromSurface": true}, &result); err != nil {
		return "", err
	}
	data, err := base64.StdEncoding.DecodeString(result.Data)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	body, err := json.MarshalIndent(map[string]any{
		"path":  relPathForOutput(t.base.root, path),
		"bytes": len(data),
	}, "", "  ")
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func (b browserToolBase) pageSnapshot(ctx context.Context, client *cdpClient, selector string) (string, error) {
	selectorExpr := "null"
	if selector != "" {
		selectorExpr = jsString(selector)
	}
	expr := fmt.Sprintf(`(() => {
const selector = %s;
const el = selector ? document.querySelector(selector) : document.body;
const text = el ? (el.innerText || el.textContent || "") : "";
return {
  url: location.href,
  title: document.title,
  selector,
  text: text.length > %d ? text.slice(0, %d) : text,
  truncated: text.length > %d
};
})()`, selectorExpr, b.maxChars, b.maxChars, b.maxChars)
	raw, err := client.evaluate(ctx, expr)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (b browserToolBase) pageClient(ctx context.Context, pageURL string, newTab bool) (*cdpClient, error) {
	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	baseURL := strings.TrimRight(strings.TrimSpace(b.cdpURL), "/")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:9222"
	}
	target, err := b.pickTarget(ctx, baseURL, pageURL, newTab)
	if err != nil {
		return nil, err
	}
	if target.WebSocketDebuggerURL == "" {
		return nil, errors.New("browser target has no websocket debugger URL")
	}
	client, err := newCDPClient(ctx, target.WebSocketDebuggerURL, b.timeout)
	if err != nil {
		return nil, err
	}
	_ = client.call(ctx, "Page.enable", map[string]any{}, nil)
	_ = client.call(ctx, "Runtime.enable", map[string]any{}, nil)
	return client, nil
}

func (b browserToolBase) pickTarget(ctx context.Context, baseURL, pageURL string, newTab bool) (browserTarget, error) {
	if newTab || pageURL != "" {
		if target, err := newBrowserTarget(ctx, baseURL, firstNonEmptyString(pageURL, "about:blank")); err == nil {
			return target, nil
		}
	}
	targets, err := listBrowserTargets(ctx, baseURL)
	if err != nil {
		return browserTarget{}, err
	}
	for _, target := range targets {
		if target.Type == "page" && target.WebSocketDebuggerURL != "" {
			return target, nil
		}
	}
	return newBrowserTarget(ctx, baseURL, firstNonEmptyString(pageURL, "about:blank"))
}

type browserTarget struct {
	ID                   string `json:"id"`
	Type                 string `json:"type"`
	URL                  string `json:"url"`
	Title                string `json:"title"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

func listBrowserTargets(ctx context.Context, baseURL string) ([]browserTarget, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/json/list", nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, browserConnectError(baseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("browser cdp list failed: HTTP %d", resp.StatusCode)
	}
	var targets []browserTarget
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		return nil, err
	}
	return targets, nil
}

func newBrowserTarget(ctx context.Context, baseURL, pageURL string) (browserTarget, error) {
	endpoint := baseURL + "/json/new?" + url.QueryEscape(pageURL)
	for _, method := range []string{http.MethodPut, http.MethodGet} {
		req, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
		if err != nil {
			return browserTarget{}, err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return browserTarget{}, browserConnectError(baseURL, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusMethodNotAllowed && method == http.MethodPut {
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return browserTarget{}, fmt.Errorf("browser cdp new target failed: HTTP %d", resp.StatusCode)
		}
		var target browserTarget
		if err := json.NewDecoder(resp.Body).Decode(&target); err != nil {
			return browserTarget{}, err
		}
		return target, nil
	}
	return browserTarget{}, errors.New("browser cdp new target failed")
}

func browserConnectError(baseURL string, err error) error {
	return fmt.Errorf("browser cdp is unavailable at %s: %w; start Chrome with --remote-debugging-port=9222 or configure DIANA_AGENT_BROWSER_CDP_URL", baseURL, err)
}

type cdpClient struct {
	conn    *websocket.Conn
	nextID  atomic.Int64
	timeout time.Duration
}

func newCDPClient(ctx context.Context, websocketURL string, timeout time.Duration) (*cdpClient, error) {
	dialer := websocket.Dialer{HandshakeTimeout: timeout}
	conn, _, err := dialer.DialContext(ctx, websocketURL, nil)
	if err != nil {
		return nil, err
	}
	return &cdpClient{conn: conn, timeout: timeout}, nil
}

func (c *cdpClient) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *cdpClient) call(ctx context.Context, method string, params map[string]any, out any) error {
	id := c.nextID.Add(1)
	if params == nil {
		params = map[string]any{}
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = c.conn.SetWriteDeadline(deadline)
		_ = c.conn.SetReadDeadline(deadline)
	} else if c.timeout > 0 {
		deadline := time.Now().Add(c.timeout)
		_ = c.conn.SetWriteDeadline(deadline)
		_ = c.conn.SetReadDeadline(deadline)
	}
	if err := c.conn.WriteJSON(map[string]any{
		"id":     id,
		"method": method,
		"params": params,
	}); err != nil {
		return err
	}
	for {
		var resp struct {
			ID     int64           `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error,omitempty"`
		}
		if err := c.conn.ReadJSON(&resp); err != nil {
			return err
		}
		if resp.ID != id {
			continue
		}
		if resp.Error != nil {
			return fmt.Errorf("cdp %s failed: %s", method, resp.Error.Message)
		}
		if out != nil && len(resp.Result) > 0 {
			if err := json.Unmarshal(resp.Result, out); err != nil {
				return err
			}
		}
		return nil
	}
}

func (c *cdpClient) evaluate(ctx context.Context, expression string) (json.RawMessage, error) {
	var out struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
	}
	if err := c.call(ctx, "Runtime.evaluate", map[string]any{
		"expression":    expression,
		"awaitPromise":  true,
		"returnByValue": true,
	}, &out); err != nil {
		return nil, err
	}
	if len(out.Result.Value) == 0 {
		return []byte("null"), nil
	}
	return out.Result.Value, nil
}

func (c *cdpClient) waitReady(ctx context.Context) error {
	_, err := c.evaluate(ctx, `new Promise(resolve => {
if (document.readyState === "complete") { resolve(true); return; }
const done = () => resolve(true);
window.addEventListener("load", done, {once:true});
setTimeout(done, 3000);
})`)
	return err
}

func validateBrowserURL(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("url is required")
	}
	if value == "about:blank" {
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("only http and https browser URLs are allowed")
	}
	if parsed.Host == "" {
		return errors.New("browser URL host is required")
	}
	return nil
}

func jsString(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
