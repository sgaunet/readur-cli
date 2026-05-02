package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
)

// errEmptyLocalPath is returned when Upload is called with an empty LocalPath.
var errEmptyLocalPath = errors.New("empty local path")

// sniffBufSize matches http.DetectContentType's internal buffer size:
// it inspects at most the first 512 bytes of a stream.
const sniffBufSize = 512

// errUploadMissingDocumentID is returned when the server's upload
// response omits the document id field.
var errUploadMissingDocumentID = errors.New("server response missing document id")

// UploadParams are the server-visible fields for a single-file upload.
//
// Labels are intentionally absent: the upstream Readur upload handler
// (POST /api/documents) ignores any "labels" multipart field, and label
// assignment is done with a separate PUT /api/labels/documents/{id}
// call (see Client.SetDocumentLabels). The CLI's runUpload composes
// the two calls.
type UploadParams struct {
	LocalPath   string
	DisplayName string
	Title       *string
	OCREnabled  *bool
	Language    *string
}

// UploadResult is the subset of the server response the CLI consumes.
type UploadResult struct {
	DocumentID string `json:"id"`
	Filename   string `json:"filename"`
}

// UploadAttempts is incremented each time a body stream is (re)opened.
// Tests read it to verify retry behavior re-opens the file.
var UploadAttempts atomic.Int64

// Upload streams the file at params.LocalPath to POST /api/documents
// as multipart/form-data. Memory is O(1) in file size; the file is
// re-opened on every retry via retryablehttp.ReaderFunc.
//
// Note on the path: the public Readur docs at
// docs.readur.app/api-reference/ describe POST /api/documents/upload,
// but real Readur deployments return 405 on that path (the /upload
// segment is parsed as a document id). The server's OPTIONS/Allow
// headers treat POST /api/documents as the create endpoint, and that
// is what this client uses.
func (c *Client) Upload(ctx context.Context, params UploadParams) (*UploadResult, error) {
	if params.LocalPath == "" {
		return nil, errEmptyLocalPath
	}
	// Probe the file once to generate a stable multipart boundary and
	// to surface NOT-FOUND early before network work.
	_, statErr := os.Stat(params.LocalPath)
	if statErr != nil {
		return nil, fmt.Errorf("stat %s: %w", params.LocalPath, statErr)
	}

	// Pre-materialize the MIME boundary so every attempt uses the same
	// Content-Type header (the server's multipart parser keys on it).
	boundary := randomBoundary()
	contentType := "multipart/form-data; boundary=" + boundary

	bodyFactory := func() (io.Reader, error) {
		UploadAttempts.Add(1)
		return openMultipartBody(params, boundary)
	}

	resp, err := c.DoStreaming(ctx, "POST", c.URL("/api/documents"), contentType, bodyFactory)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, errorBodyReadLimit))
		return nil, ClassifyStatus(resp.StatusCode, string(raw))
	}

	var out UploadResult
	decErr := json.NewDecoder(resp.Body).Decode(&out)
	if decErr != nil {
		return nil, fmt.Errorf("decode upload response: %w", decErr)
	}
	if out.DocumentID == "" {
		return nil, errUploadMissingDocumentID
	}
	return &out, nil
}

// randomBoundary returns a MIME boundary using the same algorithm
// mime/multipart.Writer uses internally. We compute it once per
// logical request so retry attempts use the same Content-Type.
func randomBoundary() string {
	var w strings.Builder
	mw := multipart.NewWriter(&w)
	b := mw.Boundary()
	_ = mw.Close()
	return b
}

// openMultipartBody returns a streaming reader that emits the
// multipart body for a single upload. It opens the file on call so
// callers (retryablehttp) can replay the body.
func openMultipartBody(params UploadParams, boundary string) (io.Reader, error) {
	f, err := os.Open(params.LocalPath) // #nosec G304 — path validated by upload.NewFromPath
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", params.LocalPath, err)
	}

	// Build the prefix (metadata parts + file-part header) and suffix
	// (closing boundary) in memory — they are tiny regardless of file
	// size.
	var prefix strings.Builder
	writeField := func(name, value string) {
		fmt.Fprintf(&prefix, "--%s\r\n", boundary)
		fmt.Fprintf(&prefix, "Content-Disposition: form-data; name=%q\r\n\r\n", name)
		fmt.Fprintf(&prefix, "%s\r\n", value)
	}

	if params.Title != nil {
		writeField("title", *params.Title)
	}
	if params.OCREnabled != nil {
		writeField("ocr_enabled", strconv.FormatBool(*params.OCREnabled))
	}
	if params.Language != nil {
		writeField("language", *params.Language)
	}

	filename := params.DisplayName
	if filename == "" {
		filename = "document"
	}
	fileContentType, err := detectFileContentType(f, filename)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	fmt.Fprintf(&prefix, "--%s\r\n", boundary)
	fmt.Fprintf(&prefix, "Content-Disposition: form-data; name=\"file\"; filename=%q\r\n", filename)
	fmt.Fprintf(&prefix, "Content-Type: %s\r\n\r\n", fileContentType)

	suffix := "\r\n--" + boundary + "--\r\n"

	return &streamBody{
		prefix: strings.NewReader(prefix.String()),
		file:   f,
		suffix: strings.NewReader(suffix),
	}, nil
}

