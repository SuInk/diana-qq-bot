package main

import (
	"context"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"diana-qq-bot/model/agent"
	"diana-qq-bot/model/llm"
	"diana-qq-bot/model/qqbot"
	"diana-qq-bot/model/storage"
	"diana-qq-bot/model/updater"
	"diana-qq-bot/webui"

	"github.com/gin-gonic/gin"
)

var (
	buildSourceRoot string
	buildCommit     string
)

// main 初始化存储、路由、机器人运行时并启动 WebUI 服务。
func main() {
	logWriter, closeLog := setupLogging()
	defer closeLog()
	probeMacOSQQAppDataAccess()

	// 所有后台 goroutine 共用这个根 context，收到 Ctrl+C 或 SIGTERM 时统一退出。
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	cfg := llmConfigFromEnv()

	// SQLite 同时保存 LLM 配置集、QQ 机器人配置、插件状态、提醒和操作日志。
	appDBPath := envOr("APP_DB_PATH", "data/diana-qq-bot.db")
	sqliteStore, err := storage.NewSQLiteStore(appDBPath)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		_ = sqliteStore.Close()
	}()

	store, err := webui.NewPersistentLLMProfileStore(ctx, sqliteStore, cfg)
	if err != nil {
		log.Fatal(err)
	}
	botProfileStore, err := webui.NewPersistentQQBotProfileStore(ctx, sqliteStore, qqBotConfigFromEnv())
	if err != nil {
		log.Fatal(err)
	}
	botGroupConfigStore, err := webui.NewPersistentQQBotGroupConfigStore(ctx, sqliteStore)
	if err != nil {
		log.Fatal(err)
	}
	reminderStore, err := webui.NewPersistentReminderStore(ctx, sqliteStore)
	if err != nil {
		log.Fatal(err)
	}
	// 模型列表必须从当前 provider 后端读取；缺失的上下文窗口再由
	// Models.dev 补齐，QQ 聊天技能和 WebUI 共用同一套结果。
	modelCatalog := llm.NewModelsDevCatalog(nil)
	modelListFactory := func(ctx context.Context, cfg llm.ProviderConfig) ([]llm.ModelInfo, error) {
		models, err := llm.ListModels(ctx, cfg)
		if err != nil {
			return nil, err
		}
		return modelCatalog.Enrich(ctx, cfg, models), nil
	}
	handler := webui.NewLLMConfigHandler(store)
	handler.SetModelListFactory(modelListFactory)
	handler.SetLogStore(sqliteStore)
	systemUpdater, err := newSystemUpdater()
	if err != nil {
		log.Fatal(err)
	}
	systemHandler := webui.NewSystemUpdateHandler(systemUpdater)
	systemHandler.SetLogStore(sqliteStore)
	runtimePersistor := webui.NewRuntimePersistor(botProfileStore)
	botCfg := botProfileStore.Current()
	webSearchHandler := webui.NewWebSearchConfigHandler(agent.ResolveWebSearchConfigPath(botCfg.AgentWorkDir))
	webSearchHandler.SetLogStore(sqliteStore)
	plugins := qqbot.NewDefaultPluginManager()
	if savedPluginStates, ok, err := sqliteStore.LoadPluginStates(ctx); err != nil {
		log.Fatal(err)
	} else if ok {
		plugins.Restore(savedPluginStates)
	}
	port := envOr("PORT", "18080")
	host := envOrAny([]string{"HOST", "BACKEND_HOST"}, "127.0.0.1")
	localMediaBaseURL := envOr(
		"DIANA_LOCAL_MEDIA_BASE_URL",
		"http://"+net.JoinHostPort(displayHost(host), port)+"/api/qqbot/media",
	)
	localMediaStore := qqbot.NewLocalMediaStore(localMediaBaseURL)
	// NapCat 使用反向 WebSocket 连接本服务；这里保留同一个 server 实例，配置变更时只更新 token/endpoint。
	oneBotServer := qqbot.NewOneBotReverseServer(qqbot.OneBotConfig{
		Endpoint:    botCfg.OneBotReverseWSEndpoint,
		AccessToken: botCfg.OneBotAccessToken,
	})
	botRuntime := qqbot.NewRuntime(botCfg, oneBotServer, plugins, store, reminderStore, runtimePersistor, func() (qqbot.LLMProvider, error) {
		return llm.NewClient(store.Current())
	})
	botRuntime.SetLLMProviderConfigFactory(func(cfg llm.ProviderConfig) (qqbot.LLMProvider, error) {
		return llm.NewClient(cfg)
	})
	botRuntime.SetGroupConfigStore(botGroupConfigStore)
	botRuntime.SetMessageHistoryStore(sqliteStore)
	botRuntime.SetInboundEventStore(sqliteStore)
	botRuntime.SetUserMemoryStore(sqliteStore)
	botRuntime.SetStructuredMemoryStore(sqliteStore)
	if err := botRuntime.SetReplySuppressionStore(ctx, sqliteStore); err != nil {
		log.Printf("qqbot reply suppression load failed: %v", err)
	}
	botRuntime.SetLLMModelLister(modelListFactory)
	botRuntime.SetAppLogWriter(sqliteStore)
	botRuntime.SetLocalMediaSharer(localMediaStore)
	if botCfg.Enabled {
		if err := botRuntime.Start(ctx); err != nil {
			log.Printf("qqbot start skipped: %v", err)
		}
	}
	botHandler := webui.NewQQBotHandlerWithFactory(ctx, botRuntime, func(cfg qqbot.BotConfig) qqbot.Channel {
		oneBotServer.SetConfig(qqbot.OneBotConfig{
			Endpoint:    cfg.OneBotReverseWSEndpoint,
			AccessToken: cfg.OneBotAccessToken,
		})
		return oneBotServer
	})
	botHandler.SetFeatureFlags(webui.QQBotFeatureFlags{
		GroupTest: boolFromEnv("QQBOT_GROUP_TEST_ENABLED", false),
	})
	botHandler.SetLocalMediaSharer(localMediaStore)
	botHandler.SetProfileStore(botProfileStore)
	botHandler.SetGroupConfigStore(botGroupConfigStore)
	botHandler.SetSQLiteStore(sqliteStore)
	logHandler := webui.NewAppLogHandler(sqliteStore)
	napCatLoginHandler, err := webui.NewNapCatLoginHandler(webui.NapCatLoginConfig{
		BaseURL: os.Getenv("DIANA_NAPCAT_WEBUI_URL"),
		Token:   os.Getenv("DIANA_NAPCAT_WEBUI_TOKEN"),
	})
	if err != nil {
		log.Fatal(err)
	}

	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.LoggerWithWriter(logWriter), gin.RecoveryWithWriter(logWriter))
	if err := router.SetTrustedProxies(nil); err != nil {
		log.Fatal(err)
	}
	adminAccess, err := webui.NewAdminAccess(webui.AdminAccessConfig{
		Token:           envOrAny([]string{"DIANA_ADMIN_TOKEN", "ADMIN_TOKEN"}, ""),
		Username:        envOrAny([]string{"DIANA_ADMIN_EMAIL", "DIANA_ADMIN_USERNAME"}, ""),
		LoginPath:       os.Getenv("DIANA_ADMIN_LOGIN_PATH"),
		SettingsPath:    envOr("DIANA_ADMIN_AUTH_CONFIG_FILE", filepath.Join(filepath.Dir(appDBPath), "admin-auth.json")),
		CredentialsPath: envOr("DIANA_ADMIN_CREDENTIALS_FILE", filepath.Join(filepath.Dir(appDBPath), "admin-credentials.json")),
	})
	if err != nil {
		log.Fatal(err)
	}
	if adminAccess.Enabled() {
		if adminAccess.Username() == "" {
			log.Printf("admin authentication enabled; first-run setup required at: %s", adminAccess.LoginPath())
		} else {
			log.Printf("admin authentication enabled; login path: %s", adminAccess.LoginPath())
		}
	} else {
		log.Printf("admin authentication disabled; set DIANA_ADMIN_TOKEN to protect the WebUI")
	}
	router.Use(adminAccess.Middleware())
	adminAccess.Register(router)
	handler.Register(router)
	webSearchHandler.Register(router)
	systemHandler.Register(router)
	botHandler.Register(router)
	napCatLoginHandler.Register(router)
	logHandler.Register(router)
	router.GET("/api/qqbot/media/:token", func(c *gin.Context) {
		localMediaStore.ServeToken(c.Writer, c.Request, c.Param("token"))
	})
	// OneBot 路由必须在 SPA fallback 之前注册，否则 NapCat 会拿到前端 HTML 而不是 WebSocket。
	router.GET("/onebot/v11/ws", gin.WrapH(oneBotServer))
	router.NoRoute(spaHandler(http.Dir(frontendDistDir())))

	addr := net.JoinHostPort(host, port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("webui listening on http://%s:%s", displayHost(host), port)
	if err := router.RunListener(listener); err != nil {
		log.Fatal(err)
	}
}

