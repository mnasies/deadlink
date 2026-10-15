package crawler

import (
	"context"
	"io"
	"net/http"
	"time"
)

// DefaultUserAgent sent with link verification probes.
const DefaultUserAgent = "deadlink/1.0 (+https://github.com/mnasies/deadlink)"

// Result represents the outcome of probing a single link.
type Result struct {
	URL        string        `json:"url"`
	ParentURL  string        `json:"parent_url"`
	StatusCode int           `json:"status_code"`
	Duration   time.Duration `json:"duration_ms"`
	Error      error         `json:"error,omitempty"`
	Broken     bool          `json:"broken"`
}

// LinkChecker encapsulates the HTTP client and configuration for probing links.
type LinkChecker struct {
	client    *http.Client
	userAgent string
}

// NewLinkChecker constructs a LinkChecker. If client is nil, http.DefaultClient is used.
func NewLinkChecker(client *http.Client, userAgent string) *LinkChecker {
	if client == nil {
		client = &http.Client{
			Timeout: 10 * time.Second,
		}
	}
	if userAgent == "" {
		userAgent = DefaultUserAgent
	}
	return &LinkChecker{
		client:    client,
		userAgent: userAgent,
	}
}

// Check probes targetURL with an HTTP request and returns a Result.
// It leverages context.Context for cancellation and timeouts.
func (c *LinkChecker) Check(ctx context.Context, targetURL, parentURL string) Result {
	start := time.Now()
	res := Result{
		URL:       targetURL,
		ParentURL: parentURL,
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		res.Duration = time.Since(start)
		res.Error = err
		res.Broken = true
		return res
	}

	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.client.Do(req)
	res.Duration = time.Since(start)

	if err != nil {
		// Network-level error: DNS failure, connection refused, TLS handshake error, or timeout.
		res.Error = err
		res.Broken = true
		return res
	}

	// CRITICAL RESOURCE LIFECYCLE (Go defer mechanics):
	// Go does not have C++ RAII destructors. If resp.Body is not explicitly closed,
	// the underlying operating system file descriptor (socket) leaks. Over thousands
	// of links, this would trigger 'too many open files' (EMFILE).
	// defer ensures resp.Body.Close() runs unconditionally when Check returns.
	defer resp.Body.Close()

	// Drain a small portion of the body (up to 4KB) before closing.
	// In HTTP/1.1 and HTTP/2, draining the response body allows Go's http.Transport
	// to reuse the underlying TCP socket in its idle connection pool (Keep-Alive),
	// avoiding the overhead of repeated 3-way TCP handshakes and TLS negotiations.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	res.StatusCode = resp.StatusCode

	// HTTP status codes in the 4xx (client error) and 5xx (server error) ranges
	// signify broken links. 2xx (Success) and 3xx (Redirects handled by client) are alive.
	if resp.StatusCode >= 400 {
		res.Broken = true
	}

	return res
}
