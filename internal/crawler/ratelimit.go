package crawler

import (
	"context"
	"strings"
	"sync"

	"golang.org/x/time/rate"
)

// HostRateLimiter manages token-bucket rate limiters partitioned per hostname.
type HostRateLimiter struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
	rps      rate.Limit
	burst    int
}

// NewHostRateLimiter constructs a HostRateLimiter with the specified requests-per-second (rps)
// and token burst allowance per host. If rps <= 0, rate limiting is effectively disabled.
func NewHostRateLimiter(rps float64, burst int) *HostRateLimiter {
	if burst <= 0 {
		burst = 1
	}
	return &HostRateLimiter{
		limiters: make(map[string]*rate.Limiter),
		rps:      rate.Limit(rps),
		burst:    burst,
	}
}

// SetRate updates the rate limit dynamically (e.g. from robots.txt crawl-delay).
func (h *HostRateLimiter) SetRate(rps float64) {
	if h == nil || rps <= 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.rps = rate.Limit(rps)
	for _, l := range h.limiters {
		l.SetLimit(h.rps)
	}
}

// Wait blocks until the token-bucket for the given host permits a request,
// or until the provided context is canceled.
func (h *HostRateLimiter) Wait(ctx context.Context, host string) error {
	if h == nil || h.rps <= 0 {
		return nil
	}

	cleanHost := strings.ToLower(strings.TrimSpace(host))
	if colon := strings.IndexByte(cleanHost, ':'); colon != -1 {
		cleanHost = cleanHost[:colon]
	}

	h.mu.Lock()
	limiter, exists := h.limiters[cleanHost]
	if !exists {
		limiter = rate.NewLimiter(h.rps, h.burst)
		h.limiters[cleanHost] = limiter
	}
	h.mu.Unlock()

	return limiter.Wait(ctx)
}
