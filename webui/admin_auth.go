package webui

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	adminAccessCookieName        = "diana_admin_access"
	adminRefreshCookieName       = "diana_admin_refresh"
	adminLegacySessionCookieName = "diana_admin_session"
	adminSessionCookieName       = adminAccessCookieName
	defaultAdminUsername         = ""
	defaultAdminLoginPath        = "/login"
	defaultAdminAccessTTL        = 15 * time.Minute
	defaultAdminSessionTTL       = 30 * 24 * time.Hour
	adminSessionTouchInterval    = 5 * time.Minute
	adminLoginWindow             = 5 * time.Minute
	adminLoginBlock              = 15 * time.Minute
	adminLoginMaxFailures        = 5
	adminAuthSessionContextKey   = "diana.admin.session_id"
	adminAuthMethodContextKey    = "diana.admin.auth_method"
)

var adminLoginPathPattern = regexp.MustCompile(`^/[A-Za-z0-9][A-Za-z0-9_-]{10,127}$`)

// AdminAuthConfig configures the management-plane authentication service.
type AdminAuthConfig struct {
	Token            string
	Username         string
	LoginPath        string
	AccessTTL        time.Duration
	SessionTTL       time.Duration
	SessionStorePath string
	CredentialPath   string
}

// AdminAuth protects the WebUI and management APIs without affecting OneBot traffic.
type AdminAuth struct {
	apiToken         string
	loginPath        string
	accessTTL        time.Duration
	sessionTTL       time.Duration
	sessionStorePath string
	credentialPath   string
	sessionKeyID     string
	jwtSecret        string
	accountID        string
	email            string
	passwordHash     string

	mu       sync.Mutex
	sessions map[string]adminSession
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
	Email    string `json:"email,omitempty"`
	Password string `json:"password,omitempty"`
	Token    string `json:"token,omitempty"`
}

type adminSetupPayload struct {
	Email           string `json:"email"`
	Password        string `json:"password"`
	PasswordConfirm string `json:"password_confirm"`
}

type adminAccountPayload struct {
	Email           string `json:"email"`
	CurrentPassword string `json:"current_password"`
}

type adminPasswordPayload struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
	PasswordConfirm string `json:"password_confirm"`
}

