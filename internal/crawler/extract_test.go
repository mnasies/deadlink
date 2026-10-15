package crawler

import (
	"net/url"
	"strings"
	"testing"
)

func TestExtractLinks(t *testing.T) {
	baseURL, err := url.Parse("https://example.com/blog/posts/hello.html")
	if err != nil {
		t.Fatalf("failed to parse base URL: %v", err)
	}

	htmlContent := `
	<!DOCTYPE html>
	<html>
	<head>
		<title>Test Page</title>
		<link rel="stylesheet" href="/assets/style.css">
		<script src="//cdn.example.com/lib.js"></script>
	</head>
	<body>
		<header>
			<a href="/about">About Us</a>
			<a href="https://example.com/about#team">Duplicate About Team</a>
			<a href="mailto:support@example.com">Email Us</a>
			<a href="javascript:void(0)">Click me</a>
		</header>
		<main>
			<p>Check out our <a href="../archive">Archive</a>.</p>
			<img src="images/banner.png" alt="Banner">
			<a href="https://external.org/info">External Reference</a>
		</main>
		<footer>
			<!-- Duplicate link to verify in-page deduplication -->
			<a href="/about">About Us Again</a>
		</footer>
	</body>
	</html>
	`

	links, err := ExtractLinks(baseURL, strings.NewReader(htmlContent))
	if err != nil {
		t.Fatalf("unexpected error from ExtractLinks: %v", err)
	}

	expected := []string{
		"https://example.com/assets/style.css",
		"https://cdn.example.com/lib.js",
		"https://example.com/about",
		"https://example.com/blog/archive",
		"https://example.com/blog/posts/images/banner.png",
		"https://external.org/info",
	}

	if len(links) != len(expected) {
		t.Fatalf("expected %d links, got %d: %v", len(expected), len(links), links)
	}

	for i, exp := range expected {
		if links[i] != exp {
			t.Errorf("link[%d] = %q, want %q", i, links[i], exp)
		}
	}
}

func TestExtractLinks_EdgeCases(t *testing.T) {
	baseURL, _ := url.Parse("https://example.com")

	t.Run("nil base url", func(t *testing.T) {
		_, err := ExtractLinks(nil, strings.NewReader("<html></html>"))
		if err == nil {
			t.Errorf("expected error with nil baseURL, got nil")
		}
	})

	t.Run("nil reader", func(t *testing.T) {
		_, err := ExtractLinks(baseURL, nil)
		if err == nil {
			t.Errorf("expected error with nil reader, got nil")
		}
	})

	t.Run("malformed html recovery", func(t *testing.T) {
		// Improperly nested tags and unclosed tags shouldn't crash the tokenizer
		malformed := `<p>Unclosed paragraph<a href="/first">Link 1<div>Improper nest<a href="/second">Link 2</a>`
		links, err := ExtractLinks(baseURL, strings.NewReader(malformed))
		if err != nil {
			t.Fatalf("ExtractLinks should handle malformed HTML gracefully: %v", err)
		}
		if len(links) != 2 {
			t.Fatalf("expected 2 links from malformed HTML, got %d: %v", len(links), links)
		}
		if links[0] != "https://example.com/first" || links[1] != "https://example.com/second" {
			t.Errorf("unexpected links extracted: %v", links)
		}
	})
}
