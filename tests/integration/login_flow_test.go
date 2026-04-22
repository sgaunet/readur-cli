package integration_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// runCLIStdin pipes a string into the binary's stdin. Integration tests
// drive login this way since the fake server accepts any non-empty
// password.
func runCLIStdin(t *testing.T, args []string, stdin string, env map[string]string) runResult {
	t.Helper()
	cmd := exec.Command(binary(t), args...)
	cmd.Stdin = strings.NewReader(stdin)
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

// FR-005 + FR-012: login creates config.toml at 0600, stores the token
// from the server, and the password never appears in the file or in
// any output stream.
func TestLogin_HappyPath_PersistsToken(t *testing.T) {
	srv := NewFakeServer(t)
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")

	const password = "sup3rS3cret-DO-NOT-LOG"
	r := runCLIStdin(t, []string{
		"--config", cfg,
		"login", "--server", srv.URL(),
		"--username", "alice",
		"--password-stdin",
	}, password+"\n", nil)
	if r.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "logged in as alice") {
		t.Fatalf("unexpected stdout: %q", r.Stdout)
	}

	// 1. File exists.
	info, err := os.Stat(cfg)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	// 2. 0600 on POSIX.
	if runtime.GOOS != "windows" {
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("mode = %o, want 0600", info.Mode().Perm())
		}
	}
	// 3. Token present, password NOT present.
	body, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	s := string(body)
	if !strings.Contains(s, `token = "`+srv.Token+`"`) {
		t.Fatalf("token not persisted:\n%s", s)
	}
	if strings.Contains(s, password) {
		t.Fatalf("PASSWORD LEAKED into config file")
	}
	// 4. default_profile set on first login.
	if !strings.Contains(s, `default_profile = "default"`) {
		t.Fatalf("default_profile not set:\n%s", s)
	}

	// 5. Password never in any output stream.
	if strings.Contains(r.Stdout, password) || strings.Contains(r.Stderr, password) {
		t.Fatalf("password leaked to streams")
	}

	// 6. Subsequent upload works with the stored token.
	testFile := filepath.Join(dir, "x.pdf")
	_ = os.WriteFile(testFile, []byte("payload"), 0o600)
	r2 := runCLI(t, []string{"--config", cfg, "upload", testFile}, nil)
	if r2.ExitCode != 0 {
		t.Fatalf("follow-up upload failed: exit=%d stderr=%q", r2.ExitCode, r2.Stderr)
	}
}

// Login against the fake server with BAD_PASSWORD returns AUTH (3).
func TestLogin_BadCredentials_IsAUTH(t *testing.T) {
	srv := NewFakeServer(t)
	cfg := filepath.Join(t.TempDir(), "config.toml")

	r := runCLIStdin(t, []string{
		"--config", cfg,
		"login", "--server", srv.URL(),
		"--username", "alice",
		"--password-stdin",
	}, "BAD_PASSWORD\n", nil)
	if r.ExitCode != 3 {
		t.Fatalf("expected AUTH exit 3, got %d stderr=%q", r.ExitCode, r.Stderr)
	}

	// No config.toml should have been written on failure.
	if _, err := os.Stat(cfg); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("config file should not exist after failed login: err=%v", err)
	}
}

// JSON mode returns a structured success envelope carrying
// profile/server/username/token_expires_at and exit_code=0.
func TestLogin_JSON_SuccessShape(t *testing.T) {
	srv := NewFakeServer(t)
	cfg := filepath.Join(t.TempDir(), "config.toml")

	r := runCLIStdin(t, []string{
		"--config", cfg, "--json",
		"login", "--server", srv.URL(),
		"--username", "alice", "--password-stdin",
	}, "secret\n", nil)
	if r.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(r.Stdout)), &got); err != nil {
		t.Fatalf("stdout not JSON: %v\n%q", err, r.Stdout)
	}
	if got["exit_code"] != float64(0) {
		t.Fatalf("exit_code = %v", got["exit_code"])
	}
	if got["profile"] != "default" {
		t.Fatalf("profile = %v", got["profile"])
	}
	if got["username"] != "alice" {
		t.Fatalf("username = %v", got["username"])
	}
	if got["server_url"] != srv.URL() {
		t.Fatalf("server_url = %v", got["server_url"])
	}
	// Token MUST NOT appear in the JSON response.
	if strings.Contains(r.Stdout, srv.Token) {
		t.Fatalf("token leaked into JSON response")
	}
}

// Second login with a different --profile creates a second profile
// without disturbing the first, and --profile on subsequent commands
// selects between them.
func TestLogin_MultipleProfiles_Isolated(t *testing.T) {
	srv := NewFakeServer(t)
	cfg := filepath.Join(t.TempDir(), "config.toml")

	// First login → creates "default".
	r1 := runCLIStdin(t, []string{
		"--config", cfg, "login",
		"--server", srv.URL(),
		"--username", "alice",
		"--password-stdin",
	}, "secret\n", nil)
	if r1.ExitCode != 0 {
		t.Fatalf("first login failed: %s", r1.Stderr)
	}

	// Second login under --profile work.
	r2 := runCLIStdin(t, []string{
		"--config", cfg, "login",
		"--server", srv.URL(),
		"--username", "bob",
		"--password-stdin",
		"--profile", "work",
	}, "secret\n", nil)
	if r2.ExitCode != 0 {
		t.Fatalf("second login failed: %s", r2.Stderr)
	}

	body, _ := os.ReadFile(cfg)
	if !strings.Contains(string(body), "[profiles.default]") ||
		!strings.Contains(string(body), "[profiles.work]") {
		t.Fatalf("both profiles not persisted:\n%s", body)
	}
	if !strings.Contains(string(body), `default_profile = "default"`) {
		t.Fatalf("default_profile should stay 'default' after second login; got:\n%s", body)
	}
}
