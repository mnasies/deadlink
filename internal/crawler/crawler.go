package crawler

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Config holds user-configurable options for the crawler.
type Config struct {
	Concurrency     int           // Bounded number of worker goroutines
	MaxDepth        int           // Maximum link recursion depth (0 = start page only)
	Timeout         time.Duration // Network timeout per probe
	UserAgent       string        // HTTP User-Agent header
	RateLimit       float64       // Max requests per second per host (0 = unlimited)
	IgnoreRobotsTxt bool          // Whether to bypass robots.txt compliance (default false: respect robots.txt)
}

// DefaultConfig provides conservative defaults for crawling.
func DefaultConfig() Config {
	return Config{
		Concurrency:     10,
		MaxDepth:        3,
		Timeout:         10 * time.Second,
		UserAgent:       DefaultUserAgent,
		RateLimit:       10.0,
		IgnoreRobotsTxt: false,
	}
}

// CrawlStats contains metrics collected throughout the crawl lifetime.
type CrawlStats struct {
	TotalChecked int           `json:"total_checked"`
	OKCount      int           `json:"ok_count"`
	DeadCount    int           `json:"dead_count"`
	BlockedCount int           `json:"blocked_count"`
	TimeoutCount int           `json:"timeout_count"`
	ErrorCount   int           `json:"error_count"`
	BrokenCount  int           `json:"broken_count"` // Number of broken links (DeadCount + ErrorCount)
	Duration     time.Duration `json:"duration_ms"`
}

// Job represents a single unit of work dispatched to a worker goroutine.
type Job struct {
	URL       string // Target URL to probe
	ParentURL string // Source page where this link was discovered
	Depth     int    // Hop distance from the root starting URL
}

// taskResult represents the output sent back from a worker to the coordinator.
type taskResult struct {
	result          Result   // Probe outcome (status code, latency, broken flag)
	discoveredLinks []string // Links extracted from HTML body (only if internal page & status == 200)
	depth           int
}

// Crawler coordinates concurrent link checking and recursive traversal.
type Crawler struct {
	config  Config
	checker *LinkChecker
}

// NewCrawler initializes a Crawler with the provided configuration and LinkChecker.
func NewCrawler(cfg Config, checker *LinkChecker) *Crawler {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 10
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	if checker == nil {
		checker = NewLinkChecker(nil, cfg.UserAgent)
	}
	if cfg.RateLimit > 0 && checker.rateLimiter == nil {
		checker.SetRateLimiter(NewHostRateLimiter(cfg.RateLimit, 1))
	}

	return &Crawler{
		config:  cfg,
		checker: checker,
	}
}

