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
	Concurrency int           // Bounded number of worker goroutines
	MaxDepth    int           // Maximum link recursion depth (0 = start page only)
	Timeout     time.Duration // Network timeout per probe
	UserAgent   string        // HTTP User-Agent header
}

// DefaultConfig provides conservative defaults for crawling.
func DefaultConfig() Config {
	return Config{
		Concurrency: 10,
		MaxDepth:    3,
		Timeout:     10 * time.Second,
		UserAgent:   DefaultUserAgent,
	}
}

// CrawlStats contains metrics collected throughout the crawl lifetime.
type CrawlStats struct {
	TotalChecked int           `json:"total_checked"`
	BrokenCount  int           `json:"broken_count"`
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
	return &Crawler{
		config:  cfg,
		checker: checker,
	}
}

// Crawl performs a recursive crawl starting at rawStartURL.
// onResult is an optional callback invoked as each link is checked (useful for live CLI output or SSE).
// Returns summary statistics and a slice of all checked link results.
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

	// jobs channel: Unbuffered channel for passing work from coordinator to workers.
	// Ownership: coordinator sends jobs, workers receive jobs, coordinator closes jobs on shutdown.
	jobs := make(chan Job)

	// results channel: Buffered channel for workers to report task outcomes back to coordinator.
	// Buffer size equals worker concurrency so workers never block reporting a completed task.
	// Ownership: workers send results, coordinator receives results, coordinator drains on exit.
	results := make(chan taskResult, c.config.Concurrency)

	// sync.WaitGroup: Synchronizes the lifecycle of the worker pool goroutines.
	// Ensures all worker goroutines have fully exited before Crawl returns, preventing goroutine leaks.
	var wg sync.WaitGroup

	// WORKER POOL: Spawn exactly c.config.Concurrency worker goroutines.
	// Why: Bounded concurrency ensures we do not spawn thousands of goroutines or exhaust OS sockets.
	for i := 0; i < c.config.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.worker(ctx, baseURL, jobs, results)
		}()
	}

	// COORDINATOR STATE:
	// All crawler state (queue, visited set, metrics) is owned EXCLUSIVELY by this goroutine.
	// Because no other goroutine reads or writes these structures, zero mutexes are needed!
	visited := make(map[string]struct{})
	queue := []Job{
		{URL: canonicalStart, ParentURL: "", Depth: 0},
	}
	visited[canonicalStart] = struct{}{}

	var allResults []Result
	brokenCount := 0
	inFlight := 0

	// COORDINATOR EVENT LOOP:
	// Dispatches queued jobs to workers when workers are ready, processes results,
	// and terminates cleanly when all in-flight work and queued jobs are exhausted.
	for {
		var currentJob Job
		var sendJobs chan Job

		// SELECT WITH DYNAMIC NIL CHANNEL:
		// In Go, sending to or receiving from a nil channel blocks forever.
		// When queue is empty, setting sendJobs = nil disables the send case in the select statement,
		// allowing the coordinator to wait exclusively for incoming results or context cancellation.
		if len(queue) > 0 {
			currentJob = queue[0]
			sendJobs = jobs
		} else {
			sendJobs = nil
		}

		select {
		case <-ctx.Done():
			// Context canceled or timed out: abort immediately.
			close(jobs)
			wg.Wait()
			return &CrawlStats{
				TotalChecked: len(allResults),
				BrokenCount:  brokenCount,
				Duration:     time.Since(start),
			}, allResults, ctx.Err()

		case sendJobs <- currentJob:
			// Dispatched one job to an available worker
			queue = queue[1:]
			inFlight++

		case tr := <-results:
			inFlight--
			allResults = append(allResults, tr.result)

			if tr.result.Broken {
				brokenCount++
			}

			if onResult != nil {
				onResult(tr.result)
			}

			// Schedule newly discovered links for crawling if within recursion depth limit
			if tr.depth < c.config.MaxDepth {
				for _, link := range tr.discoveredLinks {
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

			// TERMINATION CONDITION:
			// When inFlight == 0 AND queue is empty, the entire crawl tree is exhausted.
			if inFlight == 0 && len(queue) == 0 {
				close(jobs) // Signals all workers to finish their loops and exit
				wg.Wait()   // Wait for all workers to return

				return &CrawlStats{
					TotalChecked: len(allResults),
					BrokenCount:  brokenCount,
					Duration:     time.Since(start),
				}, allResults, nil
			}
		}
	}
}

// worker processes jobs received from the jobs channel until jobs is closed.
func (c *Crawler) worker(ctx context.Context, baseURL *url.URL, jobs <-chan Job, results chan<- taskResult) {
	for job := range jobs {
		// Respect context cancellation while waiting or between jobs
		if ctx.Err() != nil {
			return
		}

		// Step 1: Probe the link for status code and reachability
		probeCtx, cancel := context.WithTimeout(ctx, c.config.Timeout)
		res := c.checker.Check(probeCtx, job.URL, job.ParentURL)
		cancel()

		var discovered []string

		// Step 2: If the link is internal, reachable (2xx), and within depth limit:
		// fetch the HTML page and extract hyperlinks for recursive traversal.
		targetParsed, err := url.Parse(job.URL)
		isInternal := err == nil && IsInternal(baseURL, targetParsed)

		if isInternal && !res.Broken && res.StatusCode == http.StatusOK {
			discovered = c.fetchAndExtract(ctx, job.URL)
		}

		// Step 3: Send result and newly discovered links back to coordinator
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

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", c.config.UserAgent)

	resp, err := c.checker.client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	// Only parse HTML responses (skip binary files like PDFs, ZIPs, or images)
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
