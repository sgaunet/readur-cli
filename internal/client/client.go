package client

import (
	"context"
	"crypto/tls"
	stderrors "errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/hashicorp/go-retryablehttp"

	cerrors "github.com/sgaunet/readur-cli/internal/errors"
)

// local alias to avoid shadowing the package name within wrapTransportError.
var errorsAs = stderrors.As

// errorBodyReadLimit caps how many bytes of a non-2xx response body
// the client reads before classifying. 8 KiB is enough to capture any
// reasonable error message while bounding memory.
const errorBodyReadLimit = 8192

// tokenExpirySkew is the slack window used by proactive rotation: if
// the stored token expiry is within this distance of "now", the client
// re-authenticates BEFORE issuing the next request, avoiding a wasted
// 401 round-trip. Matches the AWS SDK default.
const tokenExpirySkew = 60 * time.Second

// noRotateKey is a context sentinel that disables rotation for the
// nested request. Used by Login (which IS the rotation endpoint) and by
// the internal rotate helper so rotation never recurses.
type noRotateKey struct{}

func skipRotateCtx(ctx context.Context) context.Context {
	return context.WithValue(ctx, noRotateKey{}, true)
}

func rotateSkipped(ctx context.Context) bool {
	v, _ := ctx.Value(noRotateKey{}).(bool)
	return v
}

// Options configures a Client. Zero values are sensible.
//
// When Username and Password are both set, the client will attempt to
// silently re-authenticate: proactively, if TokenExpiry is within the
// tokenExpirySkew window; reactively, once per request, on HTTP 401.
// A successful rotation calls OnTokenRotate so the caller (the CLI)
// can persist the refreshed token back to config.toml. See
// research.md §11.
type Options struct {
	ServerURL          string
	Token              string
	UserAgent          string
	InsecureSkipVerify bool
	// WarnOut receives the per-request TLS warning when insecure mode is
	// active. Typically wired to os.Stderr. If nil, warnings are dropped.
	WarnOut io.Writer
	// ProfileName is embedded in the TLS warning for clarity.
	ProfileName string

	// Username/Password are the saved credentials used by automatic
	// token rotation. Both must be non-empty for rotation to run.
	Username string
	Password string
	// TokenExpiry is the wall-clock expiry of the current Token, as
	// obtained at login. Proactive rotation fires when TokenExpiry is
	// past or within tokenExpirySkew of now. Zero disables proactive
	// rotation.
	TokenExpiry time.Time
	// OnTokenRotate is invoked after a successful rotation with the
	// freshly issued token and its expiry. The CLI wires this to
	// persist the new values back into the active profile. nil is
	// allowed — rotation still works; the caller just won't see it.
	OnTokenRotate func(newToken string, expiresAt time.Time)
}

// Client wraps retryablehttp with bearer injection, TLS posture
// control, and the mandatory per-request stderr warning when TLS
// verification is disabled (FR-016).
type Client struct {
	opts Options
	rc   *retryablehttp.Client
}

// NewClient constructs a Client honoring opts.InsecureSkipVerify.
func NewClient(opts Options) *Client {
	if opts.UserAgent == "" {
		opts.UserAgent = "readur-cli"
	}
	rc := NewRetryableClient()
	// Replace the transport to control TLS posture.
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			// #nosec G402 — opt-out is intentional (FR-016); every request emits a stderr warning.
			InsecureSkipVerify: opts.InsecureSkipVerify,
		},
		ForceAttemptHTTP2: true,
	}
	rc.HTTPClient.Transport = tr
	return &Client{opts: opts, rc: rc}
}

// ServerURL returns the configured server URL.
func (c *Client) ServerURL() string { return c.opts.ServerURL }

// URL builds the full URL for a relative API path (e.g. "/api/auth/login").
func (c *Client) URL(path string) string {
	base := strings.TrimRight(c.opts.ServerURL, "/")
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return base + path
}

// Do issues a simple (non-body-replayable) request. Used for GETs and
// POSTs with small in-memory bodies.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	if c == nil {
		return nil, cerrors.New(cerrors.CodeGeneric, "nil client", nil)
	}
	rreq, err := retryablehttp.FromRequest(req)
	if err != nil {
		return nil, fmt.Errorf("build retryable request: %w", err)
	}
	send := func() (*http.Response, error) {
		c.applyHeaders(rreq.Header)
		c.emitTLSWarning()
		return c.rc.Do(rreq)
	}
	resp, err := c.doWithRotation(req.Context(), send)
	return resp, wrapTransportError(err)
}

