# Project Progress & Learning Log

---

## Stage 1: CLI MVP

### Chunk 1.1: URL Normalization & Link Utilities
**File**: [`internal/crawler/url.go`](../internal/crawler/url.go)  
**Tests**: [`internal/crawler/url_test.go`](../internal/crawler/url_test.go)

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
**Tests**: [`internal/crawler/extract_test.go`](../internal/crawler/extract_test.go)

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