func newSystemUpdater() (*updater.GitUpdater, error) {
	root := strings.TrimSpace(os.Getenv("DIANA_UPDATE_ROOT"))
	if root == "" {
		root = strings.TrimSpace(buildSourceRoot)
	}
	if root == "" {
		root = "."
	}

	runningExecutable, _ := os.Executable()
	options := updater.Options{
		RunningCommit:     buildCommit,
		RunningExecutable: runningExecutable,
	}
	applyScript := filepath.Join(root, "scripts", "apply-update.sh")
	if runtime.GOOS != "windows" && boolFromEnv("DIANA_UPDATE_APPLY_ENABLED", true) {
		if info, statErr := os.Stat(applyScript); statErr == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			options.ApplyCommand = []string{applyScript}
		}
	}
	return updater.NewGitUpdaterWithOptions(root, options)
}

func probeMacOSQQAppDataAccess() {
	if runtime.GOOS != "darwin" {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		log.Printf("macOS QQ app data access probe skipped: %v", err)
		return
	}
	path := filepath.Join(home, "Library", "Containers", "com.tencent.qq", "Data", ".config", "QQ", "NapCat", "temp")
	if _, err := os.ReadDir(path); err != nil {
		log.Printf("macOS QQ app data access denied: %v", err)
		return
	}
	log.Printf("macOS QQ app data access granted")
}

