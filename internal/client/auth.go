package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// errAuthMissingToken is returned when the server's login response omits
// the token field.
var errAuthMissingToken = errors.New("server response missing token field")

// LoginRequest is the JSON body for POST /api/auth/login.
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoginResult is the subset of the server's response the CLI consumes.
// The server-side shape per docs:
//
//	{
//	  "token":      "<jwt>",
//	  "user":       {"username": "alice", ...},
//	  "expires_at": "2026-05-20T12:00:00Z"
//	}
type LoginResult struct {
	Token     string
	Username  string
	ExpiresAt time.Time // zero if the server did not return one
}

// loginWire mirrors the server wire format. Kept unexported so the
// caller's view is clean.
type loginWire struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at,omitempty"`
	User      struct {
		Username string `json:"username"`
	} `json:"user"`
}

// Login POSTs the credentials to /api/auth/login and parses the
// response. The username in the result is taken from the server's
// echo when present, otherwise from the request — so callers can
// always trust `.Username`.
//
// Login IS the rotation endpoint: a 401 here means the supplied
// credentials are wrong, never "token expired". Login therefore
// disables automatic rotation for its own DoJSON call so a bad
// password can never trigger a second login attempt.
func (c *Client) Login(ctx context.Context, req LoginRequest) (*LoginResult, error) {
	// #nosec G117 — LoginRequest.Password is intentionally marshaled to send credentials to the server.
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal login request: %w", err)
	}
	resp, err := c.DoJSON(skipRotateCtx(ctx), "POST", c.URL("/api/auth/login"), body)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return nil, ClassifyStatus(resp.StatusCode, string(raw))
	}

	var wire loginWire
	err = json.NewDecoder(resp.Body).Decode(&wire)
	if err != nil {
		return nil, fmt.Errorf("decode login response: %w", err)
	}
	if wire.Token == "" {
		return nil, errAuthMissingToken
	}

	out := &LoginResult{
		Token:    wire.Token,
		Username: wire.User.Username,
	}
	if out.Username == "" {
		out.Username = req.Username
	}
	if wire.ExpiresAt != "" {
		t, parseErr := time.Parse(time.RFC3339, wire.ExpiresAt)
		if parseErr == nil {
			out.ExpiresAt = t
		}
	}
	return out, nil
}
