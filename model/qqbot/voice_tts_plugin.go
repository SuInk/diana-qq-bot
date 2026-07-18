package qqbot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"diana-qq-bot/model/agent"
)

const (
	voiceTTSPluginID        = "official.voice-tts"
	voiceTTSToolName        = "diana.tts"
	defaultVoiceTTSEndpoint = "http://127.0.0.1:9880/tts"
	defaultVoiceTTSTimeout  = 120 * time.Second
	defaultVoiceTTSMaxChars = 500
	defaultVoiceTTSMaxBytes = 32 << 20
	defaultVoiceTTSMediaTTL = 10 * time.Minute
	defaultVoiceTTSSilkRate = 25000
)

type voiceCommandRunner func(context.Context, string, ...string) ([]byte, error)

type VoiceTTSPlugin struct {
	client        *http.Client
	commandRunner voiceCommandRunner

	mu     sync.RWMutex
	sharer LocalMediaSharer
}

type dianaTTSTool struct {
	plugin *VoiceTTSPlugin
}

type voiceTTSConfig struct {
	Endpoint     string
	APIKey       string
	RefAudioPath string
	PromptText   string
	TextLang     string
	PromptLang   string
	OutputDir    string
	Timeout      time.Duration
	MaxChars     int
	SpeedFactor  float64
	FFmpegPath   string
	SilkEncoder  string
	SilkBitrate  int
}

type voiceTTSResult struct {
	OK       bool   `json:"ok"`
	Action   string `json:"action"`
	CQRecord string `json:"cq_record"`
	Text     string `json:"text"`
}

func NewVoiceTTSPlugin(client *http.Client) *VoiceTTSPlugin {
	if client == nil {
		client = &http.Client{}
	}
	return &VoiceTTSPlugin{
		client: client,
		commandRunner: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, args...).CombinedOutput()
		},
	}
}

func (p *VoiceTTSPlugin) Manifest() PluginManifest {
	voiceName := voiceTTSVoiceName()
	return PluginManifest{
		ID:          voiceTTSPluginID,
		Name:        voiceName + "语音合成",
		Version:     "0.1.0",
		Description: "通过可配置的 GPT-SoVITS 服务把回复合成为" + voiceName + "音色，并以 QQ 语音消息发送。仅由 Agent 在用户明确要求语音时调用。",
		Official:    true,
		BuiltIn:     true,
		Permissions: []string{"agent:tool", "network:http", "file:write", "process:execute", "message:send"},
	}
}

// Handle intentionally performs no text matching. The Agent decides from the
// full conversation whether the user explicitly requested a voice response.
func (p *VoiceTTSPlugin) Handle(context.Context, PluginRequest) (*PluginResponse, error) {
	return nil, nil
}

func (p *VoiceTTSPlugin) AgentTools() []agent.Tool {
	return []agent.Tool{&dianaTTSTool{plugin: p}}
}

func (p *VoiceTTSPlugin) SetLocalMediaSharer(sharer LocalMediaSharer) {
	p.mu.Lock()
	p.sharer = sharer
	p.mu.Unlock()
}

func (p *VoiceTTSPlugin) share(path string) (string, bool) {
	p.mu.RLock()
	sharer := p.sharer
	p.mu.RUnlock()
	if sharer == nil {
		return "", false
	}
	return sharer.Share(path, defaultVoiceTTSMediaTTL)
}

func (t *dianaTTSTool) Name() string {
	return voiceTTSToolName
}

func (t *dianaTTSTool) Description() string {
	return fmt.Sprintf(`将最终回复合成为%s音色并直接发送一条 QQ 语音。仅当用户明确要求用语音回复、要求朗读/念出内容，或明确要求把某段文字说出来时调用；普通文字聊天、仅讨论声音/TTS/语音功能时严禁调用。调用后工具会直接完成本次回复，不要再发送重复文字。input: {"text":"实际要说的完整自然语言内容，不含 Markdown、CQ 码或工具说明"}`, voiceTTSVoiceName())
}

func (t *dianaTTSTool) Run(ctx context.Context, input map[string]any) (string, error) {
	if t == nil || t.plugin == nil {
		return "", fmt.Errorf("语音合成插件未配置")
	}
	cfg := voiceTTSConfigFromEnv()
	text := sanitizeVoiceTTSText(configToolString(input, "text"), cfg.MaxChars)
	if text == "" {
		return "", fmt.Errorf("text 不能为空")
	}

	path, err := t.plugin.synthesize(ctx, cfg, text)
	if err != nil {
		return "", err
	}
	sharedURL, ok := t.plugin.share(path)
	if !ok {
		_ = os.Remove(path)
		return "", fmt.Errorf("本地媒体共享未配置，无法把语音交给 NapCat")
	}
	cleanupLocalMediaFilesLater([]string{path}, defaultVoiceTTSMediaTTL)

	result := voiceTTSResult{
		OK:       true,
		Action:   "voice_ready",
		CQRecord: "[CQ:record,file=" + escapeCQParameter(sharedURL) + "]",
		Text:     text,
	}
	body, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func (t *dianaTTSTool) TerminalResult(output string) (string, bool) {
	var result voiceTTSResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return "", false
	}
	if !result.OK || strings.TrimSpace(result.CQRecord) == "" {
		return "", false
	}
	return result.CQRecord, true
}