// streamBody concatenates three readers: (1) the in-memory multipart
// prefix, (2) the file contents (streamed from disk), (3) the closing
// boundary. It closes the file as soon as we're done reading it, so
// the caller does not need to track the FD explicitly.
type streamBody struct {
	prefix io.Reader
	file   *os.File
	suffix io.Reader
	stage  int
}

func (b *streamBody) Read(p []byte) (int, error) {
	for {
		switch b.stage {
		case 0:
			done, n, err := b.readPrefix(p)
			if done {
				return n, err
			}
		case 1:
			done, n, err := b.readFile(p)
			if done {
				return n, err
			}
		default:
			return b.readSuffix(p)
		}
	}
}

// Close releases the underlying file if it is still open.
func (b *streamBody) Close() error {
	if b.file != nil {
		err := b.file.Close()
		b.file = nil
		if err != nil {
			return fmt.Errorf("close file: %w", err)
		}
	}
	return nil
}

// readPrefix reads from the in-memory multipart prefix.
// Returns (shouldReturn, n, err): when shouldReturn is true the caller
// must return (n, err); when false the loop advances to the next stage.
func (b *streamBody) readPrefix(p []byte) (bool, int, error) {
	n, readErr := b.prefix.Read(p)
	if n > 0 {
		return true, n, nil
	}
	if errors.Is(readErr, io.EOF) {
		b.stage = 1
		return false, 0, nil
	}
	if readErr != nil {
		return true, n, fmt.Errorf("read multipart prefix: %w", readErr)
	}
	return true, n, nil
}

// readFile reads from the open file.
// Returns (shouldReturn, n, err): when shouldReturn is true the caller
// must return (n, err); when false the loop advances to the next stage.
func (b *streamBody) readFile(p []byte) (bool, int, error) {
	if b.file == nil {
		b.stage = 2
		return false, 0, nil
	}
	n, readErr := b.file.Read(p)
	if n > 0 {
		return true, n, nil
	}
	if errors.Is(readErr, io.EOF) {
		_ = b.file.Close()
		b.file = nil
		b.stage = 2
		return false, 0, nil
	}
	if readErr != nil {
		return true, n, fmt.Errorf("read file: %w", readErr)
	}
	return true, n, nil
}

// readSuffix reads from the in-memory multipart closing boundary.
// io.EOF is returned as-is — it signals end of the whole stream to callers.
func (b *streamBody) readSuffix(p []byte) (int, error) {
	n, readErr := b.suffix.Read(p)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return n, fmt.Errorf("read multipart suffix: %w", readErr)
	}
	if errors.Is(readErr, io.EOF) {
		return n, io.EOF
	}
	return n, nil
}

// detectFileContentType picks the best MIME type to advertise in the
// multipart file part. Readur's server uses this header to tag the
// document's `File type` and to decide whether OCR applies, so a
// stale `application/octet-stream` suppresses OCR even on PDFs.
//
// Strategy:
//  1. If the filename has an extension with a known MIME mapping, use it.
//     (`.pdf` → application/pdf, `.png` → image/png, etc.)
//  2. Otherwise sniff the first 512 bytes of the file with
//     http.DetectContentType and seek back to the start.
//  3. Fall back to application/octet-stream only when both fail.
//
// We also strip any parameters the mime package attaches (e.g.
// `text/plain; charset=utf-8` → `text/plain`), since Readur treats
// the raw MIME token as the file type and the charset parameter is
// noise for OCR scheduling.
func detectFileContentType(f *os.File, filename string) (string, error) {
	if ext := filepath.Ext(filename); ext != "" {
		if ct := mime.TypeByExtension(ext); ct != "" {
			return stripMIMEParams(ct), nil
		}
	}

	// No extension (or no mapping) — sniff. Read up to 512 bytes, then
	// seek back so the subsequent body stream replays from offset 0.
	buf := make([]byte, sniffBufSize)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", fmt.Errorf("sniff content type: %w", err)
	}
	_, seekErr := f.Seek(0, io.SeekStart)
	if seekErr != nil {
		return "", fmt.Errorf("seek after sniff: %w", seekErr)
	}
	if n == 0 {
		return "application/octet-stream", nil
	}
	return stripMIMEParams(http.DetectContentType(buf[:n])), nil
}

func stripMIMEParams(ct string) string {
	before, _, found := strings.Cut(ct, ";")
	if found {
		return strings.TrimSpace(before)
	}
	return ct
}
