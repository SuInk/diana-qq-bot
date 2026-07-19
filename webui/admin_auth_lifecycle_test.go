package webui

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	testAdminEmail    = "owner@example.com"
	testAdminPassword = "correct horse battery staple"
	testAdminNewPass  = "a newer correct horse battery staple"
)

type adminAuthFixture struct {
	auth             *AdminAuth
	router           *gin.Engine
	now              time.Time
	credentialsPath  string
	sessionStorePath string
}

type adminAuthClient struct {
	router  http.Handler
	cookies map[string]*http.Cookie
}

func TestAdminAuthFirstRunJWTAndRefreshRotation(t *testing.T) {
	fixture := newAdminAuthFixture(t, false, time.Minute)
	client := newAdminAuthClient(fixture.router)

	status := client.request(t, http.MethodGet, "/api/auth/status?path=/secret-admin-entry", nil, nil)
	var statusBody map[string]any
	decodeJSONResponse(t, status, &statusBody)
	if status.Code != http.StatusOK || statusBody["setup_required"] != true || statusBody["username"] != nil {
		t.Fatalf("first-run status = %d %#v", status.Code, statusBody)
	}

	setup := client.request(t, http.MethodPost, "/api/auth/setup", map[string]any{
		"email":            testAdminEmail,
		"password":         testAdminPassword,
		"password_confirm": testAdminPassword,
	}, map[string]string{
		"X-Diana-Login-Path": "/secret-admin-entry",
		"User-Agent":         "Mozilla/5.0 (Macintosh) AppleWebKit Safari/605.1.15",
	})
	if setup.Code != http.StatusOK {
		t.Fatalf("setup = %d %s", setup.Code, setup.Body.String())
	}
	access := client.cookie(adminAccessCookieName)
	refresh := client.cookie(adminRefreshCookieName)
	if access == nil || refresh == nil || strings.Count(access.Value, ".") != 2 || refreshSessionID(refresh.Value) == "" {
		t.Fatalf("setup cookies = %#v", setup.Result().Cookies())
	}
	claims, err := verifyAdminJWT(fixture.auth.jwtSecret, access.Value, fixture.now)
	if err != nil || claims.Subject != testAdminEmail || claims.SessionID != refreshSessionID(refresh.Value) {
		t.Fatalf("JWT claims = %#v, err=%v", claims, err)
	}

	private := client.request(t, http.MethodGet, "/api/private", nil, nil)
	if private.Code != http.StatusOK {
		t.Fatalf("authenticated request = %d %s", private.Code, private.Body.String())
	}
	sessions := client.request(t, http.MethodGet, "/api/auth/sessions", nil, nil)
	var sessionBody struct {
		Sessions []adminSessionResponse `json:"sessions"`
	}
	decodeJSONResponse(t, sessions, &sessionBody)
	if len(sessionBody.Sessions) != 1 || !sessionBody.Sessions[0].Current || sessionBody.Sessions[0].DeviceName != "Safari · Mac" {
		t.Fatalf("device sessions = %#v", sessionBody.Sessions)
	}

	oldAccess := *access
	oldRefresh := *refresh
	fixture.advance(2 * time.Minute)
	accessOnly := newAdminAuthClient(fixture.router)
	accessOnly.cookies[adminAccessCookieName] = &oldAccess
	if response := accessOnly.request(t, http.MethodGet, "/api/private", nil, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("expired access without refresh = %d", response.Code)
	}

	refreshed := client.request(t, http.MethodPost, "/api/auth/refresh", nil, nil)
	if refreshed.Code != http.StatusOK || client.cookie(adminRefreshCookieName).Value == oldRefresh.Value {
		t.Fatalf("refresh = %d cookies=%#v", refreshed.Code, refreshed.Result().Cookies())
	}
	replay := newAdminAuthClient(fixture.router)
	replay.cookies[adminRefreshCookieName] = &oldRefresh
	if response := replay.request(t, http.MethodPost, "/api/auth/refresh", nil, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("replayed refresh = %d %s", response.Code, response.Body.String())
	}
	if response := client.request(t, http.MethodGet, "/api/private", nil, nil); response.Code != http.StatusOK {
		t.Fatalf("request after refresh = %d %s", response.Code, response.Body.String())
	}
}

