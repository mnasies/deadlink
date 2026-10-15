# Project Progress & Learning Log

---

## Stage 1: CLI MVP

### Chunk 1.1: URL Normalization & Link Utilities
**File**: [`internal/crawler/url.go`](../internal/crawler/url.go)  
**Tests**: [`internal/crawler/test/url_test.go`](../internal/crawler/test/url_test.go)

#### 1. What was built & Why
In web crawling, deduplication is paramount. If you visit `https://example.com/about#team`, `https://example.com/about`, and `HTTP://EXAMPLE.COM/about/`, a naive crawler would hit the network 3 separate times and risk infinite loops.
- `NormalizeURL`: Cleans a raw URL string into a canonical representation (lowercase scheme/host, stripped fragments, unified trailing slashes).
- `ResolveURL`: Resolves relative URLs (`/contact`, `../blog`, `script.js`) extracted from an HTML document against the document's base URL, then normalizes the result. Filters out non-HTTP schemes like `mailto:` and `javascript:`.
- `IsInternal`: Compares the hostnames of two URLs to determine if a target is within the same site (crawl recursively) or external (probe status only).

---

#### 2. Go Concepts Explained (from a C++ / Rust perspective)

##### Packages & Visibility
- Every Go file starts with `package <name>`.
- **Visibility rule**: Capitalized identifiers (`NormalizeURL`, `ErrEmptyURL`) are **public/exported** (visible outside the package). Lowercase identifiers (`trimmed`, `baseHost`) are **private/unexported** (package-scoped).
- No `public:`, `private:`, or `pub` keywords.

