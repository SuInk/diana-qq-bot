package webui

import (
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	adminSessionCookieName = "diana_admin_session"
	defaultAdminUsername   = "admin@diana.local"
	defaultAdminLoginPath  = "/login"
	defaultAdminSessionTTL = 12 * time.Hour
	adminLoginWindow       = 5 * time.Minute
	adminLoginBlock        = 15 * time.Minute
	adminLoginMaxFailures  = 5
)

var adminLoginPathPattern = regexp.MustCompile(`^/[A-Za-z0-9][A-Za-z0-9_-]{10,127}$`)

// AdminAuthConfig configures the management-plane token gate.
type AdminAuthConfig struct {
	Token      string
	Username   string
	LoginPath  string
	SessionTTL time.Duration
}

// AdminAuth protects the WebUI and management APIs without affecting OneBot traffic.
type AdminAuth struct {
	token      string
	username   string
	loginPath  string
	sessionTTL time.Duration

	mu       sync.Mutex
	sessions map[string]time.Time
	attempts map[string]adminLoginAttempt
	now      func() time.Time
}

type adminLoginAttempt struct {
	failures      int
	windowStarted time.Time
	blockedUntil  time.Time
}

type adminLoginPayload struct {
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Token    string `json:"token,omitempty"`
}

// NewAdminAuth builds the admin authentication service. An empty token keeps
// authentication disabled for backwards-compatible local development.
func NewAdminAuth(cfg AdminAuthConfig) (*AdminAuth, error) {
	token := strings.TrimSpace(cfg.Token)
	if token != "" && len(token) < 32 {
		return nil, fmt.Errorf("DIANA_ADMIN_TOKEN must contain at least 32 characters")
	}
	username := strings.TrimSpace(cfg.Username)
	if username == "" {
		username = defaultAdminUsername
	}
	loginPath, err := normalizeAdminLoginPath(cfg.LoginPath, token)
	if err != nil {
		return nil, err
	}
	sessionTTL := cfg.SessionTTL
	if sessionTTL <= 0 {
		sessionTTL = defaultAdminSessionTTL
	}
	return &AdminAuth{
		token:      token,
		username:   username,
		loginPath:  loginPath,
		sessionTTL: sessionTTL,
		sessions:   map[string]time.Time{},
		attempts:   map[string]adminLoginAttempt{},
		now:        time.Now,
	}, nil
}

func normalizeAdminLoginPath(raw string, token string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultAdminLoginPath, nil
	}
	if raw == defaultAdminLoginPath {
		return raw, nil
	}
	if !adminLoginPathPattern.MatchString(raw) {
		return "", fmt.Errorf("DIANA_ADMIN_LOGIN_PATH must be one path segment with at least 11 letters, digits, underscores, or hyphens")
	}
	if isAdminConsolePath(raw) || strings.HasPrefix(raw, "/api") || strings.HasPrefix(raw, "/onebot") {
		return "", fmt.Errorf("DIANA_ADMIN_LOGIN_PATH conflicts with a reserved route")
	}
	return raw, nil
}

// Enabled reports whether the management-plane token gate is active.
func (a *AdminAuth) Enabled() bool {
	return a != nil && a.token != ""
}

// LoginPath returns the configured hidden entry path for startup logging.
func (a *AdminAuth) LoginPath() string {
	if a == nil {
		return defaultAdminLoginPath
	}
	return a.loginPath
}

// Register exposes only the authentication lifecycle endpoints.
func (a *AdminAuth) Register(router gin.IRouter) {
	router.GET("/api/auth/status", a.status)
	router.POST("/api/auth/login", a.login)
	router.POST("/api/auth/logout", a.logout)
}

// Middleware protects all management APIs and known console routes.
func (a *AdminAuth) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !a.Enabled() || a.isPublicPath(c.Request.URL.Path) {
			c.Next()
			return
		}
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			if a.authenticated(c) {
				c.Next()
				return
			}
			c.Header("Cache-Control", "no-store")
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "admin authentication required"})
			return
		}
		if isAdminConsolePath(c.Request.URL.Path) && !a.authenticated(c) {
			c.Header("Cache-Control", "no-store")
			c.Redirect(http.StatusFound, a.loginPath)
			c.Abort()
			return
		}
		c.Next()
	}
}

func (a *AdminAuth) isPublicPath(requestPath string) bool {
	switch requestPath {
	case "/api/auth/status", "/api/auth/login", "/api/auth/logout", "/onebot/v11/ws":
		return true
	}
	if strings.HasPrefix(requestPath, "/api/qqbot/media/") {
		return true
	}
	return requestPath == a.loginPath
}

func isAdminConsolePath(requestPath string) bool {
	requestPath = strings.TrimSuffix(requestPath, "/")
	switch requestPath {
	case "/console", "/admin", "/webui", "/llm", "/test", "/qqbot", "/robots", "/groups", "/plugins", "/web-search", "/logs", "/security", "/theme":
		return true
	default:
		return false
	}
}

