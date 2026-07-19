package qqbot

import (
	"context"
	"net/url"
	"os"
	"strings"

	"diana-qq-bot/model/netguard"
)

func hostMatchesDomain(host string, domains ...string) bool {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	for _, domain := range domains {
		domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
		if domain != "" && (host == domain || strings.HasSuffix(host, "."+domain)) {
			return true
		}
	}
	return false
}

func urlMatchesDomain(raw string, domains ...string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return false
	}
	return hostMatchesDomain(parsed.Hostname(), domains...)
}

func configuredTwitterResolverURL(ctx context.Context, raw string) string {
	configured := strings.TrimSpace(os.Getenv("DIANA_TWITTER_RESOLVER_API"))
	if configured == "" {
		return ""
	}
	if strings.Contains(configured, "{url}") {
		configured = strings.ReplaceAll(configured, "{url}", url.QueryEscape(raw))
	} else if strings.Contains(configured, "%s") {
		configured = strings.ReplaceAll(configured, "%s", url.QueryEscape(raw))
	} else {
		parsed, err := url.Parse(configured)
		if err != nil {
			return ""
		}
		query := parsed.Query()
		query.Set("content", raw)
		parsed.RawQuery = query.Encode()
		configured = parsed.String()
	}
	parsed, err := url.Parse(configured)
	if err != nil || parsed.Scheme != "https" {
		return ""
	}
	if err := netguard.ValidatePublicURL(ctx, configured); err != nil {
		return ""
	}
	return configured
}
