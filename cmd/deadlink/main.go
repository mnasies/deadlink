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
	failOnBlocked := flags.Bool("fail-on-blocked", false, "treat bot-blocked links (403/429) as CI failures")
	failOnTimeout := flags.Bool("fail-on-timeout", false, "treat timed-out links as CI failures")

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

	var deadLinks []crawler.Result
	var blockedLinks []crawler.Result
	var timeoutLinks []crawler.Result
	var errorLinks []crawler.Result

	onResult := func(res crawler.Result) {
		switch res.Status {
		case crawler.StatusOK:
			methodTag := ""
			if res.MethodUsed == "GET" {
				methodTag = " (GET fallback)"
			}
			fmt.Fprintf(stdout, "[ OK ] %d  - %s%s\n", res.StatusCode, res.URL, methodTag)

		case crawler.StatusDead:
			deadLinks = append(deadLinks, res)
			if res.Error != nil {
				fmt.Fprintf(stdout, "[DEAD] ERR  - %s (reason: %v)\n", res.URL, res.Error)
			} else {
				fmt.Fprintf(stdout, "[DEAD] %d  - %s\n", res.StatusCode, res.URL)
			}
			if res.ParentURL != "" {
				fmt.Fprintf(stdout, "       found on: %s\n", res.ParentURL)
			}

		case crawler.StatusBlocked:
			blockedLinks = append(blockedLinks, res)
			fmt.Fprintf(stdout, "[BLOCKED] %d  - %s (bot protection / access denied)\n", res.StatusCode, res.URL)
			if res.ParentURL != "" {
				fmt.Fprintf(stdout, "          found on: %s\n", res.ParentURL)
			}

		case crawler.StatusTimeout:
			timeoutLinks = append(timeoutLinks, res)
			if res.Error != nil {
				fmt.Fprintf(stdout, "[TIMEOUT] ERR  - %s (reason: %v)\n", res.URL, res.Error)
			} else {
				fmt.Fprintf(stdout, "[TIMEOUT] %d  - %s\n", res.StatusCode, res.URL)
			}
			if res.ParentURL != "" {
				fmt.Fprintf(stdout, "          found on: %s\n", res.ParentURL)
			}

		case crawler.StatusError:
			errorLinks = append(errorLinks, res)
			if res.Error != nil {
				fmt.Fprintf(stdout, "[ERROR] ERR  - %s (reason: %v)\n", res.URL, res.Error)
			} else {
				fmt.Fprintf(stdout, "[ERROR] %d  - %s\n", res.StatusCode, res.URL)
			}
			if res.ParentURL != "" {
				fmt.Fprintf(stdout, "        found on: %s\n", res.ParentURL)
			}
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
	if stats.BlockedCount > 0 || stats.TimeoutCount > 0 || stats.ErrorCount > 0 {
		fmt.Fprintf(stdout, "Breakdown: %d ok, %d dead, %d blocked, %d timeout, %d error\n",
			stats.OKCount, stats.DeadCount, stats.BlockedCount, stats.TimeoutCount, stats.ErrorCount)
	}
	fmt.Fprintln(stdout, "--------------------------------------------------")

	if len(deadLinks) > 0 || len(errorLinks) > 0 {
		return ExitBroken
	}
	if *failOnBlocked && len(blockedLinks) > 0 {
		return ExitBroken
	}
	if *failOnTimeout && len(timeoutLinks) > 0 {
		return ExitBroken
	}

	return ExitClean
}
