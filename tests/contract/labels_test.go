package contract_test

import (
	"encoding/json"
	"strings"
	"testing"
)

// labels --help exits 0 and mentions the list subcommand.
func TestLabels_Help(t *testing.T) {
	r := Run(t, []string{"labels", "--help"}, nil)
	if r.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "list") && !strings.Contains(r.Stderr, "list") {
		t.Fatalf("help missing 'list': %q", r.Stdout)
	}
}

// labels list --help exits 0 and documents --sort.
func TestLabelsList_Help_DocumentsSort(t *testing.T) {
	r := Run(t, []string{"labels", "list", "--help"}, nil)
	if r.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
	text := r.Stdout + r.Stderr
	for _, want := range []string{"--sort", "name", "count", "id"} {
		if !strings.Contains(text, want) {
			t.Errorf("help missing %q", want)
		}
	}
}

// labels list without a profile → CONFIG (78).
func TestLabelsList_NoProfile_IsConfig(t *testing.T) {
	tmp := t.TempDir() + "/absent.toml"
	r := Run(t, []string{"--config", tmp, "labels", "list"}, nil)
	if r.ExitCode != 78 {
		t.Fatalf("exit = %d, want 78 CONFIG; stderr=%q", r.ExitCode, r.Stderr)
	}
}

// JSON error envelope on unreachable server carries exit_code and
// error.code = NETWORK.
func TestLabelsList_JSON_ErrorEnvelope(t *testing.T) {
	cfg := writeProfileFixture(t, "http://127.0.0.1:1")
	r := Run(t, []string{"--config", cfg, "--json", "labels", "list"}, nil)
	if r.ExitCode != 4 {
		t.Fatalf("exit=%d want 4", r.ExitCode)
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
		ExitCode int `json:"exit_code"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(r.Stdout)), &env); err != nil {
		t.Fatalf("stdout not JSON: %v\n%q", err, r.Stdout)
	}
	if env.ExitCode != 4 || env.Error.Code != "NETWORK" {
		t.Fatalf("envelope wrong: %+v", env)
	}
}