##### Pointers & Structs (`*url.URL`)
- In Go, `url.Parse()` returns `(*url.URL, error)` — a pointer to a struct allocated on the heap (or stack if escape analysis proves it doesn't escape).
- Go has pointers (`*T`), but **no pointer arithmetic** (unlike C++).
- Automatic dereferencing: `parsed.Scheme` works directly on `*url.URL`. You don't write `parsed->Scheme` or `(*parsed).Scheme`.

##### Multiple Return Values & Error Handling
- Go functions return multiple values: `func NormalizeURL(...) (string, error)`.
- Idiomatic error pattern:
  ```go
  result, err := SomeFunc()
  if err != nil {
      return "", err // Early return / bubble up
  }
  ```
  This is the Go equivalent of Rust's `Result<T, E>` and `?` operator, or C++23 `std::expected`. Go chooses explicit `if err != nil` over monads.

##### Sentinel Errors (`errors.New` & `errors.Is`)
- Declared as package-level `var ErrSomething = errors.New(...)`.
- Callers check error identity using `errors.Is(err, ErrUnsupportedScheme)`.
- This is analogous to `std::error_code` in C++ or custom enum variants in Rust.

##### Slices vs Arrays
- Slices (`[]struct{...}` in `url_test.go`) are dynamic views into backing arrays: a 3-word struct of `{ pointer, length, capacity }`, directly equivalent to `std::span` in C++ or `&[T]` in Rust.

---

#### 3. C++ / Rust Mental Model Mapping

| Go Concept | C++ Analogy | Rust Analogy |
|---|---|---|
| `url.URL` struct | `class URL` or `struct URL` | `struct Url` (from `url` crate) |
| `parsed.ResolveReference(ref)` | `std::filesystem::path::lexically_normal` or URI resolver | `url.join(ref)` |
| `errors.Is(err, target)` | `err == target_error_code` | `matches!(err, TargetError)` |
| Table-driven tests (`[]struct{...}`) | Catch2 / GTest parameterized tests | `rstest` or array-of-structs test loops |
| `strings.EqualFold` | `strcasecmp` / `boost::iequals` | `s1.eq_ignore_ascii_case(s2)` |

---

#### 4. API Inventory (Created in Chunk 1.1)

- **Variables / Errors**:
  - `ErrEmptyURL error`: Returned when the input URL string is empty or contains only whitespace.
  - `ErrUnsupportedScheme error`: Returned when the scheme is not `http` or `https` (e.g. `mailto:`, `ftp:`, `javascript:`).
- **Functions**:
  - `NormalizeURL(rawURL string) (string, error)`: Normalizes casing, strips fragments, cleans trailing slashes.
  - `ResolveURL(base *url.URL, ref string) (string, error)`: Resolves any relative/absolute reference against a base URL and normalizes it.
  - `IsInternal(baseURL, targetURL *url.URL) bool`: Returns `true` if both URLs share the exact same hostname (case-insensitive).

---

### Chunk 1.2: HTML Link Extractor
**File**: [`internal/crawler/extract.go`](../internal/crawler/extract.go)  
**Tests**: [`internal/crawler/test/extract_test.go`](../internal/crawler/test/extract_test.go)

#### 1. What was built & Why
A web page contains links inside hyperlinks (`<a href="...">`), external stylesheets (`<link href="...">`), scripts (`<script src="...">`), and embedded assets (`<img src="...">`).
- `ExtractLinks`: Consumes an incoming HTML byte stream (`io.Reader`), tokenizes the HTML on-the-fly, extracts all `href` and `src` attributes, resolves them relative to the base URL using `ResolveURL`, and returns a deduplicated slice of absolute URLs.
- **Architectural Decision (Streaming vs DOM Tree)**:
  - `html.Parse(r)` builds a complete hierarchical DOM tree where every tag is an `*html.Node` struct allocated on the heap. For a crawler fetching thousands of pages, this causes heavy garbage collection churn.
  - `html.NewTokenizer(r)` is a zero-to-low allocation streaming pull-parser (similar to an SAX/StAX parser in XML, or a byte-level lexer). It reads chunk-by-chunk from the `io.Reader`, avoiding DOM tree allocations.

---

#### 2. Go Concepts Explained (from a C++ / Rust perspective)

##### The `io.Reader` Interface
- In Go, `io.Reader` is defined as:
  ```go
  type Reader interface {
      Read(p []byte) (n int, err error)
  }
  ```
- Any type providing a `Read([]byte) (int, error)` method automatically satisfies `io.Reader` without explicitly declaring it (structural typing / implicit interface).
- This means `ExtractLinks` does not care whether bytes come from:
  - An HTTP socket response body (`resp.Body`)
  - An in-memory buffer (`strings.NewReader` or `bytes.Buffer`)
  - A file on disk (`os.File`)
- In C++, this is equivalent to passing `std::istream&`. In Rust, it is equivalent to accepting `impl std::io::Read`.

##### Zero-Size Struct Sets (`map[string]struct{}`)
- Go does not have a built-in `Set` type in its standard library.
- Idiomatic Go uses `map[K]struct{}`.
- `struct{}` is an empty struct. In the Go runtime, `unsafe.Sizeof(struct{}{}) == 0` bytes.
- Unlike `map[string]bool` (which consumes 1 extra byte of memory for every value boolean in the bucket array), `map[string]struct{}` occupies zero value storage in memory buckets.
- Idiom:
  ```go
  seen[url] = struct{}{}      // Insert into set
  if _, exists := seen[url]; !exists { ... } // Membership check
  ```

##### The Infinite Loop (`for { ... }`)
- Go has only one looping keyword: `for`.
- `for { ... }` is an infinite loop (equivalent to `while (true)` in C++ or `loop { ... }` in Rust).
- Loops are broken via `break`, `return`, or labels.

##### `io.EOF` as Stream Termination
- In Go stream processing, `io.EOF` ("End Of File") is a sentinel error returned by readers when no more bytes remain.
- It is treated not as a fatal failure, but as a normal stream completion signal: `if errors.Is(err, io.EOF) { return links, nil }`.

---

#### 3. C++ / Rust Mental Model Mapping

| Go Concept | C++ Analogy | Rust Analogy |
|---|---|---|
| `io.Reader` interface | `std::istream&` / abstract `IByteStream` | `std::io::Read` trait object |
| `html.NewTokenizer(r)` | Lexer / SAX streaming parser | `html5ever::tokenizer` / pull parser |
| `map[string]struct{}` | `std::unordered_set<std::string>` | `std::collections::HashSet<String>` |
| `struct{}` (zero-size type) | `struct Empty {}; sizeof(Empty) == 1` (in C++ empty structs are at least 1 byte; in Go they are 0 bytes) | `()` unit type / zero-sized type (ZST) |
| `switch tokenType` | `switch (tokenType)` | `match token_type` |

---

#### 4. API Inventory (Created in Chunk 1.2)

- **Functions**:
  - `ExtractLinks(baseURL *url.URL, r io.Reader) ([]string, error)`: Streams an HTML document, extracts, resolves, and deduplicates all target URLs from `href` and `src` attributes.

---

### Chunk 1.3: Single Link Prober
**File**: [`internal/crawler/checker.go`](../internal/crawler/checker.go)  
**Tests**: [`internal/crawler/test/checker_test.go`](../internal/crawler/test/checker_test.go)

#### 1. What was built & Why
Once URLs are extracted, the crawler needs a dedicated component to probe them over HTTP, measure network latency, detect broken statuses (4xx, 5xx), and capture network-level failures (DNS resolution errors, connection refused, TLS handshake errors, context timeouts).
- `Result`: Encapsulates the target URL, parent page URL where it was discovered, HTTP status code, duration, error, and a boolean `Broken` flag.
- `LinkChecker`: Wraps an `*http.Client` and a custom `User-Agent`.
- `Check(ctx, targetURL, parentURL)`: Executes the HTTP probe with context cancellation support. Drains up to 4KB of the response body to allow TCP connection reuse in Go's Keep-Alive pool, and guarantees socket closure via `defer resp.Body.Close()`.

---

#### 2. Go Concepts Explained (from a C++ / Rust perspective)

##### Struct Methods & Receivers (`func (c *LinkChecker) Check(...)`)
- Go does not have `class`. Data and behavior are separated:
  - Struct definition: `type LinkChecker struct { ... }`
  - Method declaration: `func (c *LinkChecker) Check(...)`
- `(c *LinkChecker)` is the **method receiver**:
  - Pointer receiver (`*LinkChecker`): analogous to `this` pointer in C++ or `&mut self` in Rust. Changes mutate the struct, and calling does not copy the struct.
  - Value receiver (`LinkChecker`): analogous to passing `self` by value in C++ / Rust. A shallow copy of the struct is passed.

##### The `defer` Statement vs. RAII Destructors
- Go does **not** have C++ destructors or Rust's `Drop` trait.
- Instead, Go provides `defer`:
  ```go
  resp, err := c.client.Do(req)
  if err != nil {
      return res
  }
  defer resp.Body.Close()
  ```
- `defer` registers a function call to execute automatically when the enclosing function returns (even if the function returns early or panics).
- **Socket File Descriptor Leaks**: In Go's `net/http`, if you do not call `resp.Body.Close()`, the underlying OS socket file descriptor is held open indefinitely by the runtime, eventually causing socket exhaustion (`EMFILE: too many open files`).

##### HTTP Keep-Alive & Body Draining
- Why `io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))`?
  - In HTTP/1.1 and HTTP/2, a single TCP connection can be reused for multiple sequential requests.
  - If a client closes `resp.Body` without reading the response data, the Go HTTP transport cannot reuse the TCP socket; it is forced to close the connection (TCP FIN/RST) and negotiate a brand new 3-way handshake + TLS session for the next request.
  - Reading a small slice of bytes (up to EOF) allows the connection to be cleanly returned to the `Transport`'s idle pool.

##### `context.Context` (Cancellation & Deadlines)
- In C++ / Rust, stopping an async or threaded socket call usually requires an atomic boolean, a cancellation token, or dropping the `Future`.
- In Go, `context.Context` is the standard currency for cancellation trees and timeouts.
- When `ctx, cancel := context.WithTimeout(..., 20*time.Millisecond)` expires, the context's internal channel `<-ctx.Done()` closes, which causes Go's netpoller to interrupt the socket syscall with `context deadline exceeded`.

##### Testing with `httptest.NewServer`
- Rather than mocking socket functions or hitting external websites during tests, Go's standard library provides `net/http/httptest.NewServer`.
- It boots a real local HTTP server on `127.0.0.1` on a random unused OS port, and provides a configured `server.Client()` that routes requests through loopback.

---

#### 3. C++ / Rust Mental Model Mapping

| Go Concept | C++ Analogy | Rust Analogy |
|---|---|---|
| `func (c *LinkChecker) Check()` | Member function `Result LinkChecker::Check()` | `impl LinkChecker { pub fn check(&self) -> Result }` |
| `defer resp.Body.Close()` | Scope guard / `std::unique_ptr` custom deleter | `impl Drop for Response` (automatic) |
| `context.Context` | `std::stop_token` (C++20) / cancellation token | `tokio_util::sync::CancellationToken` |
| `time.Since(start)` | `std::chrono::steady_clock::now() - start` | `Instant::now().elapsed()` |
| `httptest.NewServer` | In-process test server (e.g. Crow / cpp-httplib) | `wiremock` or in-process `axum` test server |

---

#### 4. API Inventory (Created in Chunk 1.3)

- **Constants**:
  - `DefaultUserAgent`: Default user-agent string identifying `deadlink`.
- **Structs**:
  - `Result`: Encapsulates URL, parent URL, status code, latency duration, error, and broken boolean.
  - `LinkChecker`: Prober struct managing the `*http.Client` and `User-Agent`.
- **Functions & Methods**:
  - `NewLinkChecker(client *http.Client, userAgent string) *LinkChecker`: Constructor with fallback defaults.
  - `(c *LinkChecker) Check(ctx context.Context, targetURL, parentURL string) Result`: Probes a single URL with context timeout support and socket cleanup.

---

### Architecture Refinement: Dedicated Test Subdirectory (`internal/crawler/test/`)
- Tests have been relocated to [`internal/crawler/test/`](../internal/crawler/test/).
- **Go Concept (Black-Box / External Testing)**:
  - In Go, a directory defines a package. Subdirectories are separate packages.
  - Test files in `internal/crawler/test/` declare `package crawler_test` and import `"github.com/mnasies/deadlink/internal/crawler"`.
  - This enforces **pure black-box testing**: tests can only interact with exported functions and types (`crawler.NormalizeURL`, `crawler.ExtractLinks`, `crawler.NewLinkChecker`), preventing accidental coupling to unexported internal implementation details.
  - Running `go test ./...` recursively tests all packages in the project tree.

---

### Chunk 1.4: Concurrent Worker Pool & Visited Deduplication Set
**File**: [`internal/crawler/crawler.go`](../internal/crawler/crawler.go)  
**Tests**: [`internal/crawler/test/crawler_test.go`](../internal/crawler/test/crawler_test.go)

#### 1. What was built & Why
A naive recursive crawler easily suffers from two catastrophic concurrency failure modes:
1. **Unbounded Goroutines**: Spawning a goroutine for every discovered URL leads to thousands of concurrent network connections, exhausting file descriptors (`EMFILE`) and triggering socket resets.
2. **Channel Deadlocks**: In a naive worker pool where workers push newly discovered child links back into the same channel they consume from, if the channel buffer fills up, all workers block attempting to push, leaving zero workers free to pull work.
- **Architectural Solution (Coordinator-Worker Pattern)**:
  - We implemented a bounded worker pool (`Config.Concurrency`, default 10 workers) managed by a central **Coordinator Loop**.
  - **Workers** only receive single jobs from `jobs chan Job` and report completed task results via `results chan taskResult`.
  - The **Coordinator** exclusively owns the pending queue slice (`[]Job`), the visited set (`map[string]struct{}`), and an `inFlight` job counter.
  - **Zero Mutex Contention**: Because the `visited` map and `queue` are only accessed by the coordinator goroutine, **no mutexes are needed**. Concurrency is coordinated purely through message passing ("Do not communicate by sharing memory; instead, share memory by communicating").

---

#### 2. Go Concepts Explained (from a C++ / Rust perspective)

##### Disabling `select` Branches via `nil` Channels
- In Go, sending to or receiving from an uninitialized / `nil` channel blocks forever:
  ```go
  var ch chan int // ch is nil
  ch <- 1         // blocks forever!
  ```
- This behavior is an intentional language feature leveraged in `select` multiplexing.
- In our coordinator:
  ```go
  var sendJobs chan Job
  if len(queue) > 0 {
      currentJob = queue[0]
      sendJobs = jobs // Active channel: select will try to send to an available worker
  } else {
      sendJobs = nil  // Nil channel: select completely disables this branch!
  }

  select {
  case sendJobs <- currentJob:
      queue = queue[1:]
      inFlight++
  case tr := <-results:
      inFlight--
      ...
  }
  ```
  When the queue is empty, `sendJobs` becomes `nil`, causing `select` to ignore the send branch without busy-waiting. The coordinator sits idle waiting exclusively on incoming results from workers or context cancellation.

##### The `inFlight` Counter & Clean Termination
- A recursive crawler cannot terminate simply because its queue is empty; workers may currently be processing pages that will yield 20 new links.
- The coordinator tracks `inFlight`:
  - When a job is sent to a worker: `inFlight++`
  - When a worker returns a result: `inFlight--`
- **Termination Invariant**: The crawl is complete if and only if:
  `inFlight == 0 && len(queue) == 0`
- Once satisfied, the coordinator closes `jobs` (signaling workers to exit) and waits for `wg.Wait()`.

##### `sync.WaitGroup` Lifecycle & Preventing Goroutine Leaks
- `sync.WaitGroup` acts like a thread barrier or atomic counter:
  - `wg.Add(1)` before spawning each worker.
  - `defer wg.Done()` inside each worker.
  - `wg.Wait()` blocks until the counter reaches 0.
- This guarantees zero orphaned goroutines remain running after `Crawl(...)` returns.

---

#### 3. C++ / Rust Mental Model Mapping

| Go Concept | C++ Analogy | Rust Analogy |
|---|---|---|
| Coordinator-Worker model | Actor pattern / single-threaded event loop + thread pool | Actix / Tokio actor loop with worker tasks |
| `jobs := make(chan Job)` | Synchronized bounded queue (lock + condvar) | `tokio::sync::mpsc::channel(N)` |
| `select` with `nil` channel | `epoll_ctl(EPOLL_CTL_DEL)` to stop listening for writable events | Modifying interest set in `mio` / custom `poll()` |
| `sync.WaitGroup` | `std::latch` / `std::barrier` (C++20) | `tokio::task::JoinSet` / `crossbeam::scope` |
| `defer wg.Done()` | Custom RAII decrement guard | Scope guard / `Drop` implementation |

---

#### 4. API Inventory (Created in Chunk 1.4)

- **Structs**:
  - `Config`: Crawl configuration (`Concurrency`, `MaxDepth`, `Timeout`, `UserAgent`).
  - `CrawlStats`: Aggregate crawl metrics (`TotalChecked`, `BrokenCount`, `Duration`).
  - `Job`: Individual work item (`URL`, `ParentURL`, `Depth`).
  - `taskResult`: Worker-to-coordinator internal payload (`Result`, `discoveredLinks`, `depth`).
  - `Crawler`: Engine struct holding configuration and link checker.
- **Functions & Methods**:
  - `DefaultConfig() Config`: Conservative default crawler settings.
  - `NewCrawler(cfg Config, checker *LinkChecker) *Crawler`: Factory constructor.
  - `(c *Crawler) Crawl(ctx context.Context, startURL string, onResult func(Result)) (*CrawlStats, []Result, error)`: Executes full recursive crawl with bounded concurrency, deduplication, and streaming result callbacks.

---

### Chunk 1.5: CLI Entrypoint & Terminal Summary
**File**: [`cmd/deadlink/main.go`](../cmd/deadlink/main.go)  
**Tests**: [`cmd/deadlink/test/cli_test.go`](../cmd/deadlink/test/cli_test.go)

#### 1. What was built & Why
A library is only as useful as its interface. In Chunk 1.5, we implemented the CLI binary entrypoint (`deadlink`):
- Accepts `--concurrency`, `--max-depth`, and `--timeout` flags via standard `flag` package.
- Streams live progress to stdout as links are checked (`[ OK ] 200` or `[DEAD] 404`).
- Implements graceful signal cancellation: hitting `Ctrl+C` does not kill the process abruptly; it cancels the crawl context, flushes the summary of links checked so far, and exits cleanly.
- Implements CI/CD-friendly exit codes:
  - `0`: Clean crawl (all checked links alive).
  - `1`: One or more broken links found (triggers build failure in GitHub Actions / GitLab CI).
  - `2`: Invalid CLI flags or missing target URL.

---

#### 2. Go Concepts Explained (from a C++ / Rust perspective)

##### Testable CLI Architecture (`main()` vs `run(...) int`)
- In Go, `os.Exit(code)` terminates the entire OS process immediately without running deferred functions (`defer`). Calling `os.Exit` inside test functions would abort the test suite.
- Idiomatic pattern:
  ```go
  func main() {
      os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
  }
  func run(args []string, stdout, stderr io.Writer) int { ... }
  ```
- By injecting `stdout` and `stderr` as `io.Writer` interfaces and returning an exit code integer, the entire CLI logic can be tested deterministically with in-memory buffers (`bytes.Buffer`).

##### `flag.FlagSet` vs Global `flag.Parse()`
- Go provides a global `flag.Parse()` package, but globals hinder isolation across tests.
- We construct an isolated parser: `flags := flag.NewFlagSet("deadlink", flag.ContinueOnError)`.
- We redirect output via `flags.SetOutput(stderr)`.

##### Graceful Signal Cancellation (`signal.NotifyContext`)
- In C++, handling `SIGINT` / `SIGTERM` requires registering a signal handler with `sigaction()` and setting an atomic flag (`std::atomic<bool>`).
- In Rust Tokio, one listens for `tokio::signal::ctrl_c()`.
- In Go:
  ```go
  ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
  defer stop()
  ```
  The Go runtime spins up an internal goroutine listening to OS signals. When `SIGINT` (Ctrl+C) or `SIGTERM` arrives, the runtime closes `ctx.Done()`. This transparently propagates cancellation down into `crawler.Crawl` and `checker.Check`, cleanly unblocking network syscalls and worker loops.

##### End-to-End Binary Testing with `os/exec`
- In `cmd/deadlink/test/cli_test.go`, we compile the binary once using `exec.Command("go", "build", ...)`, boot an `httptest.Server`, and spawn sub-processes to verify that the compiled binary produces exact stdout formatting and process exit codes (`0`, `1`, `2`).

---

#### 3. C++ / Rust Mental Model Mapping

| Go Concept | C++ Analogy | Rust Analogy |
|---|---|---|
| `flag.FlagSet` | `boost::program_options` / `CLI11` | `clap` parser |
| `signal.NotifyContext` | POSIX `sigaction` + `std::stop_source` (C++20) | `tokio::signal::ctrl_c()` |
| `run(args, stdout, stderr)` | `int cli_main(std::span<string_view>, std::ostream&, std::ostream&)` | `fn run(args: &[String], stdout: &mut dyn Write) -> ExitCode` |
| `os.Exit(code)` | `std::exit(code)` | `std::process::exit(code)` |
| `exec.Command` | `fork()` + `execvp()` / `boost::process` | `std::process::Command` |

---

#### 4. API Inventory (Created in Chunk 1.5)

- **Constants**:
  - `ExitClean = 0`: Process exit code when 0 broken links are found.
  - `ExitBroken = 1`: Process exit code when 1 or more broken links are found.
  - `ExitFailure = 2`: Process exit code for invalid CLI arguments or setup failure.
- **Functions**:
  - `main()`: Process entrypoint.
  - `run(args []string, stdout, stderr io.Writer) int`: Testable CLI executor with flag parsing, signal handling, and summary generation.

---

### Chunk 1.6: Integration Testing with `httptest` & Race Detection
**File**: [`internal/crawler/test/crawler_test.go`](../internal/crawler/test/crawler_test.go)

#### 1. What was built & Why
In Chunk 1.6, we completed the verification suite for the entire Stage 1 (CLI MVP):
- **Graph Cycle & Loop Detection Test**: Simulates an internal website where `/` links to `/about`, and `/about` links back to `/`. Proves that the visited deduplication set prevents infinite loops and terminates gracefully.
- **Non-HTML Binary Asset Handling (`TestCrawler_NonHTMLHandling`)**: Proves that binary assets (`image/png`, `application/pdf`) have their HTTP reachability probed (e.g. 200 OK or 404), but their response bodies are never parsed for HTML links.
- **High Concurrency Stress Test (`TestCrawler_HighConcurrencyStress`)**: Spawns 20 concurrent worker goroutines traversing an interlinked 20-page web graph.
- **ThreadSanitizer / Race Detector Verification (`go test -race ./...`)**: Passed with 0 data races detected, confirming that channel-based ownership of the visited map and queue prevents memory corruption.

---

#### 2. Go Concepts Explained (from a C++ / Rust perspective)

##### Go's Built-in Race Detector (`go test -race`)
- In C++ and Rust, detecting multi-threaded data races requires compiling with `-fsanitize=thread` (Clang/GCC ThreadSanitizer or LLVM TSAN).
- Go has native TSAN integration built directly into the toolchain (`go test -race`, `go build -race`).
- During compilation with `-race`, the compiler instruments all memory read and write operations with runtime bookkeeping. At runtime, if two goroutines access the same memory location concurrently where at least one is a write and without synchronization, the race detector immediately panics with a complete goroutine stack trace.
- Our coordinator architecture passed `-race` under 20 concurrent workers because shared mutable state is eliminated; workers communicate exclusively through channel value copies.

##### Simulating Real Web Topologies with `httptest.Server`
- In systems testing, creating mock HTTP servers in C++ often requires external dependencies (like cpp-httplib or libcurl mocks).
- In Go, `httptest.NewServer` spins up a lightweight `net/http` server bound to a kernel-assigned ephemeral loopback port (`127.0.0.1:0`). It generates real TCP sockets and passes through Go's actual `net/http.Transport` socket stack.

---

#### 3. C++ / Rust Mental Model Mapping

| Go Concept | C++ Analogy | Rust Analogy |
|---|---|---|
| `go test -race` | `-fsanitize=thread` (ThreadSanitizer / TSAN) | `cargo-tarpaulin` / Loom concurrency model checker |
| Circular graph crawl | BFS/DFS traversal with `std::unordered_set<std::string>` visited filter | Graph traversal with `HashSet<Url>` |
| Atomic counters (`sync/atomic`) | `std::atomic<int64_t>` | `std::sync::atomic::AtomicI64` |
| `httptest.Server.Client()` | Client with pre-configured loopback transport | `reqwest::Client` configured for mock service |

---

#### 4. API Inventory & Verification (Completed in Chunk 1.6)

- **Test Fixtures Added**:
  - `TestCrawler_NonHTMLHandling`: Asserts that binary assets (PNG, PDF) are checked for HTTP status but skipped for HTML link extraction.
  - `TestCrawler_HighConcurrencyStress`: Verifies 20-worker pool race-freedom on circular web graph.
- **Stage 1 Milestone Achieved**:
  - ✅ URL normalization & relative link resolution (`url.go`)
  - ✅ Streaming HTML link extraction via tokenizer (`extract.go`)
  - ✅ Single link prober with HTTP socket lifecycle management (`checker.go`)
  - ✅ Deadlock-free worker pool & visited set coordinator (`crawler.go`)
  - ✅ CLI entrypoint with live progress, signal handling, and exit codes (`main.go`)
  - ✅ Black-box integration tests & race detector verification (`crawler_test.go`, `cli_test.go`)

---

## Stage 2: Robustness

### Chunk 2.1: HEAD -> GET Fallback
**File**: [`internal/crawler/checker.go`](../internal/crawler/checker.go)  
**Tests**: [`internal/crawler/test/checker_test.go`](../internal/crawler/test/checker_test.go)

#### 1. What was built & Why
In theory, HTTP `HEAD` is the ideal method for link checking: it asks the web server to return headers and status codes identical to a `GET`, but without transmitting the response body. This saves enormous network bandwidth when probing thousands of links.
However, in production, real-world servers and CDNs are notoriously non-compliant:
- Many frameworks, microservices, and static servers return `405 Method Not Allowed` or `501 Not Implemented` for `HEAD`.
- Some Web Application Firewalls (Cloudflare, AWS WAF, Akamai) treat `HEAD` requests as automated scanning probes and reject them with `403 Forbidden`.
- Certain legacy web servers close the TCP connection (TCP RST) upon receiving `HEAD`.
- **Solution**:
  - `LinkChecker.Check` now issues a lightweight `HEAD` request first.
  - If the server accepts `HEAD` (< 400), the probe completes with zero body transfer.
  - If the server rejects `HEAD` with `405`, `501`, `403`, or a transport error, the checker automatically falls back to an HTTP `GET` with bounded body reading (`io.LimitReader(body, 4096)`) before marking the link dead.
  - Definite failures (such as `404 Not Found` or `410 Gone`) do not trigger a wasteful GET retry.

---

#### 2. Go Concepts Explained (from a C++ / Rust perspective)

##### HTTP Request Mutation & Header Re-use
- In Go, `http.Request` structs are mutable. However, once passed to `client.Do(req)`, the internal transport consumes fields like `Body` and state tokens.
- We encapsulated request creation into a helper:
  ```go
  func (c *LinkChecker) doRequest(ctx context.Context, method, targetURL string) (*http.Response, error)
  ```
  This cleanly isolates the generation of separate `HEAD` and `GET` requests while preserving headers (`User-Agent`) and context cancellation.

##### Selective Fallback Control Flow
- Using a clean predicate function `shouldFallbackToGET(statusCode int) bool` centralizes policy:
  ```go
  switch statusCode {
  case http.StatusMethodNotAllowed, http.StatusNotImplemented, http.StatusForbidden:
      return true
  default:
      return false
  }
  ```
  In C++, this maps to a switch on `http_status_code` enum. In Rust, this maps to `match status { StatusCode::METHOD_NOT_ALLOWED | ... => true, _ => false }`.

---

#### 3. C++ / Rust Mental Model Mapping

| Go Concept | C++ Analogy | Rust Analogy |
|---|---|---|
| HTTP `HEAD` vs `GET` | `curl_easy_setopt(curl, CURLOPT_NOBODY, 1L)` vs standard GET | `reqwest::Client::head()` vs `reqwest::Client::get()` |
| `drainAndClose(body)` | RAII reader with fixed-buffer consume + socket close | `body.take(4096).read_to_end()` + `drop(body)` |
| `Result.MethodUsed` | Status telemetry tag | Field on result struct / enum variant |

---

#### 4. API Inventory (Updated in Chunk 2.1)

- **Struct Updates**:
  - `Result.MethodUsed string`: Telemetry field recording whether availability was verified via `"HEAD"` or `"GET"` fallback.
- **Methods Added / Modified**:
  - `(c *LinkChecker) Check(ctx context.Context, targetURL, parentURL string) Result`: Upgraded to HEAD-first with automatic GET fallback.
  - `(c *LinkChecker) doRequest(ctx, method, targetURL) (*http.Response, error)`: Helper for executing context-aware HTTP requests.
  - `(c *LinkChecker) drainAndClose(body io.ReadCloser)`: Consumes up to 4KB of response body and safely closes socket file descriptor.
  - `shouldFallbackToGET(statusCode int) bool`: Predicate determining whether status code warrants GET retry.
