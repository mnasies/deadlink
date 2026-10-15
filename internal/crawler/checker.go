package crawler

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// DefaultUserAgent sent with link verification probes.
const DefaultUserAgent = "deadlink/1.0 (+https://github.com/mnasies/deadlink)"

// Sentinel errors for redirect handling.
var (
	ErrRedirectLoop     = errors.New("redirect loop detected")
	ErrTooManyRedirects = errors.New("too many redirects")
)

// LinkStatus represents the classified health and reachability state of a link.
type LinkStatus string

const (
	StatusOK      LinkStatus = "OK"      // 2xx/3xx: Accessible, alive, and healthy
	StatusDead    LinkStatus = "DEAD"    // 404, 410, DNS NXDOMAIN, connection refused, redirect loop
	StatusBlocked LinkStatus = "BLOCKED" // Access blocked by bot protection, WAF, or access controls (403, 429, 401, 451, Cloudflare challenge)
	StatusTimeout LinkStatus = "TIMEOUT" // Network timeout, context deadline exceeded, or gateway timeout (504, 408)
	StatusError   LinkStatus = "ERROR"   // Server-side errors (500, 502, 503) or socket resets/broken pipe
)

// Result represents the outcome of probing a single link.
type Result struct {
	URL           string        `json:"url"`
	ParentURL     string        `json:"parent_url"`
	FinalURL      string        `json:"final_url,omitempty"`      // Destination URL after following redirects
	StatusCode    int           `json:"status_code"`
	Duration      time.Duration `json:"duration_ms"`
	Error         error         `json:"error,omitempty"`
	Status        LinkStatus    `json:"status"`                   // Granular classification (OK, DEAD, BLOCKED, TIMEOUT, ERROR)
	Broken        bool          `json:"broken"`                   // Kept for backwards compatibility: true if Status == StatusDead || Status == StatusError
	MethodUsed    string        `json:"method_used,omitempty"`    // "HEAD" or "GET" (fallback)
	Attempts      int           `json:"attempts"`                 // Number of probe attempts made (1 + retries)
	RedirectCount int           `json:"redirect_count,omitempty"` // Number of redirect hops followed
}

// IsOK returns true if the link is alive and responding normally.
func (r Result) IsOK() bool { return r.Status == StatusOK }

// IsDead returns true if the link is definitively broken or non-existent.
func (r Result) IsDead() bool { return r.Status == StatusDead }

// IsBlocked returns true if access was denied by bot protection, WAF, or authorization.
func (r Result) IsBlocked() bool { return r.Status == StatusBlocked }

// IsTimeout returns true if the probe failed due to network or gateway timeouts.
func (r Result) IsTimeout() bool { return r.Status == StatusTimeout }

// IsError returns true if the probe failed due to a server-side 5xx error or socket error.
func (r Result) IsError() bool { return r.Status == StatusError }

// LinkChecker encapsulates the HTTP client and configuration for probing links.
type LinkChecker struct {
	client         *http.Client
	userAgent      string
	maxRetries     int              // Max retry attempts for transient errors (default 2)
	initialBackoff time.Duration    // Base backoff delay (default 100ms)
	maxRedirects   int              // Max redirect hops allowed (default 10)
	rateLimiter    *HostRateLimiter // Token bucket limiter partitioned per hostname
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
	if client.CheckRedirect == nil {
		client.CheckRedirect = checker.checkRedirectPolicy
	}

	checker.client = client
	return checker
}

// SetRateLimiter configures per-host rate limiting on the checker.
func (c *LinkChecker) SetRateLimiter(rl *HostRateLimiter) {
	c.rateLimiter = rl
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
	if len(via) >= c.maxRedirects {
		return ErrTooManyRedirects
	}

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
		if ctx.Err() != nil {
			lastResult.Error = ctx.Err()
			lastResult.Status = ClassifyStatus(lastResult.StatusCode, ctx.Err(), nil)
			lastResult.Broken = true
			lastResult.Attempts = attempt + 1
			lastResult.Duration = time.Since(start)
			return lastResult
		}

		res := c.probeOnce(ctx, targetURL, parentURL)
		res.Attempts = attempt + 1
		res.Duration = time.Since(start)
		lastResult = res

		if res.Status == StatusOK {
			return res
		}

		if !isTransient(res.StatusCode, res.Error) {
			return res
		}

		if attempt == c.maxRetries {
			return res
		}

		delay := c.initialBackoff * (1 << attempt)

		select {
		case <-time.After(delay):
		case <-ctx.Done():
			lastResult.Error = ctx.Err()
			lastResult.Status = ClassifyStatus(lastResult.StatusCode, ctx.Err(), nil)
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
			res.Status = StatusOK
			res.Broken = false
			return res
		}

		if !shouldFallbackToGET(resp.StatusCode) {
			res.Status = ClassifyStatus(resp.StatusCode, nil, resp.Header)
			res.Broken = (res.Status == StatusDead || res.Status == StatusError)
			return res
		}
	} else if isRedirectError(err) {
		res.Error = unwrapRedirectError(err)
		res.Status = StatusDead
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
		res.Status = ClassifyStatus(0, res.Error, nil)
		res.Broken = (res.Status == StatusDead || res.Status == StatusError)
		return res
	}

	res.FinalURL = getResp.Request.URL.String()
	res.RedirectCount = calculateRedirects(getResp.Request)

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
	res.Status = ClassifyStatus(getResp.StatusCode, nil, getResp.Header)
	res.Broken = (res.Status == StatusDead || res.Status == StatusError)

	return res
}

