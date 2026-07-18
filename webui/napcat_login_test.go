package webui

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
)

const napCatTestToken = "napcat-super-secret-token"

type napCatTestServer struct {
	t              *testing.T
	server         *httptest.Server
	mu             sync.Mutex
	authCount      int
	authorizedUses int
	credential     string
	state          napCatLoginState
	quickAccounts  any
	account        any
	onRequest      func(http.ResponseWriter, *http.Request) bool
}

func newNapCatTestServer(t *testing.T) *napCatTestServer {
	t.Helper()
	fake := &napCatTestServer{
		t:             t,
		credential:    "test-credential",
		state:         napCatLoginState{QRCodeURL: "https://example.test/qq-login?ticket=qr-secret"},
		quickAccounts: []any{map[string]any{"uin": 10003, "nickName": "Test Account", "faceUrl": "https://example.test/avatar.jpg"}},
		account:       map[string]any{"uin": 10003, "nick": "Test Account", "online": true, "avatarUrl": "https://example.test/avatar.jpg"},
	}
	fake.server = httptest.NewServer(http.HandlerFunc(fake.serveHTTP))
	t.Cleanup(fake.server.Close)
	return fake
}

func (s *napCatTestServer) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if s.onRequest != nil && s.onRequest(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		s.t.Errorf("method = %s, want POST", r.Method)
	}
	if r.URL.Path == "/api/auth/login" {
		var payload struct {
			Hash string `json:"hash"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			s.t.Errorf("decode auth request: %v", err)
		}
		expected := sha256.Sum256([]byte(napCatTestToken + ".napcat"))
		if payload.Hash != hex.EncodeToString(expected[:]) {
			s.t.Errorf("auth hash = %q, want SHA256(token + .napcat)", payload.Hash)
		}
		s.mu.Lock()
		s.authCount++
		s.mu.Unlock()
		s.writeEnvelope(w, 0, "success", gin.H{"Credential": s.credential})
		return
	}
	if got := r.Header.Get("Authorization"); got != "Bearer "+s.credential {
		s.t.Errorf("Authorization = %q", got)
		s.writeEnvelope(w, -1, "Unauthorized", nil)
		return
	}
	s.mu.Lock()
	s.authorizedUses++
	s.mu.Unlock()

	switch r.URL.Path {
	case "/api/QQLogin/CheckLoginStatus":
		s.writeEnvelope(w, 0, "success", s.state)
	case "/api/QQLogin/GetQuickLoginListNew":
		s.writeEnvelope(w, 0, "success", s.quickAccounts)
	case "/api/QQLogin/GetQQLoginInfo":
		s.writeEnvelope(w, 0, "success", s.account)
	case "/api/QQLogin/RefreshQRcode":
		s.writeEnvelope(w, 0, "success", nil)
	case "/api/QQLogin/SetQuickLogin":
		var payload struct {
			UIN string `json:"uin"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			s.t.Errorf("decode quick login request: %v", err)
		}
		if payload.UIN != "10003" {
			s.t.Errorf("quick login UIN = %q", payload.UIN)
		}
		s.writeEnvelope(w, 0, "success", nil)
	default:
		s.t.Errorf("unexpected path %s", r.URL.Path)
		http.NotFound(w, r)
	}
}

func (s *napCatTestServer) writeEnvelope(w http.ResponseWriter, code int, message string, data any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(gin.H{"code": code, "message": message, "data": data})
}

func (s *napCatTestServer) handler(t *testing.T) *NapCatLoginHandler {
	t.Helper()
	handler, err := NewNapCatLoginHandler(NapCatLoginConfig{BaseURL: s.server.URL, Token: napCatTestToken, Client: s.server.Client()})
	if err != nil {
		t.Fatalf("NewNapCatLoginHandler() error = %v", err)
	}
	return handler
}

func napCatTestRouter(handler *NapCatLoginHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler.Register(router)
	return router
}

func TestNapCatLoginStatusAggregatesAccountDataAndCachesCredential(t *testing.T) {
	fake := newNapCatTestServer(t)
	fake.state = napCatLoginState{IsLogin: true}
	router := napCatTestRouter(fake.handler(t))

	for requestNumber := 0; requestNumber < 2; requestNumber++ {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/napcat/login/status", nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
		}
		var payload NapCatLoginStatus
		if err := json.NewDecoder(recorder.Body).Decode(&payload); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		if !payload.Configured || !payload.IsLogin || payload.QRCodeAvailable {
			t.Fatalf("payload = %#v", payload)
		}
		if payload.Account == nil || payload.Account.UIN != "10003" || payload.Account.Nickname != "Test Account" {
			t.Fatalf("account = %#v", payload.Account)
		}
		if len(payload.QuickAccounts) != 1 || payload.QuickAccounts[0].UIN != "10003" {
			t.Fatalf("quick accounts = %#v", payload.QuickAccounts)
		}
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.authCount != 1 {
		t.Fatalf("auth requests = %d, want 1", fake.authCount)
	}
	if fake.authorizedUses != 6 {
		t.Fatalf("authorized requests = %d, want 6", fake.authorizedUses)
	}
}