func (p *VoiceTTSPlugin) synthesize(ctx context.Context, cfg voiceTTSConfig, text string) (string, error) {
	endpoint, err := url.Parse(cfg.Endpoint)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return "", fmt.Errorf("DIANA_TTS_ENDPOINT 无效")
	}
	payload := map[string]any{
		"text":               text,
		"text_lang":          cfg.TextLang,
		"ref_audio_path":     cfg.RefAudioPath,
		"prompt_text":        cfg.PromptText,
		"prompt_lang":        cfg.PromptLang,
		"text_split_method":  "cut5",
		"batch_size":         1,
		"split_bucket":       true,
		"speed_factor":       cfg.SpeedFactor,
		"fragment_interval":  0.3,
		"media_type":         "wav",
		"streaming_mode":     false,
		"parallel_infer":     true,
		"repetition_penalty": 1.35,
		"seed":               -1,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	requestCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("语音合成请求失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("语音合成服务返回 %s: %s", resp.Status, strings.TrimSpace(string(detail)))
	}

	audio, err := io.ReadAll(io.LimitReader(resp.Body, defaultVoiceTTSMaxBytes+1))
	if err != nil {
		return "", fmt.Errorf("读取语音合成结果失败: %w", err)
	}
	if len(audio) > defaultVoiceTTSMaxBytes {
		return "", fmt.Errorf("语音合成结果超过 %d MB", defaultVoiceTTSMaxBytes>>20)
	}
	if !looksLikeWAV(audio) {
		return "", fmt.Errorf("语音合成服务未返回有效 WAV 音频")
	}
	if err := os.MkdirAll(cfg.OutputDir, 0o700); err != nil {
		return "", fmt.Errorf("创建语音缓存目录失败: %w", err)
	}
	file, err := os.CreateTemp(cfg.OutputDir, "diana-tts-*.wav")
	if err != nil {
		return "", fmt.Errorf("创建语音缓存文件失败: %w", err)
	}
	path := file.Name()
	if _, writeErr := file.Write(audio); writeErr != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("写入语音缓存失败: %w", writeErr)
	}
	if closeErr := file.Close(); closeErr != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("保存语音缓存失败: %w", closeErr)
	}
	if cfg.SilkEncoder == "" {
		return path, nil
	}
	silkPath, err := p.encodeTencentSilk(ctx, cfg, path)
	if err != nil {
		_ = os.Remove(path)
		return "", err
	}
	_ = os.Remove(path)
	return silkPath, nil
}

func (p *VoiceTTSPlugin) encodeTencentSilk(ctx context.Context, cfg voiceTTSConfig, wavPath string) (string, error) {
	pcmFile, err := os.CreateTemp(cfg.OutputDir, "diana-tts-*.pcm")
	if err != nil {
		return "", fmt.Errorf("创建 PCM 缓存文件失败: %w", err)
	}
	pcmPath := pcmFile.Name()
	if closeErr := pcmFile.Close(); closeErr != nil {
		_ = os.Remove(pcmPath)
		return "", fmt.Errorf("创建 PCM 缓存文件失败: %w", closeErr)
	}
	defer os.Remove(pcmPath)

	silkFile, err := os.CreateTemp(cfg.OutputDir, "diana-tts-*.silk")
	if err != nil {
		return "", fmt.Errorf("创建 Silk 缓存文件失败: %w", err)
	}
	silkPath := silkFile.Name()
	if closeErr := silkFile.Close(); closeErr != nil {
		_ = os.Remove(silkPath)
		return "", fmt.Errorf("创建 Silk 缓存文件失败: %w", closeErr)
	}
	removeSilk := true
	defer func() {
		if removeSilk {
			_ = os.Remove(silkPath)
		}
	}()

	conversionCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	output, err := p.runVoiceCommand(conversionCtx, cfg.FFmpegPath,
		"-hide_banner", "-loglevel", "error", "-y", "-i", wavPath,
		"-ar", "24000", "-ac", "1", "-f", "s16le", pcmPath,
	)
	if err != nil {
		return "", voiceCommandError("系统 ffmpeg 转换 PCM", output, err)
	}
	output, err = p.runVoiceCommand(conversionCtx, cfg.SilkEncoder,
		"-i", pcmPath,
		"-o", silkPath,
		"-Fs_API", "24000",
		"-Fs_maxInternal", "24000",
		"-packetlength", "20",
		"-rate", strconv.Itoa(cfg.SilkBitrate),
		"-complexity", "2",
		"-STX=true",
	)
	if err != nil {
		return "", voiceCommandError("QQ Silk 编码", output, err)
	}
	header, err := readFilePrefix(silkPath, 16)
	if err != nil {
		return "", fmt.Errorf("读取 QQ Silk 结果失败: %w", err)
	}
	if !looksLikeTencentSilk(header) {
		return "", fmt.Errorf("QQ Silk 编码器未返回有效 Tencent Silk 音频")
	}
	removeSilk = false
	return silkPath, nil
}

