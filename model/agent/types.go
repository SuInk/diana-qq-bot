package agent

import (
	"context"
	"path/filepath"
	"strings"

	"diana-qq-bot/model/llm"
)

const (
	DefaultMaxSteps                 = 8
	DefaultMaxToolOutputChars       = 8000
	DefaultReadFileMaxBytes         = 64 * 1024
	DefaultListDirectoryLimit       = 200
	DefaultSkillsListBudget         = 8000
	DefaultMCPStartupTimeoutMS      = 10_000
	DefaultMCPToolTimeoutMS         = 60_000
	DefaultCommandTimeoutMS         = 10_000
	DefaultBrowserTimeoutMS         = 15_000
	DefaultToolTimeoutMS            = 60_000
	DefaultFinalizationReserveMS    = 20_000
	DefaultProtocolRepairLimit      = 3
	MaxAllowedSteps                 = 8
	MaxAllowedToolOutputChars       = 20000
	MaxAllowedReadFileMaxBytes      = 512 * 1024
	MaxAllowedCommandTimeoutMS      = 60_000
	MaxAllowedBrowserTimeoutMS      = 60_000
	MaxAllowedToolTimeoutMS         = 120_000
	MaxAllowedFinalizationReserveMS = 60_000
	MaxAllowedProtocolRepairLimit   = 6
)

type LLMClient interface {
	Generate(ctx context.Context, req llm.GenerateRequest) (*llm.GenerateResponse, error)
}

type Config struct {
	WorkDir               string
	MaxSteps              int
	MaxToolOutputChars    int
	ReadFileMaxBytes      int
	ListDirectoryLimit    int
	SkillRoots            []string
	SkillsListBudget      int
	MCPConfigPath         string
	MCPStartupTimeoutMS   int
	MCPToolTimeoutMS      int
	CommandAllowlist      []string
	CommandTimeoutMS      int
	BrowserCDPURL         string
	BrowserTimeoutMS      int
	ToolTimeoutMS         int
	FinalizationReserveMS int
	ProtocolRepairLimit   int
}

type Request struct {
	Messages []llm.Message
	TraceID  string
	Observer RunObserver
}

type Response struct {
	Text         string       `json:"text"`
	Steps        []Step       `json:"steps,omitempty"`
	Provider     llm.Provider `json:"provider,omitempty"`
	Model        string       `json:"model,omitempty"`
	Usage        llm.Usage    `json:"usage,omitempty"`
	TraceID      string       `json:"trace_id,omitempty"`
	ModelTurns   int          `json:"model_turns,omitempty"`
	FinishReason string       `json:"finish_reason,omitempty"`
	DurationMS   int64        `json:"duration_ms,omitempty"`
}

type Step struct {
	Index      int            `json:"index,omitempty"`
	Tool       string         `json:"tool"`
	Input      map[string]any `json:"input,omitempty"`
	Output     string         `json:"output,omitempty"`
	Error      string         `json:"error,omitempty"`
	Skipped    bool           `json:"skipped,omitempty"`
	DurationMS int64          `json:"duration_ms,omitempty"`
}

type RunPhase string

const (
	RunPhaseStarted        RunPhase = "started"
	RunPhaseModelCompleted RunPhase = "model_completed"
	RunPhaseProtocolRepair RunPhase = "protocol_repair"
	RunPhaseToolStarted    RunPhase = "tool_started"
	RunPhaseToolCompleted  RunPhase = "tool_completed"
	RunPhaseCompleted      RunPhase = "completed"
	RunPhaseFailed         RunPhase = "failed"
)

// RunEvent is a privacy-safe lifecycle event emitted by the Agent harness.
// It intentionally exposes input keys and sizes, never raw prompts or tool data.
type RunEvent struct {
	TraceID      string
	Phase        RunPhase
	ModelTurn    int
	ToolCall     int
	MaxToolCalls int
	Tool         string
	InputKeys    []string
	OutputChars  int
	DurationMS   int64
	Error        string
	FinishReason string
	Usage        llm.Usage
}

type RunObserver func(context.Context, RunEvent)

