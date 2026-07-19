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

	"github.com/gin-gonic/gin"
)

const adminAccessTestToken = "0123456789abcdef0123456789abcdef-access-token"

func TestAdminAccessDefaultsToLoginAndPersistsRandomSuffix(t *testing.T) {
	t.Parallel()
	settingsPath := filepath.Join(t.TempDir(), "admin-auth.json")
	access, err := NewAdminAccess(AdminAccessConfig{Token: adminAccessTestToken, SettingsPath: settingsPath})
	if err != nil {
		t.Fatal(err)
	}
	if access.LoginPath() != defaultAdminLoginPath {
		t.Fatalf("default login path = %q", access.LoginPath())
	}

	router := newAdminAccessTestRouter(t, access)
	status := performAdminAccessRequest(router, http.MethodGet, "/api/auth/status?path="+defaultAdminLoginPath, nil, nil)
	var authStatus map[string]any
	decodeAdminAccessResponse(t, status, &authStatus)
	if status.Code != http.StatusOK || authStatus["login_page"] != true {
		t.Fatalf("login status = %d %#v", status.Code, authStatus)
	}
	assertAdminAccessStatus(t, router, "/api/auth/status?path=/", false)

	login := performAdminAccessRequest(router, http.MethodPost, "/api/auth/login", strings.NewReader(`{"token":"`+adminAccessTestToken+`"}`), map[string]string{
		"Content-Type":       "application/json",
		"X-Diana-Login-Path": defaultAdminLoginPath,
	})
	if login.Code != http.StatusOK || len(login.Result().Cookies()) == 0 {
		t.Fatalf("default login = %d %s", login.Code, login.Body.String())
	}
	cookie := login.Result().Cookies()[0]

	updated := performAdminAccessRequest(router, http.MethodPut, "/api/auth/settings", strings.NewReader(`{"random_suffix_enabled":true}`), map[string]string{
		"Content-Type": "application/json",
		"Cookie":       cookie.String(),
	})
	var settings AdminAccessSettings
	decodeAdminAccessResponse(t, updated, &settings)
	if updated.Code != http.StatusOK || !settings.RandomSuffixEnabled || !strings.HasPrefix(settings.LoginPath, "/access-") {
		t.Fatalf("updated settings = %d %#v", updated.Code, settings)
	}
	firstPath := settings.LoginPath

	assertAdminAccessStatus(t, router, "/api/auth/status?path="+defaultAdminLoginPath, false)
	assertAdminAccessStatus(t, router, "/api/auth/status?path="+firstPath, true)
	security := performAdminAccessRequest(router, http.MethodGet, "/security", nil, nil)
	if security.Code != http.StatusFound || security.Header().Get("Location") != firstPath {
		t.Fatalf("unauthenticated security redirect = %d location %q", security.Code, security.Header().Get("Location"))
	}
	console := performAdminAccessRequest(router, http.MethodGet, "/console", nil, map[string]string{"Cookie": cookie.String()})
	if console.Code != http.StatusOK {
		t.Fatalf("preserved session console status = %d", console.Code)
	}

	reloaded, err := NewAdminAccess(AdminAccessConfig{Token: adminAccessTestToken, SettingsPath: settingsPath})
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.LoginPath() != firstPath {
		t.Fatalf("persisted login path = %q, want %q", reloaded.LoginPath(), firstPath)
	}
	reloadedRouter := newAdminAccessTestRouter(t, reloaded)
	reloadedConsole := performAdminAccessRequest(reloadedRouter, http.MethodGet, "/console", nil, map[string]string{"Cookie": cookie.String()})
	if reloadedConsole.Code != http.StatusOK {
		t.Fatalf("session did not survive admin access restart: status=%d", reloadedConsole.Code)
	}

	regenerated := performAdminAccessRequest(router, http.MethodPut, "/api/auth/settings", strings.NewReader(`{"random_suffix_enabled":true,"regenerate":true}`), map[string]string{
		"Content-Type": "application/json",
		"Cookie":       cookie.String(),
	})
	decodeAdminAccessResponse(t, regenerated, &settings)
	if regenerated.Code != http.StatusOK || settings.LoginPath == firstPath || !strings.HasPrefix(settings.LoginPath, "/access-") {
		t.Fatalf("regenerated settings = %d %#v", regenerated.Code, settings)
	}

	disabled := performAdminAccessRequest(router, http.MethodPut, "/api/auth/settings", strings.NewReader(`{"random_suffix_enabled":false}`), map[string]string{
		"Content-Type": "application/json",
		"Cookie":       cookie.String(),
	})
	decodeAdminAccessResponse(t, disabled, &settings)
	if disabled.Code != http.StatusOK || settings.RandomSuffixEnabled || settings.LoginPath != defaultAdminLoginPath {
		t.Fatalf("disabled settings = %d %#v", disabled.Code, settings)
	}
}

