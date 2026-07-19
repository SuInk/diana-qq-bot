package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Tool interface {
	Name() string
	Description() string
	Run(ctx context.Context, input map[string]any) (string, error)
}

type ToolCatalogItem struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// TerminalResultTool can finish the agent loop immediately after a successful
// tool call. It is intended for tools that already performed the requested
// action and return an authoritative user-facing acknowledgement.
type TerminalResultTool interface {
	Tool
	TerminalResult(output string) (string, bool)
}

type closeableTool interface {
	Close() error
}

type ToolRegistry struct {
	tools   map[string]Tool
	order   []string
	closers []closeableTool
	skills  []SkillMetadata
}

// NewDefaultToolRegistry 创建 Agent 默认工具注册表。
func NewDefaultToolRegistry(cfg Config) (*ToolRegistry, error) {
	cfg = cfg.WithDefaults()
	root, err := filepath.Abs(cfg.WorkDir)
	if err != nil {
		return nil, err
	}
	registry := NewToolRegistry()
	// 默认工具都绑定到同一个绝对工作目录，后续 safePath 负责防逃逸校验。
	registry.Register(&ListFilesTool{root: root, limit: cfg.ListDirectoryLimit})
	registry.Register(&ReadFileTool{root: root, maxBytes: cfg.ReadFileMaxBytes})
	if len(cfg.CommandAllowlist) > 0 {
		registry.Register(&RunCommandTool{
			root:      root,
			allowlist: commandAllowlistSet(cfg.CommandAllowlist),
			timeout:   time.Duration(cfg.CommandTimeoutMS) * time.Millisecond,
			maxBytes:  cfg.MaxToolOutputChars,
		})
	}
	registry.Register(&WebSearchTool{
		timeout:    defaultWebSearchTimeout,
		maxBytes:   cfg.MaxToolOutputChars,
		configPath: filepath.Join(root, DefaultWebSearchConfigFile),
	})
	registry.RegisterBrowserTools(root, cfg)
	return registry, nil
}

// NewAgentToolRegistry 创建包含本地工具、skills 工具和 MCP 工具的注册表。
func NewAgentToolRegistry(ctx context.Context, cfg Config) (*ToolRegistry, error) {
	registry, err := NewDefaultToolRegistry(cfg)
	if err != nil {
		return nil, err
	}
	skills, _ := LoadSkills(cfg.SkillRoots)
	if len(skills) > 0 {
		registry.SetSkills(skills)
		tools := NewSkillTools(skills)
		registry.Register(tools.List)
		registry.Register(tools.Read)
	}
	mcpRegistry, err := NewMCPRegistry(ctx, cfg)
	if err != nil {
		return nil, err
	}
	for _, tool := range mcpRegistry.Tools {
		registry.Register(tool)
	}
	for _, closer := range mcpRegistry.Closers {
		registry.RegisterCloser(closer)
	}
	return registry, nil
}

// SetSkills 记录可用 skill 元数据，供 Runner 构造 skills 上下文。
func (r *ToolRegistry) SetSkills(skills []SkillMetadata) {
	r.skills = append([]SkillMetadata(nil), skills...)
}

// Skills 返回当前注册表关联的 skills。
func (r *ToolRegistry) Skills() []SkillMetadata {
	if r == nil {
		return nil
	}
	return append([]SkillMetadata(nil), r.skills...)
}

// NewToolRegistry 创建工具注册表并登记初始工具。
func NewToolRegistry(tools ...Tool) *ToolRegistry {
	registry := &ToolRegistry{tools: map[string]Tool{}}
	for _, tool := range tools {
		registry.Register(tool)
	}
	return registry
}

// RegisterCloser 让注册表托管非工具资源的生命周期，例如 MCP 子进程。
func (r *ToolRegistry) RegisterCloser(closer closeableTool) {
	if closer == nil {
		return
	}
	r.closers = append(r.closers, closer)
}

// Register 将工具加入注册表并保持描述顺序稳定。
func (r *ToolRegistry) Register(tool Tool) {
	if tool == nil || strings.TrimSpace(tool.Name()) == "" {
		return
	}
	name := tool.Name()
	if _, exists := r.tools[name]; !exists {
		r.order = append(r.order, name)
		// 描述按名称排序，模型看到的工具列表稳定，测试输出也稳定。
		sort.Strings(r.order)
	}
	r.tools[name] = tool
}

