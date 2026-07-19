package webui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

const testAdminToken = "0123456789abcdef0123456789abcdef-admin-token"

func TestNewAdminAuth(t *testing.T) {
	t.Parallel()

	disabled, err := NewAdminAuth(AdminAuthConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if disabled.Enabled() || disabled.LoginPath() != "/login" {
		t.Fatalf("unexpected disabled auth: enabled=%v path=%q", disabled.Enabled(), disabled.LoginPath())
	}
	if _, err := NewAdminAuth(AdminAuthConfig{Token: "too-short"}); err == nil {
		t.Fatal("expected short token to be rejected")
	}

	configured, err := NewAdminAuth(AdminAuthConfig{Token: testAdminToken})
	if err != nil {
		t.Fatal(err)
	}
	if !configured.Enabled() || !strings.HasPrefix(configured.LoginPath(), "/access-") {
		t.Fatalf("unexpected derived login path %q", configured.LoginPath())
	}
	if _, err := NewAdminAuth(AdminAuthConfig{Token: testAdminToken, LoginPath: "/api/private-entry"}); err == nil {
		t.Fatal("expected reserved login path to be rejected")
	}
}

func TestAdminAuthRouteProtection(t *testing.T) {
	t.Parallel()

	auth, router := newAdminAuthTestRouter(t, time.Hour)

	assertStatus(t, router, http.MethodGet, "/api/private", nil, nil, http.StatusUnauthorized)
	assertRedirect(t, router, "/console", auth.LoginPath())
	assertRedirect(t, router, "/qqbot", auth.LoginPath())
	assertRedirect(t, router, "/robots", auth.LoginPath())
	assertStatus(t, router, http.MethodGet, auth.LoginPath(), nil, nil, http.StatusOK)
	assertStatus(t, router, http.MethodGet, "/onebot/v11/ws", nil, nil, http.StatusOK)
	assertStatus(t, router, http.MethodGet, "/api/qqbot/media/media-token", nil, nil, http.StatusOK)
	assertStatus(t, router, http.MethodGet, "/api/private", nil, map[string]string{"Authorization": "Bearer " + testAdminToken}, http.StatusOK)
	assertStatus(t, router, http.MethodGet, "/api/private", nil, map[string]string{"Authorization": "Bearer wrong-token"}, http.StatusUnauthorized)
}

func assertRedirect(t *testing.T, router http.Handler, path string, location string) {
	t.Helper()
	recorder := performRequest(router, http.MethodGet, path, nil, nil)
	if recorder.Code != http.StatusFound || recorder.Header().Get("Location") != location {
		t.Fatalf("%s redirect = %d location %q, want %d %q", path, recorder.Code, recorder.Header().Get("Location"), http.StatusFound, location)
	}
}

func TestAdminAuthLoginSessionAndLogout(t *testing.T) {
	t.Parallel()

	auth, router := newAdminAuthTestRouter(t, time.Hour)

	status := performRequest(router, http.MethodGet, "/api/auth/status?path=/ordinary-path", nil, nil)
	if status.Code != http.StatusOK || strings.Contains(status.Body.String(), auth.LoginPath()) {
		t.Fatalf("status leaked hidden path or failed: code=%d body=%s", status.Code, status.Body.String())
	}
	assertStatus(t, router, http.MethodPost, "/api/auth/login", strings.NewReader(`{"token":"`+testAdminToken+`"}`), nil, http.StatusNotFound)

	login := performRequest(router, http.MethodPost, "/api/auth/login", strings.NewReader(`{"token":"`+testAdminToken+`"}`), map[string]string{
		"Content-Type":       "application/json",
		"X-Diana-Login-Path": auth.LoginPath(),
	})
	if login.Code != http.StatusOK {
		t.Fatalf("login failed: code=%d body=%s", login.Code, login.Body.String())
	}
	result := login.Result()
	var sessionCookie *http.Cookie
	for _, cookie := range result.Cookies() {
		if cookie.Name == adminSessionCookieName {
			sessionCookie = cookie
			break
		}
	}
	if sessionCookie == nil || !sessionCookie.HttpOnly || sessionCookie.SameSite != http.SameSiteStrictMode || sessionCookie.MaxAge <= 0 {
		t.Fatalf("unexpected session cookie: %#v", sessionCookie)
	}

	assertStatus(t, router, http.MethodGet, "/api/private", nil, map[string]string{"Cookie": sessionCookie.String()}, http.StatusOK)
	logout := performRequest(router, http.MethodPost, "/api/auth/logout", nil, map[string]string{"Cookie": sessionCookie.String()})
	if logout.Code != http.StatusOK {
		t.Fatalf("logout failed: code=%d body=%s", logout.Code, logout.Body.String())
	}
	assertStatus(t, router, http.MethodGet, "/api/private", nil, map[string]string{"Cookie": sessionCookie.String()}, http.StatusUnauthorized)
}

func TestAdminAuthSessionExpiryAndRateLimit(t *testing.T) {
	t.Parallel()

	auth, router := newAdminAuthTestRouter(t, time.Minute)
	now := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	auth.now = func() time.Time { return now }
	login := performRequest(router, http.MethodPost, "/api/auth/login", strings.NewReader(`{"token":"`+testAdminToken+`"}`), map[string]string{
		"Content-Type":       "application/json",
		"X-Diana-Login-Path": auth.LoginPath(),
	})
	sessionCookie := login.Result().Cookies()[0]
	now = now.Add(2 * time.Minute)
	assertStatus(t, router, http.MethodGet, "/api/private", nil, map[string]string{"Cookie": sessionCookie.String()}, http.StatusUnauthorized)

	limitedAuth, limitedRouter := newAdminAuthTestRouter(t, time.Hour)
	for attempt := 1; attempt <= adminLoginMaxFailures; attempt++ {
		expected := http.StatusUnauthorized
		if attempt == adminLoginMaxFailures {
			expected = http.StatusTooManyRequests
		}
		assertStatus(t, limitedRouter, http.MethodPost, "/api/auth/login", strings.NewReader(`{"token":"wrong-admin-token"}`), map[string]string{
			"Content-Type":       "application/json",
			"X-Diana-Login-Path": limitedAuth.LoginPath(),
		}, expected)
	}
	assertStatus(t, limitedRouter, http.MethodPost, "/api/auth/login", strings.NewReader(`{"token":"`+testAdminToken+`"}`), map[string]string{
		"Content-Type":       "application/json",
		"X-Diana-Login-Path": limitedAuth.LoginPath(),
	}, http.StatusTooManyRequests)
}

func newAdminAuthTestRouter(t *testing.T, sessionTTL time.Duration) (*AdminAuth, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	auth, err := NewAdminAuth(AdminAuthConfig{Token: testAdminToken, LoginPath: "/secret-admin-entry", SessionTTL: sessionTTL})
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	if err := router.SetTrustedProxies(nil); err != nil {
		t.Fatal(err)
	}
	router.Use(auth.Middleware())
	auth.Register(router)
	router.GET("/api/private", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	router.GET("/console", func(c *gin.Context) { c.String(http.StatusOK, "console") })
	router.GET(auth.LoginPath(), func(c *gin.Context) { c.String(http.StatusOK, "login") })
	router.GET("/onebot/v11/ws", func(c *gin.Context) { c.Status(http.StatusOK) })
	router.GET("/api/qqbot/media/:token", func(c *gin.Context) { c.Status(http.StatusOK) })
	return auth, router
}

func assertStatus(t *testing.T, router http.Handler, method string, target string, body *strings.Reader, headers map[string]string, expected int) {
	t.Helper()
	response := performRequest(router, method, target, body, headers)
	if response.Code != expected {
		var payload map[string]any
		_ = json.Unmarshal(response.Body.Bytes(), &payload)
		t.Fatalf("%s %s: got %d, want %d, body=%v", method, target, response.Code, expected, payload)
	}
}

func performRequest(router http.Handler, method string, target string, body *strings.Reader, headers map[string]string) *httptest.ResponseRecorder {
	var requestBody io.Reader = http.NoBody
	if body != nil {
		requestBody = body
	}
	request := httptest.NewRequest(method, target, requestBody)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}