func displayHost(host string) string {
	host = strings.TrimSpace(host)
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		return "127.0.0.1"
	default:
		return host
	}
}

// setupLogging 配置控制台和文件日志输出。
func setupLogging() (io.Writer, func()) {
	logPath := envOrAny([]string{"LOG_PATH", "DIANA_LOG_PATH"}, "")
	if logPath == "" {
		return os.Stdout, func() {}
	}
	// Gin 请求日志和标准 log 共用同一个 writer，方便部署时只收集一个文件。
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		log.Printf("create log directory skipped: %v", err)
		return os.Stdout, func() {}
	}
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Printf("open log file skipped: %v", err)
		return os.Stdout, func() {}
	}
	writer := io.MultiWriter(os.Stdout, file)
	log.SetOutput(writer)
	log.Printf("logging to %s", logPath)
	return writer, func() {
		_ = file.Close()
	}
}

// qqBotConfigFromEnv 从环境变量构建 QQ 机器人默认配置。
func qqBotConfigFromEnv() qqbot.BotConfig {
	cfg := qqbot.DefaultBotConfig()
	// 默认回连到当前 WebUI 端口，开发环境只要 NapCat 指向这个地址即可联调。
	defaultOneBotEndpoint := "ws://127.0.0.1:" + envOr("PORT", "18080") + "/onebot/v11/ws"
	cfg.Enabled = boolFromEnv("QQBOT_ENABLED", cfg.Enabled)
	cfg.OneBotReverseWSEndpoint = envOrAny([]string{"ONEBOT_REVERSE_WS_ENDPOINT", "QQBOT_ONEBOT_REVERSE_WS_ENDPOINT"}, defaultOneBotEndpoint)
	cfg.OneBotAccessToken = envOrAny([]string{"ONEBOT_ACCESS_TOKEN", "QQBOT_ONEBOT_ACCESS_TOKEN"}, "")
	cfg.NoneBotBridgeEnabled = boolFromEnv("NONEBOT_BRIDGE_ENABLED", cfg.NoneBotBridgeEnabled)
	cfg.NoneBotBridgeEndpoint = envOrAny([]string{"NONEBOT_BRIDGE_ENDPOINT", "QQBOT_NONEBOT_BRIDGE_ENDPOINT"}, cfg.NoneBotBridgeEndpoint)
	cfg.NoneBotBridgeToken = envOrAny([]string{"NONEBOT_BRIDGE_TOKEN", "QQBOT_NONEBOT_BRIDGE_TOKEN"}, "")
	cfg.BotQQ = envOrAny([]string{"QQBOT_SELF_ID", "QQBOT_QQ", "BOT_QQ"}, "")
	cfg.OwnerID = envOrAny([]string{"DIANA_OWNER_ID", "QQBOT_OWNER_ID"}, "")
	cfg.GroupTriggers = stringListFromEnv("DIANA_GROUP_TRIGGERS", cfg.GroupTriggers)
	cfg.SystemPrompt = envOrAny([]string{"DIANA_SYSTEM_PROMPT", "QQBOT_SYSTEM_PROMPT"}, cfg.SystemPrompt)
	cfg.PassiveReplyRouterPrompt = envOr("DIANA_PASSIVE_REPLY_ROUTER_PROMPT", cfg.PassiveReplyRouterPrompt)
	cfg.PassiveReplyPrompt = envOr("DIANA_PASSIVE_REPLY_PROMPT", cfg.PassiveReplyPrompt)
	cfg.MaxInputChars = intFromEnv("DIANA_MAX_INPUT_CHARS", cfg.MaxInputChars)
	cfg.MaxReplyChars = intFromEnv("DIANA_MAX_REPLY_CHARS", cfg.MaxReplyChars)
	cfg.DirectReplyChunkSize = intFromEnv("DIANA_DIRECT_REPLY_CHUNK_SIZE", cfg.DirectReplyChunkSize)
	cfg.ForwardReplyThreshold = intFromEnv("DIANA_FORWARD_REPLY_THRESHOLD", cfg.ForwardReplyThreshold)
	cfg.RecallReplyMode = qqbot.RecallReplyMode(envOr("DIANA_RECALL_REPLY_MODE", string(cfg.RecallReplyMode)))
	llmQQIDMaskingEnabled := boolFromEnv("DIANA_LLM_QQ_ID_MASKING_ENABLED", true)
	cfg.LLMQQIDMaskingEnabled = &llmQQIDMaskingEnabled
	cfg.RecentContextLimit = intFromEnv("DIANA_RECENT_GROUP_CONTEXT_LIMIT", cfg.RecentContextLimit)
	cfg.ContextSummaryThreshold = intFromEnv("DIANA_CONTEXT_SUMMARY_THRESHOLD", cfg.ContextSummaryThreshold)
	if chance, ok := floatFromEnv("DIANA_PASSIVE_REPLY_CHANCE"); ok {
		cfg.PassiveReplyChance = chance
	}
	if threshold, ok := floatFromEnv("DIANA_PASSIVE_REPLY_THRESHOLD"); ok {
		cfg.PassiveReplyThreshold = threshold
	}
	cfg.MaxBotConcurrency = intFromEnv("DIANA_MAX_BOT_CONCURRENCY", cfg.MaxBotConcurrency)
	cfg.RequestTimeout = time.Duration(int64FromEnv("DIANA_HTTP_TIMEOUT_SECONDS", int64(cfg.RequestTimeout.Seconds()))) * time.Second
	cfg.AgentEnabled = boolFromEnv("DIANA_AGENT_ENABLED", cfg.AgentEnabled)
	cfg.AgentWorkDir = envOrAny([]string{"DIANA_AGENT_WORK_DIR", "AGENT_WORK_DIR"}, cfg.AgentWorkDir)
	cfg.AgentMaxSteps = intFromEnv("DIANA_AGENT_MAX_STEPS", cfg.AgentMaxSteps)
	cfg.AgentSkillRoots = stringListFromEnv("DIANA_AGENT_SKILL_ROOTS", cfg.AgentSkillRoots)
	cfg.AgentMCPConfigPath = envOrAny([]string{"DIANA_AGENT_MCP_CONFIG", "AGENT_MCP_CONFIG"}, cfg.AgentMCPConfigPath)
	cfg.AgentCommandAllowlist = stringListFromEnv("DIANA_AGENT_COMMAND_ALLOWLIST", cfg.AgentCommandAllowlist)
	cfg.AgentCommandTimeoutMS = intFromEnv("DIANA_AGENT_COMMAND_TIMEOUT_MS", cfg.AgentCommandTimeoutMS)
	cfg.AgentBrowserCDPURL = envOrAny([]string{"DIANA_AGENT_BROWSER_CDP_URL", "AGENT_BROWSER_CDP_URL"}, cfg.AgentBrowserCDPURL)
	cfg.AgentBrowserTimeoutMS = intFromEnv("DIANA_AGENT_BROWSER_TIMEOUT_MS", cfg.AgentBrowserTimeoutMS)
	return cfg.WithDefaults()
}