// DoStreaming issues a request whose body is produced by the provided
// ReaderFunc. The function is called fresh before every attempt so
// retries see a rewound body. The reader returned must be a fresh
// stream (e.g. re-open the underlying file).
func (c *Client) DoStreaming(
	ctx context.Context,
	method, url string,
	contentType string,
	body retryablehttp.ReaderFunc,
) (*http.Response, error) {
	if c == nil {
		return nil, cerrors.New(cerrors.CodeGeneric, "nil client", nil)
	}
	rreq, err := retryablehttp.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("build streaming request: %w", err)
	}
	rreq.Header.Set("Content-Type", contentType)
	send := func() (*http.Response, error) {
		c.applyHeaders(rreq.Header)
		c.emitTLSWarning()
		return c.rc.Do(rreq)
	}
	resp, err := c.doWithRotation(ctx, send)
	return resp, wrapTransportError(err)
}

// DoJSON is a convenience wrapper for POSTing a JSON body.
func (c *Client) DoJSON(ctx context.Context, method, url string, body []byte) (*http.Response, error) {
	rreq, err := retryablehttp.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("build JSON request: %w", err)
	}
	rreq.Header.Set("Content-Type", "application/json")
	send := func() (*http.Response, error) {
		c.applyHeaders(rreq.Header)
		c.emitTLSWarning()
		return c.rc.Do(rreq)
	}
	resp, err := c.doWithRotation(ctx, send)
	return resp, wrapTransportError(err)
}

// canRotate reports whether the client has credentials to attempt a
// silent re-authentication.
func (c *Client) canRotate() bool {
	return c.opts.Username != "" && c.opts.Password != ""
}

// shouldProactivelyRotate reports whether the stored token_expiry is
// past or within tokenExpirySkew of now.
func (c *Client) shouldProactivelyRotate() bool {
	if !c.canRotate() || c.opts.TokenExpiry.IsZero() {
		return false
	}
	return time.Now().After(c.opts.TokenExpiry.Add(-tokenExpirySkew))
}

// rotate performs a single /api/auth/login round-trip using the stored
// credentials and, on success, updates the in-memory token and fires
// the OnTokenRotate callback so the caller can persist the new values.
// The returned error is classified CodeAuth when the saved password
// itself is rejected.
func (c *Client) rotate(ctx context.Context) error {
	if !c.canRotate() {
		return cerrors.New(cerrors.CodeAuth,
			"token rejected and no saved password; run `readur login`", nil)
	}
	res, err := c.Login(skipRotateCtx(ctx), LoginRequest{
		Username: c.opts.Username,
		Password: c.opts.Password,
	})
	if err != nil {
		var cli *cerrors.CLIError
		if errorsAs(err, &cli) && cli.Code == cerrors.CodeAuth {
			return cerrors.New(cerrors.CodeAuth,
				"saved password rejected by server; run `readur login`", err)
		}
		return err
	}
	c.opts.Token = res.Token
	c.opts.TokenExpiry = res.ExpiresAt
	if c.opts.OnTokenRotate != nil {
		c.opts.OnTokenRotate(res.Token, res.ExpiresAt)
	}
	return nil
}

// doWithRotation wraps send with proactive and reactive rotation. send
// is invoked at most twice per call: once normally, and a single
// replay after a rotate triggered by a 401. Contexts carrying
// noRotateKey bypass rotation entirely (used by Login).
func (c *Client) doWithRotation(ctx context.Context, send func() (*http.Response, error)) (*http.Response, error) {
	if rotateSkipped(ctx) {
		return send()
	}
	if c.shouldProactivelyRotate() {
		err := c.rotate(ctx)
		if err != nil {
			return nil, err
		}
	}
	resp, err := send()
	if err != nil || resp.StatusCode != http.StatusUnauthorized || !c.canRotate() {
		return resp, err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	err = c.rotate(ctx)
	if err != nil {
		return nil, err
	}
	return send()
}

// wrapTransportError promotes retryablehttp "giving up after N attempts"
// errors and underlying dial/EOF errors to CodeNetwork so the exit
// code machinery reports NETWORK (4). Response-status errors go
// through ClassifyStatus and are untouched here.
func wrapTransportError(err error) error {
	if err == nil {
		return nil
	}
	var cli *cerrors.CLIError
	if errorsAs(err, &cli) {
		return err
	}
	return cerrors.New(cerrors.CodeNetwork, err.Error(), err)
}

func (c *Client) applyHeaders(h http.Header) {
	if c.opts.Token != "" {
		h.Set("Authorization", "Bearer "+c.opts.Token)
	}
	h.Set("User-Agent", c.opts.UserAgent)
}

func (c *Client) emitTLSWarning() {
	if !c.opts.InsecureSkipVerify || c.opts.WarnOut == nil {
		return
	}
	profile := c.opts.ProfileName
	if profile == "" {
		profile = "active profile"
	}
	_, _ = fmt.Fprintf(c.opts.WarnOut,
		"warning: TLS verification disabled for %s\n", profile)
}
