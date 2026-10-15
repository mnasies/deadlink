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
