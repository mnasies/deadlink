package crawler

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// DefaultUserAgent sent with link verification probes.
const DefaultUserAgent = "deadlink/1.0 (+https://github.com/mnasies/deadlink)"

// Sentinel errors for redirect handling.
var (
	ErrRedirectLoop      = errors.New("redirect loop detected")
	ErrTooManyRedirects  = errors.New("too many redirects")
)

// Result represents the outcome of probing a single link.
type Result struct {
	URL           string        `json:"url"`
	ParentURL     string        `json:"parent_url"`
	FinalURL      string        `json:"final_url,omitempty"`      // Destination URL after following redirects
	StatusCode    int           `json:"status_code"`
	Duration      time.Duration `json:"duration_ms"`
	Error         error         `json:"error,omitempty"`
	Broken        bool          `json:"broken"`
	MethodUsed    string        `json:"method_used,omitempty"`    // "HEAD" or "GET" (fallback)
	Attempts      int           `json:"attempts"`                 // Number of probe attempts made (1 + retries)
	RedirectCount int           `json:"redirect_count,omitempty"` // Number of redirect hops followed
}

// LinkChecker encapsulates the HTTP client and configuration for probing links.
type LinkChecker struct {
	client         *http.Client
	userAgent      string
	maxRetries     int           // Max retry attempts for transient errors (default 2)
	initialBackoff time.Duration // Base backoff delay (default 100ms)
	maxRedirects   int           // Max redirect hops allowed (default 10)
}

// NewLinkChecker constructs a LinkChecker with custom redirect policy and timeout.
func NewLinkChecker(client *http.Client, userAgent string) *LinkChecker {
	if userAgent == "" {
		userAgent = DefaultUserAgent
	}

	checker := &LinkChecker{
		userAgent:      userAgent,
		maxRetries:     2,
		initialBackoff: 100 * time.Millisecond,
		maxRedirects:   10,
	}

	if client == nil {
		client = &http.Client{
			Timeout: 10 * time.Second,
		}
	}

	// Install custom CheckRedirect policy if none is explicitly provided on the client.
	// This enables early circular redirect loop detection and hop limit enforcement.
	if client.CheckRedirect == nil {
		client.CheckRedirect = checker.checkRedirectPolicy
	}

	checker.client = client
	return checker
}

// SetRedirectLimit configures the maximum allowed redirect hops.
func (c *LinkChecker) SetRedirectLimit(maxRedirects int) {
	if maxRedirects >= 0 {
		c.maxRedirects = maxRedirects
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

// checkRedirectPolicy is an http.Client.CheckRedirect function that tracks redirect
// history, detects circular redirect loops early, and limits total hops.
func (c *LinkChecker) checkRedirectPolicy(req *http.Request, via []*http.Request) error {
	// 1. Enforce maximum redirect hops
	if len(via) >= c.maxRedirects {
		return ErrTooManyRedirects
	}

	// 2. Detect circular loops (e.g. /a -> /b -> /a)
	incomingURL := req.URL.String()
	for _, prev := range via {
		if strings.EqualFold(prev.URL.String(), incomingURL) {
			return ErrRedirectLoop
		}
	}

	return nil
}

// Check probes targetURL with an HTTP request and returns a Result.
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

		// Permanent errors or fatal redirect loops must NOT be retried
		if !isTransient(res.StatusCode, res.Error) {
			return res
		}

		// If we've reached our maximum allowed retries, return the final failure
		if attempt == c.maxRetries {
			return res
		}

		// Exponential backoff delay
		delay := c.initialBackoff * (1 << attempt)

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
		res.FinalURL = resp.Request.URL.String()
		res.RedirectCount = calculateRedirects(resp.Request)

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
	} else if isRedirectError(err) {
		// Redirect loops or max redirects abort immediately with clean unwrapped errors
		res.Error = unwrapRedirectError(err)
		res.Broken = true
		res.MethodUsed = http.MethodHead
		return res
	}

	// 2. Fall back to GET
	getResp, getErr := c.doRequest(ctx, http.MethodGet, targetURL)
	res.MethodUsed = http.MethodGet

	if getErr != nil {
		if isRedirectError(getErr) {
			res.Error = unwrapRedirectError(getErr)
		} else if err != nil {
			res.Error = err
		} else {
			res.Error = getErr
		}
		res.Broken = true
		return res
	}

	res.FinalURL = getResp.Request.URL.String()
	res.RedirectCount = calculateRedirects(getResp.Request)

	// Check for Retry-After header on 429 responses if available
	if getResp.StatusCode == http.StatusTooManyRequests {
		if retryAfter := getResp.Header.Get("Retry-After"); retryAfter != "" {
			if seconds, parseErr := strconv.Atoi(retryAfter); parseErr == nil && seconds > 0 {
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

// calculateRedirects estimates the number of redirects taken by checking request context.
// In Go net/http, resp.Request is the final request after following redirects.
// If resp.Request.Response is present, each chained response indicates one redirect hop.
func calculateRedirects(req *http.Request) int {
	hops := 0
	curr := req
	for curr != nil && curr.Response != nil {
		hops++
		curr = curr.Response.Request
	}
	return hops
}

// isRedirectError tests whether err is caused by a redirect loop or exceeding redirect limits.
func isRedirectError(err error) bool {
	return errors.Is(err, ErrRedirectLoop) || errors.Is(err, ErrTooManyRedirects)
}

// unwrapRedirectError extracts the underlying ErrRedirectLoop or ErrTooManyRedirects
// from Go's *url.Error wrapper.
func unwrapRedirectError(err error) error {
	if errors.Is(err, ErrRedirectLoop) {
		return ErrRedirectLoop
	}
	if errors.Is(err, ErrTooManyRedirects) {
		return ErrTooManyRedirects
	}
	return err
}

// shouldFallbackToGET returns true if the status code suggests the server
// does not support or rejects HEAD requests, but might accept GET.
func shouldFallbackToGET(statusCode int) bool {
	switch statusCode {
	case http.StatusMethodNotAllowed, // 405
		http.StatusNotImplemented,    // 501
		http.StatusForbidden:         // 403
		return true
	default:
		return false
	}
}

// isTransient determines whether a failure is transient (warranting a retry)
// or permanent (such as 404 Not Found, which should never be retried).
func isTransient(statusCode int, err error) bool {
	// Redirect errors are permanent structural failures
	if isRedirectError(err) {
		return false
	}

	switch statusCode {
	case http.StatusTooManyRequests,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	}

	if err != nil {
		if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) || errors.Is(err, io.ErrUnexpectedEOF) {
			return true
		}
		var netErr net.Error
		if errors.As(err, &netErr) && (netErr.Timeout()) {
			return true
		}
	}

	return false
}
