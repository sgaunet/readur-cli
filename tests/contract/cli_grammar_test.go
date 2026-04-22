package contract_test

import (
	"strings"
	"testing"
)

// Guarantee 1: every global flag is accepted by the root and by every
// registered subcommand without USAGE errors (just parsed).
func TestGlobalFlags_AcceptedByRoot(t *testing.T) {
	r := Run(t, []string{"--json", "--quiet", "version"}, nil)
	// --quiet alone is legal; this tests parsing, not semantics.
	if r.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
}

// Guarantee 2: --help exits 0 on every subcommand.
func TestHelp_ExitsZero(t *testing.T) {
	cases := [][]string{
		{"--help"},
		{"help"},
		{"version", "--help"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			r := Run(t, args, nil)
			if r.ExitCode != 0 {
				t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
			}
		})
	}
}

// Guarantee 4: --quiet + --verbose together yield exit code 2.
func TestQuietVerbose_Conflict(t *testing.T) {
	r := Run(t, []string{"--quiet", "--verbose", "version"}, nil)
	if r.ExitCode != 2 {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stderr, "mutually exclusive") &&
		!strings.Contains(r.Stderr, "quiet") {
		t.Fatalf("expected mutex message, got stderr=%q", r.Stderr)
	}
}

// Guarantee 5: `readur` (no subcommand) prints help to stderr and exits 2.
func TestNoSubcommand_Exits2(t *testing.T) {
	r := Run(t, nil, nil)
	if r.ExitCode != 2 {
		t.Fatalf("exit=%d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "Usage:") {
		t.Fatalf("help not on stderr: %q", r.Stderr)
	}
}

// Guarantee 6: unknown subcommand exits 2.
func TestUnknownSubcommand_Exits2(t *testing.T) {
	r := Run(t, []string{"frobnicate"}, nil)
	if r.ExitCode != 2 {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
}

// Version subcommand smoke test — asserts non-empty output on both modes.
func TestVersion_HumanAndJSON(t *testing.T) {
	hr := Run(t, []string{"version"}, nil)
	if hr.ExitCode != 0 || hr.Stdout == "" {
		t.Fatalf("human version failed: exit=%d stdout=%q", hr.ExitCode, hr.Stdout)
	}
	if !strings.HasPrefix(hr.Stdout, "readur ") {
		t.Fatalf("human version shape unexpected: %q", hr.Stdout)
	}

	jr := Run(t, []string{"--json", "version"}, nil)
	if jr.ExitCode != 0 {
		t.Fatalf("json version failed: exit=%d stderr=%q", jr.ExitCode, jr.Stderr)
	}
	v := DecodeJSONStdout[map[string]any](t, jr)
	for _, field := range []string{"version", "commit", "build_date", "go", "exit_code"} {
		if _, ok := v[field]; !ok {
			t.Fatalf("version JSON missing %q: %+v", field, v)
		}
	}
}
