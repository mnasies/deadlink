package crawler

import (
	"errors"
	"net/url"
	"strings"
)

// Sentinel errors for URL validation and resolution.
// In Go, sentinel errors are package-level exported values (starting with Err)
// that callers can check using errors.Is(err, ErrUnsupportedScheme).
var (
	ErrEmptyURL          = errors.New("empty URL")
	ErrUnsupportedScheme = errors.New("unsupported scheme: only http and https are allowed")
)

// NormalizeURL takes a raw URL string, validates its scheme, strips URL fragments
// (e.g. #section), normalizes the hostname to lowercase, and returns a canonical
// URL string suitable for deduplication in a visited set.
func NormalizeURL(rawURL string) (string, error) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return "", ErrEmptyURL
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", err
	}

	// Validate scheme: only http and https are supported.
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", ErrUnsupportedScheme
	}
	parsed.Scheme = scheme

	// Hostnames in DNS are case-insensitive; normalize to lowercase.
	parsed.Host = strings.ToLower(parsed.Host)

	// Strip fragments: #section is a client-side navigation anchor and does not
	// identify a distinct network resource on the server.
	parsed.Fragment = ""
	parsed.RawFragment = ""

	// Normalize trailing slashes:
	// A root path of "" or "/" should be treated identically (e.g. "http://example.com" vs "http://example.com/").
	// For deeper paths, strip trailing slashes (except root) to avoid duplicate visits to "/about" and "/about/".
	if parsed.Path == "" || parsed.Path == "/" {
		parsed.Path = "/"
	} else {
		parsed.Path = strings.TrimRight(parsed.Path, "/")
	}

	return parsed.String(), nil
}

// ResolveURL takes a base page URL and a reference string (which may be absolute,
// root-relative, document-relative, or protocol-relative), resolves it against the
// base, normalizes it, and returns the canonical URL string.
func ResolveURL(base *url.URL, ref string) (string, error) {
	if base == nil {
		return "", errors.New("base URL cannot be nil")
	}

	trimmedRef := strings.TrimSpace(ref)
	if trimmedRef == "" {
		return "", ErrEmptyURL
	}

	parsedRef, err := url.Parse(trimmedRef)
	if err != nil {
		return "", err
	}

	// ResolveReference handles:
	// - Absolute paths ("/about" -> "http://example.com/about")
	// - Relative paths ("team.html" -> "http://example.com/company/team.html")
	// - Parent traversals ("../contact" -> "http://example.com/contact")
	// - Protocol-relative ("//cdn.example.com/lib.js" -> "http://cdn.example.com/lib.js")
	// - Already absolute URLs ("https://google.com" -> remains "https://google.com")
	resolved := base.ResolveReference(parsedRef)

	return NormalizeURL(resolved.String())
}

// IsInternal determines whether a target URL belongs to the same domain/host
// as the base URL. Internal links are crawled recursively, while external links
// are only probed for HTTP availability without further traversal.
func IsInternal(baseURL, targetURL *url.URL) bool {
	if baseURL == nil || targetURL == nil {
		return false
	}

	// Hostname() strips any port numbers (e.g. "localhost:8080" -> "localhost")
	baseHost := strings.ToLower(baseURL.Hostname())
	targetHost := strings.ToLower(targetURL.Hostname())

	return baseHost != "" && baseHost == targetHost
}