func TestAdminAccessEnvironmentPathCannotBeChangedByWebUI(t *testing.T) {
	t.Parallel()
	access, err := NewAdminAccess(AdminAccessConfig{
		Token:        adminAccessTestToken,
		LoginPath:    "/environment-admin-entry",
		SettingsPath: filepath.Join(t.TempDir(), "admin-auth.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	router := newAdminAccessTestRouter(t, access)

	settingsResponse := performAdminAccessRequest(router, http.MethodGet, "/api/auth/settings", nil, map[string]string{
		"Authorization": "Bearer " + adminAccessTestToken,
	})
	var settings AdminAccessSettings
	decodeAdminAccessResponse(t, settingsResponse, &settings)
	if settingsResponse.Code != http.StatusOK || !settings.ManagedByEnvironment || settings.LoginPath != "/environment-admin-entry" {
		t.Fatalf("settings = %d %#v", settingsResponse.Code, settings)
	}

	update := performAdminAccessRequest(router, http.MethodPut, "/api/auth/settings", strings.NewReader(`{"random_suffix_enabled":false}`), map[string]string{
		"Authorization": "Bearer " + adminAccessTestToken,
		"Content-Type":  "application/json",
	})
	if update.Code != http.StatusConflict || access.LoginPath() != "/environment-admin-entry" {
		t.Fatalf("update = %d path=%q body=%s", update.Code, access.LoginPath(), update.Body.String())
	}
}

func TestAdminAccessWithoutTokenGeneratesLocalCredentials(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	credentialsPath := filepath.Join(dir, "admin-credentials.json")
	access, err := NewAdminAccess(AdminAccessConfig{
		SettingsPath:    filepath.Join(dir, "admin-auth.json"),
		CredentialsPath: credentialsPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !access.Enabled() || access.LoginPath() != defaultAdminLoginPath || access.Username() != defaultAdminUsername {
		t.Fatalf("enabled=%v path=%q", access.Enabled(), access.LoginPath())
	}
	router := newAdminAccessTestRouter(t, access)
	response := performAdminAccessRequest(router, http.MethodGet, "/console", nil, nil)
	if response.Code != http.StatusFound || response.Header().Get("Location") != defaultAdminLoginPath {
		t.Fatalf("console redirect = %d location %q", response.Code, response.Header().Get("Location"))
	}
	body, err := os.ReadFile(credentialsPath)
	if err != nil {
		t.Fatal(err)
	}
	var credential persistedAdminCredential
	if err := json.Unmarshal(body, &credential); err != nil {
		t.Fatal(err)
	}
	login := performAdminAccessRequest(router, http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"`+credential.Username+`","password":"`+credential.Password+`"}`), map[string]string{
		"Content-Type":       "application/json",
		"X-Diana-Login-Path": defaultAdminLoginPath,
	})
	if login.Code != http.StatusOK || len(login.Result().Cookies()) == 0 {
		t.Fatalf("generated credential login = %d %s", login.Code, login.Body.String())
	}
}

func newAdminAccessTestRouter(t *testing.T, access *AdminAccess) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	if err := router.SetTrustedProxies(nil); err != nil {
		t.Fatal(err)
	}
	router.Use(access.Middleware())
	access.Register(router)
	router.GET("/console", func(c *gin.Context) { c.String(http.StatusOK, "console") })
	router.NoRoute(func(c *gin.Context) { c.String(http.StatusOK, "spa") })
	return router
}

func performAdminAccessRequest(router http.Handler, method, target string, body *strings.Reader, headers map[string]string) *httptest.ResponseRecorder {
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

func decodeAdminAccessResponse(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(response.Body.Bytes()))
	if err := decoder.Decode(target); err != nil {
		t.Fatalf("decode response %q: %v", response.Body.String(), err)
	}
}

func assertAdminAccessStatus(t *testing.T, router http.Handler, target string, wantLoginPage bool) {
	t.Helper()
	response := performAdminAccessRequest(router, http.MethodGet, target, nil, nil)
	var status map[string]any
	decodeAdminAccessResponse(t, response, &status)
	if response.Code != http.StatusOK || status["login_page"] != wantLoginPage {
		t.Fatalf("%s: code=%d status=%#v", target, response.Code, status)
	}
}
