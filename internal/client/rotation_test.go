package client_test

import (
	stderrors "errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sgaunet/readur-cli/internal/client"
	cerrors "github.com/sgaunet/readur-cli/internal/errors"
)

// rotationServer bundles a test HTTP server together with counters and
// a mutable "current valid token" pointer, so rotation tests can
// orchestrate login ↔ data round-trips.
type rotationServer struct {
	srv          *httptest.Server
	loginHits    atomic.Int32
	dataHits     atomic.Int32
	rotateToken  atomic.Pointer[string]
	rejectPasswd string
	// alwaysRejectToken forces every /api/data response to 401 (used to
	// prove a single rotation attempt caps out).
	alwaysRejectToken atomic.Bool
}

// newRotationServer wires /api/auth/login and /api/data. login rotates
// the accepted bearer on every successful call (so the test can
// observe rotation by bearer-value change). If rejectPasswd is set and
// the body's password matches, login returns 401.
func newRotationServer(t *testing.T, initialToken, rejectPasswd string) *rotationServer {
	t.Helper()
	rs := &rotationServer{rejectPasswd: rejectPasswd}
	tok := initialToken
	rs.rotateToken.Store(&tok)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		rs.loginHits.Add(1)
		body := make([]byte, 1024)
		n, _ := r.Body.Read(body)
		raw := string(body[:n])
		if rs.rejectPasswd != "" && containsField(raw, "password", rs.rejectPasswd) {
			http.Error(w, `{"error":"invalid credentials"}`, http.StatusUnauthorized)
			return
		}
		newTok := fmt.Sprintf("tok-%d", rs.loginHits.Load())
		rs.rotateToken.Store(&newTok)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token":"` + newTok + `","user":{"username":"alice"},"expires_at":""}`))
	})
	mux.HandleFunc("/api/data", func(w http.ResponseWriter, r *http.Request) {
		rs.dataHits.Add(1)
		if rs.alwaysRejectToken.Load() {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		want := "Bearer " + *rs.rotateToken.Load()
		if r.Header.Get("Authorization") != want {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	rs.srv = httptest.NewServer(mux)
	t.Cleanup(rs.srv.Close)
	return rs
}

func containsField(body, key, val string) bool {
	// Lightweight JSON scan; avoids pulling json through a test helper.
	// We expect a flat `{"username":"...","password":"..."}` shape.
	needle := `"` + key + `":"` + val + `"`
	for i := 0; i+len(needle) <= len(body); i++ {
		if body[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func TestClient_RotatesOn401WhenCredentialsSaved(t *testing.T) {
	rs := newRotationServer(t, "good-token", "")
	c := client.NewClient(client.Options{
		ServerURL: rs.srv.URL,
		Token:     "stale", // server will reject this bearer on /api/data
		Username:  "alice",
		Password:  "correct-pw",
	})

	req, _ := http.NewRequest(http.MethodGet, rs.srv.URL+"/api/data", nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("final status = %d, want 200", resp.StatusCode)
	}
	if got := rs.loginHits.Load(); got != 1 {
		t.Fatalf("loginHits = %d, want 1", got)
	}
	if got := rs.dataHits.Load(); got != 2 {
		t.Fatalf("dataHits = %d, want 2 (initial 401 + retry 200)", got)
	}
}

func TestClient_ProactivelyRotatesNearExpiry(t *testing.T) {
	rs := newRotationServer(t, "stale-token", "")
	// First call without proactive rotate would 401, forcing a reactive
	// rotation and a second /api/data hit. With proactive rotate the
	// client must log in BEFORE the first /api/data call, so dataHits=1.
	c := client.NewClient(client.Options{
		ServerURL:   rs.srv.URL,
		Token:       "stale-token",
		Username:    "alice",
		Password:    "correct-pw",
		TokenExpiry: time.Now().Add(-1 * time.Minute), // already expired
	})

	req, _ := http.NewRequest(http.MethodGet, rs.srv.URL+"/api/data", nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := rs.loginHits.Load(); got != 1 {
		t.Fatalf("loginHits = %d, want 1 (proactive)", got)
	}
	if got := rs.dataHits.Load(); got != 1 {
		t.Fatalf("dataHits = %d, want 1 (no reactive round-trip)", got)
	}
}

func TestClient_DoesNotRotateWhenPasswordAbsent(t *testing.T) {
	rs := newRotationServer(t, "good-token", "")
	c := client.NewClient(client.Options{
		ServerURL: rs.srv.URL,
		Token:     "stale",
		Username:  "alice",
		// Password deliberately empty.
	})
	req, _ := http.NewRequest(http.MethodGet, rs.srv.URL+"/api/data", nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 passthrough", resp.StatusCode)
	}
	if got := rs.loginHits.Load(); got != 0 {
		t.Fatalf("loginHits = %d, want 0 (no rotation attempted)", got)
	}
}

func TestClient_RotationFailureReturnsCodeAuth(t *testing.T) {
	// Reject the saved password at login time → rotation itself fails.
	rs := newRotationServer(t, "good-token", "saved-but-wrong")
	c := client.NewClient(client.Options{
		ServerURL: rs.srv.URL,
		Token:     "stale",
		Username:  "alice",
		Password:  "saved-but-wrong",
	})
	req, _ := http.NewRequest(http.MethodGet, rs.srv.URL+"/api/data", nil)
	_, err := c.Do(req)
	if err == nil {
		t.Fatalf("want error, got nil")
	}
	var cli *cerrors.CLIError
	if !stderrors.As(err, &cli) {
		t.Fatalf("want *cerrors.CLIError, got %T: %v", err, err)
	}
	if cli.Code != cerrors.CodeAuth {
		t.Fatalf("Code = %d, want CodeAuth (%d)", cli.Code, cerrors.CodeAuth)
	}
	if got := rs.loginHits.Load(); got != 1 {
		t.Fatalf("loginHits = %d, want 1 (single rotation attempt)", got)
	}
}

func TestClient_RotatesAtMostOncePerRequest(t *testing.T) {
	rs := newRotationServer(t, "good-token", "")
	// /api/data always 401 — even a fresh bearer won't pass.
	rs.alwaysRejectToken.Store(true)

	c := client.NewClient(client.Options{
		ServerURL: rs.srv.URL,
		Token:     "stale",
		Username:  "alice",
		Password:  "correct-pw",
	})
	req, _ := http.NewRequest(http.MethodGet, rs.srv.URL+"/api/data", nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (second attempt also fails)", resp.StatusCode)
	}
	if got := rs.loginHits.Load(); got != 1 {
		t.Fatalf("loginHits = %d, want exactly 1 (no second rotation)", got)
	}
	if got := rs.dataHits.Load(); got != 2 {
		t.Fatalf("dataHits = %d, want 2 (original + single replay)", got)
	}
}

func TestClient_LoginItselfDoesNotRecurse(t *testing.T) {
	// Login rejects the given password; no rotation should be attempted.
	rs := newRotationServer(t, "good-token", "attempted-pw")
	c := client.NewClient(client.Options{
		ServerURL: rs.srv.URL,
		Username:  "alice",
		Password:  "attempted-pw",
	})
	_, err := c.Login(t.Context(), client.LoginRequest{
		Username: "alice",
		Password: "attempted-pw",
	})
	if err == nil {
		t.Fatalf("want auth error, got nil")
	}
	if got := rs.loginHits.Load(); got != 1 {
		t.Fatalf("loginHits = %d, want 1 (login must not self-rotate)", got)
	}
}