// doRequest enforces per-host rate limiting, constructs the request, and executes it.
func (c *LinkChecker) doRequest(ctx context.Context, method, targetURL string) (*http.Response, error) {
	// Respect per-host token bucket before touching the network
	if c.rateLimiter != nil {
		if parsed, err := url.Parse(targetURL); err == nil && parsed.Hostname() != "" {
			if err := c.rateLimiter.Wait(ctx, parsed.Hostname()); err != nil {
				return nil, err
			}
		}
	}

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

func calculateRedirects(req *http.Request) int {
	hops := 0
	curr := req
	for curr != nil && curr.Response != nil {
		hops++
		curr = curr.Response.Request
	}
	return hops
}

func isRedirectError(err error) bool {
	return errors.Is(err, ErrRedirectLoop) || errors.Is(err, ErrTooManyRedirects)
}

func unwrapRedirectError(err error) error {
	if errors.Is(err, ErrRedirectLoop) {
		return ErrRedirectLoop
	}
	if errors.Is(err, ErrTooManyRedirects) {
		return ErrTooManyRedirects
	}
	return err
}

func shouldFallbackToGET(statusCode int) bool {
	switch statusCode {
	case http.StatusMethodNotAllowed,
		http.StatusNotImplemented,
		http.StatusForbidden:
		return true
	default:
		return false
	}
}

func isTransient(statusCode int, err error) bool {
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

// ClassifyStatus analyzes the HTTP status code, network error, and response headers
// to categorize the link into one of: StatusOK, StatusDead, StatusBlocked, StatusTimeout, or StatusError.
func ClassifyStatus(statusCode int, err error, header http.Header) LinkStatus {
	if err != nil {
		// 1. Timeout detection: context deadline exceeded or network timeout
		if errors.Is(err, context.DeadlineExceeded) {
			return StatusTimeout
		}
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return StatusTimeout
		}
		errStr := strings.ToLower(err.Error())
		if strings.Contains(errStr, "timeout") || strings.Contains(errStr, "deadline exceeded") {
			return StatusTimeout
		}

		// 2. Redirect anomalies (loops, exceeded hops) represent broken URL navigation
		if isRedirectError(err) {
			return StatusDead
		}

		// 3. DNS failure or connection refused indicates non-existent host or dead service
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
			return StatusDead
		}
		if errors.Is(err, syscall.ECONNREFUSED) || strings.Contains(errStr, "connection refused") || strings.Contains(errStr, "no such host") {
			return StatusDead
		}

		// 4. Other network failures (socket reset, broken pipe, TLS failure)
		return StatusError
	}

	// 2xx and 3xx are successful
	if statusCode >= 200 && statusCode < 400 {
		return StatusOK
	}

	// HTTP timeouts
	if statusCode == http.StatusGatewayTimeout || statusCode == http.StatusRequestTimeout {
		return StatusTimeout
	}

	// Bot protection / WAF / access controls
	if statusCode == http.StatusForbidden ||
		statusCode == http.StatusTooManyRequests ||
		statusCode == http.StatusUnauthorized ||
		statusCode == http.StatusUnavailableForLegalReasons {
		return StatusBlocked
	}

	// Cloudflare / WAF 503 challenge
	if statusCode == http.StatusServiceUnavailable && header != nil {
		serverHeader := strings.ToLower(header.Get("Server"))
		if strings.Contains(serverHeader, "cloudflare") ||
			header.Get("cf-mitigated") == "challenge" ||
			header.Get("cf-chl-bypass") != "" {
			return StatusBlocked
		}
	}

	// Permanent / definitive client-side broken states
	if statusCode == http.StatusNotFound ||
		statusCode == http.StatusGone ||
		statusCode == http.StatusBadRequest ||
		statusCode == http.StatusMethodNotAllowed ||
		statusCode == http.StatusNotAcceptable ||
		statusCode == http.StatusLengthRequired ||
		statusCode == http.StatusPreconditionFailed ||
		statusCode == http.StatusRequestURITooLong ||
		statusCode == http.StatusUnsupportedMediaType ||
		statusCode == http.StatusExpectationFailed ||
		statusCode == http.StatusTeapot ||
		statusCode == http.StatusMisdirectedRequest ||
		statusCode == http.StatusUnprocessableEntity ||
		statusCode == http.StatusLocked ||
		statusCode == http.StatusFailedDependency ||
		statusCode == http.StatusUpgradeRequired ||
		statusCode == http.StatusPreconditionRequired ||
		statusCode == http.StatusRequestHeaderFieldsTooLarge {
		return StatusDead
	}

	// Server errors (500, 502, 503, etc.)
	if statusCode >= 500 {
		return StatusError
	}

	// Any other 4xx not explicitly classified
	if statusCode >= 400 {
		return StatusDead
	}

	return StatusOK
}
