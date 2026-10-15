package crawler

import (
	"errors"
	"io"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

// ExtractLinks parses an HTML stream from an io.Reader, extracts all target links
// found in 'href' and 'src' attributes, resolves relative URLs against baseURL,
// normalizes them, and returns a deduplicated slice of absolute URL strings.
//
// We intentionally use html.Tokenizer (streaming pull-parser) rather than
// html.Parse (which constructs a full in-memory DOM tree). A streaming tokenizer
// reads bytes incrementally, keeping memory usage bounded and avoiding heavy heap
// allocations for DOM node trees.
func ExtractLinks(baseURL *url.URL, r io.Reader) ([]string, error) {
	if baseURL == nil {
		return nil, errors.New("base URL cannot be nil")
	}
	if r == nil {
		return nil, errors.New("reader cannot be nil")
	}

	tokenizer := html.NewTokenizer(r)

	// In-memory set for deduplicating links within this single page.
	// In Go, map[string]struct{} is the standard idiomatic set data structure.
	// struct{} is a zero-size type (sizeof == 0 bytes), so map keys store the strings
	// while the map values consume zero additional bytes of memory.
	seen := make(map[string]struct{})
	var links []string

	for {
		tokenType := tokenizer.Next()

		switch tokenType {
		case html.ErrorToken:
			err := tokenizer.Err()
			// io.EOF is the standard sentinel error indicating the stream has finished cleanly.
			if errors.Is(err, io.EOF) {
				return links, nil
			}
			// Any error other than EOF is a legitimate stream read error.
			return links, err

		case html.StartTagToken, html.SelfClosingTagToken:
			token := tokenizer.Token()

			for _, attr := range token.Attr {
				// We care about hyperlinks (href) and embedded media/assets (src).
				key := strings.ToLower(attr.Key)
				if key == "href" || key == "src" {
					val := strings.TrimSpace(attr.Val)
					if val == "" {
						continue
					}

					resolved, err := ResolveURL(baseURL, val)
					if err != nil {
						// Ignore links with unsupported schemes (e.g. mailto:, javascript:, tel:).
						// These are not broken web links, but non-HTTP protocols.
						continue
					}

					// Deduplicate within the current page.
					if _, exists := seen[resolved]; !exists {
						seen[resolved] = struct{}{}
						links = append(links, resolved)
					}
				}
			}
		}
	}
}
