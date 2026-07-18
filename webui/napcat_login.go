package webui

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	qrcode "github.com/skip2/go-qrcode"
)

const (
	napCatCredentialTTL    = 50 * time.Minute
	napCatMaxResponseBytes = 1 << 20
	napCatQRCodeSize       = 320
)

var napCatUINPattern = regexp.MustCompile(`^[0-9]+$`)

// NapCatLoginConfig configures the private connection to NapCat's WebUI API.
type NapCatLoginConfig struct {
	BaseURL string
	Token   string
	Client  *http.Client
}

// NapCatLoginHandler exposes the subset of NapCat login operations needed by
// Diana's authenticated administration UI.
type NapCatLoginHandler struct {
	baseURL    string
	token      string
	client     *http.Client
	configured bool

	credentialMu        sync.Mutex
	credential          string
	credentialExpiresAt time.Time
}

// NapCatAccount is a sanitized account summary returned to the administration UI.
type NapCatAccount struct {
	UIN       string `json:"uin"`
	Nickname  string `json:"nickname,omitempty"`
	AvatarURL string `json:"avatar_url,omitempty"`
	Online    *bool  `json:"online,omitempty"`
}

// NapCatLoginStatus is the administration UI's NapCat login snapshot.
type NapCatLoginStatus struct {
	Configured      bool            `json:"configured"`
	IsLogin         bool            `json:"is_login"`
	IsOffline       bool            `json:"is_offline"`
	QRCodeAvailable bool            `json:"qrcode_available"`
	LoginError      string          `json:"login_error,omitempty"`
	Account         *NapCatAccount  `json:"account,omitempty"`
	QuickAccounts   []NapCatAccount `json:"quick_accounts"`
}

type napCatEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type napCatLoginState struct {
	IsLogin    bool   `json:"isLogin"`
	IsOffline  bool   `json:"isOffline"`
	QRCodeURL  string `json:"qrcodeurl"`
	LoginError string `json:"loginError"`
}

type napCatCredentialPayload struct {
	Credential string `json:"Credential"`
}

type napCatAccountPayload struct {
	UIN       napCatString `json:"uin"`
	NickName  string       `json:"nickName"`
	Nick      string       `json:"nick"`
	FaceURL   string       `json:"faceUrl"`
	AvatarURL string       `json:"avatarUrl"`
	Online    *bool        `json:"online"`
}

type napCatString string

func (s *napCatString) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		*s = napCatString(text)
		return nil
	}
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err != nil {
		return errors.New("invalid account identifier")
	}
	*s = napCatString(number.String())
	return nil
}

type napCatAPIError struct {
	unauthorized bool
}

func (e *napCatAPIError) Error() string {
	return "NapCat API request failed"
}

type napCatHTTPError struct {
	status int
}

func (e *napCatHTTPError) Error() string {
	return "NapCat HTTP request failed"
}

func (e *napCatHTTPError) unauthorized() bool {
	return e.status == http.StatusUnauthorized || e.status == http.StatusForbidden
}

// NewNapCatLoginHandler creates a login handler. Missing URL or token leaves the
// handler intentionally unconfigured so the admin UI can report that state.
func NewNapCatLoginHandler(config NapCatLoginConfig) (*NapCatLoginHandler, error) {
	baseURL := strings.TrimSpace(config.BaseURL)
	token := strings.TrimSpace(config.Token)
	client := config.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	handler := &NapCatLoginHandler{client: client}
	if baseURL == "" || token == "" {
		return handler, nil
	}

	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("invalid NapCat WebUI URL")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("invalid NapCat WebUI URL")
	}

	handler.baseURL = strings.TrimRight(baseURL, "/")
	handler.token = token
	handler.configured = true
	return handler, nil
}

// Register installs the NapCat login administration routes.
func (h *NapCatLoginHandler) Register(router gin.IRouter) {
	router.GET("/api/napcat/login/status", h.status)
	router.GET("/api/napcat/login/qrcode", h.qrCode)
	router.POST("/api/napcat/login/refresh", h.refresh)
	router.POST("/api/napcat/login/quick", h.quickLogin)
}

func (h *NapCatLoginHandler) status(c *gin.Context) {
	if !h.configured {
		c.JSON(http.StatusOK, NapCatLoginStatus{Configured: false, QuickAccounts: []NapCatAccount{}})
		return
	}
	status, err := h.loadStatus(c.Request.Context())
	if err != nil {
		h.writeUpstreamError(c)
		return
	}
	c.JSON(http.StatusOK, status)
}

