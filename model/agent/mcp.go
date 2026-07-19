package agent

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pelletier/go-toml/v2"
)

type MCPRegistry struct {
	Tools   []Tool
	Closers []closeableTool
}

type mcpConfigFile struct {
	MCPServers map[string]mcpServerConfig `json:"mcpServers" toml:"mcp_servers"`
}

type mcpServerConfig struct {
	Command           string            `json:"command" toml:"command"`
	Args              []string          `json:"args" toml:"args"`
	Env               map[string]string `json:"env" toml:"env"`
	CWD               string            `json:"cwd" toml:"cwd"`
	URL               string            `json:"url" toml:"url"`
	Enabled           *bool             `json:"enabled" toml:"enabled"`
	Required          bool              `json:"required" toml:"required"`
	StartupTimeoutSec int               `json:"startup_timeout_sec" toml:"startup_timeout_sec"`
	ToolTimeoutSec    int               `json:"tool_timeout_sec" toml:"tool_timeout_sec"`
	EnabledTools      []string          `json:"enabled_tools" toml:"enabled_tools"`
	DisabledTools     []string          `json:"disabled_tools" toml:"disabled_tools"`
}

func (cfg mcpServerConfig) enabled() bool {
	return cfg.Enabled == nil || *cfg.Enabled
}

func (cfg mcpServerConfig) allowsTool(name string) bool {
	if len(cfg.EnabledTools) > 0 {
		found := false
		for _, item := range cfg.EnabledTools {
			if item == name {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	for _, item := range cfg.DisabledTools {
		if item == name {
			return false
		}
	}
	return true
}

// NewMCPRegistry loads configured MCP stdio servers and exposes their tools.
func NewMCPRegistry(ctx context.Context, cfg Config) (MCPRegistry, error) {
	cfg = cfg.WithDefaults()
	path := strings.TrimSpace(cfg.MCPConfigPath)
	if path == "" {
		return MCPRegistry{}, nil
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(cfg.WorkDir, path)
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return MCPRegistry{}, nil
		}
		return MCPRegistry{}, err
	}
	if info.IsDir() {
		return MCPRegistry{}, nil
	}
	servers, err := loadMCPServers(path)
	if err != nil {
		return MCPRegistry{}, err
	}
	if len(servers) == 0 {
		return MCPRegistry{}, nil
	}
	var registry MCPRegistry
	usedToolNames := map[string]bool{}
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		server := servers[name]
		if !server.enabled() {
			continue
		}
		if strings.TrimSpace(server.Command) == "" {
			if server.Required {
				return registry, fmt.Errorf("mcp server %q has no stdio command", name)
			}
			continue
		}
		startupTimeout := time.Duration(firstPositive(server.StartupTimeoutSec*1000, cfg.MCPStartupTimeoutMS)) * time.Millisecond
		toolTimeout := time.Duration(firstPositive(server.ToolTimeoutSec*1000, cfg.MCPToolTimeoutMS)) * time.Millisecond
		startCtx, cancel := context.WithTimeout(ctx, startupTimeout)
		client, err := startMCPStdioClient(startCtx, name, server, toolTimeout)
		cancel()
		if err != nil {
			if server.Required {
				return registry, err
			}
			continue
		}
		tools, err := client.ListTools(ctx)
		if err != nil {
			_ = client.Close()
			if server.Required {
				return registry, err
			}
			continue
		}
		registered := 0
		for _, raw := range tools {
			if !server.allowsTool(raw.Name) {
				continue
			}
			modelName := uniqueMCPModelToolName(name, raw.Name, usedToolNames)
			registry.Tools = append(registry.Tools, &MCPTool{
				client:      client,
				serverName:  name,
				rawName:     raw.Name,
				modelName:   modelName,
				description: raw.Description,
				inputSchema: raw.InputSchema,
			})
			registered++
		}
		if registered == 0 {
			_ = client.Close()
			continue
		}
		registry.Closers = append(registry.Closers, client)
	}
	return registry, nil
}

func loadMCPServers(path string) (map[string]mcpServerConfig, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg mcpConfigFile
	switch strings.ToLower(filepath.Ext(path)) {
	case ".toml":
		if err := toml.Unmarshal(body, &cfg); err != nil {
			return nil, err
		}
	default:
		if err := json.Unmarshal(body, &cfg); err != nil {
			return nil, err
		}
	}
	return cfg.MCPServers, nil
}

type mcpToolInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

type MCPTool struct {
	client      *MCPStdioClient
	serverName  string
	rawName     string
	modelName   string
	description string
	inputSchema json.RawMessage
}

func (t *MCPTool) Name() string {
	return t.modelName
}

func (t *MCPTool) Description() string {
	var parts []string
	if strings.TrimSpace(t.description) != "" {
		parts = append(parts, t.description)
	}
	if len(t.inputSchema) > 0 && string(t.inputSchema) != "null" {
		parts = append(parts, "input schema: "+string(t.inputSchema))
	}
	if len(parts) == 0 {
		parts = append(parts, "MCP tool")
	}
	return fmt.Sprintf("MCP server %s tool %s. %s", t.serverName, t.rawName, strings.Join(parts, " "))
}

func (t *MCPTool) Run(ctx context.Context, input map[string]any) (string, error) {
	return t.client.CallTool(ctx, t.rawName, input)
}

type MCPStdioClient struct {
	name        string
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	responses   chan json.RawMessage
	stderr      bytes.Buffer
	stderrMu    sync.Mutex
	requestMu   sync.Mutex
	writeMu     sync.Mutex
	nextID      atomic.Int64
	toolTimeout time.Duration
	closed      atomic.Bool
}

func startMCPStdioClient(ctx context.Context, name string, cfg mcpServerConfig, toolTimeout time.Duration) (*MCPStdioClient, error) {
	command := strings.TrimSpace(cfg.Command)
	if command == "" {
		return nil, errors.New("mcp command is required")
	}
	cmd := exec.Command(command, cfg.Args...)
	if strings.TrimSpace(cfg.CWD) != "" {
		cmd.Dir = cfg.CWD
	}
	cmd.Env = os.Environ()
	for key, value := range cfg.Env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	client := &MCPStdioClient{
		name:        name,
		cmd:         cmd,
		stdin:       stdin,
		responses:   make(chan json.RawMessage, 32),
		toolTimeout: toolTimeout,
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go client.readStdout(stdout)
	go client.readStderr(stderr)
	if err := client.initialize(ctx); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("mcp server %q initialize failed: %w", name, err)
	}
	return client, nil
}

func (c *MCPStdioClient) initialize(ctx context.Context) error {
	params := map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "diana-qq-bot-agent",
			"version": "0.0.1",
		},
	}
	if _, err := c.request(ctx, "initialize", params); err != nil {
		return err
	}
	return c.notify("notifications/initialized", nil)
}

