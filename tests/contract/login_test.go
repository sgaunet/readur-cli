package contract_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runWithStdin is a variant of Run that pipes a string into the
// process's stdin. Used to drive --password-stdin contract tests.
func runWithStdin(t *testing.T, args []string, stdin string) Result {
	t.Helper()
	bin := Binary(t)
	cmd := exec.Command(bin, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else {
			t.Fatalf("run: %v", err)
		}
	}
	return Result{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: code}
}

// Guarantee 3: the --password flag MUST NOT exist. Any attempt to use
// it yields USAGE (2).
func TestLogin_NoPasswordFlag(t *testing.T) {
	r := Run(t, []string{"login", "--server", "http://x", "--username", "u", "--password=s"}, nil)
	if r.ExitCode != 2 {
		t.Fatalf("expected USAGE for --password, got exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
}

// Help output documents --password-stdin.
func TestLogin_Help_DocumentsStdin(t *testing.T) {
	r := Run(t, []string{"login", "--help"}, nil)
	if r.ExitCode != 0 {
		t.Fatalf("help exit = %d", r.ExitCode)
	}
	text := r.Stdout + r.Stderr
	if !strings.Contains(text, "--password-stdin") {
		t.Fatalf("help missing --password-stdin: %q", text)
	}
	// The behavioral guarantee that no --password flag exists is
	// verified in TestLogin_NoPasswordFlag, which asserts that
	// --password=... is rejected with USAGE. Prose mentioning the
	// absent flag is allowed.
}

// Non-TTY stdin without --password-stdin → USAGE error.
func TestLogin_NonTTYStdin_WithoutFlag_IsUsage(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.toml")
	r := runWithStdin(t, []string{
		"--config", cfg,
		"login", "--server", "http://127.0.0.1:1",
		"--username", "alice",
	}, "") // no --password-stdin, stdin is a pipe (not a TTY)
	if r.ExitCode != 2 {
		t.Fatalf("expected USAGE, got exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stderr, "--password-stdin") {
		t.Fatalf("error should hint at --password-stdin; got %q", r.Stderr)
	}
}

// Empty password via --password-stdin → USAGE.
func TestLogin_EmptyPassword_IsUsage(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.toml")
	r := runWithStdin(t, []string{
		"--config", cfg,
		"login", "--server", "http://127.0.0.1:1",
		"--username", "alice",
		"--password-stdin",
	}, "\n")
	if r.ExitCode != 2 {
		t.Fatalf("expected USAGE for empty password, got exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
}

// Server unreachable → NETWORK (4).
func TestLogin_UnreachableServer_IsNetwork(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.toml")
	r := runWithStdin(t, []string{
		"--config", cfg,
		"login", "--server", "http://127.0.0.1:1",
		"--username", "alice",
		"--password-stdin",
	}, "secret\n")
	if r.ExitCode != 4 {
		t.Fatalf("expected NETWORK exit 4, got %d stderr=%q", r.ExitCode, r.Stderr)
	}
}

// JSON error envelope for unreachable server carries exit_code and
// error.code = NETWORK.
func TestLogin_JSON_ErrorEnvelope(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.toml")
	r := runWithStdin(t, []string{
		"--config", cfg, "--json",
		"login", "--server", "http://127.0.0.1:1",
		"--username", "alice",
		"--password-stdin",
	}, "secret\n")
	if r.ExitCode != 4 {
		t.Fatalf("exit=%d", r.ExitCode)
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
	if env.ExitCode != 4 || env.Error.Code != "NETWORK" {
		t.Fatalf("envelope: %+v", env)
	}
}

// Guarantee 3 (amended): --save-password and --forget-password are
// mutually exclusive → USAGE (2).
func TestLogin_SaveAndForgetMutuallyExclusive(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.toml")
	r := runWithStdin(t, []string{
		"--config", cfg,
		"login", "--server", "http://127.0.0.1:1",
		"--username", "alice", "--password-stdin",
		"--save-password", "--forget-password",
	}, "secret\n")
	if r.ExitCode != 2 {
		t.Fatalf("expected USAGE exit 2, got %d stderr=%q", r.ExitCode, r.Stderr)
	}
}

// Help output documents --save-password and --forget-password.
func TestLogin_Help_DocumentsSaveAndForget(t *testing.T) {
	r := Run(t, []string{"login", "--help"}, nil)
	if r.ExitCode != 0 {
		t.Fatalf("help exit = %d", r.ExitCode)
	}
	text := r.Stdout + r.Stderr
	for _, want := range []string{"--save-password", "--forget-password"} {
		if !strings.Contains(text, want) {
			t.Fatalf("help missing %q: %q", want, text)
		}
	}
}

// No --server and no existing profile → USAGE.
func TestLogin_NoServerNoProfile_IsUsage(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.toml")
	r := runWithStdin(t, []string{
		"--config", cfg,
		"login", "--username", "alice", "--password-stdin",
	}, "secret\n")
	if r.ExitCode != 2 {
		t.Fatalf("expected USAGE, got exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
}
