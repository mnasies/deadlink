# Architecture & Technical Design

## System Architecture

`deadlink` is structured cleanly into reusable internal packages and dual command entry points:

```
[cmd/deadlink] (CLI)      [cmd/server] (Web / SSE)
       \                      /
        v                    v
     [internal/crawler] (Core Engine)
        /              \
       v                v
[internal/safety]    [internal/report]
(SSRF & Limits)      (JSON / MD / Plain)
```

---

## Concurrency Model

### 1. Worker Pool Pattern
- The crawler utilizes a worker pool to avoid unbounded goroutine creation.
- A buffered work channel (`chan CrawlJob` or `chan LinkCheckJob`) feeds a fixed number of worker goroutines (configurable via `--concurrency`, default e.g. 10-20).
- `sync.WaitGroup` manages the lifecycle of worker goroutines.
- Context cancellation (`context.WithCancel` / `context.WithTimeout`) ensures immediate termination when limits or timeouts are reached.

```
       [URL Queue / Channel]
           /       |       \
          v        v        v
     Worker 1   Worker 2  Worker N
          \        |        /
           v       v       v
         [Result Dispatcher]
        /                   \
   [Visited Set]      [Reporter / SSE Stream]
```

### 2. State & Deduplication
- A thread-safe visited set tracks normalized URLs (`sync.RWMutex` guarding `map[string]struct{}`).
- Only newly discovered internal URLs within the depth limit are scheduled for full page crawling.
- External URLs are checked once for status and never traversed.

---

## Networking & Resilience

### 1. HTTP Verification Pipeline
When probing a link:
1. Check per-host rate limiter (`rate.Limiter.Wait(ctx)`).
2. Issue an HTTP `HEAD` request with timeout.
3. If response status is `405 Method Not Allowed`, `501 Not Implemented`, or connection fails unexpectedly, fall back to a minimal `GET` request.
4. For transient errors (status 429, 503, network timeouts), retry with exponential backoff up to a maximum attempt count.

### 2. SSRF Protection Architecture (`internal/safety`)
To protect against SSRF and DNS rebinding:
- Custom `http.Transport` with a customized `DialContext`.
- When connecting to a host, resolve the hostname using `net.DefaultResolver`.
- Validate that all resolved IPs are public and outside blocked CIDRs:
  - `127.0.0.0/8` (IPv4 loopback)
  - `::1/128` (IPv6 loopback)
  - `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16` (Private RFC 1918)
  - `169.254.0.0/16`, `fe80::/10` (Link-local & Cloud Metadata)
  - `fc00::/7` (IPv6 Unique Local)
- Pin the connection to the validated IP directly in the dialer.
- Hook into `http.Client.CheckRedirect` to execute the exact same IP check on every subsequent redirect destination.

---

## Web Server & SSE Streaming Protocol (`cmd/server`)
- Built with standard `net/http` and `embed.FS`.
- The single HTML page opens an SSE connection (`GET /api/crawl/stream?url=...`).
- The server writes standard SSE format:
  ```
  event: link
  data: {"url":"https://example.com/broken","status":404,"parent":"https://example.com"}

  event: progress
  data: {"pagesCrawled":12,"linksChecked":85,"brokenCount":2}

  event: complete
  data: {"pagesCrawled":15,"linksChecked":104,"brokenCount":2,"durationMs":1420}
  ```
- Backpressure and cancellation: when the HTTP connection drops (`r.Context().Done()`), the background crawl goroutine terminates promptly.
