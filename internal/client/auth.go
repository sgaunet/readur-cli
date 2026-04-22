package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

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
func (c *Client) Login(ctx context.Context, req LoginRequest) (*LoginResult, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	resp, err := c.DoJSON(ctx, "POST", c.URL("/api/auth/login"), body)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return nil, ClassifyStatus(resp.StatusCode, string(raw))
	}

	var wire loginWire
	if err := json.NewDecoder(resp.Body).Decode(&wire); err != nil {
		return nil, fmt.Errorf("decode login response: %w", err)
	}
	if wire.Token == "" {
		return nil, fmt.Errorf("server response missing token field")
	}

	out := &LoginResult{
		Token:    wire.Token,
		Username: wire.User.Username,
	}
	if out.Username == "" {
		out.Username = req.Username
	}
	if wire.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, wire.ExpiresAt); err == nil {
			out.ExpiresAt = t
		}
	}
	return out, nil
}
