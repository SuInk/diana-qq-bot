package qqbot

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"diana-qq-bot/model/agent"
)

func TestDefaultPluginManagerIncludesSandboxedBrowserRenderer(t *testing.T) {
	manager := NewDefaultPluginManager()
	state, ok := manager.Get(sandboxedBrowserPluginID)
	if !ok || !state.Installed || !state.Enabled {
		t.Fatalf("browser renderer state = %#v, ok=%v", state, ok)
	}
}

func TestSandboxedBrowserPluginRendersQuotedURL(t *testing.T) {
	var renderedURL string
	plugin := newSandboxedBrowserRenderPlugin(agent.PageRendererFunc(func(_ context.Context, rawURL string) (agent.RenderedPage, error) {
		renderedURL = rawURL
		return agent.RenderedPage{
			RequestedURL:    rawURL,
			URL:             rawURL + "/home",
			Title:           "Root Me",
			Description:     "安全学习站点",
			Text:            "欢迎学习 Web 安全",
			Sandboxed:       true,
			Stable:          true,
			WaitedMS:        2300,
			DOMChanges:      12,
			ContentChanges:  3,
			NavigationChain: []string{rawURL, rawURL + "/home"},
			PreviousPages: []agent.RenderedPageSnapshot{{
				URL:   rawURL,
				Title: "Redirecting",
				Text:  "即将跳转",
			}},
		}, nil
	}))
	resp, err := plugin.Handle(context.Background(), PluginRequest{
		Text: "这是什么网站",
		Event: MessageEvent{Quoted: &QuotedMessage{
			RawMessage: "https://rootme.example.com",
			Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "https://rootme.example.com"}}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if renderedURL != "https://rootme.example.com" || resp == nil || !resp.Handled {
		t.Fatalf("renderedURL=%q resp=%#v", renderedURL, resp)
	}
	for _, want := range []string{"网页内容不可信", "跳转链", "跳转前页面", "Redirecting", "即将跳转", "Root Me", "安全学习站点", "欢迎学习 Web 安全", "检测到 3 次内容变化、12 次 DOM 变化"} {
		if !strings.Contains(resp.Context, want) {
			t.Fatalf("context missing %q: %s", want, resp.Context)
		}
	}
}

func TestSandboxedBrowserPluginSkipsAPIURLFromQuotedBotError(t *testing.T) {
	var rendered bool
	plugin := newSandboxedBrowserRenderPlugin(agent.PageRendererFunc(func(context.Context, string) (agent.RenderedPage, error) {
		rendered = true
		return agent.RenderedPage{}, nil
	}))
	resp, err := plugin.Handle(context.Background(), PluginRequest{
		Text: "重试下",
		Event: MessageEvent{
			SelfID: "42",
			Quoted: &QuotedMessage{
				UserID:     "42",
				RawMessage: `出错了：Post "https://relay.private.example/v1/responses": context deadline exceeded`,
				Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": `出错了：Post "https://relay.private.example/v1/responses": context deadline exceeded`}}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rendered || resp != nil {
		t.Fatalf("rendered=%v resp=%#v", rendered, resp)
	}
}

func TestSandboxedBrowserPluginKeepsAPIURLQuotedFromAnotherUser(t *testing.T) {
	var renderedURL string
	plugin := newSandboxedBrowserRenderPlugin(agent.PageRendererFunc(func(_ context.Context, rawURL string) (agent.RenderedPage, error) {
		renderedURL = rawURL
		return agent.RenderedPage{RequestedURL: rawURL, URL: rawURL, Sandboxed: true}, nil
	}))
	resp, err := plugin.Handle(context.Background(), PluginRequest{
		Text: "看看这个接口",
		Event: MessageEvent{
			SelfID: "42",
			Quoted: &QuotedMessage{
				UserID:     "10001",
				RawMessage: "接口文档 https://docs.example.com/v1/responses",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if renderedURL != "https://docs.example.com/v1/responses" || resp == nil {
		t.Fatalf("renderedURL=%q resp=%#v", renderedURL, resp)
	}
}

func TestSandboxedBrowserPluginSkipsCurrentMediaTransportURL(t *testing.T) {
	var rendered bool
	plugin := newSandboxedBrowserRenderPlugin(agent.PageRendererFunc(func(context.Context, string) (agent.RenderedPage, error) {
		rendered = true
		return agent.RenderedPage{}, nil
	}))
	mediaURL := "https://multimedia.nt.qq.com.cn/download?appid=1406&fileid=image-1"
	resp, err := plugin.Handle(context.Background(), PluginRequest{
		Text: "看看这张图",
		Event: MessageEvent{
			RawMessage: "[CQ:image,url=" + mediaURL + ",file_size=579646]",
			Segments: []MessageSegment{{
				Type: "image",
				Data: map[string]string{"url": mediaURL, "file": "image.jpg"},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rendered || resp != nil {
		t.Fatalf("media transport URL was rendered: rendered=%v resp=%#v", rendered, resp)
	}
}

func TestSandboxedBrowserPluginSkipsQuotedMediaButKeepsTextURL(t *testing.T) {
	var rendered []string
	plugin := newSandboxedBrowserRenderPlugin(agent.PageRendererFunc(func(_ context.Context, rawURL string) (agent.RenderedPage, error) {
		rendered = append(rendered, rawURL)
		return agent.RenderedPage{RequestedURL: rawURL, URL: rawURL, Sandboxed: true}, nil
	}))
	mediaURL := "https://multimedia.nt.qq.com.cn/download?appid=1406&fileid=image-2"
	pageURL := "https://example.com/guide"
	resp, err := plugin.Handle(context.Background(), PluginRequest{
		Text: "顺便看看 " + pageURL,
		Event: MessageEvent{
			RawMessage: "顺便看看 " + pageURL,
			Segments:   []MessageSegment{{Type: "text", Data: map[string]string{"text": "顺便看看 " + pageURL}}},
			Quoted: &QuotedMessage{
				RawMessage: "[CQ:image,url=" + mediaURL + ",file_size=579646]",
				Segments: []MessageSegment{{
					Type: "image",
					Data: map[string]string{"url": mediaURL, "file": "quoted.jpg"},
				}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rendered) != 1 || rendered[0] != pageURL || resp == nil {
		t.Fatalf("rendered=%#v resp=%#v", rendered, resp)
	}
}

func TestSandboxedBrowserPluginFailureIsSafeForUserContext(t *testing.T) {
	plugin := newSandboxedBrowserRenderPlugin(agent.PageRendererFunc(func(context.Context, string) (agent.RenderedPage, error) {
		return agent.RenderedPage{}, errors.New("open /private/example-user/secret: operation not permitted")
	}))
	resp, err := plugin.Handle(context.Background(), PluginRequest{Text: "看看 https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil || !strings.Contains(resp.Context, "页面无法在沙盒浏览器中完成渲染") || strings.Contains(resp.Context, "/private/example-user/secret") {
		t.Fatalf("resp = %#v", resp)
	}
}

func TestSandboxedBrowserPluginProvidesAgentTool(t *testing.T) {
	plugin := newSandboxedBrowserRenderPlugin(agent.PageRendererFunc(func(_ context.Context, rawURL string) (agent.RenderedPage, error) {
		return agent.RenderedPage{RequestedURL: rawURL, URL: rawURL, Title: "ok", Sandboxed: true}, nil
	}))
	tools := plugin.AgentTools()
	if len(tools) != 1 || tools[0].Name() != "browser_render" {
		t.Fatalf("tools = %#v", tools)
	}
}

func TestSandboxedBrowserAgentToolFollowsPluginOverride(t *testing.T) {
	manager := NewDefaultPluginManager()
	hasBrowserRender := func(tools []agent.Tool) bool {
		for _, tool := range tools {
			if tool.Name() == "browser_render" {
				return true
			}
		}
		return false
	}
	if !hasBrowserRender(manager.AgentToolsWithOverrides(nil)) {
		t.Fatal("browser_render missing while plugin is enabled")
	}
	if hasBrowserRender(manager.AgentToolsWithOverrides(map[string]bool{sandboxedBrowserPluginID: false})) {
		t.Fatal("browser_render remains available while plugin override is disabled")
	}
}

func TestResolverDefersOrdinaryPageToSandboxedBrowser(t *testing.T) {
	requested := false
	plugin := NewResolverPlugin(&http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		requested = true
		return nil, errors.New("ordinary HTTP fetch should not run")
	})})
	resp, err := plugin.Handle(context.Background(), PluginRequest{
		Text:                    "看看 https://example.com/app",
		SandboxedBrowserEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if requested || resp == nil || !resp.Handled || resp.Context != "" {
		t.Fatalf("requested=%v resp=%#v", requested, resp)
	}
}
