package webui

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const adminJWTIssuer = "diana-qq-bot-webui"

var (
	errAdminJWTInvalid = errors.New("invalid admin access token")
	errAdminJWTExpired = errors.New("admin access token expired")
)

type adminJWTClaims struct {
	Issuer    string `json:"iss"`
	Subject   string `json:"sub"`
	SessionID string `json:"sid"`
	TokenID   string `json:"jti"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
}

func signAdminJWT(secret string, claims adminJWTClaims) (string, error) {
	header, err := json.Marshal(struct {
		Algorithm string `json:"alg"`
		Type      string `json:"typ"`
	}{Algorithm: "HS256", Type: "JWT"})
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	encodedHeader := base64.RawURLEncoding.EncodeToString(header)
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	unsigned := encodedHeader + "." + encodedPayload
	signature := adminJWTSignature(secret, unsigned)
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func verifyAdminJWT(secret, token string, now time.Time) (adminJWTClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return adminJWTClaims{}, errAdminJWTInvalid
	}
	headerBody, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return adminJWTClaims{}, errAdminJWTInvalid
	}
	var header struct {
		Algorithm string `json:"alg"`
		Type      string `json:"typ"`
	}
	if err := json.Unmarshal(headerBody, &header); err != nil || header.Algorithm != "HS256" || header.Type != "JWT" {
		return adminJWTClaims{}, errAdminJWTInvalid
	}
	provided, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return adminJWTClaims{}, errAdminJWTInvalid
	}
	expected := adminJWTSignature(secret, parts[0]+"."+parts[1])
	if !hmac.Equal(provided, expected) {
		return adminJWTClaims{}, errAdminJWTInvalid
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return adminJWTClaims{}, errAdminJWTInvalid
	}
	var claims adminJWTClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return adminJWTClaims{}, errAdminJWTInvalid
	}
	if claims.Issuer != adminJWTIssuer || strings.TrimSpace(claims.Subject) == "" || strings.TrimSpace(claims.SessionID) == "" || strings.TrimSpace(claims.TokenID) == "" || claims.IssuedAt <= 0 || claims.ExpiresAt <= claims.IssuedAt {
		return adminJWTClaims{}, errAdminJWTInvalid
	}
	if now.Unix() >= claims.ExpiresAt {
		return adminJWTClaims{}, errAdminJWTExpired
	}
	if claims.IssuedAt > now.Add(time.Minute).Unix() {
		return adminJWTClaims{}, fmt.Errorf("%w: issued in the future", errAdminJWTInvalid)
	}
	return claims, nil
}

func adminJWTSignature(secret, content string) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(content))
	return mac.Sum(nil)
}

func adminSHA256(value string) string {
	digest := sha256.Sum256([]byte(value))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}
