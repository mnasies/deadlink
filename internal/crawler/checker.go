package crawler

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"syscall"
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
	Attempts   int           `json:"attempts"`              // Number of probe attempts made (1 + retries)
}

// LinkChecker encapsulates the HTTP client and configuration for probing links.
type LinkChecker struct {
	client         *http.Client
	userAgent      string
	maxRetries     int           // Max retry attempts for transient errors (default 2)
	initialBackoff time.Duration // Base backoff delay (default 100ms)
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
		client:         client,
		userAgent:      userAgent,
		maxRetries:     2,
		initialBackoff: 100 * time.Millisecond,
	}
}

// SetRetryPolicy customizes the retry behavior for transient errors.
func (c *LinkChecker) SetRetryPolicy(maxRetries int, initialBackoff time.Duration) {
	if maxRetries >= 0 {
		c.maxRetries = maxRetries
	}
	if initialBackoff > 0 {
		c.initialBackoff = initialBackoff
	}
}

// Check probes targetURL with an HTTP request and returns a Result.
// It combines a HEAD-first strategy with automatic GET fallback and exponential
// backoff retries for transient failures (429, 502, 503, 504, connection resets).
func (c *LinkChecker) Check(ctx context.Context, targetURL, parentURL string) Result {
	start := time.Now()
	var lastResult Result

	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		// Stop retrying if the parent context has been canceled
		if ctx.Err() != nil {
			lastResult.Error = ctx.Err()
			lastResult.Broken = true
			lastResult.Attempts = attempt + 1
			lastResult.Duration = time.Since(start)
			return lastResult
		}

		res := c.probeOnce(ctx, targetURL, parentURL)
		res.Attempts = attempt + 1
		res.Duration = time.Since(start)
		lastResult = res

		// If healthy (2xx or 3xx), return immediately
		if !res.Broken {
			return res
		}

		// Permanent errors (like 404 Not Found, 410 Gone, 400 Bad Request) must NOT be retried
		if !isTransient(res.StatusCode, res.Error) {
			return res
		}

		// If we've reached our maximum allowed retries, return the final failure
		if attempt == c.maxRetries {
			return res
		}

		// Calculate backoff delay with exponential scaling: initial * 2^attempt
		delay := c.initialBackoff * (1 << attempt)

		// Context-aware sleep: unblocks immediately if ctx is canceled during backoff
		select {
		case <-time.After(delay):
			// Proceed to next retry attempt
		case <-ctx.Done():
			lastResult.Error = ctx.Err()
			lastResult.Broken = true
			lastResult.Duration = time.Since(start)
			return lastResult
		}
	}

	return lastResult
}

// probeOnce performs a single HEAD-first probe with GET fallback if rejected.
func (c *LinkChecker) probeOnce(ctx context.Context, targetURL, parentURL string) Result {
	res := Result{
		URL:       targetURL,
		ParentURL: parentURL,
	}

	// 1. Issue HEAD request
	resp, err := c.doRequest(ctx, http.MethodHead, targetURL)
	if err == nil {
		res.StatusCode = resp.StatusCode
		res.MethodUsed = http.MethodHead
		c.drainAndClose(resp.Body)

		if resp.StatusCode < 400 {
			res.Broken = false
			return res
		}

		// Only fall back to GET if HEAD was specifically rejected by method
		if !shouldFallbackToGET(resp.StatusCode) {
			res.Broken = true
			return res
		}
	}

	// 2. Fall back to GET
	getResp, getErr := c.doRequest(ctx, http.MethodGet, targetURL)
	res.MethodUsed = http.MethodGet

	if getErr != nil {
		if err != nil {
			res.Error = err
		} else {
			res.Error = getErr
		}
		res.Broken = true
		return res
	}

	// Check for Retry-After header on 429 responses if available
	if getResp.StatusCode == http.StatusTooManyRequests {
		if retryAfter := getResp.Header.Get("Retry-After"); retryAfter != "" {
			if seconds, parseErr := strconv.Atoi(retryAfter); parseErr == nil && seconds > 0 {
				// Cap maximum header sleep to 5s to avoid holding workers forever
				if seconds > 5 {
					seconds = 5
				}
				c.initialBackoff = time.Duration(seconds) * time.Second
			}
		}
	}

	defer c.drainAndClose(getResp.Body)

	res.StatusCode = getResp.StatusCode
	res.Error = nil
	res.Broken = getResp.StatusCode >= 400

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

// isTransient determines whether a failure is transient (warranting a retry)
// or permanent (such as 404 Not Found, which should never be retried).
func isTransient(statusCode int, err error) bool {
	// Transient HTTP status codes
	switch statusCode {
	case http.StatusTooManyRequests,     // 429 Rate limited
		http.StatusBadGateway,          // 502 Upstream glitch
		http.StatusServiceUnavailable,  // 503 Overloaded
		http.StatusGatewayTimeout:       // 504 Timeout
		return true
	}

	// Network-level transient socket errors
	if err != nil {
		// Connection resets (ECONNRESET) or broken pipes
		if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) || errors.Is(err, io.ErrUnexpectedEOF) {
			return true
		}
		// Go net.Error with Timeout() or Temporary()
		var netErr net.Error
		if errors.As(err, &netErr) && (netErr.Timeout()) {
			return true
		}
	}

	return false
}
