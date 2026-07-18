package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMCPRegistryCallsStdioTool 验证 MCP stdio server 能被启动、列工具并调用。
func TestMCPRegistryCallsStdioTool(t *testing.T) {
	if os.Getenv("DIANA_AGENT_MCP_TEST_SERVER") == "1" {
		runMCPTestServer()
		return
	}
	dir := t.TempDir()
	configPath := filepath.Join(dir, ".mcp.json")
	body := fmt.Sprintf(`{
  "mcpServers": {
    "demo": {
      "command": %q,
      "args": ["-test.run=TestMCPRegistryCallsStdioTool"],
      "env": {"DIANA_AGENT_MCP_TEST_SERVER": "1"}
    }
  }
}`, os.Args[0])
	if err := os.WriteFile(configPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	registry, err := NewMCPRegistry(context.Background(), Config{WorkDir: dir, MCPConfigPath: configPath, MCPStartupTimeoutMS: 3000, MCPToolTimeoutMS: 3000})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, closer := range registry.Closers {
			_ = closer.Close()
		}
	}()
	if len(registry.Tools) != 1 {
		t.Fatalf("tools = %#v", registry.Tools)
	}
	if registry.Tools[0].Name() != "mcp__demo__echo" {
		t.Fatalf("tool name = %q", registry.Tools[0].Name())
	}
	got, err := registry.Tools[0].Run(context.Background(), map[string]any{"text": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "echo: hello" {
		t.Fatalf("got = %q", got)
	}
}

func runMCPTestServer() {
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var req map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			continue
		}
		method, _ := req["method"].(string)
		id, hasID := req["id"]
		if method == "notifications/initialized" {
			continue
		}
		if !hasID {
			continue
		}
		switch method {
		case "initialize":
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"protocolVersion": "2025-06-18",
					"capabilities":    map[string]any{"tools": map[string]any{"listChanged": true}},
					"serverInfo":      map[string]any{"name": "demo", "version": "0.0.1"},
				},
			})
		case "tools/list":
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"tools": []map[string]any{{
						"name":        "echo",
						"description": "Echo text.",
						"inputSchema": map[string]any{
							"type":       "object",
							"properties": map[string]any{"text": map[string]any{"type": "string"}},
						},
					}},
				},
			})
		case "tools/call":
			params, _ := req["params"].(map[string]any)
			args, _ := params["arguments"].(map[string]any)
			text, _ := args["text"].(string)
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"content": []map[string]any{{"type": "text", "text": "echo: " + strings.TrimSpace(text)}},
				},
			})
		}
	}
	os.Exit(0)
}
