package webui

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
)

// AdminAccessConfig configures the mutable WebUI entry path around AdminAuth.
type AdminAccessConfig struct {
	Token           string
	Username        string
	LoginPath       string
	SessionTTL      time.Duration
	SessionsPath    string
	SettingsPath    string
	CredentialsPath string
}

// AdminAccessSettings is the authenticated WebUI representation of the entry settings.
type AdminAccessSettings struct {
	Configured           bool   `json:"configured"`
	Username             string `json:"username"`
	RandomSuffixEnabled  bool   `json:"random_suffix_enabled"`
	LoginPath            string `json:"login_path"`
	ManagedByEnvironment bool   `json:"managed_by_environment"`
}

type persistedAdminAccessSettings struct {
	RandomSuffixEnabled bool   `json:"random_suffix_enabled"`
	LoginPath           string `json:"login_path,omitempty"`
}

type adminAccessSettingsPayload struct {
	RandomSuffixEnabled bool `json:"random_suffix_enabled"`
	Regenerate          bool `json:"regenerate,omitempty"`
}

// AdminAccess keeps the fixed login path as the default and can atomically
// switch to a persisted random suffix without restarting the server.
type AdminAccess struct {
	auth atomic.Pointer[AdminAuth]

	settingsMu      sync.Mutex
	token           string
	username        string
	sessionTTL      time.Duration
	sessionsPath    string
	settingsPath    string
	credentialsPath string
	settings        AdminAccessSettings
}

func NewAdminAccess(cfg AdminAccessConfig) (*AdminAccess, error) {
	access := &AdminAccess{
		token:           strings.TrimSpace(cfg.Token),
		username:        strings.TrimSpace(cfg.Username),
		sessionTTL:      cfg.SessionTTL,
		sessionsPath:    strings.TrimSpace(cfg.SessionsPath),
		settingsPath:    strings.TrimSpace(cfg.SettingsPath),
		credentialsPath: strings.TrimSpace(cfg.CredentialsPath),
	}
	if access.sessionsPath == "" {
		sourcePath := access.credentialsPath
		if sourcePath == "" {
			sourcePath = access.settingsPath
		}
		if sourcePath != "" {
			access.sessionsPath = filepath.Join(filepath.Dir(sourcePath), "admin-sessions.json")
		}
	}
	settings := AdminAccessSettings{
		LoginPath: defaultAdminLoginPath,
	}

	environmentPath := strings.TrimSpace(cfg.LoginPath)
	generated := false
	if environmentPath != "" {
		path, err := normalizeAdminAccessPath(environmentPath, access.token)
		if err != nil {
			return nil, err
		}
		settings.LoginPath = path
		settings.RandomSuffixEnabled = path != defaultAdminLoginPath
		settings.ManagedByEnvironment = true
	} else if persisted, ok, err := loadAdminAccessSettings(access.settingsPath); err != nil {
		return nil, err
	} else if ok && persisted.RandomSuffixEnabled {
		settings.RandomSuffixEnabled = true
		if strings.TrimSpace(persisted.LoginPath) == "" {
			settings.LoginPath, err = generateAdminAccessPath(access.token)
			if err != nil {
				return nil, err
			}
			generated = true
		} else {
			settings.LoginPath, err = normalizeAdminAccessPath(persisted.LoginPath, access.token)
			if err != nil {
				return nil, fmt.Errorf("load admin access settings: %w", err)
			}
			if settings.LoginPath == defaultAdminLoginPath {
				return nil, fmt.Errorf("load admin access settings: random suffix path cannot be the default login path")
			}
		}
	}

	auth, err := newAdminAuthAtPath(access.token, access.username, settings.LoginPath, access.sessionTTL, access.sessionsPath, access.credentialsPath)
	if err != nil {
		return nil, err
	}
	access.sessionTTL = auth.sessionTTL
	settings.Configured = auth.AccountConfigured()
	settings.Username = auth.Username()
	access.settings = settings
	access.auth.Store(auth)
	if generated {
		if err := persistAdminAccessSettings(access.settingsPath, persistedAdminAccessSettings{
			RandomSuffixEnabled: true,
			LoginPath:           settings.LoginPath,
		}); err != nil {
			return nil, err
		}
	}
	return access, nil
}

func newAdminAuthAtPath(token, username, path string, sessionTTL time.Duration, sessionsPath, credentialsPath string) (*AdminAuth, error) {
	path, err := normalizeAdminAccessPath(path, token)
	if err != nil {
		return nil, err
	}
	auth, err := NewAdminAuth(AdminAuthConfig{
		Token:            token,
		Username:         username,
		LoginPath:        path,
		SessionTTL:       sessionTTL,
		SessionStorePath: sessionsPath,
		CredentialPath:   credentialsPath,
	})
	if err != nil {
		return nil, err
	}
	return auth, nil
}

func normalizeAdminAccessPath(path, token string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return defaultAdminLoginPath, nil
	}
	return normalizeAdminLoginPath(path, token)
}

func generateAdminAccessPath(token string) (string, error) {
	random, err := randomToken()
	if err != nil {
		return "", err
	}
	return normalizeAdminAccessPath("/access-"+random, token)
}

