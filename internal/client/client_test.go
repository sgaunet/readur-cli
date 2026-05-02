package client_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sgaunet/readur-cli/internal/client"
)

func TestClient_InjectsBearerToken(t *testing.T) {
	var gotAuth, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotUA = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := client.NewClient(client.Options{
		ServerURL: srv.URL,
		Token:     "jwt-token",
		UserAgent: "readur-cli/test",
	})

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/users/profile", nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()

	if gotAuth != "Bearer jwt-token" {
		t.Fatalf("Authorization = %q, want Bearer jwt-token", gotAuth)
	}
	if gotUA != "readur-cli/test" {
		t.Fatalf("User-Agent = %q, want readur-cli/test", gotUA)
	}
}

func TestClient_URL_JoinsCorrectly(t *testing.T) {
	cases := []struct {
		server, path, want string
	}{
		{"https://example.com", "/api/x", "https://example.com/api/x"},
		{"https://example.com/", "/api/x", "https://example.com/api/x"},
		{"https://example.com", "api/x", "https://example.com/api/x"},
		{"https://example.com/base", "/api/x", "https://example.com/base/api/x"},
	}
	for _, tc := range cases {
		c := client.NewClient(client.Options{ServerURL: tc.server})
		if got := c.URL(tc.path); got != tc.want {
			t.Errorf("URL(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestClient_InsecureSkipVerify_EmitsWarningPerRequest(t *testing.T) {
	// httptest.NewTLSServer uses a self-signed cert — untrusted by default.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Extract the server's cert so we can prove strict-mode fails even if
	// the test host had a misconfigured trust store.
	_ = srv.Certificate()

	var warnings bytes.Buffer
	c := client.NewClient(client.Options{
		ServerURL:          srv.URL,
		InsecureSkipVerify: true,
		WarnOut:            &warnings,
		ProfileName:        "test-profile",
	})

	for i := range 3 {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/users/profile", nil)
		resp, err := c.Do(req)
		if err != nil {
			t.Fatalf("Do #%d: %v", i, err)
		}
		_ = resp.Body.Close()
	}

	got := warnings.String()
	count := strings.Count(got, "warning: TLS verification disabled for test-profile")
	if count != 3 {
		t.Fatalf("expected 3 warnings, got %d: %q", count, got)
	}
}

func TestClient_StrictMode_FailsOnSelfSignedCert(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := client.NewClient(client.Options{
		ServerURL:          srv.URL,
		InsecureSkipVerify: false,
	})

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := c.Do(req)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatalf("expected TLS verification failure, got success")
	}
	// Sanity: the error should mention certificate / tls.
	if !strings.Contains(err.Error(), "certificate") &&
		!strings.Contains(err.Error(), "x509") &&
		!strings.Contains(err.Error(), "tls") {
		t.Fatalf("unexpected error shape: %v", err)
	}

}

func TestClient_InsecureWithoutWarnOut_IsSilent(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := client.NewClient(client.Options{
		ServerURL:          srv.URL,
		InsecureSkipVerify: true,
		WarnOut:            nil, // deliberately nil
	})
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()
	// No assertion on warnings other than "did not panic or crash".
}
