# deadlink

A fast, resilient, and secure broken link checker written in Go.

Given a URL, `deadlink` crawls the website, identifies all hyperlinks and assets, checks their availability concurrently, and reports any broken links along with their HTTP status or network error and the source page they were found on.

## Features

- **Concurrent Crawling**: Bounded worker pools with configurable concurrency.
- **Safe by Design**: Built-in SSRF protection, IP verification, and resource caps.
- **Resilient**: `HEAD` request with automatic `GET` fallback, retries with backoff, and redirect loop detection.
- **Dual Interfaces**: 
  - Standalone CLI for terminals and CI/CD pipelines.
  - Minimal web server embedding a single-page UI with live Server-Sent Events (SSE) streaming.
- **Standard Library Core**: Zero heavy web frameworks. Single static binary.

## Development & Stages

Detailed documentation and stage-by-stage implementation guides for autonomous agents and contributors are available in the [`.agents/`](.agents/) directory:
- [`.agents/SPEC.md`](.agents/SPEC.md): Full technical specification & requirements
- [`.agents/STAGES.md`](.agents/STAGES.md): 6-stage development roadmap
- [`.agents/ARCHITECTURE.md`](.agents/ARCHITECTURE.md): Concurrency, networking, and safety architecture
- [`.agents/RULES.md`](.agents/RULES.md): Coding rules and educational guidelines