// RegisterBrowserTools 登记基于 Chrome DevTools Protocol 的浏览器工具。
func (r *ToolRegistry) RegisterBrowserTools(root string, cfg Config) {
	timeout := time.Duration(cfg.BrowserTimeoutMS) * time.Millisecond
	base := browserToolBase{
		root:     root,
		cdpURL:   cfg.BrowserCDPURL,
		timeout:  timeout,
		maxChars: cfg.MaxToolOutputChars,
	}
	r.Register(&BrowserOpenTool{base: base})
	r.Register(&BrowserTextTool{base: base})
	r.Register(&BrowserClickTool{base: base})
	r.Register(&BrowserTypeTool{base: base})
	r.Register(&BrowserScreenshotTool{base: base})
}

// Get 按名称查找工具。
func (r *ToolRegistry) Get(name string) (Tool, bool) {
	tool, ok := r.tools[name]
	return tool, ok
}

// Retain removes every tool not present in allowed. A nil allowlist keeps all
// tools and is used only for the bot Owner's unrestricted registry.
func (r *ToolRegistry) Retain(allowed map[string]bool) {
	if r == nil || allowed == nil {
		return
	}
	order := make([]string, 0, len(r.order))
	for _, name := range r.order {
		if allowed[name] {
			order = append(order, name)
			continue
		}
		delete(r.tools, name)
	}
	r.order = order
	if !allowed["skills.list"] && !allowed["skills.read"] {
		r.skills = nil
	}
}

// Remove deletes one tool while preserving the stable order of the remaining
// registry entries.
func (r *ToolRegistry) Remove(name string) {
	if r == nil {
		return
	}
	name = strings.TrimSpace(name)
	if _, exists := r.tools[name]; !exists {
		return
	}
	delete(r.tools, name)
	order := r.order[:0]
	for _, current := range r.order {
		if current != name {
			order = append(order, current)
		}
	}
	r.order = order
	if name == "skills.list" || name == "skills.read" {
		if _, hasList := r.tools["skills.list"]; !hasList {
			if _, hasRead := r.tools["skills.read"]; !hasRead {
				r.skills = nil
			}
		}
	}
}

func (r *ToolRegistry) Len() int {
	if r == nil {
		return 0
	}
	return len(r.tools)
}

// Names returns the registered tool names in the same stable order used by the
// Agent prompt.
func (r *ToolRegistry) Names() []string {
	if r == nil {
		return nil
	}
	return append([]string(nil), r.order...)
}

// Catalog returns a compact semantic routing catalog. Input schemas stay out of
// the router request and are shown only to the answering Agent after selection.
func (r *ToolRegistry) Catalog(descriptionRunes int) []ToolCatalogItem {
	if r == nil {
		return nil
	}
	if descriptionRunes <= 0 {
		descriptionRunes = 180
	}
	items := make([]ToolCatalogItem, 0, len(r.order))
	for _, name := range r.order {
		description := strings.TrimSpace(r.tools[name].Description())
		if index := strings.Index(strings.ToLower(description), "input:"); index >= 0 {
			description = strings.TrimSpace(description[:index])
		}
		description = strings.Join(strings.Fields(description), " ")
		items = append(items, ToolCatalogItem{
			Name:        name,
			Description: truncateRunes(description, descriptionRunes),
		})
	}
	return items
}

// Descriptions 返回给模型看的工具描述列表。
func (r *ToolRegistry) Descriptions() string {
	if r == nil || len(r.order) == 0 {
		return "无可用工具。"
	}
	var builder strings.Builder
	for _, name := range r.order {
		tool := r.tools[name]
		builder.WriteString("- ")
		builder.WriteString(tool.Name())
		builder.WriteString(": ")
		builder.WriteString(tool.Description())
		builder.WriteByte('\n')
	}
	return strings.TrimSpace(builder.String())
}

// Close 释放工具持有的外部资源。
func (r *ToolRegistry) Close() error {
	if r == nil {
		return nil
	}
	var parts []string
	seen := map[closeableTool]bool{}
	for _, closer := range r.closers {
		if seen[closer] {
			continue
		}
		seen[closer] = true
		if err := closer.Close(); err != nil {
			parts = append(parts, err.Error())
		}
	}
	for _, tool := range r.tools {
		closer, ok := tool.(closeableTool)
		if !ok || seen[closer] {
			continue
		}
		seen[closer] = true
		if err := closer.Close(); err != nil {
			parts = append(parts, err.Error())
		}
	}
	if len(parts) > 0 {
		return errors.New(strings.Join(parts, "; "))
	}
	return nil
}

type ListFilesTool struct {
	root  string
	limit int
}

// Name 返回列目录工具名称。
func (t *ListFilesTool) Name() string {
	return "list_files"
}

// Description 返回列目录工具说明。
func (t *ListFilesTool) Description() string {
	return `列出 Agent 工作目录内的文件。input: {"path":"相对目录，可选"}`
}