func (c *MCPStdioClient) ListTools(ctx context.Context) ([]mcpToolInfo, error) {
	var all []mcpToolInfo
	var cursor string
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := c.request(ctx, "tools/list", params)
		if err != nil {
			return nil, fmt.Errorf("mcp server %q tools/list failed: %w", c.name, err)
		}
		var result struct {
			Tools      []mcpToolInfo `json:"tools"`
			NextCursor string        `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			return nil, err
		}
		all = append(all, result.Tools...)
		if strings.TrimSpace(result.NextCursor) == "" {
			return all, nil
		}
		cursor = result.NextCursor
	}
}

func (c *MCPStdioClient) CallTool(ctx context.Context, name string, arguments map[string]any) (string, error) {
	if arguments == nil {
		arguments = map[string]any{}
	}
	timeout := c.toolTimeout
	if timeout <= 0 {
		timeout = time.Duration(DefaultMCPToolTimeoutMS) * time.Millisecond
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	raw, err := c.request(callCtx, "tools/call", map[string]any{
		"name":      name,
		"arguments": arguments,
	})
	if err != nil {
		return "", fmt.Errorf("mcp server %q tools/call %q failed: %w", c.name, name, err)
	}
	return formatMCPToolResult(raw)
}

func (c *MCPStdioClient) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.requestMu.Lock()
	defer c.requestMu.Unlock()
	id := c.nextID.Add(1)
	if err := c.writeJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}); err != nil {
		return nil, err
	}
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case raw, ok := <-c.responses:
			if !ok {
				return nil, c.closedError()
			}
			var envelope struct {
				ID     any             `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  *struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(raw, &envelope); err != nil {
				continue
			}
			if !jsonIDMatches(envelope.ID, id) {
				continue
			}
			if envelope.Error != nil {
				return nil, fmt.Errorf("%d: %s", envelope.Error.Code, envelope.Error.Message)
			}
			return envelope.Result, nil
		}
	}
}

