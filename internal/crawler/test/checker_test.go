package crawler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mnasies/deadlink/internal/crawler"
)

func TestLinkChecker_Check(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("User-Agent"), "deadlink") {
			http.Error(w, "missing user agent", http.StatusBadRequest)
			return
		}

		switch r.URL.Path {
		case "/ok":
			w.WriteHeader(http.StatusOK)

		case "/reject-head-405":
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusMethodNotAllowed)
			} else {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("GET content"))
			}

		case "/reject-head-403":
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusForbidden)
			} else {
				w.WriteHeader(http.StatusOK)
			}

		case "/not-found":
			w.WriteHeader(http.StatusNotFound)

		case "/server-error":
			w.WriteHeader(http.StatusInternalServerError)

		case "/slow":
			time.Sleep(100 * time.Millisecond)
			w.WriteHeader(http.StatusOK)

		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()

	checker := crawler.NewLinkChecker(server.Client(), "deadlink-test/1.0")

	tests := []struct {
		name           string
		path           string
		timeout        time.Duration
		expectedStatus int
		expectBroken   bool
		expectErr      bool
		expectedMethod string
	}{
		{
			name:           "healthy link succeeds on HEAD directly",
			path:           "/ok",
			timeout:        1 * time.Second,
			expectedStatus: http.StatusOK,
			expectBroken:   false,
			expectErr:      false,
			expectedMethod: http.MethodHead,
		},
		{
			name:           "405 on HEAD falls back to GET successfully",
			path:           "/reject-head-405",
			timeout:        1 * time.Second,
			expectedStatus: http.StatusOK,
			expectBroken:   false,
			expectErr:      false,
			expectedMethod: http.MethodGet,
		},
		{
			name:           "403 on HEAD falls back to GET successfully",
			path:           "/reject-head-403",
			timeout:        1 * time.Second,
			expectedStatus: http.StatusOK,
			expectBroken:   false,
			expectErr:      false,
			expectedMethod: http.MethodGet,
		},
		{
			name:           "broken link 404 does not need GET fallback",
			path:           "/not-found",
			timeout:        1 * time.Second,
			expectedStatus: http.StatusNotFound,
			expectBroken:   true,
			expectErr:      false,
			expectedMethod: http.MethodHead,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), tt.timeout)
			defer cancel()

			targetURL := server.URL + tt.path
			res := checker.Check(ctx, targetURL, "https://example.com")

			if res.Broken != tt.expectBroken {
				t.Errorf("expected Broken = %v, got %v", tt.expectBroken, res.Broken)
			}
			if res.StatusCode != tt.expectedStatus {
				t.Errorf("expected StatusCode %d, got %d", tt.expectedStatus, res.StatusCode)
			}
			if res.MethodUsed != tt.expectedMethod {
				t.Errorf("expected MethodUsed %s, got %s", tt.expectedMethod, res.MethodUsed)
			}
			if tt.expectErr && res.Error == nil {
				t.Errorf("expected non-nil error, got nil")
			}
			if !tt.expectErr && res.Error != nil {
				t.Errorf("unexpected error: %v", res.Error)
			}
		})
	}
}

func TestLinkChecker_RetriesAndBackoff(t *testing.T) {
	var transient503Calls int64
	var rateLimit429Calls int64
	var permanent404Calls int64
	var persistent503Calls int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/flaky-503":
			// Fails first attempt with 503, succeeds on 2nd attempt with 200
			count := atomic.AddInt64(&transient503Calls, 1)
			if count == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
			} else {
				w.WriteHeader(http.StatusOK)
			}

		case "/flaky-429":
			// Fails first attempt with 429 + Retry-After: 1, succeeds on 2nd attempt
			count := atomic.AddInt64(&rateLimit429Calls, 1)
			if count == 1 {
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
			} else {
				w.WriteHeader(http.StatusOK)
			}

		case "/permanent-404":
			atomic.AddInt64(&permanent404Calls, 1)
			w.WriteHeader(http.StatusNotFound)

		case "/persistent-503":
			atomic.AddInt64(&persistent503Calls, 1)
			w.WriteHeader(http.StatusServiceUnavailable)

		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()

	checker := crawler.NewLinkChecker(server.Client(), "deadlink-test/1.0")
	// Fast backoff for unit tests: 10ms initial delay
	checker.SetRetryPolicy(2, 10*time.Millisecond)

	t.Run("transient 503 recovers on retry", func(t *testing.T) {
		res := checker.Check(context.Background(), server.URL+"/flaky-503", "https://example.com")
		if res.Broken {
			t.Errorf("expected recovery on retry, got broken result: %+v", res)
		}
		if res.StatusCode != http.StatusOK {
			t.Errorf("expected status 200, got %d", res.StatusCode)
		}
		if res.Attempts != 2 {
			t.Errorf("expected 2 attempts, got %d", res.Attempts)
		}
	})

	t.Run("permanent 404 is not retried", func(t *testing.T) {
		res := checker.Check(context.Background(), server.URL+"/permanent-404", "https://example.com")
		if !res.Broken {
			t.Errorf("expected 404 to be broken, got: %+v", res)
		}
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("expected status 404, got %d", res.StatusCode)
		}
		if res.Attempts != 1 {
			t.Errorf("expected 404 to only be probed once (no retries), got %d attempts", res.Attempts)
		}
	})

	t.Run("persistent 503 exhausts max retries", func(t *testing.T) {
		res := checker.Check(context.Background(), server.URL+"/persistent-503", "https://example.com")
		if !res.Broken {
			t.Errorf("expected persistent 503 to be broken, got: %+v", res)
		}
		// Initial attempt (1) + 2 retries = 3 total attempts
		if res.Attempts != 3 {
			t.Errorf("expected exactly 3 attempts (1 + 2 retries), got %d", res.Attempts)
		}
	})

	t.Run("context cancellation aborts retry sleep", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
		defer cancel()

		slowChecker := crawler.NewLinkChecker(server.Client(), "deadlink-test/1.0")
		slowChecker.SetRetryPolicy(3, 100*time.Millisecond) // backoff is 100ms, context expires in 15ms

		res := slowChecker.Check(ctx, server.URL+"/persistent-503", "https://example.com")
		if !res.Broken {
			t.Errorf("expected canceled probe to be broken, got: %+v", res)
		}
		if res.Error == nil {
			t.Errorf("expected context cancellation error, got nil")
		}
	})
}