// Crawl performs a recursive crawl starting at rawStartURL.
func (c *Crawler) Crawl(ctx context.Context, rawStartURL string, onResult func(Result)) (*CrawlStats, []Result, error) {
	start := time.Now()

	canonicalStart, err := NormalizeURL(rawStartURL)
	if err != nil {
		return nil, nil, err
	}

	baseURL, err := url.Parse(canonicalStart)
	if err != nil {
		return nil, nil, err
	}

	// Step 1: Fetch and parse robots.txt unless explicitly disabled
	var robots *RobotsData
	if !c.config.IgnoreRobotsTxt {
		robots = FetchRobots(c.checker.client, baseURL, c.config.UserAgent)
		// If robots.txt specifies a Crawl-delay, adjust rate limiter accordingly
		if robots != nil && robots.CrawlDelay > 0 && c.checker.rateLimiter != nil {
			delaySec := robots.CrawlDelay.Seconds()
			if delaySec > 0 {
				c.checker.rateLimiter.SetRate(1.0 / delaySec)
			}
		}
	}

	jobs := make(chan Job)
	results := make(chan taskResult, c.config.Concurrency)

	var wg sync.WaitGroup

	for i := 0; i < c.config.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.worker(ctx, baseURL, jobs, results)
		}()
	}

	visited := make(map[string]struct{})
	queue := []Job{
		{URL: canonicalStart, ParentURL: "", Depth: 0},
	}
	visited[canonicalStart] = struct{}{}

	var allResults []Result
	okCount := 0
	deadCount := 0
	blockedCount := 0
	timeoutCount := 0
	errorCount := 0
	inFlight := 0

	for {
		var currentJob Job
		var sendJobs chan Job

		if len(queue) > 0 {
			currentJob = queue[0]
			sendJobs = jobs
		} else {
			sendJobs = nil
		}

		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return &CrawlStats{
				TotalChecked: len(allResults),
				OKCount:      okCount,
				DeadCount:    deadCount,
				BlockedCount: blockedCount,
				TimeoutCount: timeoutCount,
				ErrorCount:   errorCount,
				BrokenCount:  deadCount + errorCount,
				Duration:     time.Since(start),
			}, allResults, ctx.Err()

		case sendJobs <- currentJob:
			queue = queue[1:]
			inFlight++

		case tr := <-results:
			inFlight--
			allResults = append(allResults, tr.result)

			switch tr.result.Status {
			case StatusOK:
				okCount++
			case StatusDead:
				deadCount++
			case StatusBlocked:
				blockedCount++
			case StatusTimeout:
				timeoutCount++
			case StatusError:
				errorCount++
			default:
				if tr.result.Broken {
					deadCount++
				} else {
					okCount++
				}
			}

			if onResult != nil {
				onResult(tr.result)
			}

			// Schedule newly discovered links for crawling if within recursion depth limit
			if tr.depth < c.config.MaxDepth {
				for _, link := range tr.discoveredLinks {
					linkParsed, parseErr := url.Parse(link)
					if parseErr != nil {
						continue
					}

					// Respect robots.txt: skip queuing disallowed internal pages
					if robots != nil && IsInternal(baseURL, linkParsed) && !robots.IsPathAllowed(linkParsed) {
						continue
					}

					if _, alreadyVisited := visited[link]; !alreadyVisited {
						visited[link] = struct{}{}
						queue = append(queue, Job{
							URL:       link,
							ParentURL: tr.result.URL,
							Depth:     tr.depth + 1,
						})
					}
				}
			}

			if inFlight == 0 && len(queue) == 0 {
				close(jobs)
				wg.Wait()

				return &CrawlStats{
					TotalChecked: len(allResults),
					OKCount:      okCount,
					DeadCount:    deadCount,
					BlockedCount: blockedCount,
					TimeoutCount: timeoutCount,
					ErrorCount:   errorCount,
					BrokenCount:  deadCount + errorCount,
					Duration:     time.Since(start),
				}, allResults, nil
			}
		}
	}
}

// worker processes jobs received from the jobs channel until jobs is closed.
func (c *Crawler) worker(ctx context.Context, baseURL *url.URL, jobs <-chan Job, results chan<- taskResult) {
	for job := range jobs {
		if ctx.Err() != nil {
			return
		}

		probeCtx, cancel := context.WithTimeout(ctx, c.config.Timeout)
		res := c.checker.Check(probeCtx, job.URL, job.ParentURL)
		cancel()

		var discovered []string

		targetParsed, err := url.Parse(job.URL)
		isInternal := err == nil && IsInternal(baseURL, targetParsed)

		if isInternal && !res.Broken && res.StatusCode == http.StatusOK {
			discovered = c.fetchAndExtract(ctx, job.URL)
		}

		select {
		case results <- taskResult{
			result:          res,
			discoveredLinks: discovered,
			depth:           job.Depth,
		}:
		case <-ctx.Done():
			return
		}
	}
}

// fetchAndExtract performs an HTTP GET on an internal page and extracts target links.
func (c *Crawler) fetchAndExtract(ctx context.Context, pageURL string) []string {
	reqCtx, cancel := context.WithTimeout(ctx, c.config.Timeout)
	defer cancel()

	resp, err := c.checker.doRequest(reqCtx, http.MethodGet, pageURL)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(strings.ToLower(contentType), "text/html") && contentType != "" {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		return nil
	}

	pageParsed, err := url.Parse(pageURL)
	if err != nil {
		return nil
	}

	links, err := ExtractLinks(pageParsed, resp.Body)
	if err != nil {
		return nil
	}
	return links
}
