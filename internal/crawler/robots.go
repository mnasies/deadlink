package crawler

import (
	"bufio"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// RobotsRule defines an allow or disallow path pattern.
type RobotsRule struct {
	Pattern *regexp.Regexp
	Allowed bool
	Length  int // Pattern length for specificity resolution
}

// RobotsData stores parsed directives for our user-agent from robots.txt.
type RobotsData struct {
	Rules      []RobotsRule
	CrawlDelay time.Duration
}

// IsAllowed tests whether path is permitted by the robots.txt rules.
// Following RFC 9309, if multiple rules match, the most specific (longest) rule wins.
// If an Allow and Disallow rule have the exact same length, Allow takes precedence.
func (r *RobotsData) IsAllowed(rawPath string) bool {
	if r == nil || len(r.Rules) == 0 {
		return true // Default is allowed if no rules specified
	}

	cleanPath := rawPath
	if cleanPath == "" {
		cleanPath = "/"
	}

	var bestRule *RobotsRule

	for i := range r.Rules {
		rule := &r.Rules[i]
		if rule.Pattern.MatchString(cleanPath) {
			if bestRule == nil || rule.Length > bestRule.Length {
				bestRule = rule
			} else if rule.Length == bestRule.Length && rule.Allowed {
				bestRule = rule // Allow wins ties
			}
		}
	}

	if bestRule != nil {
		return bestRule.Allowed
	}

	return true // Implicitly allowed if no rule matched
}

// ParseRobots reads a robots.txt stream and extracts rules applicable to the given userAgent.
// It prioritizes specific user-agent blocks over wildcard '*' blocks.
func ParseRobots(reader io.Reader, userAgent string) *RobotsData {
	if reader == nil {
		return &RobotsData{}
	}

	normalizedAgent := strings.ToLower(strings.TrimSpace(userAgent))
	scanner := bufio.NewScanner(reader)

	type agentBlock struct {
		isTargetAgent bool
		isWildcard    bool
		rules         []RobotsRule
		crawlDelay    time.Duration
	}

	var blocks []agentBlock
	var currentAgents []string
	var currentRules []RobotsRule
	var currentDelay time.Duration

	flushBlock := func() {
		if len(currentAgents) > 0 {
			for _, agent := range currentAgents {
				isTarget := strings.Contains(normalizedAgent, agent) || strings.Contains(agent, normalizedAgent)
				isWild := agent == "*"
				blocks = append(blocks, agentBlock{
					isTargetAgent: isTarget,
					isWildcard:    isWild,
					rules:         currentRules,
					crawlDelay:    currentDelay,
				})
			}
		}
		currentAgents = nil
		currentRules = nil
		currentDelay = 0
	}

	for scanner.Scan() {
		line := scanner.Text()
		// Strip comments
		if commentIdx := strings.IndexByte(line, '#'); commentIdx != -1 {
			line = line[:commentIdx]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.ToLower(strings.TrimSpace(parts[0]))
		val := strings.TrimSpace(parts[1])

		switch key {
		case "user-agent":
			// A new user-agent after directives starts a new block
			if len(currentRules) > 0 || currentDelay > 0 {
				flushBlock()
			}
			currentAgents = append(currentAgents, strings.ToLower(val))

		case "disallow":
			if val == "" {
				// Empty Disallow means allow everything
				currentRules = append(currentRules, compileRule("/", true))
			} else {
				currentRules = append(currentRules, compileRule(val, false))
			}

		case "allow":
			if val != "" {
				currentRules = append(currentRules, compileRule(val, true))
			}

		case "crawl-delay":
			if secs, err := strconv.ParseFloat(val, 64); err == nil && secs > 0 {
				currentDelay = time.Duration(secs * float64(time.Second))
			}
		}
	}
	flushBlock()

	// Selection priority:
	// 1. Exact/specific user-agent match
	// 2. Wildcard '*' match
	// 3. Empty rules (allow all)
	var chosenBlock *agentBlock
	for i := range blocks {
		if blocks[i].isTargetAgent {
			chosenBlock = &blocks[i]
			break
		}
	}

	if chosenBlock == nil {
		for i := range blocks {
			if blocks[i].isWildcard {
				chosenBlock = &blocks[i]
				break
			}
		}
	}

	if chosenBlock != nil {
		return &RobotsData{
			Rules:      chosenBlock.rules,
			CrawlDelay: chosenBlock.crawlDelay,
		}
	}

	return &RobotsData{}
}

// compileRule transforms a robots.txt path pattern into a regex.
// Supports wildcards '*' and end-of-path markers '$'.
func compileRule(pattern string, allowed bool) RobotsRule {
	var sb strings.Builder
	sb.WriteString("^")

	for i := 0; i < len(pattern); i++ {
		char := pattern[i]
		switch char {
		case '*':
			sb.WriteString(".*")
		case '$':
			if i == len(pattern)-1 {
				sb.WriteString("$")
			} else {
				sb.WriteString(`\$`)
			}
		case '.', '+', '?', '(', ')', '[', ']', '{', '}', '|', '^', '\\':
			sb.WriteByte('\\')
			sb.WriteByte(char)
		default:
			sb.WriteByte(char)
		}
	}

	re, err := regexp.Compile(sb.String())
	if err != nil {
		// Fallback to literal prefix matching if regex compilation fails
		re = regexp.MustCompile("^" + regexp.QuoteMeta(pattern))
	}

	return RobotsRule{
		Pattern: re,
		Allowed: allowed,
		Length:  len(pattern),
	}
}

// FetchRobots locates and parses /robots.txt for a given base URL.
// If the server returns 404 Not Found or errors, it returns an empty RobotsData (allow all).
func FetchRobots(client *http.Client, baseURL *url.URL, userAgent string) *RobotsData {
	if client == nil || baseURL == nil {
		return &RobotsData{}
	}

	robotsURL := &url.URL{
		Scheme: baseURL.Scheme,
		Host:   baseURL.Host,
		Path:   "/robots.txt",
	}

	req, err := http.NewRequest(http.MethodGet, robotsURL.String(), nil)
	if err != nil {
		return &RobotsData{}
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return &RobotsData{} // Treat unreachable robots.txt as allow all
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		return &RobotsData{} // 404 or any non-200 means no restrictions
	}

	return ParseRobots(io.LimitReader(resp.Body, 512*1024), userAgent) // Read up to 512KB max
}

// IsPathAllowed is a convenience helper checking if a relative/absolute target path is permitted.
func (r *RobotsData) IsPathAllowed(targetURL *url.URL) bool {
	if r == nil || targetURL == nil {
		return true
	}
	p := targetURL.EscapedPath()
	if p == "" {
		p = "/"
	}
	// Clean path to remove double slashes or dot traversals
	p = path.Clean(p)
	return r.IsAllowed(p)
}
