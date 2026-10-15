# Learner Profile & Project Session Guide

## Learner Context (Project-Specific)
- **Name**: Mnasie
- **Background**: Strong C++ and systems programming (TCP/UDP socket protocols, custom binary framing, OS internals, socket syscalls).
- **Go Knowledge**: Complete beginner — **zero prior experience with Go**.
- **Mental Model Bridge**: Connect all Go constructs to low-level / C++ systems equivalents:
  - *Goroutines* vs OS threads & thread pools (m:n runtime scheduling, lightweight stack growth).
  - *Channels* vs thread-safe queues / ring buffers (locks + condvars / wait queues).
  - *Interfaces* vs C++ vtables (fat pointers with itab and data pointer).
  - *Slices* vs C++ `std::span` / `{ptr, len, cap}` structs.
  - *Error handling* vs C++ `std::expected` / Rust `Result` (explicit value returns, no exceptions).
  - *netpoller* vs Linux `epoll` / non-blocking socket loops.

---

## Session Format: Passive Micro-Chunked
- The agent writes the code; the learner observes, reads the code, and absorbs the concepts.
- **Rule**: Never dump an entire stage at once. Each stage is broken down into small, distinct chunks.
- **Workflow for each chunk**:
  1. Build the specific chunk (clean code + inline comments on *why*).
  2. Explain the concepts used (Go syntax, runtime mechanics, networking details, C++ comparison).
  3. Pause so the learner can inspect the code and confirm understanding before moving to the next chunk.

---

## Stage Breakdown by Chunks

### Stage 1: CLI MVP
- **Chunk 1.1**: URL Normalization & Link Utilities (`internal/crawler/url.go`)
  - Strip fragments, trailing slashes, resolve relative URLs, classify internal vs external.
- **Chunk 1.2**: HTML Link Extractor (`internal/crawler/extract.go`)
  - Streaming tokenizer (`x/net/html`), parse `href` and `src` attributes without loading full DOM tree.
- **Chunk 1.3**: Single Link Prober (`internal/crawler/checker.go`)
  - Issue HTTP probe, record status code / error into a `Result` struct.
- **Chunk 1.4**: Worker Pool & Visited Deduplication Set (`internal/crawler/crawler.go`)
  - Mutex-guarded visited set, bounded concurrency with goroutines and channels, in-flight job coordinator.
- **Chunk 1.5**: CLI Entrypoint & Terminal Summary (`cmd/deadlink/main.go`)
  - CLI flags (`flag` package), terminal progress output, broken link summary, exit codes.
- **Chunk 1.6**: Integration Testing with `httptest` (`internal/crawler/crawler_test.go`)
  - Mock local HTTP server with working/broken/external links, verify crawler behavior without real network.

### Stage 2: Robustness
- **Chunk 2.1**: HEAD -> GET Fallback
  - Probing with HEAD; gracefully retrying with GET if server returns 405/501 or closes connection.
- **Chunk 2.2**: Context Timeouts & Retries with Backoff
  - `context.WithTimeout`, exponential backoff for transient errors (429, 503, network timeouts).
- **Chunk 2.3**: Redirect Control & Loop Detection
  - Custom `http.Client.CheckRedirect` policy to detect infinite redirect loops and enforce hop limit.
- **Chunk 2.4**: Per-Host Rate Limiting
  - Token-bucket rate limiter (`x/time/rate`) per hostname to prevent flooding servers.
- **Chunk 2.5**: robots.txt Compliance
  - Fetch and parse `/robots.txt` before crawling internal paths; respect disallow rules.

### Stage 3: Output & Automation
- **Chunk 3.1**: Report Data Model & Interface (`internal/report`)
- **Chunk 3.2**: JSON Formatter (machine-readable CI/CD output)
- **Chunk 3.3**: Markdown Formatter (human-readable tables for PR comments)
- **Chunk 3.4**: CLI Formatting Flags & Strict Exit Codes (`0` = clean, `1` = broken, `2` = fatal error)

### Stage 4: Safety & SSRF Guard
- **Chunk 4.1**: IP Filtering & Forbidden CIDR Ranges (`internal/safety`)
  - Block loopback, RFC 1918 private IPs, link-local, cloud metadata (`169.254.169.254`).
- **Chunk 4.2**: Safe Custom Dialer & IP Pinning
  - Pre-connection DNS resolution, socket dialing pinned to validated IP (defeats DNS rebinding).
- **Chunk 4.3**: Redirect Hop SSRF Re-validation
  - Re-verify target IP on every HTTP redirect hop.
- **Chunk 4.4**: Resource Caps & Guards
  - Limit HTML response size (`io.LimitReader`), max pages, max depth, global crawl timeout.
- **Chunk 4.5**: SSRF & Safety Test Suite
  - `httptest` tests simulating private IP probes, slowloris timeouts, memory bombs.

### Stage 5: Web Server & Live SSE Streaming
- **Chunk 5.1**: Embedded Single-Page UI (`web/`, `go:embed`)
- **Chunk 5.2**: Server-Sent Events (SSE) Protocol Engine (`cmd/server`)
  - Streaming live link events and crawl stats over `text/event-stream`.
- **Chunk 5.3**: Web Protection & Concurrency Limits
  - Per-IP rate limiting and global active crawl semaphore.
- **Chunk 5.4**: Frontend SSE Integration & Strict HTML Escaping

### Stage 6: Ship it (Docker, Deploy, Release)
- **Chunk 6.1**: Multi-stage Static Dockerfile
- **Chunk 6.2**: Render Deployment Blueprint (`deadlink.mnasies.me`)
- **Chunk 6.3**: GitHub Actions & GoReleaser Pipeline
