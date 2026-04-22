package client

import (
	"context"
	"crypto/tls"
	stderrors "errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/hashicorp/go-retryablehttp"

	cerrors "github.com/sgaunet/readur-cli/internal/errors"
)

// local alias to avoid shadowing the package name within wrapTransportError.
var errorsAs = stderrors.As

// Options configures a Client. Zero values are sensible.
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
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: opts.InsecureSkipVerify, // #nosec G402 — opt-out is intentional (FR-016); every request emits a stderr warning.
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
		return nil, err
	}
	c.applyHeaders(rreq.Header)
	c.emitTLSWarning()
	resp, err := c.rc.Do(rreq)
	return resp, wrapTransportError(err)
}

// DoStreaming issues a request whose body is produced by the provided
// ReaderFunc. The function is called fresh before every attempt so
// retries see a rewound body. The reader returned must be a fresh
// stream (e.g. re-open the underlying file).
func (c *Client) DoStreaming(ctx context.Context, method, url string, contentType string, body retryablehttp.ReaderFunc) (*http.Response, error) {
	if c == nil {
		return nil, cerrors.New(cerrors.CodeGeneric, "nil client", nil)
	}
	rreq, err := retryablehttp.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	rreq.Header.Set("Content-Type", contentType)
	c.applyHeaders(rreq.Header)
	c.emitTLSWarning()
	resp, err := c.rc.Do(rreq)
	return resp, wrapTransportError(err)
}

// DoJSON is a convenience wrapper for POSTing a JSON body.
func (c *Client) DoJSON(ctx context.Context, method, url string, body []byte) (*http.Response, error) {
	rreq, err := retryablehttp.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	rreq.Header.Set("Content-Type", "application/json")
	c.applyHeaders(rreq.Header)
	c.emitTLSWarning()
	resp, err := c.rc.Do(rreq)
	return resp, wrapTransportError(err)
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
