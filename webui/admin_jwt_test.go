package webui

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestAdminJWTSignVerifyTamperAndExpiry(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	secret := "jwt-secret-with-at-least-thirty-two-characters"
	claims := adminJWTClaims{
		Issuer:    adminJWTIssuer,
		Subject:   "owner@example.com",
		SessionID: "session-id",
		TokenID:   "access-id",
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(time.Minute).Unix(),
	}
	token, err := signAdminJWT(secret, claims)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(token, ".") != 2 {
		t.Fatalf("token is not a compact JWT: %q", token)
	}
	verified, err := verifyAdminJWT(secret, token, now)
	if err != nil || verified != claims {
		t.Fatalf("verified claims = %#v, err=%v", verified, err)
	}
	if _, err := verifyAdminJWT(secret, token+"x", now); !errors.Is(err, errAdminJWTInvalid) {
		t.Fatalf("tampered token error = %v", err)
	}
	if _, err := verifyAdminJWT(secret, token, now.Add(time.Minute)); !errors.Is(err, errAdminJWTExpired) {
		t.Fatalf("expired token error = %v", err)
	}
}
