package contract_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

var (
	buildOnce sync.Once
	binPath   string
	buildErr  error
)

// Binary returns a path to a freshly-built readur binary shared across
// this package's tests. It is built on first use and cached for the
// process lifetime of `go test`.
func Binary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "readur-bin-*")
		if err != nil {
			buildErr = err
			return
		}
		out := filepath.Join(dir, "readur")
		if rel, ok := projectRoot(); ok {
			cmd := exec.Command("go", "build", "-o", out, "./cmd/readur")
			cmd.Dir = rel
			cmd.Stderr = os.Stderr
			cmd.Stdout = os.Stderr
			if err := cmd.Run(); err != nil {
				buildErr = err
				return
			}
			binPath = out
			return
		}
		buildErr = errors.New("project root not found")
	})
	if buildErr != nil {
		t.Fatalf("build readur: %v", buildErr)
	}
	return binPath
}

// projectRoot walks upward from the working dir looking for go.mod.
func projectRoot() (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// Result captures a single CLI invocation's output.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Run executes the readur binary with args + env and returns captured
// streams and exit code. Caller-supplied env is appended to the
// inherited environment; pass nil to use only the inherited env.
func Run(t *testing.T, args []string, env map[string]string) Result {
	t.Helper()
	bin := Binary(t)

	cmd := exec.Command(bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

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
			t.Fatalf("unexpected run error: %v (stderr=%q)", err, stderr.String())
		}
	}
	return Result{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: code,
	}
}

// DecodeJSONStdout decodes the JSON document printed to stdout.
func DecodeJSONStdout[T any](t *testing.T, r Result) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(r.Stdout), &v); err != nil {
		t.Fatalf("stdout not JSON: %v\nstdout=%q", err, r.Stdout)
	}
	return v
}