func (c *MCPStdioClient) notify(method string, params any) error {
	msg := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if params != nil {
		msg["params"] = params
	}
	return c.writeJSON(msg)
}

func (c *MCPStdioClient) writeJSON(message any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.closed.Load() {
		return c.closedError()
	}
	body, err := json.Marshal(message)
	if err != nil {
		return err
	}
	body = append(body, '\n')
	_, err = c.stdin.Write(body)
	return err
}

func (c *MCPStdioClient) readStdout(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		raw := append(json.RawMessage(nil), line...)
		select {
		case c.responses <- raw:
		default:
		}
	}
	close(c.responses)
}

func (c *MCPStdioClient) readStderr(stderr io.Reader) {
	_, _ = io.Copy(&lockedBuffer{buffer: &c.stderr, mu: &c.stderrMu}, stderr)
}

func (c *MCPStdioClient) Close() error {
	if c == nil || c.closed.Swap(true) {
		return nil
	}
	_ = c.stdin.Close()
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	if c.cmd != nil {
		_ = c.cmd.Wait()
	}
	return nil
}

func (c *MCPStdioClient) closedError() error {
	c.stderrMu.Lock()
	stderr := strings.TrimSpace(c.stderr.String())
	c.stderrMu.Unlock()
	if stderr != "" {
		return fmt.Errorf("mcp server %q closed: %s", c.name, stderr)
	}
	return fmt.Errorf("mcp server %q closed", c.name)
}

type lockedBuffer struct {
	buffer *bytes.Buffer
	mu     *sync.Mutex
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.buffer.Len() > 32*1024 {
		b.buffer.Reset()
	}
	return b.buffer.Write(p)
}

func jsonIDMatches(value any, want int64) bool {
	switch typed := value.(type) {
	case float64:
		return int64(typed) == want
	case string:
		return typed == fmt.Sprint(want)
	default:
		return false
	}
}

func formatMCPToolResult(raw json.RawMessage) (string, error) {
	var result struct {
		Content []struct {
			Type string          `json:"type"`
			Text string          `json:"text,omitempty"`
			Data json.RawMessage `json:"data,omitempty"`
		} `json:"content"`
		IsError bool `json:"isError,omitempty"`
	}
	if err := json.Unmarshal(raw, &result); err != nil || len(result.Content) == 0 {
		return string(raw), nil
	}
	var parts []string
	for _, item := range result.Content {
		if item.Type == "text" {
			parts = append(parts, item.Text)
			continue
		}
		if len(item.Data) > 0 {
			parts = append(parts, string(item.Data))
			continue
		}
		encoded, _ := json.Marshal(item)
		parts = append(parts, string(encoded))
	}
	output := strings.TrimSpace(strings.Join(parts, "\n"))
	if result.IsError {
		return output, errors.New(output)
	}
	return output, nil
}

func mcpModelToolName(server, tool string) string {
	name := "mcp__" + sanitizeToolName(server) + "__" + sanitizeToolName(tool)
	if len(name) <= 64 {
		return name
	}
	sum := sha256.Sum256([]byte(name))
	suffix := "_" + hex.EncodeToString(sum[:])[:8]
	limit := 64 - len(suffix)
	if limit < 1 {
		return suffix[1:]
	}
	return name[:limit] + suffix
}

func uniqueMCPModelToolName(server, tool string, used map[string]bool) string {
	base := mcpModelToolName(server, tool)
	if !used[base] {
		used[base] = true
		return base
	}
	for i := 1; ; i++ {
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s\000%s\000%d", server, tool, i)))
		suffix := "_" + hex.EncodeToString(sum[:])[:8]
		limit := 64 - len(suffix)
		candidate := base
		if len(candidate) > limit {
			candidate = candidate[:limit]
		}
		candidate += suffix
		if !used[candidate] {
			used[candidate] = true
			return candidate
		}
	}
}

func sanitizeToolName(name string) string {
	name = strings.TrimSpace(name)
	var builder strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			builder.WriteRune(r)
		} else {
			builder.WriteByte('_')
		}
	}
	if builder.Len() == 0 {
		return "_"
	}
	return builder.String()
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}
