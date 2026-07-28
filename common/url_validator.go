package common

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/constant"
)

// ParseAbsoluteHTTPURL validates the common syntax requirements for outbound
// HTTP targets. It deliberately does not apply deployment-specific
// domain/IP policy; callers that fetch user-controlled URLs must still apply
// SSRF validation before dispatch.
func ParseAbsoluteHTTPURL(rawURL string) (*url.URL, error) {
	if rawURL == "" || rawURL != strings.TrimSpace(rawURL) {
		return nil, fmt.Errorf("URL must not be empty or contain surrounding whitespace")
	}
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL format: %w", err)
	}
	parsedURL.Scheme = strings.ToLower(parsedURL.Scheme)
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return nil, fmt.Errorf("invalid URL scheme: only http and https are allowed")
	}
	if parsedURL.Host == "" || parsedURL.Hostname() == "" || parsedURL.Opaque != "" {
		return nil, fmt.Errorf("URL must be absolute and include a host")
	}
	if portText := parsedURL.Port(); portText != "" {
		port, err := strconv.Atoi(portText)
		if err != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("URL contains an invalid port")
		}
	}
	if parsedURL.User != nil {
		return nil, fmt.Errorf("URL credentials are not allowed")
	}
	if parsedURL.Fragment != "" || parsedURL.RawFragment != "" {
		return nil, fmt.Errorf("URL fragments are not allowed")
	}
	return parsedURL, nil
}

// ValidateRedirectURL validates that a redirect URL is safe to use.
// It checks that:
//   - The URL is properly formatted
//   - The scheme is either http or https
//   - The domain is in the trusted domains list (exact match or subdomain)
//
// Returns nil if the URL is valid and trusted, otherwise returns an error
// describing why the validation failed.
func ValidateRedirectURL(rawURL string) error {
	// Parse the URL
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL format: %s", err.Error())
	}

	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return fmt.Errorf("invalid URL scheme: only http and https are allowed")
	}

	domain := strings.ToLower(parsedURL.Hostname())

	for _, trustedDomain := range constant.TrustedRedirectDomains {
		if domain == trustedDomain || strings.HasSuffix(domain, "."+trustedDomain) {
			return nil
		}
	}

	return fmt.Errorf("domain %s is not in the trusted domains list", domain)
}