// llmConfigFromEnv 从环境变量构建默认 LLM provider 配置。
func llmConfigFromEnv() llm.ProviderConfig {
	provider := providerFromEnv("LLM_PROVIDER", llm.ProviderOpenAICompatible)
	cfg := llm.ProviderConfig{
		Provider:            provider,
		APIKey:              os.Getenv("LLM_API_KEY"),
		BaseURL:             os.Getenv("LLM_BASE_URL"),
		APIFormat:           llm.APIFormat(os.Getenv("LLM_API_FORMAT")),
		Model:               envOr("LLM_MODEL", llm.DefaultModel(provider)),
		ImageModel:          os.Getenv("LLM_IMAGE_MODEL"),
		ImageBaseURL:        os.Getenv("LLM_IMAGE_BASE_URL"),
		ImageOrigin:         os.Getenv("LLM_IMAGE_ORIGIN"),
		ImageTimeout:        time.Duration(int64FromEnv("LLM_IMAGE_TIMEOUT_MS", 0)) * time.Millisecond,
		UserAgent:           os.Getenv("LLM_USER_AGENT"),
		ReasoningEffort:     os.Getenv("LLM_REASONING_EFFORT"),
		ContextWindowTokens: int64FromEnv("LLM_CONTEXT_WINDOW_TOKENS", llm.DefaultContextWindowTokens),
		MaxContextTokens:    int64FromEnv("LLM_MAX_CONTEXT_TOKENS", llm.DefaultMaxContextTokens),
		MaxOutputTokens:     int64FromEnv("LLM_MAX_OUTPUT_TOKENS", 1024),
		Timeout:             time.Duration(int64FromEnv("LLM_TIMEOUT_MS", 60000)) * time.Millisecond,
	}
	if temp, ok := floatFromEnv("LLM_TEMPERATURE"); ok {
		cfg.Temperature = &temp
	}
	return cfg.WithDefaults()
}

