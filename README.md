# deadlink

A fast, resilient, and secure broken link checker written in Go.

Given a URL, `deadlink` crawls the website, identifies all hyperlinks and assets, checks their availability concurrently, and reports any broken links along with their HTTP status or network error and the source page they were found on.

## Features

- **Concurrent Crawling**: Bounded worker pools with configurable concurrency.
- **Safe by Design**: Built-in SSRF protection, IP verification, and resource caps.
- **Resilient**: `HEAD` request with automatic `GET` fallback, retries with backoff, and redirect loop detection.
- **Dual Interfaces**: 
  - Standalone CLI for terminals and CI/CD pipelines.
  - Minimal web server embedding a single-page UI with live Server-Sent Events (SSE) streaming.
- **Standard Library Core**: Zero heavy web frameworks. Single static binary.

## Installation & Usage

### Build from source
```bash
go build -o deadlink ./cmd/deadlink
```

### Basic Crawl
```bash
./deadlink https://example.com
```

### Options
```text
Usage: deadlink [options] <url>

Options:
  -concurrency int
        Number of concurrent worker goroutines (default 10)
  -max-depth int
        Maximum link recursion depth for internal pages (default 3)
  -rate-limit float
        Maximum requests per second per host (0 = unlimited) (default 10)
  -timeout duration
        Network timeout per link probe (default 10s)
  -ignore-robots
        Bypass robots.txt restrictions (default false)
  -fail-on-blocked
        Treat bot-blocked links (403/429) as CI failures (default false)
  -fail-on-timeout
        Treat timed-out links as CI failures (default false)
```

### Exit Codes
- `0`: Crawl completed cleanly with no broken/dead links detected.
- `1`: One or more broken links detected (CI failure signal).
- `2`: Invalid flags, arguments, or initialization failure.
