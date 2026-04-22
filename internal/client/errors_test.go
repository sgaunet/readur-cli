package client_test

import (
	stderr "errors"
	"io"
	"net"
	"net/url"
	"strings"
	"syscall"
	"testing"

	"github.com/sgaunet/readur-cli/internal/client"
	cerrors "github.com/sgaunet/readur-cli/internal/errors"
)

func TestIsRetryableStatus(t *testing.T) {
	cases := map[int]bool{
		200: false,
		201: false,
		400: false,
		401: false,
		403: false,
		404: false,
		408: true,
		413: false,
		429: true,
		500: true,
		502: true,
		503: true,
		504: true,
	}
	for code, want := range cases {
		if got := client.IsRetryableStatus(code); got != want {
			t.Errorf("IsRetryableStatus(%d) = %v, want %v", code, got, want)
		}
	}
}

type fakeNetErr struct{ timeout bool }

func (f fakeNetErr) Error() string   { return "fake net err" }
func (f fakeNetErr) Timeout() bool   { return f.timeout }
func (f fakeNetErr) Temporary() bool { return f.timeout }

func TestIsRetryableErr(t *testing.T) {
	if client.IsRetryableErr(nil) {
		t.Fatalf("nil should not be retryable")
	}
	if !client.IsRetryableErr(io.EOF) {
		t.Fatalf("io.EOF should be retryable")
	}
	if !client.IsRetryableErr(io.ErrUnexpectedEOF) {
		t.Fatalf("io.ErrUnexpectedEOF should be retryable")
	}
	if !client.IsRetryableErr(syscall.ECONNRESET) {
		t.Fatalf("ECONNRESET should be retryable")
	}
	if !client.IsRetryableErr(syscall.EPIPE) {
		t.Fatalf("EPIPE should be retryable")
	}
	if !client.IsRetryableErr(fakeNetErr{timeout: true}) {
		t.Fatalf("timeout net.Error should be retryable")
	}
	if client.IsRetryableErr(fakeNetErr{timeout: false}) {
		t.Fatalf("non-timeout net.Error should NOT be retryable")
	}
	// URL-wrapped EOF is retryable
	if !client.IsRetryableErr(&url.Error{Op: "Get", URL: "x", Err: io.EOF}) {
		t.Fatalf("wrapped EOF should be retryable")
	}
	// TLS verification failure is NOT retryable
	if client.IsRetryableErr(stderr.New("x509: certificate signed by unknown authority")) {
		t.Fatalf("x509 error should not be retryable")
	}
}

func TestClassifyStatus(t *testing.T) {
	cases := []struct {
		name string
		code int
		body string
		want int // exit code
	}{
		{"auth_401", 401, "", cerrors.CodeAuth},
		{"auth_403", 403, "forbidden", cerrors.CodeAuth},
		{"413_oversize", 413, "too big", cerrors.CodeGeneric},
		{"generic_400", 400, "validation failed", cerrors.CodeGeneric},
		{"generic_405", 405, "method not allowed", cerrors.CodeGeneric},
		{"generic_409", 409, "", cerrors.CodeGeneric},
		{"network_500", 500, "boom", cerrors.CodeNetwork},
		{"network_503", 503, "", cerrors.CodeNetwork},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := client.ClassifyStatus(tc.code, tc.body)
			if err == nil {
				t.Fatalf("expected error")
			}
			if got := cerrors.Classify(err); got != tc.want {
				t.Fatalf("exit code = %d, want %d", got, tc.want)
			}
		})
	}
}

// 405 must produce an actionable message that names the diagnostic
// command. Script consumers can still rely on the stable code (GENERIC).
func TestClassifyStatus_405_IsActionable(t *testing.T) {
	err := client.ClassifyStatus(405, "")
	if err == nil {
		t.Fatalf("expected error")
	}
	msg := err.Error()
	for _, want := range []string{"HTTP 405", "readur config show", "Allow"} {
		if !strings.Contains(msg, want) {
			t.Errorf("405 message missing %q: %q", want, msg)
		}
	}
}

func TestClassifyStatus_NoErrorOn2xx(t *testing.T) {
	if err := client.ClassifyStatus(200, ""); err != nil {
		t.Fatalf("200 should not produce error: %v", err)
	}
	if err := client.ClassifyStatus(201, "body"); err != nil {
		t.Fatalf("201 should not produce error: %v", err)
	}
}

// This test ensures that an opaque net.Error without Timeout still falls
// into the non-retryable bucket (Principle II: deterministic classifier).
var _ net.Error = fakeNetErr{}
