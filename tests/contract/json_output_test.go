package contract_test

import (
	"testing"
)

// General invariant: every --json invocation of any subcommand must
// emit exactly one valid JSON document to stdout and nothing else.
// This file grows as each story's commands come online.
func TestJSON_VersionShape(t *testing.T) {
	r := Run(t, []string{"--json", "version"}, nil)
	if r.ExitCode != 0 {
		t.Fatalf("exit=%d", r.ExitCode)
	}
	v := DecodeJSONStdout[map[string]any](t, r)
	if v["exit_code"] != float64(0) {
		t.Fatalf("exit_code field missing or wrong type: %+v", v)
	}
}
