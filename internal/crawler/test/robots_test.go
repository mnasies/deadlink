package crawler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mnasies/deadlink/internal/crawler"
)

func TestParseRobots_Directives(t *testing.T) {
	robotsTxt := `
	# Global robots rules
	User-agent: *
	Disallow: /private/
	Disallow: /admin/
	Allow: /admin/public/
	Disallow: /*.secret$
	Crawl-delay: 2

	# Specific override for our bot
	User-agent: deadlink
	Disallow: /deadlink-blocked/
	Allow: /private/
	`

	t.Run("specific user-agent precedence", func(t *testing.T) {
		rd := crawler.ParseRobots(strings.NewReader(robotsTxt), "deadlink/1.0")

		// Under deadlink block, /private/ is allowed
		if !rd.IsAllowed("/private/doc.html") {
			t.Errorf("expected /private/doc.html to be allowed for deadlink user-agent")
		}
		// Under deadlink block, /deadlink-blocked/ is disallowed
		if rd.IsAllowed("/deadlink-blocked/file") {
			t.Errorf("expected /deadlink-blocked/file to be disallowed")
		}
	})

	t.Run("wildcard user-agent fallback", func(t *testing.T) {
		rd := crawler.ParseRobots(strings.NewReader(robotsTxt), "generic-bot")

		// General rule: /private/ is disallowed
		if rd.IsAllowed("/private/doc.html") {
			t.Errorf("expected /private/ to be disallowed for generic bot")
		}
		// Longest match: /admin/ is disallowed, but /admin/public/ is allowed
		if rd.IsAllowed("/admin/settings") {
			t.Errorf("expected /admin/settings to be disallowed")
		}
		if !rd.IsAllowed("/admin/public/index.html") {
			t.Errorf("expected /admin/public/index.html to be allowed (longest rule wins)")
		}
		// Wildcard pattern: *.secret$
		if rd.IsAllowed("/data/key.secret") {
			t.Errorf("expected /data/key.secret to be disallowed by wildcard")
		}
		if !rd.IsAllowed("/data/key.secret.txt") {
			t.Errorf("expected /data/key.secret.txt to be allowed (not ending in .secret)")
		}
		// Crawl delay parsed
		if rd.CrawlDelay != 2*time.Second {
			t.Errorf("expected crawl delay 2s, got %v", rd.CrawlDelay)
		}
	})
}

func TestCrawler_RobotsTxtCompliance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/robots.txt":
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`
				User-agent: *
				Disallow: /restricted/
			`))

		case "/":
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`
				<html>
				<body>
					<a href="/public-page">Public Page</a>
					<a href="/restricted/secret-page">Secret Page</a>
				</body>
				</html>
			`))

		case "/public-page":
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<html><body><p>Public Content</p></body></html>`))

		case "/restricted/secret-page":
			// If crawler violates robots.txt, it would visit this page containing broken links!
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<html><body><a href="/404-should-not-reach">Dead Link</a></body></html>`))

		case "/404-should-not-reach":
			w.WriteHeader(http.StatusNotFound)

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	t.Run("crawler respects robots.txt and skips restricted paths", func(t *testing.T) {
		cfg := crawler.Config{
			Concurrency:     2,
			MaxDepth:        3,
			Timeout:         2 * time.Second,
			RateLimit:       0, // disable rate limit delay for test speed
			IgnoreRobotsTxt: false,
		}
		c := crawler.NewCrawler(cfg, nil)

		stats, results, err := c.Crawl(context.Background(), server.URL, nil)
		if err != nil {
			t.Fatalf("unexpected crawl error: %v", err)
		}

		if stats.BrokenCount != 0 {
			t.Errorf("expected 0 broken links (restricted path should be skipped), got %d (results: %+v)", stats.BrokenCount, results)
		}

		// Visited should only be:
		// 1. Root /
		// 2. /public-page
		// /restricted/secret-page must NOT be queued for crawling
		for _, res := range results {
			if strings.Contains(res.URL, "/restricted/") {
				t.Errorf("robots.txt violation: crawled restricted URL %s", res.URL)
			}
		}
	})

	t.Run("crawler traverses restricted paths when IgnoreRobotsTxt is true", func(t *testing.T) {
		cfg := crawler.Config{
			Concurrency:     2,
			MaxDepth:        3,
			Timeout:         2 * time.Second,
			RateLimit:       0,
			IgnoreRobotsTxt: true, // explicit bypass
		}
		c := crawler.NewCrawler(cfg, nil)

		stats, _, err := c.Crawl(context.Background(), server.URL, nil)
		if err != nil {
			t.Fatalf("unexpected crawl error: %v", err)
		}

		// When bypassing robots.txt, /restricted/secret-page is crawled, reaching /404-should-not-reach
		if stats.BrokenCount != 1 {
			t.Errorf("expected 1 broken link when bypassing robots.txt, got %d", stats.BrokenCount)
		}
	})
}
