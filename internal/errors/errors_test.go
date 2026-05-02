package errors_test

import (
	stderr "errors"
	"fmt"
	"testing"

	cerrors "github.com/sgaunet/readur-cli/internal/errors"
)

func TestClassify_Nil(t *testing.T) {
	if got := cerrors.Classify(nil); got != cerrors.CodeOK {
		t.Fatalf("Classify(nil) = %d, want %d", got, cerrors.CodeOK)
	}
}

func TestClassify_UnknownErrorIsGeneric(t *testing.T) {
	if got := cerrors.Classify(stderr.New("boom")); got != cerrors.CodeGeneric {
		t.Fatalf("Classify(plain) = %d, want %d", got, cerrors.CodeGeneric)
	}
}

func TestClassify_DirectCLIError(t *testing.T) {
	cases := []struct {
		name string
		code int
	}{
		{"usage", cerrors.CodeUsage},
		{"noinput", cerrors.CodeNoInput},
		{"config", cerrors.CodeConfig},
		{"auth", cerrors.CodeAuth},
		{"network", cerrors.CodeNetwork},
		{"cantcreat", cerrors.CodeCantCreat},
		{"partial", cerrors.CodePartial},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := cerrors.New(tc.code, tc.name, nil)
			if got := cerrors.Classify(err); got != tc.code {
				t.Fatalf("Classify = %d, want %d", got, tc.code)
			}
		})
	}
}

func TestClassify_WrappedCLIErrorPreservesCode(t *testing.T) {
	inner := cerrors.New(cerrors.CodeAuth, "token rejected", nil)
	wrapped := fmt.Errorf("while doing X: %w", inner)
	if got := cerrors.Classify(wrapped); got != cerrors.CodeAuth {
		t.Fatalf("Classify(wrapped) = %d, want AUTH", got)
	}
}

func TestWrap_PreservesOriginalCode(t *testing.T) {
	orig := cerrors.New(cerrors.CodeNetwork, "dial", nil)
	w := cerrors.Wrap(orig, cerrors.CodeGeneric, "context")
	if w.Code != cerrors.CodeNetwork {
		t.Fatalf("Wrap overrode Code: got %d, want %d", w.Code, cerrors.CodeNetwork)
	}
	if !stderr.Is(w, orig) {
		t.Fatalf("Wrap did not keep cause reachable via errors.Is")
	}
}

func TestWrap_PromotesPlainError(t *testing.T) {
	w := cerrors.Wrap(stderr.New("io"), cerrors.CodeCantCreat, "write state")
	if cerrors.Classify(w) != cerrors.CodeCantCreat {
		t.Fatalf("Classify(Wrap(plain)) = %d, want CANTCREAT", cerrors.Classify(w))
	}
}

func TestWrap_NilReturnsNil(t *testing.T) {
	if cerrors.Wrap(nil, cerrors.CodeGeneric, "x") != nil {
		t.Fatalf("Wrap(nil) should be nil")
	}
}

func TestName_KnownCodes(t *testing.T) {
	want := map[int]string{
		cerrors.CodeOK:        "OK",
		cerrors.CodeGeneric:   "GENERIC",
		cerrors.CodeUsage:     "USAGE",
		cerrors.CodeAuth:      "AUTH",
		cerrors.CodeNetwork:   "NETWORK",
		cerrors.CodePartial:   "PARTIAL",
		cerrors.CodeNoInput:   "NOINPUT",
		cerrors.CodeCantCreat: "CANTCREAT",
		cerrors.CodeConfig:    "CONFIG",
	}
	for code, name := range want {
		if got := cerrors.Name(code); got != name {
			t.Fatalf("Name(%d) = %q, want %q", code, got, name)
		}
	}
	if got := cerrors.Name(999); got != "UNKNOWN" {
		t.Fatalf("Name(999) = %q, want UNKNOWN", got)
	}
}

// Precedence is enforced in Classify by the first-match-wins rule that
// callers apply — the Classify function itself returns a single code
// per error chain. This test ensures that when multiple CLIErrors are
// chained, the innermost (most specific) one wins — matching the
// precedence documentation in contracts/exit-codes.md.
func TestClassify_InnerMostWins(t *testing.T) {
	auth := cerrors.New(cerrors.CodeAuth, "token rejected", nil)
	network := cerrors.Wrap(auth, cerrors.CodeNetwork, "while refreshing")
	if got := cerrors.Classify(network); got != cerrors.CodeAuth {
		t.Fatalf("Classify = %d, want AUTH (inner wins)", got)
	}
}
