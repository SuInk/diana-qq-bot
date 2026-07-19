package netguard

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestValidatePublicURLRejectsPrivateTargets(t *testing.T) {
	t.Setenv(allowPrivateFetchesEnv, "false")
	credentialURL := (&url.URL{
		Scheme: "https",
		User:   url.UserPassword("test-user", "test-password"),
		Host:   "example.com",
	}).String()
	for _, raw := range []string{
		"http://127.0.0.1/admin",
		"http://[::1]/admin",
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.1/",
		"http://localhost/",
		"file:///etc/passwd",
		credentialURL,
	} {
		if err := ValidatePublicURL(context.Background(), raw); err == nil {
			t.Fatalf("ValidatePublicURL(%q) error = nil", raw)
		}
	}
}

func TestValidatePublicURLAcceptsPublicLiteral(t *testing.T) {
	t.Setenv(allowPrivateFetchesEnv, "false")
	if err := ValidatePublicURL(context.Background(), "https://1.1.1.1/"); err != nil {
		t.Fatalf("public URL rejected: %v", err)
	}
}

func TestPublicHTTPClientRevalidatesRedirectTargets(t *testing.T) {
	t.Setenv(allowPrivateFetchesEnv, "false")
	client := NewPublicHTTPClient(time.Second)
	redirect, err := http.NewRequest(http.MethodGet, "http://127.0.0.1/private", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.CheckRedirect(redirect, nil); err == nil {
		t.Fatal("redirect to a private address was accepted")
	}

	publicRedirect, err := http.NewRequest(http.MethodGet, "https://1.1.1.1/public", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.CheckRedirect(publicRedirect, nil); err != nil {
		t.Fatalf("public redirect rejected: %v", err)
	}
}

func TestStrictValidationIgnoresPrivateFetchOptIn(t *testing.T) {
	t.Setenv(allowPrivateFetchesEnv, "true")
	if err := ValidatePublicURL(context.Background(), "http://127.0.0.1/trusted-media"); err != nil {
		t.Fatalf("explicit trusted private fetch was rejected: %v", err)
	}
	if err := ValidatePublicURLStrict(context.Background(), "http://127.0.0.1/agent-target"); err == nil {
		t.Fatal("strict validation accepted a private agent target")
	}
}