func loadAdminAccessSettings(path string) (persistedAdminAccessSettings, bool, error) {
	if strings.TrimSpace(path) == "" {
		return persistedAdminAccessSettings{}, false, nil
	}
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return persistedAdminAccessSettings{}, false, nil
	}
	if err != nil {
		return persistedAdminAccessSettings{}, false, fmt.Errorf("read admin access settings: %w", err)
	}
	var settings persistedAdminAccessSettings
	if err := json.Unmarshal(body, &settings); err != nil {
		return persistedAdminAccessSettings{}, false, fmt.Errorf("decode admin access settings: %w", err)
	}
	return settings, true, nil
}

func persistAdminAccessSettings(path string, settings persistedAdminAccessSettings) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create admin access settings directory: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".admin-auth-*.json")
	if err != nil {
		return fmt.Errorf("create admin access settings: %w", err)
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(settings); err != nil {
		_ = file.Close()
		return fmt.Errorf("encode admin access settings: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close admin access settings: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace admin access settings: %w", err)
	}
	return nil
}

func (a *AdminAccess) current() *AdminAuth {
	if a == nil {
		return nil
	}
	return a.auth.Load()
}

func (a *AdminAccess) Enabled() bool {
	auth := a.current()
	return auth != nil && auth.Enabled()
}

func (a *AdminAccess) LoginPath() string {
	auth := a.current()
	if auth == nil {
		return defaultAdminLoginPath
	}
	return auth.LoginPath()
}

func (a *AdminAccess) Username() string {
	if a == nil || a.current() == nil {
		return ""
	}
	return a.current().Username()
}

func (a *AdminAccess) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		auth := a.current()
		if auth == nil {
			c.Next()
			return
		}
		auth.Middleware()(c)
	}
}

func (a *AdminAccess) Register(router gin.IRouter) {
	router.GET("/api/auth/status", func(c *gin.Context) { a.current().status(c) })
	router.POST("/api/auth/login", func(c *gin.Context) { a.current().login(c) })
	router.POST("/api/auth/setup", func(c *gin.Context) { a.current().setup(c) })
	router.POST("/api/auth/refresh", func(c *gin.Context) { a.current().refresh(c) })
	router.POST("/api/auth/logout", func(c *gin.Context) { a.current().logout(c) })
	router.GET("/api/auth/sessions", func(c *gin.Context) { a.current().listSessions(c) })
	router.DELETE("/api/auth/sessions/:id", func(c *gin.Context) { a.current().revokeSession(c) })
	router.POST("/api/auth/sessions/revoke-others", func(c *gin.Context) { a.current().revokeOtherSessions(c) })
	router.PUT("/api/auth/account", func(c *gin.Context) { a.current().updateAccount(c) })
	router.PUT("/api/auth/password", func(c *gin.Context) { a.current().changePassword(c) })
	router.GET("/api/auth/settings", a.getSettings)
	router.PUT("/api/auth/settings", a.updateSettings)
}

func (a *AdminAccess) getSettings(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	a.settingsMu.Lock()
	settings := a.settings
	a.settingsMu.Unlock()
	settings.LoginPath = a.LoginPath()
	settings.Configured = a.current().AccountConfigured()
	settings.Username = a.Username()
	c.JSON(http.StatusOK, settings)
}

func (a *AdminAccess) updateSettings(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var payload adminAccessSettingsPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	if a.settings.ManagedByEnvironment {
		c.JSON(http.StatusConflict, gin.H{"error": "admin login path is managed by environment"})
		return
	}

	loginPath := defaultAdminLoginPath
	if payload.RandomSuffixEnabled {
		loginPath = a.settings.LoginPath
		if !a.settings.RandomSuffixEnabled || payload.Regenerate || loginPath == defaultAdminLoginPath {
			var err error
			loginPath, err = generateAdminAccessPath(a.token)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "unable to generate admin login path"})
				return
			}
		}
	}
	nextAuth, err := newAdminAuthAtPath(a.token, a.username, loginPath, a.sessionTTL, a.sessionsPath, a.credentialsPath)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := persistAdminAccessSettings(a.settingsPath, persistedAdminAccessSettings{
		RandomSuffixEnabled: payload.RandomSuffixEnabled,
		LoginPath:           loginPath,
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	previous := a.current()
	preserveAdminAuthState(previous, nextAuth)
	a.settings.RandomSuffixEnabled = payload.RandomSuffixEnabled
	a.settings.LoginPath = loginPath
	a.settings.Configured = nextAuth.AccountConfigured()
	a.settings.Username = nextAuth.Username()
	a.auth.Store(nextAuth)
	c.JSON(http.StatusOK, a.settings)
}

func preserveAdminAuthState(previous, next *AdminAuth) {
	if previous == nil || next == nil {
		return
	}
	now := previous.now()
	previous.mu.Lock()
	previous.cleanupLocked(now)
	next.email = previous.email
	next.passwordHash = previous.passwordHash
	next.accountID = previous.accountID
	for sessionID, expiresAt := range previous.sessions {
		next.sessions[sessionID] = expiresAt
	}
	for clientKey, attempt := range previous.attempts {
		next.attempts[clientKey] = attempt
	}
	previous.mu.Unlock()
}