// WithDefaults 补齐 Agent 配置默认值并限制上限。
func (cfg Config) WithDefaults() Config {
	if cfg.WorkDir == "" {
		cfg.WorkDir = "."
	}
	if cfg.MaxSteps <= 0 {
		cfg.MaxSteps = DefaultMaxSteps
	}
	if cfg.MaxSteps > MaxAllowedSteps {
		// Agent 步数设置硬上限，避免模型反复调用工具导致一次 QQ 回复无限拖长。
		cfg.MaxSteps = MaxAllowedSteps
	}
	if cfg.MaxToolOutputChars <= 0 {
		cfg.MaxToolOutputChars = DefaultMaxToolOutputChars
	}
	if cfg.MaxToolOutputChars > MaxAllowedToolOutputChars {
		// 工具输出会回填给模型，过长会撑爆上下文，所以这里做全局上限。
		cfg.MaxToolOutputChars = MaxAllowedToolOutputChars
	}
	if cfg.ReadFileMaxBytes <= 0 {
		cfg.ReadFileMaxBytes = DefaultReadFileMaxBytes
	}
	if cfg.ReadFileMaxBytes > MaxAllowedReadFileMaxBytes {
		// 文件读取限制按字节控制，防止工具误读大文件。
		cfg.ReadFileMaxBytes = MaxAllowedReadFileMaxBytes
	}
	if cfg.ListDirectoryLimit <= 0 {
		cfg.ListDirectoryLimit = DefaultListDirectoryLimit
	}
	if cfg.SkillsListBudget <= 0 {
		cfg.SkillsListBudget = DefaultSkillsListBudget
	}
	if cfg.MCPStartupTimeoutMS <= 0 {
		cfg.MCPStartupTimeoutMS = DefaultMCPStartupTimeoutMS
	}
	if cfg.MCPToolTimeoutMS <= 0 {
		cfg.MCPToolTimeoutMS = DefaultMCPToolTimeoutMS
	}
	if cfg.CommandTimeoutMS <= 0 {
		cfg.CommandTimeoutMS = DefaultCommandTimeoutMS
	}
	if cfg.CommandTimeoutMS > MaxAllowedCommandTimeoutMS {
		cfg.CommandTimeoutMS = MaxAllowedCommandTimeoutMS
	}
	if len(cfg.CommandAllowlist) == 0 {
		cfg.CommandAllowlist = defaultCommandAllowlist()
	}
	cfg.CommandAllowlist = cleanStringList(cfg.CommandAllowlist)
	if cfg.BrowserTimeoutMS <= 0 {
		cfg.BrowserTimeoutMS = DefaultBrowserTimeoutMS
	}
	if cfg.BrowserTimeoutMS > MaxAllowedBrowserTimeoutMS {
		cfg.BrowserTimeoutMS = MaxAllowedBrowserTimeoutMS
	}
	if cfg.ToolTimeoutMS <= 0 {
		cfg.ToolTimeoutMS = DefaultToolTimeoutMS
	}
	if cfg.ToolTimeoutMS > MaxAllowedToolTimeoutMS {
		cfg.ToolTimeoutMS = MaxAllowedToolTimeoutMS
	}
	if cfg.FinalizationReserveMS <= 0 {
		cfg.FinalizationReserveMS = DefaultFinalizationReserveMS
	}
	if cfg.FinalizationReserveMS > MaxAllowedFinalizationReserveMS {
		cfg.FinalizationReserveMS = MaxAllowedFinalizationReserveMS
	}
	if cfg.ProtocolRepairLimit <= 0 {
		cfg.ProtocolRepairLimit = DefaultProtocolRepairLimit
	}
	if cfg.ProtocolRepairLimit > MaxAllowedProtocolRepairLimit {
		cfg.ProtocolRepairLimit = MaxAllowedProtocolRepairLimit
	}
	if strings.TrimSpace(cfg.BrowserCDPURL) == "" {
		cfg.BrowserCDPURL = "http://127.0.0.1:9222"
	}
	cfg.SkillRoots = defaultSkillRoots(cfg.WorkDir, cfg.SkillRoots)
	if strings.TrimSpace(cfg.MCPConfigPath) == "" {
		cfg.MCPConfigPath = defaultMCPConfigPath(cfg.WorkDir)
	}
	return cfg
}

func defaultMCPConfigPath(workDir string) string {
	return filepath.Join(workDir, ".mcp.json")
}

func defaultCommandAllowlist() []string {
	return []string{
		"awk", "cat", "echo", "find", "git", "go", "grep", "ls", "make",
		"node", "npm", "npx", "pip", "pip3", "pwd", "python", "python3",
		"rg", "sed",
	}
}

func cleanStringList(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func defaultSkillRoots(workDir string, configured []string) []string {
	base, err := filepath.Abs(workDir)
	if err != nil {
		base = workDir
	}
	seen := map[string]bool{}
	var roots []string
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(base, path)
		}
		cleaned := filepath.Clean(path)
		if !seen[cleaned] {
			seen[cleaned] = true
			roots = append(roots, cleaned)
		}
	}
	for _, path := range configured {
		add(path)
	}
	add(filepath.Join(base, ".agents", "skills"))
	add(filepath.Join(base, "skills"))
	return roots
}