func TestAdminAuthDeviceRevocationPasswordAndEmailChange(t *testing.T) {
	fixture := newAdminAuthFixture(t, true, 5*time.Minute)
	first := newAdminAuthClient(fixture.router)
	second := newAdminAuthClient(fixture.router)
	loginAdminTestClient(t, first, testAdminEmail, testAdminPassword, "Mozilla/5.0 (Macintosh) Chrome/140.0")
	loginAdminTestClient(t, second, testAdminEmail, testAdminPassword, "Mozilla/5.0 (Windows NT 10.0) Firefox/141.0")

	secondSessionID := refreshSessionID(second.cookie(adminRefreshCookieName).Value)
	sessions := first.request(t, http.MethodGet, "/api/auth/sessions", nil, nil)
	var listed struct {
		Sessions []adminSessionResponse `json:"sessions"`
	}
	decodeJSONResponse(t, sessions, &listed)
	if len(listed.Sessions) != 2 {
		t.Fatalf("session count = %d, want 2", len(listed.Sessions))
	}
	revoke := first.request(t, http.MethodDelete, "/api/auth/sessions/"+secondSessionID, nil, nil)
	if revoke.Code != http.StatusOK {
		t.Fatalf("revoke device = %d %s", revoke.Code, revoke.Body.String())
	}
	if response := second.request(t, http.MethodGet, "/api/private", nil, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("revoked device request = %d", response.Code)
	}

	second = newAdminAuthClient(fixture.router)
	loginAdminTestClient(t, second, testAdminEmail, testAdminPassword, "Mozilla/5.0 (iPhone) Safari/605.1")
	oldCurrentAccess := *first.cookie(adminAccessCookieName)
	change := first.request(t, http.MethodPut, "/api/auth/password", map[string]any{
		"current_password": testAdminPassword,
		"new_password":     testAdminNewPass,
		"password_confirm": testAdminNewPass,
	}, nil)
	if change.Code != http.StatusOK {
		t.Fatalf("change password = %d %s", change.Code, change.Body.String())
	}
	oldAccessClient := newAdminAuthClient(fixture.router)
	oldAccessClient.cookies[adminAccessCookieName] = &oldCurrentAccess
	if response := oldAccessClient.request(t, http.MethodGet, "/api/private", nil, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("pre-change JWT remained valid = %d", response.Code)
	}
	if response := second.request(t, http.MethodGet, "/api/private", nil, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("other device survived password change = %d", response.Code)
	}
	failedLogin := newAdminAuthClient(fixture.router)
	if response := loginAdminTestClient(t, failedLogin, testAdminEmail, testAdminPassword, "test"); response.Code != http.StatusUnauthorized {
		t.Fatalf("old password login = %d", response.Code)
	}

	preEmailAccess := *first.cookie(adminAccessCookieName)
	account := first.request(t, http.MethodPut, "/api/auth/account", map[string]any{
		"email":            "new-owner@example.com",
		"current_password": testAdminNewPass,
	}, nil)
	if account.Code != http.StatusOK {
		t.Fatalf("change email = %d %s", account.Code, account.Body.String())
	}
	staleEmailClient := newAdminAuthClient(fixture.router)
	staleEmailClient.cookies[adminAccessCookieName] = &preEmailAccess
	if response := staleEmailClient.request(t, http.MethodGet, "/api/private", nil, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("pre-email-change JWT remained valid = %d", response.Code)
	}
	newLogin := newAdminAuthClient(fixture.router)
	if response := loginAdminTestClient(t, newLogin, "new-owner@example.com", testAdminNewPass, "Mozilla/5.0 (Linux) Chrome/140.0"); response.Code != http.StatusOK {
		t.Fatalf("new account login = %d %s", response.Code, response.Body.String())
	}
}

func TestAdminAuthCurrentSessionCanBeRevoked(t *testing.T) {
	fixture := newAdminAuthFixture(t, true, time.Minute)
	client := newAdminAuthClient(fixture.router)
	loginAdminTestClient(t, client, testAdminEmail, testAdminPassword, "Mozilla/5.0 (Macintosh) Safari/605.1")
	sessionID := refreshSessionID(client.cookie(adminRefreshCookieName).Value)
	response := client.request(t, http.MethodDelete, "/api/auth/sessions/"+sessionID, nil, nil)
	var body map[string]any
	decodeJSONResponse(t, response, &body)
	if response.Code != http.StatusOK || body["current"] != true || client.cookie(adminAccessCookieName) != nil || client.cookie(adminRefreshCookieName) != nil {
		t.Fatalf("current revoke = %d %#v cookies=%#v", response.Code, body, client.cookies)
	}
	if response := client.request(t, http.MethodGet, "/api/private", nil, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("revoked current request = %d", response.Code)
	}
}