type adminSessionResponse struct {
	ID         string    `json:"id"`
	DeviceName string    `json:"device_name"`
	UserAgent  string    `json:"user_agent,omitempty"`
	IPAddress  string    `json:"ip_address,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Current    bool      `json:"current"`
}

// NewAdminAuth builds the admin authentication service. A credential path
// enables first-run setup even when no static API token was configured.
func NewAdminAuth(cfg AdminAuthConfig) (*AdminAuth, error) {
	apiToken := strings.TrimSpace(cfg.Token)
	if apiToken != "" && len(apiToken) < 32 {
		return nil, fmt.Errorf("DIANA_ADMIN_TOKEN must contain at least 32 characters")
	}
	loginPath, err := normalizeAdminLoginPath(cfg.LoginPath, apiToken)
	if err != nil {
		return nil, err
	}
	accessTTL := cfg.AccessTTL
	if accessTTL <= 0 {
		accessTTL = defaultAdminAccessTTL
	}
	sessionTTL := cfg.SessionTTL
	if sessionTTL <= 0 {
		sessionTTL = defaultAdminSessionTTL
	}
	if accessTTL > sessionTTL {
		accessTTL = sessionTTL
	}

	credentialPath := strings.TrimSpace(cfg.CredentialPath)
	credential, err := loadOrCreateAdminCredential(credentialPath, cfg.Username, apiToken)
	if err != nil {
		return nil, err
	}
	sessionStorePath := strings.TrimSpace(cfg.SessionStorePath)
	sessionKeyID := adminSessionKeyID(credential.JWTSecret)
	sessions, err := loadAdminSessions(sessionStorePath, sessionKeyID, time.Now())
	if err != nil {
		return nil, err
	}
	return &AdminAuth{
		apiToken:         apiToken,
		loginPath:        loginPath,
		accessTTL:        accessTTL,
		sessionTTL:       sessionTTL,
		sessionStorePath: sessionStorePath,
		credentialPath:   credentialPath,
		sessionKeyID:     sessionKeyID,
		jwtSecret:        credential.JWTSecret,
		accountID:        credential.AccountID,
		email:            credential.Email,
		passwordHash:     credential.PasswordHash,
		sessions:         sessions,
		attempts:         map[string]adminLoginAttempt{},
		now:              time.Now,
	}, nil
}

func normalizeAdminLoginPath(raw string, _ string) (string, error) {
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

// Enabled reports whether the management plane is protected.
func (a *AdminAuth) Enabled() bool {
	return a != nil && (a.apiToken != "" || a.jwtSecret != "")
}

func (a *AdminAuth) AccountConfigured() bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return strings.TrimSpace(a.email) != "" && strings.TrimSpace(a.passwordHash) != ""
}

func (a *AdminAuth) Username() string {
	if a == nil {
		return ""
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.email
}

func (a *AdminAuth) LoginPath() string {
	if a == nil {
		return defaultAdminLoginPath
	}
	return a.loginPath
}

func (a *AdminAuth) Register(router gin.IRouter) {
	router.GET("/api/auth/status", a.status)
	router.POST("/api/auth/login", a.login)
	router.POST("/api/auth/setup", a.setup)
	router.POST("/api/auth/refresh", a.refresh)
	router.POST("/api/auth/logout", a.logout)
	router.GET("/api/auth/sessions", a.listSessions)
	router.DELETE("/api/auth/sessions/:id", a.revokeSession)
	router.POST("/api/auth/sessions/revoke-others", a.revokeOtherSessions)
	router.PUT("/api/auth/account", a.updateAccount)
	router.PUT("/api/auth/password", a.changePassword)
}

func (a *AdminAuth) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !a.Enabled() || a.isPublicPath(c.Request.URL.Path) {
			c.Next()
			return
		}
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			if a.authenticateRequest(c, true) {
				c.Next()
				return
			}
			c.Header("Cache-Control", "no-store")
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "admin authentication required"})
			return
		}
		if isAdminConsolePath(c.Request.URL.Path) && !a.authenticateRequest(c, true) {
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
	case "/api/auth/status", "/api/auth/login", "/api/auth/setup", "/api/auth/refresh", "/api/auth/logout", "/onebot/v11/ws":
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
	authenticated := !a.Enabled() || a.authenticateRequest(c, true)
	a.mu.Lock()
	configured := strings.TrimSpace(a.email) != "" && strings.TrimSpace(a.passwordHash) != ""
	email := a.email
	a.mu.Unlock()
	response := gin.H{
		"configured":     a.Enabled(),
		"setup_required": a.Enabled() && !configured,
		"authenticated":  authenticated,
		"login_page":     secureEqual(candidatePath, a.loginPath),
		"login_path":     a.loginPath,
	}
	if authenticated && configured {
		response["email"] = email
		response["username"] = email
	}
	c.JSON(http.StatusOK, response)
}

func (a *AdminAuth) login(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if !a.Enabled() {
		c.JSON(http.StatusOK, gin.H{"authenticated": true})
		return
	}
	if !a.validLoginPathHeader(c) {
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
	if !a.AccountConfigured() && (strings.TrimSpace(payload.Token) == "" || a.apiToken == "" || !secureEqual(strings.TrimSpace(payload.Token), a.apiToken)) {
		c.JSON(http.StatusConflict, gin.H{"error": "administrator setup is required", "setup_required": true})
		return
	}
	subject, ok := a.validLoginPayload(payload)
	if !ok {
		if retryAfter, blocked := a.recordLoginFailure(clientKey, now); blocked {
			c.Header("Retry-After", fmt.Sprintf("%d", max(1, int(retryAfter.Seconds()))))
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many login attempts"})
			return
		}
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid email or password"})
		return
	}
	if err := a.startBrowserSession(c, subject, now); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "unable to create admin session"})
		return
	}
	a.mu.Lock()
	delete(a.attempts, clientKey)
	a.mu.Unlock()
}

func (a *AdminAuth) setup(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if !a.Enabled() || !a.validLoginPathHeader(c) {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	var payload adminSetupPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if payload.Password != payload.PasswordConfirm {
		c.JSON(http.StatusBadRequest, gin.H{"error": "password confirmation does not match"})
		return
	}
	email, err := normalizeAdminEmail(payload.Email)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	hash, err := hashAdminPassword(payload.Password)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	a.mu.Lock()
	if a.email != "" || a.passwordHash != "" {
		a.mu.Unlock()
		c.JSON(http.StatusConflict, gin.H{"error": "administrator account is already configured"})
		return
	}
	previousEmail, previousHash := a.email, a.passwordHash
	a.email, a.passwordHash = email, hash
	if err := a.persistCredentialLocked(); err != nil {
		a.email, a.passwordHash = previousEmail, previousHash
		a.mu.Unlock()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "unable to persist administrator account"})
		return
	}
	a.mu.Unlock()
	if err := a.startBrowserSession(c, email, a.now()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "account was created but the session could not be started"})
	}
}

func (a *AdminAuth) refresh(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if !a.Enabled() {
		c.JSON(http.StatusOK, gin.H{"authenticated": true})
		return
	}
	if ok, err := a.refreshBrowserSession(c); !ok {
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "refresh token is invalid or expired"})
			return
		}
		c.JSON(http.StatusUnauthorized, gin.H{"error": "refresh token is required"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"authenticated": true})
}

func (a *AdminAuth) logout(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	sessionID := a.sessionIDFromRefreshCookie(c)
	if sessionID == "" {
		if accessToken, err := c.Cookie(adminAccessCookieName); err == nil {
			if claims, err := verifyAdminJWT(a.jwtSecret, accessToken, a.now()); err == nil || errors.Is(err, errAdminJWTExpired) {
				sessionID = claims.SessionID
			}
		}
	}
	var persistErr error
	if sessionID != "" {
		a.mu.Lock()
		delete(a.sessions, sessionID)
		persistErr = a.persistSessionsLocked()
		a.mu.Unlock()
	}
	a.clearAuthCookies(c)
	if persistErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"authenticated": false, "error": "unable to persist admin logout"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"authenticated": false})
}

func (a *AdminAuth) listSessions(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	currentID := sessionIDFromContext(c)
	now := a.now()
	a.mu.Lock()
	a.cleanupLocked(now)
	items := make([]adminSessionResponse, 0, len(a.sessions))
	for _, session := range a.sessions {
		items = append(items, adminSessionResponse{
			ID:         session.ID,
			DeviceName: session.DeviceName,
			UserAgent:  session.UserAgent,
			IPAddress:  session.IPAddress,
			CreatedAt:  session.CreatedAt,
			LastSeenAt: session.LastSeenAt,
			ExpiresAt:  session.ExpiresAt,
			Current:    session.ID == currentID,
		})
	}
	a.mu.Unlock()
	sort.Slice(items, func(i, j int) bool { return items[i].LastSeenAt.After(items[j].LastSeenAt) })
	c.JSON(http.StatusOK, gin.H{"sessions": items})
}

func (a *AdminAuth) revokeSession(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	sessionID := strings.TrimSpace(c.Param("id"))
	if sessionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "session id is required"})
		return
	}
	a.mu.Lock()
	session, exists := a.sessions[sessionID]
	if !exists {
		a.mu.Unlock()
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}
	delete(a.sessions, sessionID)
	if err := a.persistSessionsLocked(); err != nil {
		a.sessions[sessionID] = session
		a.mu.Unlock()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "unable to revoke session"})
		return
	}
	a.mu.Unlock()
	current := sessionID == sessionIDFromContext(c)
	if current {
		a.clearAuthCookies(c)
	}
	c.JSON(http.StatusOK, gin.H{"revoked": true, "current": current})
}

func (a *AdminAuth) revokeOtherSessions(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	currentID := sessionIDFromContext(c)
	a.mu.Lock()
	previous := cloneAdminSessions(a.sessions)
	revoked := 0
	for id := range a.sessions {
		if currentID != "" && id == currentID {
			continue
		}
		delete(a.sessions, id)
		revoked++
	}
	if err := a.persistSessionsLocked(); err != nil {
		a.sessions = previous
		a.mu.Unlock()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "unable to revoke sessions"})
		return
	}
	a.mu.Unlock()
	c.JSON(http.StatusOK, gin.H{"revoked": revoked})
}

func (a *AdminAuth) updateAccount(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var payload adminAccountPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	email, err := normalizeAdminEmail(payload.Email)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := a.updateCredentials(c, payload.CurrentPassword, email, ""); err != nil {
		a.writeCredentialUpdateError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"email": email})
}

func (a *AdminAuth) changePassword(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var payload adminPasswordPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if payload.NewPassword != payload.PasswordConfirm {
		c.JSON(http.StatusBadRequest, gin.H{"error": "password confirmation does not match"})
		return
	}
	hash, err := hashAdminPassword(payload.NewPassword)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := a.updateCredentials(c, payload.CurrentPassword, "", hash); err != nil {
		a.writeCredentialUpdateError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"changed": true, "other_sessions_revoked": true})
}

var (
	errCurrentPasswordInvalid = errors.New("current password is invalid")
	errCredentialPersist      = errors.New("unable to persist administrator credentials")
)

func (a *AdminAuth) updateCredentials(c *gin.Context, currentPassword, nextEmail, nextHash string) error {
	now := a.now()
	currentID := sessionIDFromContext(c)
	a.mu.Lock()
	defer a.mu.Unlock()
	if !verifyAdminPassword(a.passwordHash, currentPassword) {
		return errCurrentPasswordInvalid
	}
	previousCredential := adminCredentialState{
		Email:        a.email,
		PasswordHash: a.passwordHash,
		JWTSecret:    a.jwtSecret,
		AccountID:    a.accountID,
	}
	previousSessions := cloneAdminSessions(a.sessions)
	restore := func() {
		a.email = previousCredential.Email
		a.passwordHash = previousCredential.PasswordHash
		a.accountID = previousCredential.AccountID
		a.sessions = previousSessions
	}
	nextAccountID, err := randomToken()
	if err != nil {
		return err
	}
	if nextEmail != "" {
		a.email = nextEmail
	}
	if nextHash != "" {
		a.passwordHash = nextHash
	}
	a.accountID = nextAccountID
	nextSessions := make(map[string]adminSession, 1)
	var accessToken, refreshToken string
	var accessExpiry, refreshExpiry time.Time
	for id := range a.sessions {
		if id != currentID {
			continue
		}
		session := a.sessions[id]
		session.AccountID = a.accountID
		var updated adminSession
		accessToken, accessExpiry, refreshToken, updated, err = a.rotateSessionTokensLocked(session, now)
		if err != nil {
			restore()
			return err
		}
		refreshExpiry = updated.ExpiresAt
		nextSessions[id] = updated
	}
	a.sessions = nextSessions

	// Persist session invalidation first. If credential persistence then fails,
	// the old password remains valid and the previous sessions can be restored.
	if err := a.persistSessionsLocked(); err != nil {
		restore()
		return fmt.Errorf("%w: %v", errCredentialPersist, err)
	}
	if err := a.persistCredentialLocked(); err != nil {
		restore()
		if rollbackErr := a.persistSessionsLocked(); rollbackErr != nil {
			return fmt.Errorf("%w: %v (session rollback failed: %v)", errCredentialPersist, err, rollbackErr)
		}
		return fmt.Errorf("%w: %v", errCredentialPersist, err)
	}
	if currentID != "" && accessToken != "" {
		a.setAuthCookies(c, accessToken, accessExpiry, refreshToken, refreshExpiry)
	}
	return nil
}

func (a *AdminAuth) writeCredentialUpdateError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, errCurrentPasswordInvalid):
		c.JSON(http.StatusUnauthorized, gin.H{"error": errCurrentPasswordInvalid.Error()})
	case errors.Is(err, errCredentialPersist):
		c.JSON(http.StatusInternalServerError, gin.H{"error": errCredentialPersist.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "unable to update administrator credentials"})
	}
}

func (a *AdminAuth) validLoginPathHeader(c *gin.Context) bool {
	return secureEqual(strings.TrimSpace(c.GetHeader("X-Diana-Login-Path")), a.loginPath)
}

func (a *AdminAuth) validLoginPayload(payload adminLoginPayload) (string, bool) {
	if strings.TrimSpace(payload.Token) != "" && a.apiToken != "" && secureEqual(strings.TrimSpace(payload.Token), a.apiToken) {
		a.mu.Lock()
		subject := a.email
		a.mu.Unlock()
		if subject == "" {
			subject = "administrator"
		}
		return subject, true
	}
	email := strings.TrimSpace(payload.Email)
	if email == "" {
		email = strings.TrimSpace(payload.Username)
	}
	a.mu.Lock()
	expectedEmail, passwordHash := a.email, a.passwordHash
	a.mu.Unlock()
	emailMatches := secureEqual(strings.ToLower(email), expectedEmail)
	passwordMatches := verifyAdminPassword(passwordHash, payload.Password)
	return expectedEmail, emailMatches && passwordMatches
}

func (a *AdminAuth) startBrowserSession(c *gin.Context, subject string, now time.Time) error {
	sessionID, err := randomToken()
	if err != nil {
		return err
	}
	refreshSecret, err := randomToken()
	if err != nil {
		return err
	}
	refreshToken := sessionID + "." + refreshSecret
	refreshExpiry := now.Add(a.sessionTTL)
	a.mu.Lock()
	accountID := a.accountID
	a.mu.Unlock()
	session := adminSession{
		ID:          sessionID,
		RefreshHash: adminSessionIDHash(refreshToken),
		AccountID:   accountID,
		DeviceName:  describeAdminDevice(c.GetHeader("User-Agent")),
		UserAgent:   truncateAdminMetadata(c.GetHeader("User-Agent"), 512),
		IPAddress:   truncateAdminMetadata(c.ClientIP(), 64),
		CreatedAt:   now.UTC(),
		LastSeenAt:  now.UTC(),
		ExpiresAt:   refreshExpiry.UTC(),
	}
	accessToken, accessExpiry, accessID, err := a.newAccessToken(subject, sessionID, now, refreshExpiry)
	if err != nil {
		return err
	}
	session.AccessID = accessID
	a.mu.Lock()
	a.cleanupLocked(now)
	a.sessions[sessionID] = session
	if err := a.persistSessionsLocked(); err != nil {
		delete(a.sessions, sessionID)
		a.mu.Unlock()
		return err
	}
	a.mu.Unlock()
	a.setAuthCookies(c, accessToken, accessExpiry, refreshToken, refreshExpiry)
	c.JSON(http.StatusOK, gin.H{
		"authenticated":      true,
		"access_expires_at":  accessExpiry.UTC(),
		"refresh_expires_at": refreshExpiry.UTC(),
	})
	return nil
}

func (a *AdminAuth) newAccessToken(subject, sessionID string, now, sessionExpiry time.Time) (string, time.Time, string, error) {
	expiresAt := now.Add(a.accessTTL)
	if expiresAt.After(sessionExpiry) {
		expiresAt = sessionExpiry
	}
	tokenID, err := randomToken()
	if err != nil {
		return "", time.Time{}, "", err
	}
	token, err := signAdminJWT(a.jwtSecret, adminJWTClaims{
		Issuer:    adminJWTIssuer,
		Subject:   subject,
		SessionID: sessionID,
		TokenID:   tokenID,
		IssuedAt:  now.Unix(),
		ExpiresAt: expiresAt.Unix(),
	})
	return token, expiresAt, tokenID, err
}

func (a *AdminAuth) authenticateRequest(c *gin.Context, allowRefresh bool) bool {
	if candidate, ok := bearerToken(c.GetHeader("Authorization")); ok {
		if a.apiToken != "" && secureEqual(candidate, a.apiToken) {
			c.Set(adminAuthMethodContextKey, "api_token")
			return true
		}
		if a.authenticateAccessToken(c, candidate) {
			return true
		}
	}
	if accessToken, err := c.Cookie(adminAccessCookieName); err == nil && a.authenticateAccessToken(c, accessToken) {
		return true
	}
	if allowRefresh {
		ok, _ := a.refreshBrowserSession(c)
		return ok
	}
	return false
}

func (a *AdminAuth) authenticateAccessToken(c *gin.Context, token string) bool {
	now := a.now()
	claims, err := verifyAdminJWT(a.jwtSecret, token, now)
	if err != nil {
		return false
	}
	a.mu.Lock()
	a.cleanupLocked(now)
	session, ok := a.sessions[claims.SessionID]
	expectedSubject := a.email
	if expectedSubject == "" {
		expectedSubject = "administrator"
	}
	if !ok || !secureEqual(claims.Subject, expectedSubject) || !secureEqual(claims.TokenID, session.AccessID) || !secureEqual(session.AccountID, a.accountID) || !now.Before(session.ExpiresAt) {
		a.mu.Unlock()
		return false
	}
	if now.Sub(session.LastSeenAt) >= adminSessionTouchInterval {
		previous := session.LastSeenAt
		session.LastSeenAt = now.UTC()
		a.sessions[session.ID] = session
		if err := a.persistSessionsLocked(); err != nil {
			session.LastSeenAt = previous
			a.sessions[session.ID] = session
		}
	}
	a.mu.Unlock()
	c.Set(adminAuthSessionContextKey, claims.SessionID)
	c.Set(adminAuthMethodContextKey, "access_token")
	return true
}

func (a *AdminAuth) refreshBrowserSession(c *gin.Context) (bool, error) {
	refreshToken, err := c.Cookie(adminRefreshCookieName)
	if err != nil || strings.TrimSpace(refreshToken) == "" {
		return false, nil
	}
	sessionID := refreshSessionID(refreshToken)
	if sessionID == "" {
		return false, errAdminJWTInvalid
	}
	now := a.now()
	a.mu.Lock()
	a.cleanupLocked(now)
	session, ok := a.sessions[sessionID]
	if !ok || !now.Before(session.ExpiresAt) || !secureEqual(session.AccountID, a.accountID) || !secureEqual(session.RefreshHash, adminSessionIDHash(refreshToken)) {
		a.mu.Unlock()
		return false, errAdminJWTInvalid
	}
	accessToken, accessExpiry, nextRefreshToken, updated, err := a.rotateSessionTokensLocked(session, now)
	if err != nil {
		a.mu.Unlock()
		return false, err
	}
	a.sessions[sessionID] = updated
	if err := a.persistSessionsLocked(); err != nil {
		a.sessions[sessionID] = session
		a.mu.Unlock()
		return false, err
	}
	a.mu.Unlock()
	a.setAuthCookies(c, accessToken, accessExpiry, nextRefreshToken, updated.ExpiresAt)
	c.Set(adminAuthSessionContextKey, sessionID)
	c.Set(adminAuthMethodContextKey, "refresh_token")
	return true, nil
}

func (a *AdminAuth) rotateSessionTokensLocked(session adminSession, now time.Time) (string, time.Time, string, adminSession, error) {
	refreshSecret, err := randomToken()
	if err != nil {
		return "", time.Time{}, "", session, err
	}
	refreshToken := session.ID + "." + refreshSecret
	subject := a.email
	if subject == "" {
		subject = "administrator"
	}
	accessToken, accessExpiry, accessID, err := a.newAccessToken(subject, session.ID, now, session.ExpiresAt)
	if err != nil {
		return "", time.Time{}, "", session, err
	}
	session.RefreshHash = adminSessionIDHash(refreshToken)
	session.AccessID = accessID
	session.LastSeenAt = now.UTC()
	return accessToken, accessExpiry, refreshToken, session, nil
}

func (a *AdminAuth) setAuthCookies(c *gin.Context, accessToken string, accessExpiry time.Time, refreshToken string, refreshExpiry time.Time) {
	c.SetSameSite(http.SameSiteStrictMode)
	secure := c.Request.TLS != nil
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     adminAccessCookieName,
		Value:    accessToken,
		Path:     "/",
		Expires:  accessExpiry,
		MaxAge:   max(1, int(accessExpiry.Sub(a.now()).Seconds())),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     adminRefreshCookieName,
		Value:    refreshToken,
		Path:     "/api/auth",
		Expires:  refreshExpiry,
		MaxAge:   max(1, int(refreshExpiry.Sub(a.now()).Seconds())),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
	clearCookie(c, adminLegacySessionCookieName, "/")
}

func (a *AdminAuth) clearAuthCookies(c *gin.Context) {
	c.SetSameSite(http.SameSiteStrictMode)
	clearCookie(c, adminAccessCookieName, "/")
	clearCookie(c, adminRefreshCookieName, "/api/auth")
	clearCookie(c, adminLegacySessionCookieName, "/")
}

func clearCookie(c *gin.Context, name, path string) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     path,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   c.Request.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	})
}

func (a *AdminAuth) sessionIDFromRefreshCookie(c *gin.Context) string {
	refreshToken, err := c.Cookie(adminRefreshCookieName)
	if err != nil {
		return ""
	}
	return refreshSessionID(refreshToken)
}

func refreshSessionID(token string) string {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return ""
	}
	return parts[0]
}

func sessionIDFromContext(c *gin.Context) string {
	value, _ := c.Get(adminAuthSessionContextKey)
	sessionID, _ := value.(string)
	return sessionID
}

func bearerToken(header string) (string, bool) {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}
	return parts[1], true
}

func (a *AdminAuth) persistSessionsLocked() error {
	return persistAdminSessions(a.sessionStorePath, a.sessionKeyID, a.sessions)
}

func (a *AdminAuth) persistCredentialLocked() error {
	return persistAdminCredential(a.credentialPath, adminCredentialState{
		Email:        a.email,
		PasswordHash: a.passwordHash,
		JWTSecret:    a.jwtSecret,
		AccountID:    a.accountID,
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
	for sessionID, session := range a.sessions {
		if !now.Before(session.ExpiresAt) {
			delete(a.sessions, sessionID)
		}
	}
	for clientKey, attempt := range a.attempts {
		if now.After(attempt.blockedUntil) && now.Sub(attempt.windowStarted) >= adminLoginWindow {
			delete(a.attempts, clientKey)
		}
	}
}

func cloneAdminSessions(source map[string]adminSession) map[string]adminSession {
	cloned := make(map[string]adminSession, len(source))
	for id, session := range source {
		cloned[id] = session
	}
	return cloned
}

func describeAdminDevice(userAgent string) string {
	ua := strings.ToLower(userAgent)
	platform := "未知设备"
	switch {
	case strings.Contains(ua, "iphone"):
		platform = "iPhone"
	case strings.Contains(ua, "ipad"):
		platform = "iPad"
	case strings.Contains(ua, "android"):
		platform = "Android"
	case strings.Contains(ua, "macintosh") || strings.Contains(ua, "mac os x"):
		platform = "Mac"
	case strings.Contains(ua, "windows"):
		platform = "Windows"
	case strings.Contains(ua, "linux"):
		platform = "Linux"
	}
	browser := "浏览器"
	switch {
	case strings.Contains(ua, "edg/"):
		browser = "Edge"
	case strings.Contains(ua, "firefox/"):
		browser = "Firefox"
	case strings.Contains(ua, "chrome/") || strings.Contains(ua, "crios/"):
		browser = "Chrome"
	case strings.Contains(ua, "safari/"):
		browser = "Safari"
	}
	return browser + " · " + platform
}

func truncateAdminMetadata(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

func secureEqual(left string, right string) bool {
	leftDigest := sha256.Sum256([]byte(left))
	rightDigest := sha256.Sum256([]byte(right))
	return subtle.ConstantTimeCompare(leftDigest[:], rightDigest[:]) == 1
}
