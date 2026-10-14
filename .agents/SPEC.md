# deadlink - Project Specification

## Overview
`deadlink` is a high-performance, resilient, and safe broken link checker written in Go.
Given a starting URL, it crawls the website and reports every broken link along with:
- Target URL
- HTTP status code or network error
- The source page (parent URL) where the link was discovered

The project provides two interfaces sharing a single core library:
1. **CLI (`cmd/deadlink`)**: Fast terminal utility for developers and CI/CD pipelines. Exits with code `1` if broken links are found.
2. **Web Service (`cmd/server`)**: A lightweight web server embedding a single web interface (`go:embed`) with live streaming results via Server-Sent Events (SSE).

---

## Technical Stack & Constraints
- **Language**: Go (`go1.27+`)
- **HTML Parsing**: `golang.org/x/net/html`
- **Rate Limiting**: `golang.org/x/time/rate`
- **CLI Flags**: Standard library `flag` package
- **Networking**: Standard library `net/http`
- **Concurrency**: `context.Context`, Goroutines, Channels, `sync.WaitGroup`, bounded worker pools
- **Testing**: `net/http/httptest`
- **Packaging**: Single static binary, multi-stage Dockerfile
- **No external heavy frameworks**: Keep dependencies minimal, focused, and standard.

---

## Directory Layout
```
deadlink/
├── .agents/             # Agent guidelines, specifications, stages, and architecture docs
│   ├── SPEC.md          # Complete project specification
│   ├── STAGES.md        # The 6 learning & implementation stages
│   ├── ARCHITECTURE.md  # Architectural deep dive (concurrency, networking, safety)
│   └── RULES.md         # Working agreements and code style rules
├── cmd/
│   ├── deadlink/        # CLI entry point
│   └── server/          # Web server entry point (embeds UI, handles SSE)
├── internal/
│   ├── crawler/         # Core crawler logic, worker pool, traversal
│   ├── safety/          # SSRF prevention, IP validation, redirects check, limits
│   └── report/          # Output formatting (JSON, Markdown, terminal)
├── web/                 # Static assets and single-page HTML template (embedded in server)
├── Dockerfile           # Minimal static container image
├── go.mod
├── go.sum
└── README.md
```

---

## Core Behavior
- **URL Extraction**: Download page, parse HTML AST using `golang.org/x/net/html`, extract `href` and `src` attributes.
- **Normalization**: Strip URI fragments (`#hash`), handle trailing slashes consistently, resolve relative links against current base URL.
- **Deduplication**: In-memory visited set (thread-safe) to avoid duplicate network checks and infinite loops.
- **Traversal Strategy**:
  - *Internal links* (same origin/host): crawled recursively up to `maxDepth` and `maxPages`.
  - *External links*: status-checked only (non-recursive).
- **Worker Pool**: Bounded concurrency pool preventing resource exhaustion.
- **Per-Host Rate Limiting**: Token bucket rate limiter per host (`golang.org/x/time/rate`) to avoid overwhelming target servers.
- **HTTP Verification**:
  - Issue `HEAD` request first (cheap probe).
  - Fallback to `GET` if `HEAD` fails with status `405 Method Not Allowed`, `501 Not Implemented`, or unexpected rejection.
- **Network Resilience**:
  - Context timeouts per request and crawl-level timeout.
  - Redirect limits to detect and abort redirect loops.
  - Retries with backoff for transient network issues before marking a link as broken.
- **Politeness**:
  - Parse and respect `robots.txt`.
  - Send a descriptive `User-Agent` header.
- **Exit Code**: Exit `1` on broken links; `0` on clean crawl.

---

## Security (Strict & Non-Negotiable)
- **Scheme Restriction**: Allow only `http://` and `https://`.
- **SSRF Guard**:
  - Resolve DNS hostnames to IP addresses prior to connecting.
  - Block loopback (`127.0.0.0/8`, `::1`), private networks (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`), link-local addresses (`169.254.0.0/16`, `fe80::/10`), IPv4-mapped IPv6, and cloud metadata endpoints (`169.254.169.254`).
  - Re-validate on every HTTP redirect hop.
  - Pin the connection to the validated IP to prevent DNS rebinding (via custom `http.Transport.DialContext`).
- **Resource Limits**:
  - `maxPages` cap (e.g., max 1000 pages).
  - `maxLinks` cap.
  - `maxDepth` cap (e.g., max depth 5).
  - `maxResponseSize` (e.g., limit HTML read to 2-5 MB using `io.LimitReader`).
  - Overall crawl timeout.
- **Web Server Protection**:
  - Per-IP rate limiting for the web server endpoint.
  - Global concurrent crawl limit (prevent server resource starvation).
  - Strict HTML escaping of all user and crawl inputs in UI (prevent XSS).
