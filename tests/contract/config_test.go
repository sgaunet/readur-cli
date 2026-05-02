package contract_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// config show --example prints the annotated template, exit 0.
func TestConfigShow_ExampleOnly(t *testing.T) {
	r := Run(t, []string{"config", "show", "--example"}, nil)
	if r.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
	for _, needle := range []string{
		"default_profile",
		"[profiles.default]",
		"server_url",
		"username",
		"token",
		"obtained_at",
		"insecure_skip_verify",
		"config.toml",
	} {
		if !strings.Contains(r.Stdout, needle) {
			t.Errorf("template missing %q", needle)
		}
	}
	// The token field is illustrated with a placeholder — no real secret
	// string allowed through.
	if !strings.Contains(r.Stdout, "eyJhbGciOi") {
		t.Errorf("template should show a JWT-shaped placeholder")
	}
}

// config show (no config file) prints template + "does not exist yet".
func TestConfigShow_MissingFile(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "no-such-config.toml")
	r := Run(t, []string{"--config", tmp, "config", "show"}, nil)
	if r.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "does not exist yet") {
		t.Fatalf("expected 'does not exist yet' banner, got:\n%s", r.Stdout)
	}
	if !strings.Contains(r.Stdout, tmp) {
		t.Fatalf("expected resolved path %q in output", tmp)
	}
}

// config show with an existing config redacts the token.
func TestConfigShow_ExistingFile_RedactsToken(t *testing.T) {
	cfg := writeProfileFixture(t, "http://example.test")
	// Ensure the test fixture actually contains the token sentinel.
	raw, _ := os.ReadFile(cfg)
	if !strings.Contains(string(raw), `token      = "tk"`) {
		t.Fatalf("fixture invariant violated: %s", raw)
	}

	r := Run(t, []string{"--config", cfg, "config", "show"}, nil)
	if r.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
	// The actual token MUST NOT appear anywhere in stdout or stderr.
	if strings.Contains(r.Stdout, `"tk"`) || strings.Contains(r.Stderr, `"tk"`) {
		t.Fatalf("token leaked: stdout=%q stderr=%q", r.Stdout, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "token      = present (redacted)") {
		t.Fatalf("expected 'present (redacted)' line, got:\n%s", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "default profile     : default") {
		t.Fatalf("expected default profile line, got:\n%s", r.Stdout)
	}
}

// FR-012 (amended): a saved password is redacted in config show
// output — the value MUST NEVER appear; the row reports "present
// (redacted)" when saved, "absent" otherwise.
func TestConfigShow_RedactsPassword(t *testing.T) {
	const secretPassword = "PLEASE-DO-NOT-LEAK-ME"
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	body := `default_profile = "default"

[profiles.default]
server_url = "http://example.test"
username   = "alice"
token      = "tk"
password   = "` + secretPassword + `"
obtained_at = 2026-04-20T10:00:00Z
`
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatalf("write cfg: %v", err)
	}

	r := Run(t, []string{"--config", cfg, "config", "show"}, nil)
	if r.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
	if strings.Contains(r.Stdout, secretPassword) || strings.Contains(r.Stderr, secretPassword) {
		t.Fatalf("password leaked: stdout=%q stderr=%q", r.Stdout, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "password    = present (redacted)") {
		t.Fatalf("expected 'password = present (redacted)' line, got:\n%s", r.Stdout)
	}

	// Same profile without a saved password must report "absent".
	cfg2 := filepath.Join(dir, "config2.toml")
	body2 := strings.ReplaceAll(body, `password   = "`+secretPassword+`"`+"\n", "")
	if err := os.WriteFile(cfg2, []byte(body2), 0o600); err != nil {
		t.Fatalf("write cfg2: %v", err)
	}
	r2 := Run(t, []string{"--config", cfg2, "config", "show"}, nil)
	if r2.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", r2.ExitCode, r2.Stderr)
	}
	if !strings.Contains(r2.Stdout, "password    = absent") {
		t.Fatalf("expected 'password = absent' line, got:\n%s", r2.Stdout)
	}
}

// FR-012 (amended): JSON config-show carries has_password without
// exposing the password value.
func TestConfigShow_JSON_HasPassword(t *testing.T) {
	const secretPassword = "PLEASE-DO-NOT-LEAK-ME"
	cfg := filepath.Join(t.TempDir(), "config.toml")
	body := `default_profile = "default"

[profiles.default]
server_url = "http://example.test"
username   = "alice"
token      = "tk"
password   = "` + secretPassword + `"
obtained_at = 2026-04-20T10:00:00Z
`
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatalf("write cfg: %v", err)
	}
	r := Run(t, []string{"--config", cfg, "--json", "config", "show"}, nil)
	if r.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
	if strings.Contains(r.Stdout, secretPassword) {
		t.Fatalf("password leaked into JSON output: %q", r.Stdout)
	}
	var got struct {
		Profiles []struct {
			HasPassword bool `json:"has_password"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(r.Stdout)), &got); err != nil {
		t.Fatalf("stdout not JSON: %v", err)
	}
	if len(got.Profiles) != 1 || !got.Profiles[0].HasPassword {
		t.Fatalf("has_password not true: %+v", got)
	}
}

// config show --json emits a parseable document with no token value.
func TestConfigShow_JSON_Shape(t *testing.T) {
	cfg := writeProfileFixture(t, "http://example.test")
	r := Run(t, []string{"--config", cfg, "--json", "config", "show"}, nil)
	if r.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
	var got struct {
		ConfigPath     string `json:"config_path"`
		StateDir       string `json:"state_dir"`
		Exists         bool   `json:"exists"`
		DefaultProfile string `json:"default_profile"`
		Profiles       []struct {
			Name      string `json:"name"`
			ServerURL string `json:"server_url"`
			Username  string `json:"username"`
			HasToken  bool   `json:"has_token"`
		} `json:"profiles"`
		Example  string `json:"example_config"`
		ExitCode int    `json:"exit_code"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(r.Stdout)), &got); err != nil {
		t.Fatalf("stdout not JSON: %v\n%q", err, r.Stdout)
	}
	if got.ExitCode != 0 || !got.Exists || got.DefaultProfile != "default" {
		t.Fatalf("shape wrong: %+v", got)
	}
	if len(got.Profiles) != 1 || got.Profiles[0].Name != "default" || !got.Profiles[0].HasToken {
		t.Fatalf("profiles: %+v", got.Profiles)
	}
	// Token value must not appear anywhere in the JSON output.
	if strings.Contains(r.Stdout, `"tk"`) {
		t.Fatalf("token leaked into JSON output")
	}
	if !strings.Contains(got.Example, "default_profile") {
		t.Fatalf("example_config missing content")
	}
}

