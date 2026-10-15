# deadlink

A fast broken link checker written in Go that crawls your site, checks every link, and doesn't freak out over bot blocks.

```text
$ deadlink https://example.com
Crawling https://example.com (concurrency=10, max-depth=3, rate-limit=10.0/s, timeout=10s)...

[ OK ] 200  - https://example.com/
[ OK ] 200  - https://example.com/docs
[DEAD] 404  - https://example.com/broken-link
       found on: https://example.com/
[ OK ] 200  - https://github.com/

--------------------------------------------------
Summary: 4 links checked in 544ms. 1 broken link(s) found.
--------------------------------------------------
```

## Why I built this

Most link checkers are either heavy web crawlers or quick scripts that break on real-world websites. They hammer third-party servers until they get IP banned, choke on redirect loops, or fail your CI pipeline just because Cloudflare threw a 403 challenge at a perfectly valid external link.

`deadlink` is built to be fast, polite, and reliable in CI:

- **Recursive internal crawler**: Discovers internal pages up to a configurable depth while keeping external checks to a single probe.
- **Smart classification**: Separates actual broken links (`404`, `410`, DNS errors, redirect loops) from bot throttling (`403`, `429`, Cloudflare challenges) and network timeouts.
- **Polite by default**: Obeys `robots.txt` rules and crawl-delays, with per-host token bucket rate limiting to prevent slamming servers.
- **Bandwidth-saving probes**: Tries `HEAD` first, falls back to `GET` only when servers reject `HEAD`, and reuses TCP sockets.
- **Zero DOM overhead**: Streams HTML tokens directly to extract links instead of buffering huge page trees in memory.

## Install

### Go install
```bash
go install github.com/mnasies/deadlink/cmd/deadlink@latest
```

### Build from source
Requires Go 1.22 or newer:
```bash
git clone https://github.com/mnasies/deadlink.git
cd deadlink
go build -o deadlink ./cmd/deadlink
```

## Usage

Check a website with default settings (10 workers, max depth 3, 10 req/s per host):
```bash
deadlink https://example.com
```

Only check the landing page without crawling deeper links:
```bash
deadlink -max-depth=0 https://example.com
```

Run in CI and fail the build if any bot-blocked links or timeouts occur:
```bash
deadlink -fail-on-blocked -fail-on-timeout https://example.com
```

### Flags

| Flag | Default | Description |
| --- | --- | --- |
| `-concurrency` | `10` | Number of worker goroutines checking links concurrently |
| `-max-depth` | `3` | Maximum link recursion depth for internal pages (`0` checks root page only) |
| `-rate-limit` | `10.0` | Maximum requests per second per hostname (`0` disables rate limiting) |
| `-timeout` | `10s` | Network timeout per probe |
| `-ignore-robots` | `false` | Bypass `robots.txt` disallow rules and crawl-delays |
| `-fail-on-blocked` | `false` | Treat bot-blocked responses (`403`, `429`, WAF challenges) as CI failures |
| `-fail-on-timeout` | `false` | Treat network timeouts as CI failures |

### Exit codes

- `0`: Crawl completed cleanly with no dead links.
- `1`: Found one or more broken links (or bot-blocked/timeout links if the `-fail-on-*` flags are enabled).
- `2`: Bad arguments, invalid flags, or initial connection failure.

## How it works

1. **Discovery & Queue**: `deadlink` normalizes the root URL and fetches `/robots.txt`. If the host declares a `Crawl-delay`, the per-host rate limiter automatically throttles down to match.
2. **Worker Pool**: Goroutines pull URLs from a central job queue. Internal URLs (matching the initial hostname) are extracted for further crawling; external URLs are probed once for reachability and dropped.
3. **HTTP Prober**:
   - Sends a `HEAD` request with a descriptive user agent.
   - If the server rejects `HEAD` with `405 Method Not Allowed`, `501 Not Implemented`, or `403 Forbidden`, it retries with `GET`.
   - Transient failures (`429`, `502`, `503`, `504`, connection resets) retry with exponential backoff and respect `Retry-After` headers up to 5 seconds.
   - Tracks redirect history and halts early if circular redirects or too many hops (>10) are detected.
4. **Streaming HTML Extraction**: When an internal page responds with `200 OK` and `text/html`, the tokenizer pulls `href` and `src` attributes on the fly, resolves relative paths, strips fragments (`#section`), and deduplicates URLs before queuing.
5. **Classification**:
   - `OK`: 2xx / 3xx responses.
   - `DEAD`: 404, 410, DNS resolution failures, connection refused, or redirect loops.
   - `BLOCKED`: 401, 403, 429, 451, or Cloudflare challenge pages.
   - `TIMEOUT`: Network timeouts, context deadlines, or 504 gateway timeouts.
   - `ERROR`: 500 server errors or broken sockets.

## Security and limits

- **Network reach**: `deadlink` does not currently block private IP addresses or loopback ranges (`127.0.0.1`, `10.0.0.0/8`, `169.254.169.254`). Do not expose it as an open endpoint or webhook runner without network isolation.
- **Unsupported schemes**: Non-HTTP links (`mailto:`, `tel:`, `javascript:`, `ftp:`) are ignored during extraction and won't be flagged as broken.
- **Graceful shutdown**: Pressing `Ctrl+C` cleanly drains workers and outputs summary metrics for all links checked up to that moment.

## What it can't do yet

- **JavaScript rendering**: Links rendered dynamically via client-side JavaScript (SPAs like React or Vue) aren't executed. Only links present in the raw HTML stream are picked up.
- **Export formats**: Results are currently printed to stdout/stderr. Machine-readable formats (`--json`, `--markdown`) are planned.
- **SSRF protection**: IP pinning and private subnet filtering are not implemented yet.
- **Web UI**: A browser interface with live SSE updates is on the roadmap, but the current build is CLI-only.

## Development

Run all tests:
```bash
go test -v ./...
```

Run race detector:
```bash
go test -race ./...
```

Build the CLI binary:
```bash
go build -o deadlink ./cmd/deadlink
```

### Project layout

```text
deadlink/
├── cmd/
│   └── deadlink/        # CLI command, flag parsing, and console output
├── internal/
│   └── crawler/         # Core crawler, worker pool, tokenizer, and HTTP checker
│       └── test/        # Unit and integration test suites
├── go.mod
└── go.sum
```

## Contributing and license

Pull requests and bug reports are welcome. Please ensure `go test ./...` passes before opening a PR.

This project does not have an explicit open-source license file yet. If you plan to use it commercially or redistribute it, check back once a `LICENSE` is added.
