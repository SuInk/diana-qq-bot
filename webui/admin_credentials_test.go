package webui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdminCredentialFirstRunHasNoDefaultEmail(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "admin-credentials.json")
	state, err := loadOrCreateAdminCredential(path, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if state.configured() || state.Email != "" || state.PasswordHash != "" || len(state.JWTSecret) < 32 || len(state.AccountID) < 32 {
		t.Fatalf("unexpected first-run state: %#v", state)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credential mode = %o, want 600", info.Mode().Perm())
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "admin@diana.local") || strings.Contains(string(content), `"password":`) {
		t.Fatalf("first-run credential contains a default account or plaintext password: %s", content)
	}
}

func TestAdminCredentialMigratesLegacyPlaintext(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "admin-credentials.json")
	legacyPassword := "legacy-password-with-more-than-32-characters"
	legacy := `{"username":"Owner@Example.COM","password":"` + legacyPassword + `"}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := loadOrCreateAdminCredential(path, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if state.Email != "owner@example.com" || !verifyAdminPassword(state.PasswordHash, legacyPassword) || len(state.JWTSecret) < 32 || len(state.AccountID) < 32 {
		t.Fatalf("unexpected migrated state: %#v", state)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), legacyPassword) || strings.Contains(string(content), `"password":`) || strings.Contains(string(content), `"username":`) {
		t.Fatalf("migration retained legacy plaintext fields: %s", content)
	}
	var stored persistedAdminCredential
	if err := json.Unmarshal(content, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Version != adminCredentialStoreVersion || stored.Email != "owner@example.com" || stored.PasswordHash == "" {
		t.Fatalf("unexpected migrated credential: %#v", stored)
	}
}

func TestAdminCredentialValidation(t *testing.T) {
	t.Parallel()
	if _, err := normalizeAdminEmail("not-an-email"); err == nil {
		t.Fatal("expected invalid email to be rejected")
	}
	if _, err := hashAdminPassword("too-short"); err == nil {
		t.Fatal("expected short password to be rejected")
	}
	if email, err := normalizeAdminEmail(" Owner+Bot@Example.com "); err != nil || email != "owner+bot@example.com" {
		t.Fatalf("normalized email = %q, err=%v", email, err)
	}
}

func TestAdminCredentialRejectsFutureStoreVersion(t *testing.T) {
	t.Parallel()
	_, _, err := decodeAdminCredential(persistedAdminCredential{
		Version:      adminCredentialStoreVersion + 1,
		Email:        testAdminEmail,
		PasswordHash: "$2a$10$untrusted.future.version.hash.value",
		JWTSecret:    "future-jwt-secret-with-at-least-thirty-two-characters",
		AccountID:    "future-account-id-with-at-least-thirty-two-characters",
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported admin credential store version") {
		t.Fatalf("future version error = %v", err)
	}
}
