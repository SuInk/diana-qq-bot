package webui

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

const (
	adminCredentialStoreVersion = 3
	adminPasswordMinLength      = 12
	adminPasswordMaxLength      = 1024
	adminEmailMaxLength         = 254
)

type persistedAdminCredential struct {
	Version      int    `json:"version,omitempty"`
	Email        string `json:"email,omitempty"`
	PasswordHash string `json:"password_hash,omitempty"`
	JWTSecret    string `json:"jwt_secret,omitempty"`
	AccountID    string `json:"account_id,omitempty"`

	// Version 1 stored browser credentials in plaintext. These fields are read
	// only for a one-way migration and are never written again.
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

type adminCredentialState struct {
	Email        string
	PasswordHash string
	JWTSecret    string
	AccountID    string
}

func (s adminCredentialState) configured() bool {
	return strings.TrimSpace(s.Email) != "" && strings.TrimSpace(s.PasswordHash) != ""
}

func loadOrCreateAdminCredential(path, environmentEmail, environmentPassword string) (adminCredentialState, error) {
	environmentEmail = strings.TrimSpace(environmentEmail)
	environmentPassword = strings.TrimSpace(environmentPassword)

	if strings.TrimSpace(path) == "" {
		return inMemoryAdminCredential(environmentEmail, environmentPassword)
	}

	body, err := os.ReadFile(path)
	if err == nil {
		var stored persistedAdminCredential
		if err := json.Unmarshal(body, &stored); err != nil {
			return adminCredentialState{}, fmt.Errorf("decode admin credentials: %w", err)
		}
		state, migrated, err := decodeAdminCredential(stored)
		if err != nil {
			return adminCredentialState{}, err
		}
		if migrated {
			if err := persistAdminCredential(path, state); err != nil {
				return adminCredentialState{}, err
			}
		}
		return state, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return adminCredentialState{}, fmt.Errorf("read admin credentials: %w", err)
	}

	secret, err := randomToken()
	if err != nil {
		return adminCredentialState{}, fmt.Errorf("generate admin JWT secret: %w", err)
	}
	accountID, err := randomToken()
	if err != nil {
		return adminCredentialState{}, fmt.Errorf("generate admin account id: %w", err)
	}
	state := adminCredentialState{JWTSecret: secret, AccountID: accountID}
	// An explicitly configured email keeps environment-managed deployments
	// backwards compatible. Without one, the owner completes first-run setup.
	if environmentEmail != "" && environmentPassword != "" {
		email, err := normalizeAdminEmail(environmentEmail)
		if err != nil {
			return adminCredentialState{}, err
		}
		hash, err := hashAdminPassword(environmentPassword)
		if err != nil {
			return adminCredentialState{}, err
		}
		state.Email = email
		state.PasswordHash = hash
	}
	if err := persistAdminCredential(path, state); err != nil {
		return adminCredentialState{}, err
	}
	return state, nil
}

func inMemoryAdminCredential(email, password string) (adminCredentialState, error) {
	if password == "" {
		return adminCredentialState{}, nil
	}
	secretDigest := adminSHA256("diana-admin-jwt-v3\x00" + password)
	state := adminCredentialState{
		JWTSecret: secretDigest,
		AccountID: adminSHA256("diana-admin-account-v3\x00" + secretDigest + "\x00" + strings.ToLower(email)),
	}
	if email == "" {
		return state, nil
	}
	normalized, err := normalizeAdminEmail(email)
	if err != nil {
		return adminCredentialState{}, err
	}
	hash, err := hashAdminPassword(password)
	if err != nil {
		return adminCredentialState{}, err
	}
	state.Email = normalized
	state.PasswordHash = hash
	return state, nil
}

func decodeAdminCredential(stored persistedAdminCredential) (adminCredentialState, bool, error) {
	if stored.Version > adminCredentialStoreVersion {
		return adminCredentialState{}, false, fmt.Errorf("unsupported admin credential store version %d", stored.Version)
	}
	if stored.Version >= 2 {
		if len(strings.TrimSpace(stored.JWTSecret)) < 32 {
			return adminCredentialState{}, false, fmt.Errorf("admin credential JWT secret must contain at least 32 characters")
		}
		state := adminCredentialState{
			Email:        strings.TrimSpace(stored.Email),
			PasswordHash: strings.TrimSpace(stored.PasswordHash),
			JWTSecret:    strings.TrimSpace(stored.JWTSecret),
			AccountID:    strings.TrimSpace(stored.AccountID),
		}
		migrated := stored.Version < adminCredentialStoreVersion
		if len(state.AccountID) < 32 {
			accountID, err := randomToken()
			if err != nil {
				return adminCredentialState{}, false, fmt.Errorf("generate admin account id: %w", err)
			}
			state.AccountID = accountID
			migrated = true
		}
		if state.Email != "" {
			email, err := normalizeAdminEmail(state.Email)
			if err != nil {
				return adminCredentialState{}, false, err
			}
			state.Email = email
		}
		if state.PasswordHash != "" {
			if _, err := bcrypt.Cost([]byte(state.PasswordHash)); err != nil {
				return adminCredentialState{}, false, fmt.Errorf("admin password hash is invalid: %w", err)
			}
		}
		if (state.Email == "") != (state.PasswordHash == "") {
			return adminCredentialState{}, false, fmt.Errorf("admin credentials must contain both email and password hash")
		}
		return state, migrated, nil
	}

	legacyPassword := strings.TrimSpace(stored.Password)
	if legacyPassword == "" {
		return adminCredentialState{}, false, fmt.Errorf("admin credentials password is empty")
	}
	legacyEmail := strings.TrimSpace(stored.Username)
	if legacyEmail == "" {
		return adminCredentialState{}, false, fmt.Errorf("admin credentials email is empty")
	}
	email, err := normalizeAdminEmail(legacyEmail)
	if err != nil {
		return adminCredentialState{}, false, err
	}
	hash, err := hashAdminPassword(legacyPassword)
	if err != nil {
		return adminCredentialState{}, false, err
	}
	secret, err := randomToken()
	if err != nil {
		return adminCredentialState{}, false, fmt.Errorf("generate admin JWT secret: %w", err)
	}
	accountID, err := randomToken()
	if err != nil {
		return adminCredentialState{}, false, fmt.Errorf("generate admin account id: %w", err)
	}
	return adminCredentialState{Email: email, PasswordHash: hash, JWTSecret: secret, AccountID: accountID}, true, nil
}

func persistAdminCredential(path string, state adminCredentialState) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	credential := persistedAdminCredential{
		Version:      adminCredentialStoreVersion,
		Email:        strings.TrimSpace(state.Email),
		PasswordHash: strings.TrimSpace(state.PasswordHash),
		JWTSecret:    strings.TrimSpace(state.JWTSecret),
		AccountID:    strings.TrimSpace(state.AccountID),
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create admin credentials directory: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".admin-credentials-*.json")
	if err != nil {
		return fmt.Errorf("create admin credentials: %w", err)
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("secure admin credentials: %w", err)
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(credential); err != nil {
		_ = file.Close()
		return fmt.Errorf("encode admin credentials: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close admin credentials: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace admin credentials: %w", err)
	}
	return nil
}

func normalizeAdminEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	if email == "" {
		return "", fmt.Errorf("email is required")
	}
	if len(email) > adminEmailMaxLength {
		return "", fmt.Errorf("email is too long")
	}
	parsed, err := mail.ParseAddress(email)
	if err != nil || !secureEqual(strings.ToLower(parsed.Address), email) {
		return "", fmt.Errorf("email is invalid")
	}
	return email, nil
}

func validateAdminPassword(password string) error {
	if len(password) < adminPasswordMinLength {
		return fmt.Errorf("password must contain at least %d characters", adminPasswordMinLength)
	}
	if len(password) > adminPasswordMaxLength {
		return fmt.Errorf("password is too long")
	}
	return nil
}

func hashAdminPassword(password string) (string, error) {
	if err := validateAdminPassword(password); err != nil {
		return "", err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash admin password: %w", err)
	}
	return string(hash), nil
}

func verifyAdminPassword(hash, password string) bool {
	if strings.TrimSpace(hash) == "" || len(password) > adminPasswordMaxLength {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
