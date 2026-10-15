package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mnasies/deadlink/internal/crawler"
)

// Exit codes following standard Unix CLI conventions.
const (
	ExitClean   = 0 // No broken links found
	ExitBroken  = 1 // One or more broken links detected (CI failure signal)
	ExitFailure = 2 // Invalid flags, syntax, or initial network setup failure
)

func main() {
	code := run(os.Args[1:], os.Stdout, os.Stderr)
	os.Exit(code)
}

// run parses CLI flags, configures the crawler, listens for OS signals (Ctrl+C),
// streams results to stdout, and returns an exit code.
func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("deadlink", flag.ContinueOnError)
	flags.SetOutput(stderr)

	concurrency := flags.Int("concurrency", 10, "number of concurrent worker goroutines")
	maxDepth := flags.Int("max-depth", 3, "maximum link recursion depth for internal pages")
	timeout := flags.Duration("timeout", 10*time.Second, "network timeout per link probe")
	rateLimit := flags.Float64("rate-limit", 10.0, "maximum requests per second per host (0 = unlimited)")
	ignoreRobots := flags.Bool("ignore-robots", false, "bypass robots.txt restrictions")

	flags.Usage = func() {
		fmt.Fprintf(stderr, "Usage: deadlink [options] <url>\n\n")
		fmt.Fprintf(stderr, "A fast, concurrent broken link checker.\n\nOptions:\n")
		flags.PrintDefaults()
	}

	if err := flags.Parse(args); err != nil {
		return ExitFailure
	}

	if flags.NArg() < 1 {
		fmt.Fprintf(stderr, "Error: missing required target URL\n\n")
		flags.Usage()
		return ExitFailure
	}

	rawTargetURL := flags.Arg(0)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := crawler.Config{
		Concurrency:     *concurrency,
		MaxDepth:        *maxDepth,
		Timeout:         *timeout,
		UserAgent:       crawler.DefaultUserAgent,
		RateLimit:       *rateLimit,
		IgnoreRobotsTxt: *ignoreRobots,
	}

	engine := crawler.NewCrawler(cfg, nil)

	fmt.Fprintf(stdout, "Crawling %s (concurrency=%d, max-depth=%d, rate-limit=%.1f/s, timeout=%s)...\n\n",
		rawTargetURL, cfg.Concurrency, cfg.MaxDepth, cfg.RateLimit, cfg.Timeout)

	var brokenLinks []crawler.Result

	onResult := func(res crawler.Result) {
		if res.Broken {
			brokenLinks = append(brokenLinks, res)
			if res.Error != nil {
				fmt.Fprintf(stdout, "[DEAD] ERR  - %s (reason: %v)\n", res.URL, res.Error)
			} else {
				fmt.Fprintf(stdout, "[DEAD] %d  - %s\n", res.StatusCode, res.URL)
			}
			if res.ParentURL != "" {
				fmt.Fprintf(stdout, "       found on: %s\n", res.ParentURL)
			}
		} else {
			methodTag := ""
			if res.MethodUsed == "GET" {
				methodTag = " (GET fallback)"
			}
			fmt.Fprintf(stdout, "[ OK ] %d  - %s%s\n", res.StatusCode, res.URL, methodTag)
		}
	}

	stats, _, err := engine.Crawl(ctx, rawTargetURL, onResult)
	if err != nil && ctx.Err() == nil {
		fmt.Fprintf(stderr, "\nFatal crawl error: %v\n", err)
		return ExitFailure
	}

	fmt.Fprintln(stdout, "\n--------------------------------------------------")
	if ctx.Err() != nil {
		fmt.Fprintln(stdout, "Crawl interrupted by user.")
	}
	fmt.Fprintf(stdout, "Summary: %d links checked in %s. %d broken link(s) found.\n",
		stats.TotalChecked, stats.Duration.Round(time.Millisecond), stats.BrokenCount)
	fmt.Fprintln(stdout, "--------------------------------------------------")

	if len(brokenLinks) > 0 {
		return ExitBroken
	}

	return ExitClean
}