// frontendDistDir 查找生产前端静态文件目录。
func frontendDistDir() string {
	// 同时兼容源码目录运行、打包后从二进制旁边运行、以及测试工作目录切换。
	candidates := []string{
		envOr("FRONTEND_DIST", "frontend/dist"),
		"frontend/dist",
		"../../frontend/dist",
	}
	for _, candidate := range candidates {
		if stat, err := os.Stat(candidate); err == nil && stat.IsDir() {
			return candidate
		}
	}
	return candidates[0]
}

// spaHandler 返回前端单页应用的兜底路由处理器。
func spaHandler(root http.FileSystem) gin.HandlerFunc {
	return func(c *gin.Context) {
		// API 未命中时返回 JSON 404，避免前端路由兜底掩盖接口拼写错误。
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}

		path := strings.TrimPrefix(c.Request.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}

		file, err := root.Open(path)
		if err != nil {
			serveFile(c, root, "index.html")
			return
		}
		_ = file.Close()
		serveFile(c, root, path)
	}
}

// serveFile 按静态文件路径写出响应内容。
func serveFile(c *gin.Context, root http.FileSystem, path string) {
	file, err := root.Open(path)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	defer file.Close()

	contentType := mime.TypeByExtension(filepath.Ext(path))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	c.Header("Content-Type", contentType)
	c.Status(http.StatusOK)
	if _, err := io.Copy(c.Writer, file); err != nil {
		log.Printf("serve %s: %v", path, err)
	}
}

// envOr 读取环境变量，空值时返回默认值。
func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

// envOrAny 按顺序读取多个环境变量并返回第一个非空值。
func envOrAny(keys []string, fallback string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return fallback
}

// boolFromEnv 将环境变量解析为布尔值。
func boolFromEnv(key string, fallback bool) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	switch value {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

// intFromEnv 将环境变量解析为 int。
func intFromEnv(key string, fallback int) int {
	parsed := int64FromEnv(key, int64(fallback))
	if parsed > int64(^uint(0)>>1) {
		return fallback
	}
	return int(parsed)
}

// stringListFromEnv 将逗号分隔的环境变量解析为字符串列表。
func stringListFromEnv(key string, fallback []string) []string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	if len(out) == 0 {
		return fallback
	}
	return out
}

// providerFromEnv 将环境变量解析为受支持的 LLM provider。
func providerFromEnv(key string, fallback llm.Provider) llm.Provider {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case string(llm.ProviderGemini), "google", "google_genai":
		return llm.ProviderGemini
	case string(llm.ProviderAnthropic), "claude":
		return llm.ProviderAnthropic
	case string(llm.ProviderOpenAICompatible), "openai", "openai-compatible":
		return llm.ProviderOpenAICompatible
	default:
		return fallback
	}
}

// int64FromEnv 将环境变量解析为 int64。
func int64FromEnv(key string, fallback int64) int64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

// floatFromEnv 将环境变量解析为 float64，并返回是否解析成功。
func floatFromEnv(key string) (float64, bool) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return 0, false
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, false
	}
	return parsed, true
}
