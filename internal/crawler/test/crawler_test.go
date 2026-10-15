package crawler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/mnasies/deadlink/internal/crawler"
)

func TestCrawler_Crawl(t *testing.T) {
	// External target server to test external link status probing (without recursive traversal)
	externalServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/external-ok":
			w.WriteHeader(http.StatusOK)
		case "/external-dead":
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer externalServer.Close()

	// Internal target server: simulates a site with internal pages, loops, and broken links
	var internalServer *httptest.Server
	internalServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")

		switch r.URL.Path {
		case "/":
			// Root page linking to internal pages, external links, and a 404
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`
				<html>
				<body>
					<a href="/about">About Us</a>
					<a href="/missing-page">Missing Page</a>
					<a href="` + externalServer.URL + `/external-ok">External OK</a>
					<a href="` + externalServer.URL + `/external-dead">External Dead</a>
					<!-- Duplicate link to verify visited deduplication -->
					<a href="/about">About Again</a>
				</body>
				</html>
			`))

		case "/about":
			// Page linking deeper and linking BACK to root (testing circular graph loops)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`
				<html>
				<body>
					<a href="/">Home Loop</a>
					<a href="/team">Our Team</a>
				</body>
				</html>
			`))

		case "/team":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`
				<html>
				<body>
					<p>Meet the team</p>
				</body>
				</html>
			`))

		case "/missing-page":
			w.WriteHeader(http.StatusNotFound)

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer internalServer.Close()

	checker := crawler.NewLinkChecker(internalServer.Client(), "deadlink-crawler-test/1.0")

	t.Run("full recursive crawl with loop detection", func(t *testing.T) {
		cfg := crawler.Config{
			Concurrency: 4,
			MaxDepth:    3,
			Timeout:     2 * time.Second,
		}
		c := crawler.NewCrawler(cfg, checker)

		var mu sync.Mutex
		var streamedResults []crawler.Result

		stats, results, err := c.Crawl(context.Background(), internalServer.URL, func(r crawler.Result) {
			mu.Lock()
			streamedResults = append(streamedResults, r)
			mu.Unlock()
		})

		if err != nil {
			t.Fatalf("unexpected crawl error: %v", err)
		}

		if stats.BrokenCount != 2 {
			t.Errorf("expected 2 broken links (/missing-page and /external-dead), got %d", stats.BrokenCount)
		}

		// Expected checked targets:
		// 1. Root /
		// 2. /about
		// 3. /missing-page (broken)
		// 4. external-ok
		// 5. external-dead (broken)
		// 6. /team (discovered on /about)
		expectedCount := 6
		if stats.TotalChecked != expectedCount {
			t.Errorf("expected %d total checked links, got %d", expectedCount, stats.TotalChecked)
		}
		if len(results) != expectedCount {
			t.Errorf("expected %d results returned, got %d", expectedCount, len(results))
		}

		mu.Lock()
		if len(streamedResults) != expectedCount {
			t.Errorf("expected %d streamed results via onResult, got %d", expectedCount, len(streamedResults))
		}
		mu.Unlock()
	})

	t.Run("max depth limit enforcement", func(t *testing.T) {
		// Depth 0: only check root page, do not crawl children
		cfg := crawler.Config{
			Concurrency: 2,
			MaxDepth:    0,
			Timeout:     2 * time.Second,
		}
		c := crawler.NewCrawler(cfg, checker)

		stats, results, err := c.Crawl(context.Background(), internalServer.URL, nil)
		if err != nil {
			t.Fatalf("unexpected crawl error: %v", err)
		}

		// Depth 0 should only probe the starting URL itself
		if stats.TotalChecked != 1 {
			t.Errorf("expected 1 checked URL at depth 0, got %d", stats.TotalChecked)
		}
		if len(results) != 1 || results[0].URL != internalServer.URL+"/" {
			t.Errorf("unexpected depth 0 results: %+v", results)
		}
	})

	t.Run("context cancellation aborts crawl", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		cfg := crawler.Config{
			Concurrency: 2,
			MaxDepth:    2,
			Timeout:     2 * time.Second,
		}
		c := crawler.NewCrawler(cfg, checker)

		_, _, err := c.Crawl(ctx, internalServer.URL, nil)
		if err == nil {
			t.Errorf("expected context cancellation error, got nil")
		}
	})
}
