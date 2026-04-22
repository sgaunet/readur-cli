package integration_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var (
	binOnce sync.Once
	binPath string
	binErr  error
)

// binary returns a path to the readur binary built once per process.
func binary(t *testing.T) string {
	t.Helper()
	binOnce.Do(func() {
		dir, err := os.MkdirTemp("", "readur-int-*")
		if err != nil {
			binErr = err
			return
		}
		out := filepath.Join(dir, "readur")
		cmd := exec.Command("go", "build", "-o", out, "./cmd/readur")
		cmd.Dir = repoRoot()
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			binErr = errors.New(err.Error() + ": " + stderr.String())
			return
		}
		binPath = out
	})
	if binErr != nil {
		t.Fatalf("build binary: %v", binErr)
	}
	return binPath
}

func repoRoot() string {
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

type runResult struct {
	Stdout, Stderr string
	ExitCode       int
}

func runCLI(t *testing.T, args []string, env map[string]string) runResult {
	t.Helper()
	cmd := exec.Command(binary(t), args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if env != nil {
		cmd.Env = os.Environ()
		for k, v := range env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else {
			t.Fatalf("run: %v\nstderr=%q", err, errb.String())
		}
	}
	return runResult{Stdout: out.String(), Stderr: errb.String(), ExitCode: code}
}

// writeProfile writes a minimal config.toml pointing at serverURL and
// returns its path.
func writeProfile(t *testing.T, serverURL, token string, insecure bool) string {
	t.Helper()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	body := `default_profile = "default"

[profiles.default]
server_url = "` + serverURL + `"
username   = "alice"
token      = "` + token + `"
obtained_at = 2026-04-20T10:00:00Z
`
	if insecure {
		body += "insecure_skip_verify = true\n"
	}
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatalf("write profile: %v", err)
	}
	return cfg
}

// FR-001 + FR-002: upload succeeds, metadata arrives, document id is
// returned to the user.
func TestUpload_HappyPath(t *testing.T) {
	srv := NewFakeServer(t)
	path := filepath.Join(t.TempDir(), "q2.pdf")
	if err := os.WriteFile(path, []byte("scanned content"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg := writeProfile(t, srv.URL(), srv.Token, false)

	r := runCLI(t, []string{
		"--config", cfg,
		"upload", path,
		"--title", "Q2 Invoice",
		"--label", "invoices",
		"--label", "q2",
		"--language", "eng",
	}, nil)
	if r.ExitCode != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", r.ExitCode, r.Stdout, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "uploaded:") || !strings.Contains(r.Stdout, "doc-0001") {
		t.Fatalf("stdout missing success line: %q", r.Stdout)
	}
	if len(srv.Uploads) != 1 {
		t.Fatalf("server saw %d uploads", len(srv.Uploads))
	}
	u := srv.Uploads[0]
	if u.Filename != "q2.pdf" {
		t.Fatalf("filename = %q", u.Filename)
	}
	if u.Title != "Q2 Invoice" {
		t.Fatalf("title = %q", u.Title)
	}
	if strings.Join(u.Labels, ",") != "invoices,q2" {
		t.Fatalf("labels = %v", u.Labels)
	}
	if u.Language != "eng" {
		t.Fatalf("language = %q", u.Language)
	}
}

// FR-010: stdout/stderr split — piping stdout must not contain
// diagnostic text. stderr must receive progress/debug.
func TestUpload_StdoutStderrSplit(t *testing.T) {
	srv := NewFakeServer(t)
	path := filepath.Join(t.TempDir(), "x.pdf")
	_ = os.WriteFile(path, []byte("x"), 0o600)
	cfg := writeProfile(t, srv.URL(), srv.Token, false)

	r := runCLI(t, []string{"--config", cfg, "upload", path}, nil)
	if r.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
	// stdout must be the single success line, ≤ 200 chars.
	if len(r.Stdout) > 200 {
		t.Fatalf("stdout too chatty: %q", r.Stdout)
	}
	// stdout must be usable in a pipe — trailing newline, single line.
	lines := strings.Split(strings.TrimRight(r.Stdout, "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("stdout should be one line, got %d: %q", len(lines), r.Stdout)
	}
}

// JSON mode produces the full shape per contracts/json-output.md.
func TestUpload_JSONMode_Shape(t *testing.T) {
	srv := NewFakeServer(t)
	path := filepath.Join(t.TempDir(), "doc.pdf")
	_ = os.WriteFile(path, []byte("abcdef"), 0o600)
	cfg := writeProfile(t, srv.URL(), srv.Token, false)

	r := runCLI(t, []string{"--config", cfg, "--json", "upload", path, "--label", "x"}, nil)
	if r.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(r.Stdout)), &got); err != nil {
		t.Fatalf("not JSON: %v: %q", err, r.Stdout)
	}
	for _, f := range []string{"local_path", "document_id", "size_bytes", "labels", "duration_ms", "exit_code"} {
		if _, ok := got[f]; !ok {
			t.Errorf("JSON missing %q in %+v", f, got)
		}
	}
	if got["exit_code"] != float64(0) {
		t.Fatalf("exit_code = %v", got["exit_code"])
	}
	if got["size_bytes"] != float64(6) {
		t.Fatalf("size_bytes = %v", got["size_bytes"])
	}
}

// FR-013: server rejection (413 oversize) surfaces as exit 1 with
// readable reason.
func TestUpload_Oversize_Exit1_WithReason(t *testing.T) {
	srv := NewFakeServer(t)
	srv.UploadOversize = true
	path := filepath.Join(t.TempDir(), "big.pdf")
	_ = os.WriteFile(path, []byte("x"), 0o600)
	cfg := writeProfile(t, srv.URL(), srv.Token, false)

	r := runCLI(t, []string{"--config", cfg, "upload", path}, nil)
	if r.ExitCode != 1 {
		t.Fatalf("exit = %d, want 1 GENERIC; stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(strings.ToLower(r.Stderr), "size") {
		t.Fatalf("expected size/413 hint in stderr, got %q", r.Stderr)
	}
}

// FR-016: insecure profile config triggers per-request stderr warning.
// (Exercised against a plain-HTTP server; the warning is posture-driven,
// not TLS-driven.)
func TestUpload_InsecureSkipVerify_WarningEmitted(t *testing.T) {
	srv := NewFakeServer(t)
	path := filepath.Join(t.TempDir(), "x.pdf")
	_ = os.WriteFile(path, []byte("abc"), 0o600)
	cfg := writeProfile(t, srv.URL(), srv.Token, true) // insecure=true

	r := runCLI(t, []string{"--config", cfg, "upload", path}, nil)
	if r.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stderr, "TLS verification disabled") {
		t.Fatalf("expected TLS warning on stderr, got %q", r.Stderr)
	}
}

// Retry: server 5xx three times then success — upload must succeed.
func TestUpload_RetryOn5xx(t *testing.T) {
	srv := NewFakeServer(t)
	srv.Uploads5xxN = 3 // fail first 3, succeed on 4th (the last allowed attempt)
	path := filepath.Join(t.TempDir(), "x.pdf")
	_ = os.WriteFile(path, []byte("content"), 0o600)
	cfg := writeProfile(t, srv.URL(), srv.Token, false)

	r := runCLI(t, []string{"--config", cfg, "upload", path}, nil)
	if r.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
	if got := srv.UploadHits.Load(); got != 4 {
		t.Fatalf("server saw %d attempts, want 4 (budget exhausted with success on last)", got)
	}
}

// 401 from server → AUTH exit code 3.
func TestUpload_AuthFailed(t *testing.T) {
	srv := NewFakeServer(t)
	// Write a profile with the WRONG token.
	path := filepath.Join(t.TempDir(), "x.pdf")
	_ = os.WriteFile(path, []byte("x"), 0o600)
	cfg := writeProfile(t, srv.URL(), "WRONG-TOKEN", false)

	r := runCLI(t, []string{"--config", cfg, "upload", path}, nil)
	if r.ExitCode != 3 {
		t.Fatalf("exit = %d, want 3 AUTH; stderr=%q", r.ExitCode, r.Stderr)
	}
}
