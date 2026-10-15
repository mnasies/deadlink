package crawler

import (
	"errors"
	"net/url"
	"testing"
)

func TestNormalizeURL(t *testing.T) {
	// Table-driven tests: standard Go idiom for testing multiple inputs/outputs
	// against the same function. Similar to parameterized tests in C++ GTest.
	tests := []struct {
		name        string
		input       string
		expected    string
		expectError bool
		targetErr   error
	}{
		{
			name:     "strip fragment",
			input:    "https://example.com/page#section-1",
			expected: "https://example.com/page",
		},
		{
			name:     "lowercase host and scheme",
			input:    "HTTP://EXAMPLE.COM/Page",
			expected: "http://example.com/Page",
		},
		{
			name:     "root path normalization without trailing slash",
			input:    "https://example.com",
			expected: "https://example.com/",
		},
		{
			name:     "root path normalization with trailing slash",
			input:    "https://example.com/",
			expected: "https://example.com/",
		},
		{
			name:     "subpath trailing slash stripped",
			input:    "https://example.com/about/",
			expected: "https://example.com/about",
		},
		{
			name:     "preserve query parameters",
			input:    "https://example.com/search?q=golang&sort=asc#results",
			expected: "https://example.com/search?q=golang&sort=asc",
		},
		{
			name:        "empty url error",
			input:       "   ",
			expectError: true,
			targetErr:   ErrEmptyURL,
		},
		{
			name:        "reject unsupported scheme mailto",
			input:       "mailto:admin@example.com",
			expectError: true,
			targetErr:   ErrUnsupportedScheme,
		},
		{
			name:        "reject unsupported scheme javascript",
			input:       "javascript:void(0)",
			expectError: true,
			targetErr:   ErrUnsupportedScheme,
		},
		{
			name:        "reject relative url directly into NormalizeURL",
			input:       "/just/a/path",
			expectError: true,
			targetErr:   ErrUnsupportedScheme,
		},
	}

	for _, tt := range tests {
		// t.Run runs each test case as a named subtest
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeURL(tt.input)
			if tt.expectError {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tt.targetErr != nil && !errors.Is(err, tt.targetErr) {
					t.Errorf("expected error %v, got %v", tt.targetErr, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.expected {
				t.Errorf("NormalizeURL(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestResolveURL(t *testing.T) {
	baseURL, err := url.Parse("https://example.com/blog/posts/index.html")
	if err != nil {
		t.Fatalf("failed to parse base URL: %v", err)
	}

	tests := []struct {
		name        string
		ref         string
		expected    string
		expectError bool
		targetErr   error
	}{
		{
			name:     "root-relative path",
			ref:      "/about",
			expected: "https://example.com/about",
		},
		{
			name:     "document-relative path",
			ref:      "detail.html",
			expected: "https://example.com/blog/posts/detail.html",
		},
		{
			name:     "parent traversal relative path",
			ref:      "../archive",
			expected: "https://example.com/blog/archive",
		},
		{
			name:     "absolute url override",
			ref:      "https://github.com/golang/go#readme",
			expected: "https://github.com/golang/go",
		},
		{
			name:     "protocol-relative url",
			ref:      "//cdn.example.com/app.js",
			expected: "https://cdn.example.com/app.js",
		},
		{
			name:        "ignore mailto link",
			ref:         "mailto:user@domain.com",
			expectError: true,
			targetErr:   ErrUnsupportedScheme,
		},
		{
			name:        "ignore javascript link",
			ref:         "javascript:alert(1)",
			expectError: true,
			targetErr:   ErrUnsupportedScheme,
		},
		{
			name:        "empty reference",
			ref:         "",
			expectError: true,
			targetErr:   ErrEmptyURL,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveURL(baseURL, tt.ref)
			if tt.expectError {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tt.targetErr != nil && !errors.Is(err, tt.targetErr) {
					t.Errorf("expected error %v, got %v", tt.targetErr, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.expected {
				t.Errorf("ResolveURL(%q) = %q, want %q", tt.ref, got, tt.expected)
			}
		})
	}
}

func TestIsInternal(t *testing.T) {
	base, _ := url.Parse("https://example.com/docs")

	tests := []struct {
		target   string
		expected bool
	}{
		{"https://example.com/blog", true},
		{"http://example.com/contact", true},
		{"https://EXAMPLE.COM/about", true},
		{"https://example.com:8080/api", true}, // same hostname, port stripped
		{"https://sub.example.com/docs", false}, // subdomain is distinct host
		{"https://google.com", false},
	}

	for _, tt := range tests {
		targetURL, err := url.Parse(tt.target)
		if err != nil {
			t.Fatalf("failed to parse target URL %q: %v", tt.target, err)
		}

		got := IsInternal(base, targetURL)
		if got != tt.expected {
			t.Errorf("IsInternal(%q, %q) = %v, want %v", base.String(), tt.target, got, tt.expected)
		}
	}
}
