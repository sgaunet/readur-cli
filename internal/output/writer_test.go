package output_test

import (
	"bytes"
	"encoding/json"
	stderr "errors"
	"strings"
	"testing"

	cerrors "github.com/sgaunet/readur-cli/internal/errors"
	"github.com/sgaunet/readur-cli/internal/output"
)

func newWriter() (*output.Writer, *bytes.Buffer, *bytes.Buffer) {
	var out, errb bytes.Buffer
	w := &output.Writer{Stdout: &out, Stderr: &errb}
	return w, &out, &errb
}

func TestApplyFlags_QuietAndVerboseConflict(t *testing.T) {
	w, _, _ := newWriter()
	err := w.ApplyFlags(false, true, true, false)
	if err == nil {
		t.Fatalf("expected USAGE error")
	}
	if cerrors.Classify(err) != cerrors.CodeUsage {
		t.Fatalf("got code %d, want USAGE", cerrors.Classify(err))
	}
}

func TestPrimary_HumanString(t *testing.T) {
	w, out, _ := newWriter()
	_ = w.ApplyFlags(false, false, false, false)
	_ = w.Primary("hello")
	if out.String() != "hello\n" {
		t.Fatalf("stdout = %q, want %q", out.String(), "hello\n")
	}
}

func TestPrimary_JSONMinifiedWithNewline(t *testing.T) {
	w, out, _ := newWriter()
	_ = w.ApplyFlags(true, false, false, false)
	payload := map[string]any{"a": 1, "b": "two"}
	if err := w.Primary(payload); err != nil {
		t.Fatalf("Primary: %v", err)
	}
	got := out.String()
	if !strings.HasSuffix(got, "\n") {
		t.Fatalf("missing newline: %q", got)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(strings.TrimRight(got, "\n")), &decoded); err != nil {
		t.Fatalf("invalid JSON: %v; got %q", err, got)
	}
	if strings.Contains(got, "\n  ") {
		t.Fatalf("JSON should be minified, got %q", got)
	}
}

func TestInfo_SuppressedInQuiet(t *testing.T) {
	w, _, errb := newWriter()
	_ = w.ApplyFlags(false, true, false, false)
	w.Infof("progress 1/3")
	if errb.Len() != 0 {
		t.Fatalf("stderr should be empty in quiet, got %q", errb.String())
	}
}

func TestInfo_EmittedInNormal(t *testing.T) {
	w, _, errb := newWriter()
	_ = w.ApplyFlags(false, false, false, false)
	w.Infof("progress 1/3")
	if !strings.Contains(errb.String(), "progress 1/3") {
		t.Fatalf("expected progress on stderr, got %q", errb.String())
	}
}

func TestWarn_AlwaysEmitted(t *testing.T) {
	for _, verbose := range []bool{false, true} {
		w, _, errb := newWriter()
		_ = w.ApplyFlags(false, !verbose, verbose, false)
		w.Warnf("careful")
		if !strings.Contains(errb.String(), "warning: careful") {
			t.Fatalf("verbose=%v: expected warning on stderr, got %q", verbose, errb.String())
		}
	}
}

func TestDebug_OnlyInVerbose(t *testing.T) {
	// normal
	w, _, errb := newWriter()
	_ = w.ApplyFlags(false, false, false, false)
	w.Debugf("tick")
	if errb.Len() != 0 {
		t.Fatalf("normal: debug should be silent, got %q", errb.String())
	}
	// verbose
	w, _, errb = newWriter()
	_ = w.ApplyFlags(false, false, true, false)
	w.Debugf("tick")
	if !strings.Contains(errb.String(), "debug: tick") {
		t.Fatalf("verbose: expected debug line, got %q", errb.String())
	}
}

func TestExitTrailer_OnlyInVerbose(t *testing.T) {
	w, _, errb := newWriter()
	_ = w.ApplyFlags(false, false, false, false)
	w.ExitTrailer(cerrors.CodeAuth)
	if errb.Len() != 0 {
		t.Fatalf("normal: no trailer, got %q", errb.String())
	}
	w, _, errb = newWriter()
	_ = w.ApplyFlags(false, false, true, false)
	w.ExitTrailer(cerrors.CodeAuth)
	if !strings.Contains(errb.String(), "exit=3 reason=AUTH") {
		t.Fatalf("expected exit trailer, got %q", errb.String())
	}
}

func TestRenderError_JSONEnvelope(t *testing.T) {
	w, out, errb := newWriter()
	_ = w.ApplyFlags(true, false, false, false)
	w.RenderError(cerrors.New(cerrors.CodeNoInput, "file not found: foo.pdf", nil))

	if errb.Len() != 0 {
		t.Fatalf("JSON mode must not emit human text on stderr, got %q", errb.String())
	}
	var env output.ErrorEnvelope
	if err := json.Unmarshal([]byte(strings.TrimRight(out.String(), "\n")), &env); err != nil {
		t.Fatalf("not valid JSON: %v; %q", err, out.String())
	}
	if env.ExitCode != cerrors.CodeNoInput || env.Error.Code != "NOINPUT" {
		t.Fatalf("envelope = %+v", env)
	}
	if !strings.Contains(env.Error.Message, "file not found") {
		t.Fatalf("message lost: %q", env.Error.Message)
	}
}

func TestRenderError_HumanToStderr(t *testing.T) {
	w, out, errb := newWriter()
	_ = w.ApplyFlags(false, false, false, false)
	w.RenderError(stderr.New("boom"))
	if out.Len() != 0 {
		t.Fatalf("human mode must not write errors to stdout, got %q", out.String())
	}
	if !strings.Contains(errb.String(), "error: boom") {
		t.Fatalf("human error text missing: %q", errb.String())
	}
}
