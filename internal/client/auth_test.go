package client_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sgaunet/readur-cli/internal/client"
	cerrors "github.com/sgaunet/readur-cli/internal/errors"
)

func TestLogin_HappyPath(t *testing.T) {
	var got client.LoginRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth/login" {
			http.Error(w, "wrong path", http.StatusNotFound)
			return
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "jwt-ok",
			"expires_at": "2026-05-20T12:00:00Z",
			"user":       map[string]string{"username": "alice"},
		})
	}))
	defer srv.Close()

	c := client.NewClient(client.Options{ServerURL: srv.URL})
	res, err := c.Login(context.Background(), client.LoginRequest{
		Username: "alice", Password: "secret",
	})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if got.Username != "alice" || got.Password != "secret" {
		t.Fatalf("server saw wrong request: %+v", got)
	}
	if res.Token != "jwt-ok" {
		t.Fatalf("token = %q", res.Token)
	}
	if res.Username != "alice" {
		t.Fatalf("username = %q", res.Username)
	}
	if res.ExpiresAt.IsZero() {
		t.Fatalf("expiry not parsed")
	}
}

func TestLogin_MissingExpiryOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token": "jwt-ok",
			"user":  map[string]string{"username": "bob"},
		})
	}))
	defer srv.Close()
	c := client.NewClient(client.Options{ServerURL: srv.URL})
	res, err := c.Login(context.Background(), client.LoginRequest{Username: "bob", Password: "p"})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if !res.ExpiresAt.IsZero() {
		t.Fatalf("expected zero expiry, got %v", res.ExpiresAt)
	}
}

func TestLogin_401_Is_AUTH(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"invalid credentials"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := client.NewClient(client.Options{ServerURL: srv.URL})
	_, err := c.Login(context.Background(), client.LoginRequest{Username: "u", Password: "bad"})
	if err == nil {
		t.Fatalf("expected AUTH error")
	}
	if cerrors.Classify(err) != cerrors.CodeAuth {
		t.Fatalf("code = %d, want AUTH", cerrors.Classify(err))
	}
}

func TestLogin_MissingTokenField_Errors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"user":{"username":"alice"}}`))
	}))
	defer srv.Close()
	c := client.NewClient(client.Options{ServerURL: srv.URL})
	_, err := c.Login(context.Background(), client.LoginRequest{Username: "alice", Password: "p"})
	if err == nil {
		t.Fatalf("expected error for missing token")
	}
}