// Run 列出 Agent 工作目录内的文件。
func (t *ListFilesTool) Run(_ context.Context, input map[string]any) (string, error) {
	rel := stringFromInput(input, "path")
	if rel == "" {
		rel = "."
	}
	path, err := safePath(t.root, rel)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return "", err
	}
	limit := t.limit
	if limit <= 0 {
		limit = DefaultListDirectoryLimit
	}
	type entry struct {
		Name string `json:"name"`
		Type string `json:"type"`
		Size int64  `json:"size,omitempty"`
	}
	out := make([]entry, 0, min(len(entries), limit))
	for i, item := range entries {
		if i >= limit {
			break
		}
		itemType := "file"
		if item.IsDir() {
			itemType = "directory"
		}
		row := entry{Name: item.Name(), Type: itemType}
		if info, err := item.Info(); err == nil && !item.IsDir() {
			row.Size = info.Size()
		}
		out = append(out, row)
	}
	body, err := json.MarshalIndent(map[string]any{
		"path":    rel,
		"entries": out,
		// truncated 告诉模型目录没列完，必要时可以继续读更具体路径。
		"truncated": len(entries) > limit,
	}, "", "  ")
	if err != nil {
		return "", err
	}
	return string(body), nil
}

type ReadFileTool struct {
	root     string
	maxBytes int
}

type RunCommandTool struct {
	root      string
	allowlist map[string]bool
	timeout   time.Duration
	maxBytes  int
}

// Name 返回命令执行工具名称。
func (t *RunCommandTool) Name() string {
	return "run_command"
}

// Description 返回命令执行工具说明。
func (t *RunCommandTool) Description() string {
	return `在 Agent 工作目录内执行短时本地命令，不经过 shell。不要用于网页搜索、计时、提醒、周期任务、sleep 或后台驻留；这些场景必须使用对应的专用工具。实时网页搜索必须优先使用 web_search.search。input: {"command":"命令名","args":["参数"],"cwd":"相对目录，可选","timeout_ms":10000}`
}