func (p *VoiceTTSPlugin) runVoiceCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	if p.commandRunner == nil {
		return exec.CommandContext(ctx, name, args...).CombinedOutput()
	}
	return p.commandRunner(ctx, name, args...)
}

func voiceCommandError(action string, output []byte, err error) error {
	detail := strings.Join(strings.Fields(string(output)), " ")
	if runes := []rune(detail); len(runes) > 400 {
		detail = string(runes[:400])
	}
	if detail == "" {
		return fmt.Errorf("%s失败: %w", action, err)
	}
	return fmt.Errorf("%s失败: %w (%s)", action, err, detail)
}

func readFilePrefix(path string, maxBytes int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, maxBytes))
}

func voiceTTSConfigFromEnv() voiceTTSConfig {
	timeoutSeconds := envInt("DIANA_TTS_TIMEOUT_SECONDS", int(defaultVoiceTTSTimeout/time.Second))
	if timeoutSeconds < 1 {
		timeoutSeconds = 1
	}
	maxChars := envInt("DIANA_TTS_MAX_CHARS", defaultVoiceTTSMaxChars)
	if maxChars < 1 {
		maxChars = defaultVoiceTTSMaxChars
	}
	speedFactor := 1.0
	if parsed, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv("DIANA_TTS_SPEED_FACTOR")), 64); err == nil && parsed >= 0.5 && parsed <= 2 {
		speedFactor = parsed
	}
	return voiceTTSConfig{
		Endpoint:     firstNonEmpty(strings.TrimSpace(os.Getenv("DIANA_TTS_ENDPOINT")), defaultVoiceTTSEndpoint),
		APIKey:       strings.TrimSpace(os.Getenv("DIANA_TTS_API_KEY")),
		RefAudioPath: strings.TrimSpace(os.Getenv("DIANA_TTS_REF_AUDIO_PATH")),
		PromptText:   strings.TrimSpace(os.Getenv("DIANA_TTS_PROMPT_TEXT")),
		TextLang:     firstNonEmpty(strings.TrimSpace(os.Getenv("DIANA_TTS_TEXT_LANG")), "zh"),
		PromptLang:   firstNonEmpty(strings.TrimSpace(os.Getenv("DIANA_TTS_PROMPT_LANG")), "zh"),
		OutputDir:    voiceTTSOutputDir(),
		Timeout:      time.Duration(timeoutSeconds) * time.Second,
		MaxChars:     maxChars,
		SpeedFactor:  speedFactor,
		FFmpegPath:   firstNonEmpty(strings.TrimSpace(os.Getenv("DIANA_TTS_FFMPEG_PATH")), "ffmpeg"),
		SilkEncoder:  strings.TrimSpace(os.Getenv("DIANA_TTS_SILK_ENCODER_PATH")),
		SilkBitrate:  voiceTTSSilkBitrate(),
	}
}

func voiceTTSVoiceName() string {
	return firstNonEmpty(strings.TrimSpace(os.Getenv("DIANA_TTS_VOICE_NAME")), "自定义")
}

func voiceTTSSilkBitrate() int {
	bitrate := envInt("DIANA_TTS_SILK_BITRATE", defaultVoiceTTSSilkRate)
	if bitrate < 5000 || bitrate > 100000 {
		return defaultVoiceTTSSilkRate
	}
	return bitrate
}

func voiceTTSOutputDir() string {
	if configured := strings.TrimSpace(os.Getenv("DIANA_TTS_OUTPUT_DIR")); configured != "" {
		return configured
	}
	if dbPath := strings.TrimSpace(os.Getenv("APP_DB_PATH")); dbPath != "" {
		if absolute, err := filepath.Abs(dbPath); err == nil {
			return filepath.Join(filepath.Dir(absolute), "tts-cache")
		}
	}
	if cacheDir, err := os.UserCacheDir(); err == nil && cacheDir != "" {
		return filepath.Join(cacheDir, "diana-qq-bot", "tts-cache")
	}
	return filepath.Join(os.TempDir(), "diana-qq-bot-tts")
}

func sanitizeVoiceTTSText(text string, maxRunes int) string {
	text = strings.TrimSpace(text)
	if strings.Contains(text, "[CQ:") {
		text = PlainText(CQToSegments(text))
	}
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if maxRunes > 0 && len(runes) > maxRunes {
		runes = runes[:maxRunes]
	}
	return strings.TrimSpace(string(runes))
}

func looksLikeWAV(audio []byte) bool {
	return len(audio) >= 12 && string(audio[:4]) == "RIFF" && string(audio[8:12]) == "WAVE"
}

func looksLikeTencentSilk(audio []byte) bool {
	if bytes.HasPrefix(audio, []byte("#!SILK")) {
		return true
	}
	return len(audio) > 1 && audio[0] == 0x02 && bytes.HasPrefix(audio[1:], []byte("#!SILK"))
}

func escapeCQParameter(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "[", "&#91;", "]", "&#93;", ",", "&#44;")
	return replacer.Replace(value)
}
