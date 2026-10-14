# Implementation Stages Roadmap

Development is split into 6 focused, iterative stages.
After each stage, stop for user review and provide an explanation of the concurrency and networking concepts implemented.

---

## Stage 1: CLI MVP
**Goal**: Crawl one domain, check links concurrently, and print broken ones.

### What it does
- Takes a starting URL via command-line argument.
- Downloads the initial page and parses HTML using `golang.org/x/net/html`.
- Extracts all `href` and `src` links, normalizes them, and distinguishes internal vs external links.
- Uses a worker pool pattern with goroutines and channels to concurrently probe links.
- Keeps a thread-safe `visited` set to avoid visiting URLs multiple times or getting caught in loops.
- Differentiates broken links (HTTP 4xx, 5xx, DNS errors, connection refused) from working links (HTTP 2xx, 3xx).
- Prints broken links found (URL, status/error, source page).

### Concurrency & Networking Focus
- Goroutines as lightweight concurrent execution units.
- Channels as synchronization and work distribution primitives.
- Bounded concurrency with worker pools (`sync.WaitGroup`).
- Mutexes vs concurrent maps for the visited set.

---

## Stage 2: Robustness
**Goal**: Handle real-world network quirks and server idiosyncrasies gracefully.

### What it does
- **HEAD -> GET Fallback**: Some servers reject `HEAD` requests (e.g. 405 Method Not Allowed, 501 Not Implemented, or dropping connections). Fall back to a lightweight `GET` request (reading only the headers).
- **Timeouts & Retries**: Add per-request timeouts with `context.WithTimeout` and retries with exponential backoff for transient network errors (e.g., 429 Too Many Requests, 503 Service Unavailable, connection resets).
- **Redirects & Loops**: Custom `http.Client.CheckRedirect` policy to detect infinite redirect loops and limit maximum redirect hops (e.g., max 10).
- **Per-Host Rate Limiting**: Implement token bucket rate limiters per hostname using `golang.org/x/time/rate` to avoid slamming third-party servers.
- **robots.txt**: Fetch and parse `/robots.txt` before crawling internal pages; respect disallow directives and crawl-delay.

### Concurrency & Networking Focus
- Context propagation and cancellation.
- HTTP semantics (idempotency, HEAD vs GET, transfer encoding).
- Token bucket algorithm for rate limiting.

---

## Stage 3: Output & Automation
**Goal**: Rich reporting formats and CI/CD readiness.

### What it does
- Implement `/internal/report` with dual output formatters:
  - **JSON output** (`--format=json`): structured machine-readable format for automation and scripts.
  - **Markdown output** (`--format=markdown`): human-readable tables suitable for posting to PR comments or issue summaries.
  - **Terminal / Plain text**: default clean streaming output with summary statistics (checked, alive, broken, skipped).
- Strict exit code semantics:
  - Exit code `0`: Crawl completed and no broken links were found.
  - Exit code `1`: One or more broken links were found.
  - Exit code `2`: Fatal configuration or initial connectivity error.

---

## Stage 4: Safety & SSRF Guard
**Goal**: Robust defense against Server-Side Request Forgery and resource exhaustion.

### What it does
- Implement `/internal/safety` package.
- **SSRF Guard**:
  - DNS resolution before connection.
  - IP filtering: reject loopback (`127.0.0.0/8`, `::1`), private (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`), link-local (`169.254.0.0/16`, `fe80::/10`), IPv4-mapped IPv6, broadcast, and cloud metadata IPs (`169.254.169.254`).
  - Pin dialer to the validated IP to defeat DNS rebinding attacks.
  - Re-validate target IP on every redirect hop.
- **Crawl Limits**:
  - Hard caps on `maxPages`, `maxLinks`, `maxDepth`, and `maxResponseSize` (`io.LimitReader`).
  - Global crawl timeout.
- **Unit & Integration Tests**:
  - Comprehensive tests using `httptest.Server` simulating:
    - 404 Not Found & 500 Internal Server Error
    - Redirect loops
    - Slow responses (timeout testing)
    - Private IP / SSRF targets
    - Large payload / memory bomb attempts

---

## Stage 5: Web Server & Live SSE Streaming
**Goal**: Provide a clean web dashboard with live crawl results.

### What it does
- Implement `/cmd/server` that serves a single embedded HTML dashboard (`go:embed`).
- **Server-Sent Events (SSE)**:
  - Client submits a URL.
  - Server initiates crawler in a background goroutine and streams live events (`link-checked`, `broken-link`, `progress`, `completed`, `error`) over an `HTTP text/event-stream`.
- **Web Safety**:
  - Web-specific rate limiting per client IP.
  - Global concurrent crawl limit (e.g., max 3 active crawls simultaneously) to avoid server exhaustion on free-tier hosting.
  - Strict HTML escaping for all displayed URLs and error strings.

---

## Stage 6: Ship it (Docker, Render, CI/CD)
**Goal**: Package, containerize, deploy, and automate distribution.

### What it does
- **Multi-stage Dockerfile**: builds a scratch or alpine minimal static binary with ca-certificates.
- **Render Deployment**: configuration and instructions to host the web interface at `deadlink.mnasies.me`.
- **GitHub Actions & GoReleaser**:
  - Workflow to run `go test ./...` and `golangci-lint` on pull requests.
  - GoReleaser configuration to compile static binaries for Linux, macOS (Intel/ARM), and Windows, publishing GitHub releases automatically on Git tags.
