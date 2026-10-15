package crawler_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
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

// TestCrawler_NonHTMLHandling verifies that binary files (images, PDFs)
// are checked for HTTP status but their bodies are NOT tokenized as HTML.
func TestCrawler_NonHTMLHandling(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`
				<html>
				<body>
					<img src="/image.png">
					<a href="/document.pdf">Download</a>
				</body>
				</html>
			`))
		case "/image.png":
			w.Header().Set("Content-Type", "image/png")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte{0x89, 'P', 'N', 'G'}) // PNG magic bytes
		case "/document.pdf":
			w.Header().Set("Content-Type", "application/pdf")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("%PDF-1.4"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	checker := crawler.NewLinkChecker(server.Client(), "test/1.0")
	c := crawler.NewCrawler(crawler.Config{Concurrency: 3, MaxDepth: 2, Timeout: 2 * time.Second}, checker)

	stats, results, err := c.Crawl(context.Background(), server.URL, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if stats.BrokenCount != 0 {
		t.Errorf("expected 0 broken links, got %d", stats.BrokenCount)
	}
	// Root (/) + image.png + document.pdf = 3 total checked
	if stats.TotalChecked != 3 {
		t.Errorf("expected 3 links checked, got %d (results: %+v)", stats.TotalChecked, results)
	}
}

// TestCrawler_HighConcurrencyStress simulates a dense web graph with high concurrency
// to prove thread-safety, race-freedom, and deadlock-free termination.
func TestCrawler_HighConcurrencyStress(t *testing.T) {
	var requestCount int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount, 1)
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)

		// Each page links to multiple other pages across a 20-node graph
		var pageNum int
		_, _ = fmt.Sscanf(r.URL.Path, "/page-%d", &pageNum)

		next1 := (pageNum + 1) % 20
		next2 := (pageNum + 3) % 20
		next3 := (pageNum + 7) % 20

		_, _ = fmt.Fprintf(w, `
			<html>
			<body>
				<a href="/page-%d">Link 1</a>
				<a href="/page-%d">Link 2</a>
				<a href="/page-%d">Link 3</a>
			</body>
			</html>
		`, next1, next2, next3)
	}))
	defer server.Close()

	checker := crawler.NewLinkChecker(server.Client(), "stress/1.0")
	// 20 concurrent workers traversing the circular graph
	cfg := crawler.Config{
		Concurrency: 20,
		MaxDepth:    5,
		Timeout:     3 * time.Second,
	}
	c := crawler.NewCrawler(cfg, checker)

	startURL := server.URL + "/page-0"
	stats, results, err := c.Crawl(context.Background(), startURL, nil)
	if err != nil {
		t.Fatalf("unexpected crawl error: %v", err)
	}

	if stats.BrokenCount != 0 {
		t.Errorf("expected 0 broken links, got %d", stats.BrokenCount)
	}

	// Graph has 20 nodes; all 20 should be visited and deduplicated cleanly
	if stats.TotalChecked != 20 {
		t.Errorf("expected 20 unique pages visited, got %d", stats.TotalChecked)
	}
	if len(results) != 20 {
		t.Errorf("expected 20 results, got %d", len(results))
	}
}

// TestCrawler_MixedStatusClassification verifies that the crawler aggregates
// OK, Dead, and Blocked links accurately without conflating bot protection with dead links.
func TestCrawler_MixedStatusClassification(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`
				<html>
				<body>
					<a href="/healthy">Healthy Child</a>
					<a href="/bot-blocked">Bot Blocked Link</a>
					<a href="/dead-link">Dead Link</a>
				</body>
				</html>
			`))
		case "/healthy":
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<html><body>OK</body></html>`))

		case "/bot-blocked":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`403 Forbidden - Access Denied`))

		case "/dead-link":
			w.WriteHeader(http.StatusNotFound)

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	checker := crawler.NewLinkChecker(server.Client(), "test/1.0")
	c := crawler.NewCrawler(crawler.Config{Concurrency: 2, MaxDepth: 2, Timeout: 2 * time.Second}, checker)

	stats, results, err := c.Crawl(context.Background(), server.URL, nil)
	if err != nil {
		t.Fatalf("unexpected crawl error: %v", err)
	}

	if stats.TotalChecked != 4 {
		t.Errorf("expected 4 checked links (root, healthy, bot-blocked, dead-link), got %d", stats.TotalChecked)
	}
	if stats.OKCount != 2 {
		t.Errorf("expected 2 OK links (root and healthy), got %d", stats.OKCount)
	}
	if stats.BlockedCount != 1 {
		t.Errorf("expected 1 Blocked link (bot-blocked), got %d", stats.BlockedCount)
	}
	if stats.DeadCount != 1 {
		t.Errorf("expected 1 Dead link (dead-link), got %d", stats.DeadCount)
	}
	if stats.BrokenCount != 1 {
		t.Errorf("expected BrokenCount to equal DeadCount (1), got %d", stats.BrokenCount)
	}

	// Verify individual result statuses
	statusMap := make(map[string]crawler.LinkStatus)
	for _, res := range results {
		statusMap[res.URL] = res.Status
	}

	if statusMap[server.URL+"/bot-blocked"] != crawler.StatusBlocked {
		t.Errorf("expected /bot-blocked to have StatusBlocked, got %v", statusMap[server.URL+"/bot-blocked"])
	}
	if statusMap[server.URL+"/dead-link"] != crawler.StatusDead {
		t.Errorf("expected /dead-link to have StatusDead, got %v", statusMap[server.URL+"/dead-link"])
	}
	if statusMap[server.URL+"/healthy"] != crawler.StatusOK {
		t.Errorf("expected /healthy to have StatusOK, got %v", statusMap[server.URL+"/healthy"])
	}
}