func (a *AdminAuth) status(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	candidatePath := strings.TrimSuffix(strings.TrimSpace(c.Query("path")), "/")
	if candidatePath == "" {
		candidatePath = "/"
	}
	authenticated := !a.Enabled() || a.authenticated(c)
	c.JSON(http.StatusOK, gin.H{
		"configured":    a.Enabled(),
		"authenticated": authenticated,
		"login_page":    secureEqual(candidatePath, a.loginPath),
		"login_path":    a.loginPath,
		"username":      a.username,
	})
}

func (a *AdminAuth) login(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if !a.Enabled() {
		c.JSON(http.StatusOK, gin.H{"authenticated": true})
		return
	}
	if !secureEqual(strings.TrimSpace(c.GetHeader("X-Diana-Login-Path")), a.loginPath) {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	now := a.now()
	clientKey := c.ClientIP()
	if retryAfter, blocked := a.loginBlocked(clientKey, now); blocked {
		c.Header("Retry-After", fmt.Sprintf("%d", max(1, int(retryAfter.Seconds()))))
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many login attempts"})
		return
	}
	var payload adminLoginPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if !a.validLoginPayload(payload) {
		if retryAfter, blocked := a.recordLoginFailure(clientKey, now); blocked {
			c.Header("Retry-After", fmt.Sprintf("%d", max(1, int(retryAfter.Seconds()))))
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many login attempts"})
			return
		}
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid account or password"})
		return
	}
	sessionID, err := randomToken()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "unable to create admin session"})
		return
	}
	expiresAt := now.Add(a.sessionTTL)
	a.mu.Lock()
	a.cleanupLocked(now)
	delete(a.attempts, clientKey)
	a.sessions[sessionID] = expiresAt
	a.mu.Unlock()
	a.setSessionCookie(c, sessionID, expiresAt)
	c.JSON(http.StatusOK, gin.H{"authenticated": true, "expires_at": expiresAt.UTC()})
}

func (a *AdminAuth) validLoginPayload(payload adminLoginPayload) bool {
	if strings.TrimSpace(payload.Token) != "" {
		return secureEqual(strings.TrimSpace(payload.Token), a.token)
	}
	return secureEqual(strings.TrimSpace(payload.Username), a.username) && secureEqual(strings.TrimSpace(payload.Password), a.token)
}

func (a *AdminAuth) logout(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if sessionID, err := c.Cookie(adminSessionCookieName); err == nil && sessionID != "" {
		a.mu.Lock()
		delete(a.sessions, sessionID)
		a.mu.Unlock()
	}
	c.SetSameSite(http.SameSiteStrictMode)
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     adminSessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   c.Request.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	})
	c.JSON(http.StatusOK, gin.H{"authenticated": false})
}

func (a *AdminAuth) authenticated(c *gin.Context) bool {
	if candidate, ok := bearerToken(c.GetHeader("Authorization")); ok && secureEqual(candidate, a.token) {
		return true
	}
	sessionID, err := c.Cookie(adminSessionCookieName)
	if err != nil || sessionID == "" {
		return false
	}
	now := a.now()
	a.mu.Lock()
	defer a.mu.Unlock()
	expiresAt, ok := a.sessions[sessionID]
	if !ok || !now.Before(expiresAt) {
		delete(a.sessions, sessionID)
		return false
	}
	return true
}

func bearerToken(header string) (string, bool) {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}
	return parts[1], true
}

func (a *AdminAuth) setSessionCookie(c *gin.Context, sessionID string, expiresAt time.Time) {
	c.SetSameSite(http.SameSiteStrictMode)
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     adminSessionCookieName,
		Value:    sessionID,
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   max(1, int(expiresAt.Sub(a.now()).Seconds())),
		HttpOnly: true,
		Secure:   c.Request.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	})
}

func (a *AdminAuth) loginBlocked(clientKey string, now time.Time) (time.Duration, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cleanupLocked(now)
	attempt, ok := a.attempts[clientKey]
	if !ok || !now.Before(attempt.blockedUntil) {
		return 0, false
	}
	return attempt.blockedUntil.Sub(now), true
}

func (a *AdminAuth) recordLoginFailure(clientKey string, now time.Time) (time.Duration, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cleanupLocked(now)
	attempt := a.attempts[clientKey]
	if attempt.windowStarted.IsZero() || now.Sub(attempt.windowStarted) >= adminLoginWindow {
		attempt = adminLoginAttempt{windowStarted: now}
	}
	attempt.failures++
	if attempt.failures >= adminLoginMaxFailures {
		attempt.blockedUntil = now.Add(adminLoginBlock)
	}
	a.attempts[clientKey] = attempt
	if now.Before(attempt.blockedUntil) {
		return attempt.blockedUntil.Sub(now), true
	}
	return 0, false
}

func (a *AdminAuth) cleanupLocked(now time.Time) {
	for sessionID, expiresAt := range a.sessions {
		if !now.Before(expiresAt) {
			delete(a.sessions, sessionID)
		}
	}
	for clientKey, attempt := range a.attempts {
		if now.After(attempt.blockedUntil) && now.Sub(attempt.windowStarted) >= adminLoginWindow {
			delete(a.attempts, clientKey)
		}
	}
}

func secureEqual(left string, right string) bool {
	leftDigest := sha256.Sum256([]byte(left))
	rightDigest := sha256.Sum256([]byte(right))
	return subtle.ConstantTimeCompare(leftDigest[:], rightDigest[:]) == 1
}
