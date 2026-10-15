# Learner Profile & Project Session Guide

## Learner Context (Project-Specific)
- **Name**: Mnasie
- **Background**:
  - Strong C++ systems programming (polling, socket syscalls, binary framing, virtual functions/vtables).
  - Familiar with Rust systems concepts (Tokio async runtime, `mpsc` channels, trait objects / dynamic dispatch, `Result` error modeling).
- **Go Knowledge**: Complete beginner — **zero prior experience with Go**.
- **Mental Model Bridge**: Map all Go constructs directly to C++ and Rust equivalents:
  - *Goroutines vs Tokio Tasks / OS Threads*: Unlike Rust's `async`/`await` state machines (function coloring), Go uses M:N cooperative/preemptive green threads with dynamic stacks (~2KB starting size) scheduled across OS threads by the Go runtime (GMP model).
  - *Go Channels vs Rust `mpsc`*: Native language primitive (`chan T`), acts as a synchronized ring buffer + wait queues. `select` in Go works like Rust's `tokio::select!` but at the language level.
  - *Go Interfaces vs C++ Virtual Functions / Rust `dyn Trait`*: Structural (duck) typing without explicit `implements` or `override`. Under the hood, an interface is a 2-word fat pointer: `itab` (type descriptor + method table) and a pointer to the concrete value.
  - *Go netpoller vs Linux `epoll` / C++ Event Loops*: Go code writes sequential, blocking-style socket calls (`conn.Read()`), but the Go runtime registers the underlying file descriptor with `epoll`/`kqueue`. When a socket blocks, only that goroutine is descheduled while the OS thread keeps running other goroutines.
  - *Memory & Allocation*: Escape analysis decides stack vs heap. No RAII destructors; cleanup is handled via `defer` (for resource handles/closing sockets) and a concurrent mark-and-sweep GC (for memory).
  - *Error Handling*: Explicit multiple return values `(val, error)`, directly comparable to Rust's `Result<T, E>`.

---

## Session Format: Passive Micro-Chunked
- The agent writes the code; the learner observes, reads the code, and absorbs the concepts.
- **Rule**: Never dump an entire stage at once. Each stage is broken down into small, distinct chunks.
- **Workflow for each chunk**:
  1. Build the specific chunk (clean code + inline comments on *why*).
  2. Explain the concepts used (Go syntax, runtime mechanics, networking details, C++ / Rust comparisons).
  3. Pause so the learner can inspect the code and confirm understanding before moving to the next chunk.

---

## Stage Breakdown by Chunks

### Stage 1: CLI MVP
- **Chunk 1.1**: URL Normalization & Link Utilities (`internal/crawler/url.go`)
  - Parse, strip fragments (`#hash`), handle trailing slashes, scheme validation (`http`/`https`).
  - Resolve relative paths against base URL.
  - Classify internal vs external URLs based on hostname.
- **Chunk 1.2**: HTML Link Extractor (`internal/crawler/extract.go`)
  - Stream HTML via `golang.org/x/net/html` tokenizer (avoiding expensive DOM trees).
  - Extract all `href` and `src` attributes.
- **Chunk 1.3**: Single Link Prober (`internal/crawler/checker.go`)
  - Issue HTTP request, record status code or network errors into a `Result` struct.
  - Basic error classification (broken vs alive).
- **Chunk 1.4**: Worker Pool & Visited Deduplication Set (`internal/crawler/crawler.go`)
  - Thread-safe visited set with `sync.RWMutex`.
  - Worker pool using goroutines and channels (`chan Job`, `chan Result`).
  - In-flight counter to coordinate recursive internal traversal and clean termination.
- **Chunk 1.5**: CLI Entrypoint & Terminal Summary (`cmd/deadlink/main.go`)
  - Flag parsing (`--concurrency`, `--timeout`, `--max-depth`).
  - Clean streaming terminal output and broken links summary table.
  - Exit code `0` (clean) vs `1` (broken links found).
- **Chunk 1.6**: Integration Testing with `httptest` (`internal/crawler/crawler_test.go`)
  - Local mock server with internal pages, broken links (404), external links, and loop protection.

### Stage 2: Robustness
- **Chunk 2.1**: HEAD -> GET Fallback
  - Probing with HEAD; automatically falling back to GET on 405/501 or unexpected drops.
- **Chunk 2.2**: Context Timeouts & Retries with Backoff
  - `context.WithTimeout`, exponential backoff for transient errors (429, 503, connection resets).
- **Chunk 2.3**: Redirect Control & Loop Detection
  - Custom `http.Client.CheckRedirect` policy to detect infinite redirect loops and enforce hop limit.
- **Chunk 2.4**: Per-Host Rate Limiting
  - Token-bucket rate limiter (`golang.org/x/time/rate`) per domain to avoid slamming target servers.
- **Chunk 2.5**: robots.txt Compliance
  - Fetch and parse `/robots.txt` before crawling internal paths; respect disallow rules and crawl-delay.

### Stage 3: Output & Automation
- **Chunk 3.1**: Report Data Model & Interface (`internal/report`)
- **Chunk 3.2**: JSON Formatter (`--format=json` for automation and scripts)
- **Chunk 3.3**: Markdown Formatter (`--format=markdown` for PR comments and summaries)
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

### Stage 6: Ship it (Docker, Render, CI/CD)
- **Chunk 6.1**: Multi-stage Static Dockerfile
- **Chunk 6.2**: Render Deployment Blueprint (`deadlink.mnasies.me`)
- **Chunk 6.3**: GitHub Actions & GoReleaser Pipeline
