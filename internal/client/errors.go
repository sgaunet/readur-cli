package client

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"

	cerrors "github.com/sgaunet/readur-cli/internal/errors"
)

// ServerResponseSummary mirrors the authoritative outcome of a single
// HTTP call. Purely internal — not persisted.
type ServerResponseSummary struct {
	DocumentID string // populated on successful upload (201)
	StatusCode int    // HTTP status or 0 if the request never reached the server
	ErrorBody  string // trimmed server error body on non-2xx
	Retryable  bool   // classifier output
}

// IsRetryableStatus reports whether an HTTP status is in the retryable
// class per the clarification in spec.md Q2/Q3: 5xx, 408, 429.
func IsRetryableStatus(code int) bool {
	if code >= 500 && code <= 599 {
		return true
	}
	switch code {
	case http.StatusRequestTimeout, // 408
		http.StatusTooManyRequests: // 429
		return true
	}
	return false
}

// IsRetryableErr reports whether a transport-level error is transient
// enough to justify a retry within the per-request budget.
//
// Retryable classes:
//   - connection reset by peer
//   - broken pipe
//   - unexpected EOF mid-response
//   - DNS temporary errors
//   - context deadline exceeded (when the user did not cancel)
//
// Non-retryable classes (explicit):
//   - x509 / TLS verification failures — the posture is intentional;
//     retrying changes nothing
//   - URL parse errors — a fix is a code change, not time
func IsRetryableErr(err error) bool {
	if err == nil {
		return false
	}
	// URL errors: unwrap to the underlying net/TLS/HTTP error.
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		err = urlErr.Err
	}

	// TLS certificate verification failures: NOT retryable.
	if strings.Contains(err.Error(), "x509") ||
		strings.Contains(err.Error(), "tls:") {
		// Exception: tls.RecordError on a truncated read is a connection
		// failure, not a verification failure.
		if !strings.Contains(err.Error(), "record overflow") &&
			!strings.Contains(err.Error(), "unexpected EOF") {
			return false
		}
	}

	if errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.EPIPE) {
		return true
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}
	return false
}

// ClassifyStatus converts an HTTP status + optional body into a
// CLIError with the appropriate exit code. Used after retries are
// exhausted.
func ClassifyStatus(code int, body string) error {
	trimmed := strings.TrimSpace(body)
	switch {
	case code == http.StatusUnauthorized, code == http.StatusForbidden:
		return cerrors.New(cerrors.CodeAuth,
			fmt.Sprintf("server rejected credentials (HTTP %d)", code), nil)
	case code == http.StatusRequestEntityTooLarge:
		return cerrors.New(cerrors.CodeGeneric,
			"file exceeds server size limit (HTTP 413)", nil)
	case code == http.StatusMethodNotAllowed:
		// The server returned 405 with its Allow header listing the
		// methods it actually accepts for the URL. Point the user at
		// the diagnostic path — this usually means the server URL is
		// wrong or the deployment diverges from the documented API.
		return cerrors.New(cerrors.CodeGeneric,
			"server does not accept this HTTP method at that path (HTTP 405); "+
				"verify the server URL with `readur config show` "+
				"and check the server's response Allow header", nil)
	case code >= 400 && code < 500:
		if trimmed == "" {
			return cerrors.New(cerrors.CodeGeneric,
				fmt.Sprintf("server rejected request (HTTP %d)", code), nil)
		}
		return cerrors.New(cerrors.CodeGeneric,
			fmt.Sprintf("server rejected request (HTTP %d): %s", code, trimmed), nil)
	case code >= 500:
		return cerrors.New(cerrors.CodeNetwork,
			fmt.Sprintf("server error (HTTP %d)", code), nil)
	}
	return nil
}
