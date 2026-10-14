# Developer & Agent Guidelines

When working on `deadlink`, adhere strictly to these rules:

1. **Step-by-Step Staging**:
   - Work strictly within the current stage.
   - **STOP** after completing each stage so the user can review before proceeding.
   - Do not jump ahead into subsequent stages without user confirmation.

2. **Code Simplicity & Readability**:
   - Write idiomatic Go. Avoid premature abstraction and unnecessary interfaces.
   - Rely on the standard library whenever possible. Only use the approved dependencies:
     - `golang.org/x/net/html`
     - `golang.org/x/time/rate`

3. **Concurrency Documentation**:
   - For every `go` statement (goroutine) and channel instantiation, provide a concise comment explaining **WHY** it exists, who owns it, and how it is closed or terminated.
   - Prevent goroutine leaks by always coupling goroutines with context cancellation or channel closure.

4. **Educational Explanations**:
   - After each stage, include a clear, concise educational explanation of the concurrency, Go idioms, and networking concepts implemented during that stage.

5. **Test-Driven & Tested Alongside Code**:
   - Write tests alongside code for each package.
   - Use `net/http/httptest` to verify HTTP behavior, status codes, timeouts, redirects, and SSRF guards without hitting external networks during unit tests.

6. **Security-First**:
   - SSRF protection, redirect verification, IP pinning, and resource limits are non-negotiable requirements.
   - All dynamic output rendered into HTML must be properly escaped.
