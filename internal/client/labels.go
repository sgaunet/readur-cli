package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// errLabelsUnexpectedShape is returned when the server response body
// starts with a byte that is neither '[' (array) nor '{' (object).
var errLabelsUnexpectedShape = errors.New("unexpected labels response shape")

// labelsBodyReadLimit caps the labels list response body at 16 MiB —
// enough headroom for very large label catalogs while preventing a
// pathological server from exhausting memory.
const labelsBodyReadLimit = 16 << 20

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
	CreatedAt     time.Time `json:"created_at,omitzero"`
}

// ListLabels calls GET /api/labels and returns the server's labels.
// The method tolerates two wire shapes observed in the wild:
//  1. `{"labels": [...]}` — the documented envelope
//  2. `[...]` — a bare array, emitted by some deployments
func (c *Client) ListLabels(ctx context.Context) ([]Label, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL("/api/labels"), nil)
	if err != nil {
		return nil, fmt.Errorf("build labels request: %w", err)
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, errorBodyReadLimit))
		return nil, ClassifyStatus(resp.StatusCode, string(raw))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, labelsBodyReadLimit))
	if err != nil {
		return nil, fmt.Errorf("read labels response: %w", err)
	}
	return parseLabelsBody(body)
}

// SetDocumentLabels replaces the labels attached to a document.
//
// The Readur upload endpoint (POST /api/documents) does not process a
// "labels" multipart field — label assignment is done with this
// separate call. Empty labelIDs is a no-op (no request issued), so the
// upload code path can call this unconditionally without an extra
// branch on its own.
func (c *Client) SetDocumentLabels(ctx context.Context, documentID string, labelIDs []string) error {
	if documentID == "" {
		return errEmptyDocumentID
	}
	if len(labelIDs) == 0 {
		return nil
	}
	payload := struct {
		LabelIDs []string `json:"label_ids"`
	}{LabelIDs: labelIDs}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal label_ids: %w", err)
	}
	resp, err := c.DoJSON(ctx, http.MethodPut,
		c.URL("/api/labels/documents/"+documentID), body)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, errorBodyReadLimit))
		return ClassifyStatus(resp.StatusCode, string(raw))
	}
	return nil
}

// errEmptyDocumentID guards against accidentally calling
// SetDocumentLabels with the empty string, which would otherwise PUT
// to /api/labels/documents/ and yield a confusing 404/405.
var errEmptyDocumentID = errors.New("empty document id")

// parseLabelsBody decodes either the envelope or the bare-array shape.
func parseLabelsBody(body []byte) ([]Label, error) {
	trimmed := bytes.TrimLeft(body, " \t\r\n")
	if len(trimmed) == 0 {
		return []Label{}, nil
	}
	switch trimmed[0] {
	case '[':
		var arr []Label
		err := json.Unmarshal(body, &arr)
		if err != nil {
			return nil, fmt.Errorf("decode labels (array): %w", err)
		}
		return arr, nil
	case '{':
		var env struct {
			Labels []Label `json:"labels"`
		}
		err := json.Unmarshal(body, &env)
		if err != nil {
			return nil, fmt.Errorf("decode labels (envelope): %w", err)
		}
		return env.Labels, nil
	default:
		return nil, fmt.Errorf("%w (first byte %q)", errLabelsUnexpectedShape, trimmed[0])
	}
}
