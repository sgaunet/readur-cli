package contract_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Minimal profile fixture used by upload contract tests.
func writeProfileFixture(t *testing.T, serverURL string) string {
	t.Helper()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	body := `default_profile = "default"

[profiles.default]
server_url = "` + serverURL + `"
username   = "alice"
token      = "tk"
obtained_at = 2026-04-20T10:00:00Z
`
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return cfg
}

// Guarantee 3: no --password flag exists; the only password path is
// --password-stdin (tested under US3 login, but we check absence here
// by asserting `--password=<foo>` is rejected).
func TestUpload_NoPasswordFlag(t *testing.T) {
	r := Run(t, []string{"upload", "foo.pdf", "--password=hunter2"}, nil)
	if r.ExitCode != 2 {
		t.Fatalf("expected USAGE rejection of --password, got exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
}

// Grammar: exact args required (1 positional).
func TestUpload_RequiresPositional(t *testing.T) {
	r := Run(t, []string{"upload"}, nil)
	if r.ExitCode != 2 {
		t.Fatalf("missing positional should yield USAGE, got exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
}

// Grammar: --ocr and --no-ocr are mutually exclusive.
func TestUpload_OCRMutex(t *testing.T) {
	cfg := writeProfileFixture(t, "http://127.0.0.1:1")
	// file must exist so we reach the mutex check
	tmp := filepath.Join(t.TempDir(), "x.pdf")
	_ = os.WriteFile(tmp, []byte("x"), 0o600)
	r := Run(t, []string{"--config", cfg, "upload", tmp, "--ocr", "--no-ocr"}, nil)
	if r.ExitCode != 2 {
		t.Fatalf("expected USAGE, got exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
}

// Exit code: missing file yields NOINPUT (66).
func TestUpload_MissingFile_IsNOINPUT(t *testing.T) {
	cfg := writeProfileFixture(t, "http://127.0.0.1:1")
	r := Run(t, []string{"--config", cfg, "upload", "/nonexistent/path.pdf"}, nil)
	if r.ExitCode != 66 {
		t.Fatalf("exit = %d, want 66 (NOINPUT); stderr=%q", r.ExitCode, r.Stderr)
	}
}

// JSON shape on NOINPUT failure: envelope with code + exit_code.
func TestUpload_JSONErrorEnvelope(t *testing.T) {
	cfg := writeProfileFixture(t, "http://127.0.0.1:1")
	r := Run(t, []string{"--config", cfg, "--json", "upload", "/nowhere/path.pdf"}, nil)
	if r.ExitCode != 66 {
		t.Fatalf("exit = %d want 66", r.ExitCode)
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		ExitCode int `json:"exit_code"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(r.Stdout)), &env); err != nil {
		t.Fatalf("stdout not JSON: %v\n%q", err, r.Stdout)
	}
	if env.ExitCode != 66 || env.Error.Code != "NOINPUT" {
		t.Fatalf("envelope wrong: %+v", env)
	}
	if env.Error.Message == "" {
		t.Fatalf("empty error message")
	}
}

// Help for upload exits 0 and mentions key flags.
func TestUpload_Help(t *testing.T) {
	r := Run(t, []string{"upload", "--help"}, nil)
	if r.ExitCode != 0 {
		t.Fatalf("help exit = %d", r.ExitCode)
	}
	for _, want := range []string{"--title", "--label", "--ocr", "--no-ocr", "--language"} {
		if !strings.Contains(r.Stdout, want) && !strings.Contains(r.Stderr, want) {
			t.Errorf("help missing %q", want)
		}
	}
}