// config path prints just the resolved path (human + JSON).
func TestConfigPath_PrintsResolvedFile(t *testing.T) {
	cfg := writeProfileFixture(t, "http://example.test")

	r := Run(t, []string{"--config", cfg, "config", "path"}, nil)
	if r.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
	if strings.TrimSpace(r.Stdout) != cfg {
		t.Fatalf("stdout = %q, want %q", strings.TrimSpace(r.Stdout), cfg)
	}

	jr := Run(t, []string{"--config", cfg, "--json", "config", "path"}, nil)
	if jr.ExitCode != 0 {
		t.Fatalf("json exit=%d", jr.ExitCode)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(jr.Stdout)), &out); err != nil {
		t.Fatalf("not JSON: %v: %q", err, jr.Stdout)
	}
	if out["config_path"] != cfg {
		t.Fatalf("config_path = %v", out["config_path"])
	}
}

// config --help prints help for the command group.
func TestConfig_Help(t *testing.T) {
	r := Run(t, []string{"config", "--help"}, nil)
	if r.ExitCode != 0 {
		t.Fatalf("exit=%d", r.ExitCode)
	}
	for _, want := range []string{"show", "path"} {
		if !strings.Contains(r.Stdout, want) && !strings.Contains(r.Stderr, want) {
			t.Errorf("help missing %q", want)
		}
	}
}
