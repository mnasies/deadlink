package test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLI_EndToEnd(t *testing.T) {
	// Compile deadlink binary once for accurate exit code verification
	binPath := filepath.Join(t.TempDir(), "deadlink")
	buildCmd := exec.Command("go", "build", "-o", binPath, "github.com/mnasies/deadlink/cmd/deadlink")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build deadlink binary: %v\nOutput: %s", err, string(out))
	}

	// Mock server with one good link and one dead link
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		switch r.URL.Path {
		case "/":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`
				<html>
				<body>
					<a href="/healthy">Good Link</a>
					<a href="/missing">Bad Link</a>
				</body>
				</html>
			`))
		case "/healthy":
			w.WriteHeader(http.StatusOK)
		case "/missing":
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	t.Run("missing arguments exits with code 2", func(t *testing.T) {
		cmd := exec.Command(binPath)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr

		err := cmd.Run()
		if err == nil {
			t.Fatalf("expected error from missing arguments, got nil")
		}

		if exitErr, ok := err.(*exec.ExitError); ok {
			if exitErr.ExitCode() != 2 {
				t.Errorf("expected exit code 2, got %d", exitErr.ExitCode())
			}
		} else {
			t.Fatalf("expected *exec.ExitError, got %T: %v", err, err)
		}

		if !strings.Contains(stderr.String(), "Usage: deadlink") {
			t.Errorf("expected stderr to contain usage instructions, got:\n%s", stderr.String())
		}
	})

	t.Run("crawl with broken links exits with code 1", func(t *testing.T) {
		cmd := exec.Command(binPath, "--timeout=2s", server.URL)
		var stdout bytes.Buffer
		cmd.Stdout = &stdout

		err := cmd.Run()
		if err == nil {
			t.Fatalf("expected exit code 1 due to broken links, got exit 0")
		}

		if exitErr, ok := err.(*exec.ExitError); ok {
			if exitErr.ExitCode() != 1 {
				t.Errorf("expected exit code 1, got %d", exitErr.ExitCode())
			}
		}

		out := stdout.String()
		if !strings.Contains(out, "[DEAD] 404") {
			t.Errorf("expected stdout to contain '[DEAD] 404', got:\n%s", out)
		}
		if !strings.Contains(out, "[ OK ] 200") {
			t.Errorf("expected stdout to contain '[ OK ] 200', got:\n%s", out)
		}
		if !strings.Contains(out, "1 broken link(s) found") {
			t.Errorf("expected stdout to report broken link count, got:\n%s", out)
		}
	})

	t.Run("clean crawl exits with code 0", func(t *testing.T) {
		cleanServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<html><body><p>No links here</p></body></html>`))
		}))
		defer cleanServer.Close()

		cmd := exec.Command(binPath, cleanServer.URL)
		var stdout bytes.Buffer
		cmd.Stdout = &stdout

		if err := cmd.Run(); err != nil {
			t.Fatalf("expected exit code 0 for clean crawl, got error: %v", err)
		}

		out := stdout.String()
		if !strings.Contains(out, "0 broken link(s) found") {
			t.Errorf("expected 0 broken links in output, got:\n%s", out)
		}
	})

	t.Run("bot-blocked link outputs [BLOCKED] and exits clean by default", func(t *testing.T) {
		botServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			switch r.URL.Path {
			case "/":
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`<html><body><a href="/protected">Protected</a></body></html>`))
			case "/protected":
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`403 Forbidden`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer botServer.Close()

		// Run without --fail-on-blocked: should exit 0
		cmd := exec.Command(binPath, botServer.URL)
		var stdout bytes.Buffer
		cmd.Stdout = &stdout

		if err := cmd.Run(); err != nil {
			t.Fatalf("expected exit code 0 for bot-blocked link by default, got error: %v", err)
		}

		out := stdout.String()
		if !strings.Contains(out, "[BLOCKED] 403") {
			t.Errorf("expected stdout to contain '[BLOCKED] 403', got:\n%s", out)
		}
		if !strings.Contains(out, "0 broken link(s) found") {
			t.Errorf("expected 0 broken links reported, got:\n%s", out)
		}
		if !strings.Contains(out, "1 blocked") {
			t.Errorf("expected breakdown to mention '1 blocked', got:\n%s", out)
		}

		// Run WITH --fail-on-blocked: should exit 1
		failCmd := exec.Command(binPath, "--fail-on-blocked", botServer.URL)
		var failStdout bytes.Buffer
		failCmd.Stdout = &failStdout

		err := failCmd.Run()
		if err == nil {
			t.Fatalf("expected exit code 1 when --fail-on-blocked is enabled, got exit 0")
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			if exitErr.ExitCode() != 1 {
				t.Errorf("expected exit code 1, got %d", exitErr.ExitCode())
			}
		}
	})
}
