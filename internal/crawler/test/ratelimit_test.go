package crawler_test

import (
	"context"
	"testing"
	"time"

	"github.com/mnasies/deadlink/internal/crawler"
)

func TestHostRateLimiter_Wait(t *testing.T) {
	t.Run("throttles requests to the same host", func(t *testing.T) {
		// 10 requests per second = 1 token every 100ms
		limiter := crawler.NewHostRateLimiter(10.0, 1)

		ctx := context.Background()
		start := time.Now()

		// 3 calls to the same host:
		// Call 1: consumes initial burst token (0ms)
		// Call 2: waits ~100ms for 2nd token
		// Call 3: waits ~100ms for 3rd token
		for i := 0; i < 3; i++ {
			if err := limiter.Wait(ctx, "api.example.com"); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		}

		elapsed := time.Since(start)
		// Expected minimum wait is ~180ms
		if elapsed < 180*time.Millisecond {
			t.Errorf("expected at least 180ms for 3 rate-limited calls, got %v", elapsed)
		}
	})

	t.Run("independent buckets for different hosts", func(t *testing.T) {
		// 5 requests per second (200ms per token)
		limiter := crawler.NewHostRateLimiter(5.0, 1)
		ctx := context.Background()
		start := time.Now()

		// Calling different hosts should consume separate burst tokens without waiting
		if err := limiter.Wait(ctx, "alpha.org"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := limiter.Wait(ctx, "beta.org"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := limiter.Wait(ctx, "gamma.org"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		elapsed := time.Since(start)
		// Because each host has its own burst bucket of 1, all 3 complete near instantaneously (< 50ms)
		if elapsed > 50*time.Millisecond {
			t.Errorf("expected independent hosts to not block each other, took %v", elapsed)
		}
	})

	t.Run("context cancellation aborts wait", func(t *testing.T) {
		// Slow limiter: 1 request every 10 seconds
		limiter := crawler.NewHostRateLimiter(0.1, 1)

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()

		// Call 1: consumes burst
		_ = limiter.Wait(context.Background(), "slow.com")

		// Call 2: must wait 10 seconds, but context expires in 20ms
		err := limiter.Wait(ctx, "slow.com")
		if err == nil {
			t.Errorf("expected context cancellation error, got nil")
		}
	})
}
