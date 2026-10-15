package crawler

import (
	"context"
	"strings"
	"sync"

	"golang.org/x/time/rate"
)

// HostRateLimiter manages token-bucket rate limiters partitioned per hostname.
// This ensures that requests to one domain do not bottleneck requests to another,
// while preventing any single host from being hammered by concurrent workers.
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

// Wait blocks until the token-bucket for the given host permits a request,
// or until the provided context is canceled.
func (h *HostRateLimiter) Wait(ctx context.Context, host string) error {
	if h == nil || h.rps <= 0 {
		return nil
	}

	cleanHost := strings.ToLower(strings.TrimSpace(host))
	// Strip port numbers (e.g., "example.com:8080" -> "example.com")
	if colon := strings.IndexByte(cleanHost, ':'); colon != -1 {
		cleanHost = cleanHost[:colon]
	}

	// Double-check / get-or-create limiter under lock
	h.mu.Lock()
	limiter, exists := h.limiters[cleanHost]
	if !exists {
		limiter = rate.NewLimiter(h.rps, h.burst)
		h.limiters[cleanHost] = limiter
	}
	h.mu.Unlock()

	// Wait outside the lock so other goroutines targeting different hosts can proceed immediately.
	// rate.Limiter is internally thread-safe and respects context cancellation.
	return limiter.Wait(ctx)
}