func (h *NapCatLoginHandler) qrCode(c *gin.Context) {
	if !h.requireConfigured(c) {
		return
	}
	state, err := h.loadLoginState(c.Request.Context())
	if err != nil {
		h.writeUpstreamError(c)
		return
	}
	qrCodeURL := strings.TrimSpace(state.QRCodeURL)
	if state.IsLogin || qrCodeURL == "" {
		c.JSON(http.StatusConflict, gin.H{"error": "NapCat login QR code is not available"})
		return
	}
	png, err := qrcode.Encode(qrCodeURL, qrcode.Medium, napCatQRCodeSize)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "NapCat login QR code could not be generated"})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, "image/png", png)
}

func (h *NapCatLoginHandler) refresh(c *gin.Context) {
	if !h.requireConfigured(c) {
		return
	}
	if err := h.call(c.Request.Context(), "/api/QQLogin/RefreshQRcode", struct{}{}, nil); err != nil {
		h.writeUpstreamError(c)
		return
	}
	status, err := h.loadStatus(c.Request.Context())
	if err != nil {
		h.writeUpstreamError(c)
		return
	}
	c.JSON(http.StatusOK, status)
}

func (h *NapCatLoginHandler) quickLogin(c *gin.Context) {
	if !h.requireConfigured(c) {
		return
	}
	var payload struct {
		UIN string `json:"uin"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid quick login request"})
		return
	}
	payload.UIN = strings.TrimSpace(payload.UIN)
	if !napCatUINPattern.MatchString(payload.UIN) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "QQ account must contain digits only"})
		return
	}
	if err := h.call(c.Request.Context(), "/api/QQLogin/SetQuickLogin", gin.H{"uin": payload.UIN}, nil); err != nil {
		h.writeUpstreamError(c)
		return
	}
	status, err := h.loadStatus(c.Request.Context())
	if err != nil {
		h.writeUpstreamError(c)
		return
	}
	c.JSON(http.StatusOK, status)
}

func (h *NapCatLoginHandler) requireConfigured(c *gin.Context) bool {
	if h.configured {
		return true
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": "NapCat login is not configured"})
	return false
}

func (h *NapCatLoginHandler) writeUpstreamError(c *gin.Context) {
	c.JSON(http.StatusBadGateway, gin.H{"error": "NapCat login service is unavailable"})
}

func (h *NapCatLoginHandler) loadStatus(ctx context.Context) (NapCatLoginStatus, error) {
	state, err := h.loadLoginState(ctx)
	if err != nil {
		return NapCatLoginStatus{}, err
	}

	var quickRaw json.RawMessage
	if err := h.call(ctx, "/api/QQLogin/GetQuickLoginListNew", struct{}{}, &quickRaw); err != nil {
		return NapCatLoginStatus{}, err
	}
	quickAccounts, err := parseNapCatAccounts(quickRaw)
	if err != nil {
		return NapCatLoginStatus{}, err
	}

	status := NapCatLoginStatus{
		Configured:      true,
		IsLogin:         state.IsLogin,
		IsOffline:       state.IsOffline,
		QRCodeAvailable: !state.IsLogin && strings.TrimSpace(state.QRCodeURL) != "",
		LoginError:      state.LoginError,
		QuickAccounts:   quickAccounts,
	}
	if state.IsLogin {
		var payload napCatAccountPayload
		if err := h.call(ctx, "/api/QQLogin/GetQQLoginInfo", struct{}{}, &payload); err != nil {
			return NapCatLoginStatus{}, err
		}
		account := sanitizeNapCatAccount(payload)
		status.Account = &account
	}
	return status, nil
}

func (h *NapCatLoginHandler) loadLoginState(ctx context.Context) (napCatLoginState, error) {
	var state napCatLoginState
	if err := h.call(ctx, "/api/QQLogin/CheckLoginStatus", struct{}{}, &state); err != nil {
		return napCatLoginState{}, err
	}
	return state, nil
}

func (h *NapCatLoginHandler) call(ctx context.Context, path string, requestBody any, responseData any) error {
	for attempt := 0; attempt < 2; attempt++ {
		credential, err := h.getCredential(ctx)
		if err != nil {
			return err
		}
		err = h.do(ctx, path, requestBody, credential, responseData)
		if err == nil {
			return nil
		}
		if attempt == 0 && isNapCatUnauthorized(err) {
			h.invalidateCredential(credential)
			continue
		}
		return err
	}
	return errors.New("NapCat API request failed")
}

func (h *NapCatLoginHandler) getCredential(ctx context.Context) (string, error) {
	h.credentialMu.Lock()
	defer h.credentialMu.Unlock()
	if h.credential != "" && time.Now().Before(h.credentialExpiresAt) {
		return h.credential, nil
	}

	sum := sha256.Sum256([]byte(h.token + ".napcat"))
	var payload napCatCredentialPayload
	if err := h.do(ctx, "/api/auth/login", gin.H{"hash": hex.EncodeToString(sum[:])}, "", &payload); err != nil {
		return "", err
	}
	payload.Credential = strings.TrimSpace(payload.Credential)
	if payload.Credential == "" {
		return "", errors.New("NapCat returned an empty credential")
	}
	h.credential = payload.Credential
	h.credentialExpiresAt = time.Now().Add(napCatCredentialTTL)
	return h.credential, nil
}

func (h *NapCatLoginHandler) invalidateCredential(credential string) {
	h.credentialMu.Lock()
	defer h.credentialMu.Unlock()
	if h.credential == credential {
		h.credential = ""
		h.credentialExpiresAt = time.Time{}
	}
}

func (h *NapCatLoginHandler) do(ctx context.Context, path string, requestBody any, credential string, responseData any) error {
	body, err := json.Marshal(requestBody)
	if err != nil {
		return errors.New("could not encode NapCat request")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, h.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return errors.New("could not create NapCat request")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	if credential != "" {
		request.Header.Set("Authorization", "Bearer "+credential)
	}

	response, err := h.client.Do(request)
	if err != nil {
		return errors.New("NapCat transport failed")
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return &napCatHTTPError{status: response.StatusCode}
	}

	limited := io.LimitReader(response.Body, napCatMaxResponseBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil || len(data) > napCatMaxResponseBytes {
		return errors.New("invalid NapCat response")
	}
	var envelope napCatEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return errors.New("invalid NapCat response")
	}
	if envelope.Code != 0 {
		return &napCatAPIError{unauthorized: strings.EqualFold(strings.TrimSpace(envelope.Message), "unauthorized")}
	}
	if responseData == nil {
		return nil
	}
	if len(envelope.Data) == 0 || bytes.Equal(bytes.TrimSpace(envelope.Data), []byte("null")) {
		return errors.New("NapCat response data is missing")
	}
	if err := json.Unmarshal(envelope.Data, responseData); err != nil {
		return errors.New("invalid NapCat response data")
	}
	return nil
}

func isNapCatUnauthorized(err error) bool {
	var httpErr *napCatHTTPError
	if errors.As(err, &httpErr) && httpErr.unauthorized() {
		return true
	}
	var apiErr *napCatAPIError
	return errors.As(err, &apiErr) && apiErr.unauthorized
}

func parseNapCatAccounts(raw json.RawMessage) ([]NapCatAccount, error) {
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, errors.New("invalid NapCat account list")
	}
	accounts := make([]NapCatAccount, 0, len(entries))
	for _, entry := range entries {
		var payload napCatAccountPayload
		if err := json.Unmarshal(entry, &payload); err == nil && strings.TrimSpace(string(payload.UIN)) != "" {
			accounts = append(accounts, sanitizeNapCatAccount(payload))
			continue
		}
		var uin napCatString
		if err := json.Unmarshal(entry, &uin); err != nil || strings.TrimSpace(string(uin)) == "" {
			return nil, errors.New("invalid NapCat account")
		}
		accounts = append(accounts, NapCatAccount{UIN: strings.TrimSpace(string(uin))})
	}
	return accounts, nil
}

func sanitizeNapCatAccount(payload napCatAccountPayload) NapCatAccount {
	nickname := strings.TrimSpace(payload.NickName)
	if nickname == "" {
		nickname = strings.TrimSpace(payload.Nick)
	}
	avatarURL := strings.TrimSpace(payload.FaceURL)
	if avatarURL == "" {
		avatarURL = strings.TrimSpace(payload.AvatarURL)
	}
	return NapCatAccount{
		UIN:       strings.TrimSpace(string(payload.UIN)),
		Nickname:  nickname,
		AvatarURL: avatarURL,
		Online:    payload.Online,
	}
}
