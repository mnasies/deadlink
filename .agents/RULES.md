# Developer & Agent Guidelines

When working on `deadlink`, adhere strictly to these rules:

1. **Passive Micro-Chunked Workflow**:
   - Work strictly within the designated micro-chunk (e.g. Chunk 1.1, Chunk 1.2).
   - Never dump an entire stage or multiple chunks at once.
   - **STOP** after completing each chunk for user review and inspection before advancing.

2. **Progress Tracking (`PROGRESS.md`)**:
   - After completing each chunk, update `PROGRESS.md` with:
     - What was built and why.
     - Key Go concepts used (syntax, runtime behavior, idioms).
     - Rust / C++ analogies (mapping to tokio, mpsc, vtables, epoll, etc.).
     - Exhaustive list of newly created functions, structs/types, methods, and error types.

3. **Code Simplicity & Readability**:
   - Write idiomatic, readable Go. Avoid premature abstraction and unnecessary interfaces.
   - Stdlib-first. Use approved dependencies (`x/net/html`, `x/time/rate`).

4. **Concurrency & Lifecycle Documentation**:
   - For every `go` statement (goroutine), channel creation, and mutex lock, provide an inline comment explaining **WHY** it exists, who owns it, and how it is cleaned up or terminated.
   - Prevent goroutine leaks by always coupling goroutines with context cancellation or channel closure.

5. **Test-Driven & Tested Alongside Code**:
   - Write tests alongside code using `net/http/httptest` to verify behavior locally without hitting external networks.

6. **Security-First**:
   - SSRF protection, redirect verification, IP pinning, and resource limits are non-negotiable requirements.
   - All dynamic output rendered into HTML must be properly escaped.