func TestNapCatLoginQRCodeReturnsPNGWithoutExposingSourceURL(t *testing.T) {
	fake := newNapCatTestServer(t)
	router := napCatTestRouter(fake.handler(t))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/napcat/login/qrcode", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if !bytes.HasPrefix(recorder.Body.Bytes(), []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}) {
		t.Fatalf("response is not a PNG")
	}
	if strings.Contains(recorder.Body.String(), "qr-secret") {
		t.Fatal("raw QR URL leaked in response")
	}
}

func TestNapCatLoginRefreshAndQuickLoginReturnUpdatedStatus(t *testing.T) {
	fake := newNapCatTestServer(t)
	var mu sync.Mutex
	called := map[string]int{}
	fake.onRequest = func(_ http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/api/QQLogin/RefreshQRcode" || r.URL.Path == "/api/QQLogin/SetQuickLogin" {
			mu.Lock()
			called[r.URL.Path]++
			mu.Unlock()
		}
		return false
	}
	router := napCatTestRouter(fake.handler(t))

	refresh := httptest.NewRecorder()
	router.ServeHTTP(refresh, httptest.NewRequest(http.MethodPost, "/api/napcat/login/refresh", nil))
	if refresh.Code != http.StatusOK {
		t.Fatalf("refresh status = %d, body = %s", refresh.Code, refresh.Body.String())
	}

	quick := httptest.NewRecorder()
	router.ServeHTTP(quick, httptest.NewRequest(http.MethodPost, "/api/napcat/login/quick", strings.NewReader(`{"uin":"10003"}`)))
	if quick.Code != http.StatusOK {
		t.Fatalf("quick status = %d, body = %s", quick.Code, quick.Body.String())
	}

	mu.Lock()
	defer mu.Unlock()
	if called["/api/QQLogin/RefreshQRcode"] != 1 || called["/api/QQLogin/SetQuickLogin"] != 1 {
		t.Fatalf("action calls = %#v", called)
	}
}

func TestNapCatLoginRejectsInvalidQuickLoginUIN(t *testing.T) {
	fake := newNapCatTestServer(t)
	router := napCatTestRouter(fake.handler(t))

	for _, body := range []string{`{"uin":"625 059"}`, `{"uin":"abc"}`, `{}`} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/napcat/login/quick", strings.NewReader(body)))
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("body %s: status = %d, response = %s", body, recorder.Code, recorder.Body.String())
		}
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.authCount != 0 {
		t.Fatalf("invalid input made %d auth requests", fake.authCount)
	}
}

func TestNapCatLoginRetriesExpiredCredentialOnce(t *testing.T) {
	fake := newNapCatTestServer(t)
	var once sync.Once
	fake.onRequest = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/api/QQLogin/CheckLoginStatus" {
			return false
		}
		handled := false
		once.Do(func() {
			handled = true
			http.Error(w, "expired", http.StatusUnauthorized)
		})
		return handled
	}
	router := napCatTestRouter(fake.handler(t))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/napcat/login/status", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.authCount != 2 {
		t.Fatalf("auth requests = %d, want 2", fake.authCount)
	}
}

func TestNapCatLoginDoesNotLeakUpstreamSecrets(t *testing.T) {
	fake := newNapCatTestServer(t)
	fake.onRequest = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/api/QQLogin/CheckLoginStatus" {
			fake.writeEnvelope(w, -1, fmt.Sprintf("failed at %s with token %s", fake.server.URL, napCatTestToken), nil)
			return true
		}
		return false
	}
	router := napCatTestRouter(fake.handler(t))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/napcat/login/status", nil))

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	if strings.Contains(body, napCatTestToken) || strings.Contains(body, fake.server.URL) || strings.Contains(body, "failed at") {
		t.Fatalf("upstream detail leaked: %s", body)
	}
}

func TestNapCatLoginUnconfiguredStatus(t *testing.T) {
	handler, err := NewNapCatLoginHandler(NapCatLoginConfig{})
	if err != nil {
		t.Fatalf("NewNapCatLoginHandler() error = %v", err)
	}
	router := napCatTestRouter(handler)

	status := httptest.NewRecorder()
	router.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/api/napcat/login/status", nil))
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"configured":false`) {
		t.Fatalf("status = %d, body = %s", status.Code, status.Body.String())
	}

	qr := httptest.NewRecorder()
	router.ServeHTTP(qr, httptest.NewRequest(http.MethodGet, "/api/napcat/login/qrcode", nil))
	if qr.Code != http.StatusServiceUnavailable {
		t.Fatalf("QR status = %d, body = %s", qr.Code, qr.Body.String())
	}
}
