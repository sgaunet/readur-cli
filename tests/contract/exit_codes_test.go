package contract_test

import (
	"testing"
)

// Pre-dispatch USAGE: unknown flag.
func TestExitCode_UnknownFlag_IsUsage(t *testing.T) {
	r := Run(t, []string{"--does-not-exist"}, nil)
	if r.ExitCode != 2 {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
}

// Pre-dispatch USAGE: missing required positional is a future test when
// upload/bulk are wired; for now, verify that an unknown subcommand
// still pins USAGE, not GENERIC.
func TestExitCode_UnknownSubcommand_IsUsage(t *testing.T) {
	r := Run(t, []string{"nope"}, nil)
	if r.ExitCode != 2 {
		t.Fatalf("exit=%d", r.ExitCode)
	}
}
