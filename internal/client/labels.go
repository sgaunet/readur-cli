package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Label mirrors one entry in the Readur labels collection. Fields map
// to the JSON shape documented at docs.readur.app/api-reference/.
// Unknown fields are tolerated — json decoding ignores extras.
type Label struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Color         string    `json:"color,omitempty"`
	Description   string    `json:"description,omitempty"`
	DocumentCount int       `json:"document_count,omitempty"`
	CreatedBy     string    `json:"created_by,omitempty"`
	CreatedAt     time.Time `json:"created_at,omitempty"`
}

// ListLabels calls GET /api/labels and returns the server's labels.
// The method tolerates two wire shapes observed in the wild:
//  1. `{"labels": [...]}` — the documented envelope
//  2. `[...]` — a bare array, emitted by some deployments
func (c *Client) ListLabels(ctx context.Context) ([]Label, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL("/api/labels"), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return nil, ClassifyStatus(resp.StatusCode, string(raw))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20)) // 16 MiB cap
	if err != nil {
		return nil, fmt.Errorf("read labels response: %w", err)
	}
	return parseLabelsBody(body)
}

// parseLabelsBody decodes either the envelope or the bare-array shape.
func parseLabelsBody(body []byte) ([]Label, error) {
	trimmed := bytes.TrimLeft(body, " \t\r\n")
	if len(trimmed) == 0 {
		return []Label{}, nil
	}
	switch trimmed[0] {
	case '[':
		var arr []Label
		if err := json.Unmarshal(body, &arr); err != nil {
			return nil, fmt.Errorf("decode labels (array): %w", err)
		}
		return arr, nil
	case '{':
		var env struct {
			Labels []Label `json:"labels"`
		}
		if err := json.Unmarshal(body, &env); err != nil {
			return nil, fmt.Errorf("decode labels (envelope): %w", err)
		}
		return env.Labels, nil
	default:
		return nil, fmt.Errorf("unexpected labels response shape (first byte %q)", trimmed[0])
	}
}
