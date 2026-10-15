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
	MethodUsed string        `json:"method_used,omitempty"` // "HEAD" or "GET" (fallback)
}

// LinkChecker encapsulates the HTTP client and configuration for probing links.
type LinkChecker struct {
	client    *http.Client
	userAgent string
}

// NewLinkChecker constructs a LinkChecker. If client is nil, a sane default is provided.
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
// It uses a HEAD-first strategy: sends a cheap HEAD request, and if the target
// server rejects it (405 Method Not Allowed, 501 Not Implemented) or returns 403,
// it automatically falls back to a GET request before declaring the link dead.
func (c *LinkChecker) Check(ctx context.Context, targetURL, parentURL string) Result {
	start := time.Now()
	res := Result{
		URL:       targetURL,
		ParentURL: parentURL,
	}

	// Step 1: Issue cheap HEAD request (header probe, no body transfer)
	resp, err := c.doRequest(ctx, http.MethodHead, targetURL)
	if err == nil {
		res.StatusCode = resp.StatusCode
		res.MethodUsed = http.MethodHead

		// Clean up socket descriptor and drain
		c.drainAndClose(resp.Body)

		// If HEAD succeeded with a 2xx or 3xx status, link is healthy!
		if resp.StatusCode < 400 {
			res.Duration = time.Since(start)
			res.Broken = false
			return res
		}

		// If the server rejected HEAD specifically with:
		// - 405 Method Not Allowed
		// - 501 Not Implemented
		// - 403 Forbidden (some WAFs/CDNs block HEAD but allow GET)
		// we must not declare it dead yet: retry with GET!
		if !shouldFallbackToGET(resp.StatusCode) {
			// Definite failure like 404 Not Found, 410 Gone, or 500 Server Error
			res.Duration = time.Since(start)
			res.Broken = true
			return res
		}
	}

	// Step 2: Fallback to GET
	// Reached if HEAD returned 405/501/403 OR if HEAD encountered a network transport error
	getResp, getErr := c.doRequest(ctx, http.MethodGet, targetURL)
	res.Duration = time.Since(start)
	res.MethodUsed = http.MethodGet

	if getErr != nil {
		// Both HEAD and GET failed
		if err != nil {
			res.Error = err
		} else {
			res.Error = getErr
		}
		res.Broken = true
		return res
	}

	defer c.drainAndClose(getResp.Body)

	res.StatusCode = getResp.StatusCode
	res.Error = nil

	if getResp.StatusCode >= 400 {
		res.Broken = true
	} else {
		res.Broken = false
	}

	return res
}

// doRequest constructs and executes an HTTP request with context and headers.
func (c *LinkChecker) doRequest(ctx context.Context, method, targetURL string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, targetURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	return c.client.Do(req)
}

// drainAndClose consumes up to 4KB of body to allow connection reuse, then closes the socket.
func (c *LinkChecker) drainAndClose(body io.ReadCloser) {
	if body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 4096))
	_ = body.Close()
}

// shouldFallbackToGET returns true if the status code suggests the server
// does not support or rejects HEAD requests, but might accept GET.
func shouldFallbackToGET(statusCode int) bool {
	switch statusCode {
	case http.StatusMethodNotAllowed, // 405
		http.StatusNotImplemented,    // 501
		http.StatusForbidden:         // 403 (many CDNs block HEAD)
		return true
	default:
		return false
	}
}