func TestAdminAuthPasswordChangeRollsBackWhenSessionsCannotPersist(t *testing.T) {
	fixture := newAdminAuthFixture(t, true, time.Minute)
	client := newAdminAuthClient(fixture.router)
	loginAdminTestClient(t, client, testAdminEmail, testAdminPassword, "Mozilla/5.0 (Macintosh) Safari/605.1")

	blockedPath := filepath.Join(t.TempDir(), "sessions-as-directory")
	if err := os.Mkdir(blockedPath, 0o700); err != nil {
		t.Fatal(err)
	}
	fixture.auth.sessionStorePath = blockedPath
	response := client.request(t, http.MethodPut, "/api/auth/password", map[string]any{
		"current_password": testAdminPassword,
		"new_password":     testAdminNewPass,
		"password_confirm": testAdminNewPass,
	}, nil)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("password change = %d %s", response.Code, response.Body.String())
	}
	if !verifyAdminPassword(fixture.auth.passwordHash, testAdminPassword) || verifyAdminPassword(fixture.auth.passwordHash, testAdminNewPass) {
		t.Fatal("password changed in memory after session persistence failed")
	}
	stored, err := loadOrCreateAdminCredential(fixture.credentialsPath, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !verifyAdminPassword(stored.PasswordHash, testAdminPassword) || verifyAdminPassword(stored.PasswordHash, testAdminNewPass) {
		t.Fatal("password changed on disk after session persistence failed")
	}
}

func newAdminAuthFixture(t *testing.T, configured bool, accessTTL time.Duration) *adminAuthFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	credentialsPath := filepath.Join(dir, "admin-credentials.json")
	sessionStorePath := filepath.Join(dir, "admin-sessions.json")
	secret := "test-jwt-secret-with-at-least-thirty-two-characters"
	if configured {
		hash, err := hashAdminPassword(testAdminPassword)
		if err != nil {
			t.Fatal(err)
		}
		if err := persistAdminCredential(credentialsPath, adminCredentialState{Email: testAdminEmail, PasswordHash: hash, JWTSecret: secret}); err != nil {
			t.Fatal(err)
		}
	}
	auth, err := NewAdminAuth(AdminAuthConfig{
		LoginPath:        "/secret-admin-entry",
		AccessTTL:        accessTTL,
		SessionTTL:       24 * time.Hour,
		SessionStorePath: sessionStorePath,
		CredentialPath:   credentialsPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture := &adminAuthFixture{
		auth:             auth,
		now:              time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC),
		credentialsPath:  credentialsPath,
		sessionStorePath: sessionStorePath,
	}
	auth.now = func() time.Time { return fixture.now }
	router := gin.New()
	if err := router.SetTrustedProxies(nil); err != nil {
		t.Fatal(err)
	}
	router.Use(auth.Middleware())
	auth.Register(router)
	router.GET("/api/private", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	fixture.router = router
	return fixture
}

func (f *adminAuthFixture) advance(duration time.Duration) {
	f.now = f.now.Add(duration)
}

func newAdminAuthClient(router http.Handler) *adminAuthClient {
	return &adminAuthClient{router: router, cookies: map[string]*http.Cookie{}}
}

func (c *adminAuthClient) request(t *testing.T, method, target string, payload any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var body io.Reader = http.NoBody
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, target, body)
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	for _, cookie := range c.cookies {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	c.router.ServeHTTP(recorder, request)
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.MaxAge < 0 || cookie.Value == "" {
			delete(c.cookies, cookie.Name)
			continue
		}
		copy := *cookie
		c.cookies[cookie.Name] = &copy
	}
	return recorder
}

func (c *adminAuthClient) cookie(name string) *http.Cookie {
	return c.cookies[name]
}

func loginAdminTestClient(t *testing.T, client *adminAuthClient, email, password, userAgent string) *httptest.ResponseRecorder {
	t.Helper()
	return client.request(t, http.MethodPost, "/api/auth/login", map[string]any{
		"email":    email,
		"password": password,
	}, map[string]string{
		"X-Diana-Login-Path": "/secret-admin-entry",
		"User-Agent":         userAgent,
	})
}

func decodeJSONResponse(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatalf("decode response %q: %v", response.Body.String(), err)
	}
}
