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
	var headCount, getCount int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("User-Agent"), "deadlink") {
			http.Error(w, "missing user agent", http.StatusBadRequest)
			return
		}

		switch r.Method {
		case http.MethodHead:
			atomic.AddInt64(&headCount, 1)
		case http.MethodGet:
			atomic.AddInt64(&getCount, 1)
		}

		switch r.URL.Path {
		case "/ok":
			w.WriteHeader(http.StatusOK)

		case "/reject-head-405":
			// Real-world scenario: endpoint rejects HEAD with 405, but serves 200 on GET
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusMethodNotAllowed)
			} else {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("GET content"))
			}

		case "/reject-head-403":
			// Real-world scenario: WAF/CDN rejects HEAD with 403, but allows GET
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
		{
			name:           "broken link 500 fails directly on HEAD",
			path:           "/server-error",
			timeout:        1 * time.Second,
			expectedStatus: http.StatusInternalServerError,
			expectBroken:   true,
			expectErr:      false,
			expectedMethod: http.MethodHead,
		},
		{
			name:           "timeout deadline aborts probe",
			path:           "/slow",
			timeout:        20 * time.Millisecond,
			expectedStatus: 0,
			expectBroken:   true,
			expectErr:      true,
			expectedMethod: http.MethodGet, // timeout on HEAD retried GET which also timed out
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
