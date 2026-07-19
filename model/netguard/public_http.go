package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const allowPrivateFetchesEnv = "DIANA_ALLOW_PRIVATE_HTTP_FETCHES"

// NewPublicHTTPClient returns a client that only connects to public HTTP(S)
// destinations and revalidates every redirect target.
func NewPublicHTTPClient(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Untrusted URL fetches intentionally ignore process HTTP proxy variables.
	// A local proxy would otherwise become an SSRF hop outside this guard.
	transport.Proxy = nil
	transport.DialContext = guardedDialContext
	return &http.Client{
		Timeout:   timeout,
		Transport: &publicRoundTripper{base: transport},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			return ValidatePublicURL(req.Context(), req.URL.String())
		},
	}
}

type publicRoundTripper struct {
	base http.RoundTripper
}

func (t *publicRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil {
		return nil, errors.New("public HTTP request URL is required")
	}
	if err := ValidatePublicURL(req.Context(), req.URL.String()); err != nil {
		return nil, err
	}
	return t.base.RoundTrip(req)
}

// ValidatePublicURL rejects credentials and hosts that resolve to local,
// private, reserved, or non-unicast addresses.
func ValidatePublicURL(ctx context.Context, value string) error {
	return validatePublicURL(ctx, value, privateFetchesAllowed())
}

// ValidatePublicURLStrict always rejects private destinations, including when
// trusted media fetches were explicitly enabled for the process.
func ValidatePublicURLStrict(ctx context.Context, value string) error {
	return validatePublicURL(ctx, value, false)
}

func validatePublicURL(ctx context.Context, value string, allowPrivate bool) error {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("only http and https URLs are allowed")
	}
	if parsed.User != nil {
		return errors.New("URLs containing credentials are not allowed")
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if host == "" {
		return errors.New("URL host is required")
	}
	if allowPrivate {
		return nil
	}
	if blockedHostname(host) {
		return fmt.Errorf("access to local host %q is blocked", host)
	}
	if addr, parseErr := netip.ParseAddr(host); parseErr == nil {
		if unsafeAddress(addr, false) {
			return fmt.Errorf("access to private or reserved address %q is blocked", host)
		}
		return nil
	}

	lookupCtx, cancel := context.WithTimeout(contextOrBackground(ctx), 3*time.Second)
	defer cancel()
	addresses, err := net.DefaultResolver.LookupNetIP(lookupCtx, "ip", host)
	if err != nil {
		return fmt.Errorf("host resolution failed: %w", err)
	}
	if len(addresses) == 0 {
		return errors.New("host did not resolve to an address")
	}
	for _, address := range addresses {
		if unsafeAddress(address.Unmap(), true) {
			return fmt.Errorf("host %q resolves to a private or reserved address", host)
		}
	}
	return nil
}

func guardedDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if privateFetchesAllowed() {
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	host = strings.Trim(host, "[]")
	if blockedHostname(strings.ToLower(strings.TrimSuffix(host, "."))) {
		return nil, fmt.Errorf("access to local host %q is blocked", host)
	}
	addresses, err := net.DefaultResolver.LookupNetIP(contextOrBackground(ctx), "ip", host)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, address := range addresses {
		address = address.Unmap()
		if unsafeAddress(address, net.ParseIP(host) == nil) {
			continue
		}
		conn, dialErr := (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(address.String(), port))
		if dialErr == nil {
			return conn, nil
		}
		lastErr = dialErr
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("host %q has no allowed address", host)
}

func blockedHostname(host string) bool {
	if host == "localhost" || host == "localhost.localdomain" || host == "metadata.google.internal" {
		return true
	}
	for _, suffix := range []string{".localhost", ".local", ".internal", ".home.arpa"} {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return host == "host.docker.internal" || host == "gateway.docker.internal"
}

func unsafeAddress(addr netip.Addr, allowSyntheticDNS bool) bool {
	addr = addr.Unmap()
	if allowSyntheticDNS && netip.MustParsePrefix("198.18.0.0/15").Contains(addr) {
		// Transparent proxy/TUN clients on macOS commonly use RFC 2544 fake IPs.
		return false
	}
	if !addr.IsValid() || !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified() {
		return true
	}
	for _, prefix := range blockedPrefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:db8::/32"),
}

func privateFetchesAllowed() bool {
	allowed, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(allowPrivateFetchesEnv)))
	return err == nil && allowed
}

func contextOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
