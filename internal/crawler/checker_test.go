package crawler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLinkChecker_Check(t *testing.T) {
	// httptest.NewServer starts a real, local HTTP server listening on a random loopback port.
	// It is the standard Go way to test HTTP clients without mocking network sockets.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify custom User-Agent
		if !strings.Contains(r.Header.Get("User-Agent"), "deadlink") {
			http.Error(w, "missing user agent", http.StatusBadRequest)
			return
		}

		switch r.URL.Path {
		case "/ok":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("OK content"))
		case "/not-found":
			w.WriteHeader(http.StatusNotFound)
		case "/server-error":
			w.WriteHeader(http.StatusInternalServerError)
		case "/slow":
			// Sleep longer than the client's timeout to test deadline handling
			time.Sleep(100 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()

	checker := NewLinkChecker(server.Client(), "deadlink-test/1.0")

	tests := []struct {
		name           string
		path           string
		parentURL      string
		timeout        time.Duration
		expectedStatus int
		expectBroken   bool
		expectErr      bool
	}{
		{
			name:           "healthy link 200",
			path:           "/ok",
			parentURL:      "https://example.com",
			timeout:        1 * time.Second,
			expectedStatus: http.StatusOK,
			expectBroken:   false,
			expectErr:      false,
		},
		{
			name:           "broken link 404",
			path:           "/not-found",
			parentURL:      "https://example.com/blog",
			timeout:        1 * time.Second,
			expectedStatus: http.StatusNotFound,
			expectBroken:   true,
			expectErr:      false,
		},
		{
			name:           "broken link 500",
			path:           "/server-error",
			parentURL:      "https://example.com/blog",
			timeout:        1 * time.Second,
			expectedStatus: http.StatusInternalServerError,
			expectBroken:   true,
			expectErr:      false,
		},
		{
			name:           "timeout canceled context",
			path:           "/slow",
			parentURL:      "https://example.com/slow-page",
			timeout:        20 * time.Millisecond, // times out before 100ms sleep
			expectedStatus: 0,
			expectBroken:   true,
			expectErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), tt.timeout)
			defer cancel()

			targetURL := server.URL + tt.path
			res := checker.Check(ctx, targetURL, tt.parentURL)

			if res.URL != targetURL {
				t.Errorf("expected URL %q, got %q", targetURL, res.URL)
			}
			if res.ParentURL != tt.parentURL {
				t.Errorf("expected ParentURL %q, got %q", tt.parentURL, res.ParentURL)
			}
			if res.Broken != tt.expectBroken {
				t.Errorf("expected Broken = %v, got %v", tt.expectBroken, res.Broken)
			}
			if res.StatusCode != tt.expectedStatus {
				t.Errorf("expected StatusCode %d, got %d", tt.expectedStatus, res.StatusCode)
			}
			if tt.expectErr && res.Error == nil {
				t.Errorf("expected non-nil error, got nil")
			}
			if !tt.expectErr && res.Error != nil {
				t.Errorf("unexpected error: %v", res.Error)
			}
			if res.Duration <= 0 {
				t.Errorf("expected positive duration, got %v", res.Duration)
			}
		})
	}
}