// Run 在 Agent 工作目录内执行白名单命令。
func (t *RunCommandTool) Run(ctx context.Context, input map[string]any) (string, error) {
	command := stringFromInput(input, "command")
	if command == "" {
		return "", errors.New("command is required")
	}
	if strings.ContainsAny(command, `/\`) {
		return "", errors.New("command must be a binary name, not a path")
	}
	if !t.commandAllowed(command) {
		return "", fmt.Errorf("command %q is not allowed", command)
	}
	cwd, err := safePath(t.root, stringFromInput(input, "cwd"))
	if err != nil {
		return "", err
	}
	timeout := time.Duration(intFromInput(input, "timeout_ms", int(t.timeout.Milliseconds()))) * time.Millisecond
	if timeout <= 0 || timeout > t.timeout {
		timeout = t.timeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := stringSliceFromInput(input, "args")
	cmd := exec.CommandContext(runCtx, command, args...)
	cmd.Dir = cwd
	buffer := &limitedBuffer{limit: t.maxBytes}
	cmd.Stdout = buffer
	cmd.Stderr = buffer
	start := time.Now()
	err = cmd.Run()
	duration := time.Since(start)
	exitCode := 0
	if err != nil {
		exitCode = -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else if runCtx.Err() == context.DeadlineExceeded {
			exitCode = -1
		} else {
			return "", err
		}
	}
	body, err := json.MarshalIndent(map[string]any{
		"command":     command,
		"args":        args,
		"cwd":         relPathForOutput(t.root, cwd),
		"exit_code":   exitCode,
		"timed_out":   runCtx.Err() == context.DeadlineExceeded,
		"duration_ms": duration.Milliseconds(),
		"truncated":   buffer.truncated,
		"output":      buffer.String(),
	}, "", "  ")
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func (t *RunCommandTool) commandAllowed(command string) bool {
	if t.allowlist["*"] {
		return true
	}
	return t.allowlist[command]
}

func commandAllowlistSet(values []string) map[string]bool {
	out := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out[value] = true
		}
	}
	return out
}

type limitedBuffer struct {
	builder   strings.Builder
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	written := len(p)
	if b.limit <= 0 {
		b.limit = DefaultMaxToolOutputChars
	}
	remaining := b.limit - b.builder.Len()
	if remaining <= 0 {
		b.truncated = true
		return written, nil
	}
	if len(p) > remaining {
		b.truncated = true
		p = p[:remaining]
	}
	_, _ = b.builder.Write(p)
	return written, nil
}

func (b *limitedBuffer) String() string {
	return b.builder.String()
}

// Name 返回读文件工具名称。
func (t *ReadFileTool) Name() string {
	return "read_file"
}

// Description 返回读文件工具说明。
func (t *ReadFileTool) Description() string {
	return `读取 Agent 工作目录内的文本文件。input: {"path":"相对文件路径","max_bytes":65536}`
}

// Run 读取 Agent 工作目录内的文本文件。
func (t *ReadFileTool) Run(_ context.Context, input map[string]any) (string, error) {
	rel := stringFromInput(input, "path")
	if rel == "" {
		return "", errors.New("path is required")
	}
	path, err := safePath(t.root, rel)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory", rel)
	}
	maxBytes := intFromInput(input, "max_bytes", t.maxBytes)
	if maxBytes <= 0 || maxBytes > t.maxBytes {
		// 用户输入只能缩小读取范围，不能突破工具注册时的最大字节限制。
		maxBytes = t.maxBytes
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	limited := io.LimitReader(file, int64(maxBytes)+1)
	content, err := io.ReadAll(limited)
	if err != nil {
		return "", err
	}
	truncated := len(content) > maxBytes
	if truncated {
		// 多读的 1 字节只用于判断截断，返回内容仍严格不超过 maxBytes。
		content = content[:maxBytes]
	}
	body, err := json.MarshalIndent(map[string]any{
		"path":      rel,
		"size":      info.Size(),
		"truncated": truncated,
		"content":   string(content),
	}, "", "  ")
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// safePath 将相对路径限制在 Agent 工作目录内。
func safePath(root, rel string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", errors.New("agent workdir is empty")
	}
	if strings.TrimSpace(rel) == "" {
		rel = "."
	}
	cleanRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(rel) {
		return "", errors.New("absolute paths are not allowed")
	}
	candidate, err := filepath.Abs(filepath.Join(cleanRoot, filepath.Clean(rel)))
	if err != nil {
		return "", err
	}
	relation, err := filepath.Rel(cleanRoot, candidate)
	if err != nil {
		return "", err
	}
	if relation == ".." || strings.HasPrefix(relation, ".."+string(filepath.Separator)) {
		// filepath.Clean 后再 Rel 校验，阻止 ../ 逃出 Agent 工作目录。
		return "", errors.New("path escapes agent workdir")
	}
	resolvedRoot, err := filepath.EvalSymlinks(cleanRoot)
	if err != nil {
		return "", err
	}
	resolvedCandidate, err := evalSymlinksAllowMissing(candidate)
	if err != nil {
		return "", err
	}
	relation, err = filepath.Rel(resolvedRoot, resolvedCandidate)
	if err != nil {
		return "", err
	}
	if relation == ".." || strings.HasPrefix(relation, ".."+string(filepath.Separator)) {
		return "", errors.New("path resolves outside agent workdir")
	}
	return candidate, nil
}

func evalSymlinksAllowMissing(path string) (string, error) {
	current := path
	missing := make([]string, 0, 4)
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return resolved, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

// stringFromInput 从工具输入中读取字符串字段。
func stringFromInput(input map[string]any, key string) string {
	if input == nil {
		return ""
	}
	value, _ := input[key].(string)
	return strings.TrimSpace(value)
}

// rawStringFromInput 从工具输入中读取字符串字段，不裁剪空白。
func rawStringFromInput(input map[string]any, key string) string {
	if input == nil {
		return ""
	}
	value, _ := input[key].(string)
	return value
}

// stringSliceFromInput 从工具输入中读取字符串数组字段。
func stringSliceFromInput(input map[string]any, key string) []string {
	if input == nil {
		return nil
	}
	switch values := input[key].(type) {
	case []string:
		return append([]string(nil), values...)
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			text, ok := value.(string)
			if ok {
				out = append(out, text)
			}
		}
		return out
	case string:
		if strings.TrimSpace(values) == "" {
			return nil
		}
		return strings.Fields(values)
	default:
		return nil
	}
}

// boolFromInput 从工具输入中读取布尔字段。
func boolFromInput(input map[string]any, key string, fallback bool) bool {
	if input == nil {
		return fallback
	}
	switch value := input[key].(type) {
	case bool:
		return value
	case string:
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	}
	return fallback
}

// intFromInput 从工具输入中读取整数字段。
func intFromInput(input map[string]any, key string, fallback int) int {
	if input == nil {
		return fallback
	}
	// JSON 反序列化后数字常见为 float64/json.Number，工具参数统一兼容这些类型。
	switch value := input[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		if parsed, err := value.Int64(); err == nil {
			return int(parsed)
		}
	}
	return fallback
}

func relPathForOutput(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return "."
	}
	return filepath.ToSlash(rel)
}
