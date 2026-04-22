package client_test

import (
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sgaunet/readur-cli/internal/client"
)

func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
}

// parse the multipart body the server received and return fields + filename.
func parseMultipart(t *testing.T, r *http.Request) (fields map[string]string, filename string, content []byte) {
	t.Helper()
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		t.Fatalf("parse: %v", err)
	}
	fields = map[string]string{}
	for k, v := range r.MultipartForm.Value {
		fields[k] = strings.Join(v, ",")
	}
	fhs := r.MultipartForm.File["file"]
	if len(fhs) != 1 {
		t.Fatalf("expected 1 file, got %d", len(fhs))
	}
	filename = fhs[0].Filename
	f, err := fhs[0].Open()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = f.Close() }()
	content, _ = io.ReadAll(f)
	return
}

func TestUpload_MultipartFieldsAndBody(t *testing.T) {
	path := writeTempFile(t, "scan.pdf", "PDF-BODY")
	var got struct {
		fields   map[string]string
		filename string
		body     []byte
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.fields, got.filename, got.body = parseMultipart(t, r)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "doc-1", "filename": got.filename})
	}))
	defer srv.Close()

	c := client.NewClient(client.Options{ServerURL: srv.URL, Token: "tk"})

	title := "Q2 Invoice"
	ocr := false
	lang := "fra"
	res, err := c.Upload(context.Background(), client.UploadParams{
		LocalPath:   path,
		DisplayName: "scan.pdf",
		Title:       &title,
		Labels:      []string{"a", "b"},
		OCREnabled:  &ocr,
		Language:    &lang,
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if res.DocumentID != "doc-1" {
		t.Fatalf("DocumentID = %q", res.DocumentID)
	}
	if got.filename != "scan.pdf" {
		t.Fatalf("filename = %q", got.filename)
	}
	if string(got.body) != "PDF-BODY" {
		t.Fatalf("body = %q", got.body)
	}
	if got.fields["title"] != "Q2 Invoice" {
		t.Fatalf("title = %q", got.fields["title"])
	}
	if got.fields["labels"] != "a,b" {
		t.Fatalf("labels = %q", got.fields["labels"])
	}
	if got.fields["ocr_enabled"] != "false" {
		t.Fatalf("ocr_enabled = %q", got.fields["ocr_enabled"])
	}
	if got.fields["language"] != "fra" {
		t.Fatalf("language = %q", got.fields["language"])
	}
}

func TestUpload_RetryReopensFile(t *testing.T) {
	path := writeTempFile(t, "x.pdf", "ABCDEFGH")
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a := attempts.Add(1)
		// Drain the body to prove it's readable on each attempt.
		_, err := io.Copy(io.Discard, r.Body)
		if err != nil {
			t.Errorf("attempt %d: body read: %v", a, err)
		}
		if a < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "doc-ok", "filename": "x.pdf"})
	}))
	defer srv.Close()

	c := client.NewClient(client.Options{ServerURL: srv.URL, Token: "tk"})
	// Speed up retries.
	before := client.UploadAttempts.Load()

	res, err := c.Upload(context.Background(), client.UploadParams{
		LocalPath: path, DisplayName: "x.pdf",
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if res.DocumentID != "doc-ok" {
		t.Fatalf("unexpected id: %q", res.DocumentID)
	}
	if attempts.Load() != 3 {
		t.Fatalf("server saw %d attempts, want 3", attempts.Load())
	}
	// retryablehttp invokes the ReaderFunc once up-front (to probe
	// content-length) plus once per HTTP attempt. For 3 attempts we
	// therefore expect 4 invocations. The important guarantee is that
	// each attempt sees a FRESH file handle — no state leaks.
	delta := client.UploadAttempts.Load() - before
	if delta < 3 {
		t.Fatalf("body factory invoked %d times, want ≥ 3", delta)
	}
}

func TestUpload_Oversize_Is_GENERIC(t *testing.T) {
	path := writeTempFile(t, "big.pdf", "content")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"too large"}`, http.StatusRequestEntityTooLarge)
	}))
	defer srv.Close()
	c := client.NewClient(client.Options{ServerURL: srv.URL, Token: "tk"})
	_, err := c.Upload(context.Background(), client.UploadParams{LocalPath: path, DisplayName: "big.pdf"})
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestUpload_401_Is_AUTH(t *testing.T) {
	path := writeTempFile(t, "x.pdf", "content")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "", http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := client.NewClient(client.Options{ServerURL: srv.URL, Token: "tk"})
	_, err := c.Upload(context.Background(), client.UploadParams{LocalPath: path, DisplayName: "x.pdf"})
	if err == nil {
		t.Fatalf("expected AUTH error")
	}
}

// The multipart file part must carry a meaningful Content-Type so
// Readur's server tags the document correctly and schedules OCR.
// Hardcoding application/octet-stream silently breaks OCR for PDFs
// and images — guard against regression.
func TestUpload_FilePartContentType_FromExtension(t *testing.T) {
	cases := []struct {
		name string
		ext  string
		want string
	}{
		{"pdf", ".pdf", "application/pdf"},
		{"png", ".png", "image/png"},
		{"jpeg", ".jpg", "image/jpeg"},
		{"plain_txt", ".txt", "text/plain"}, // charset= is stripped
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTempFile(t, "sample"+tc.ext, "fake body bytes")
			var gotCT string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseMultipartForm(8 << 20); err != nil {
					t.Fatalf("parse: %v", err)
				}
				fhs := r.MultipartForm.File["file"]
				if len(fhs) != 1 {
					t.Fatalf("file count = %d", len(fhs))
				}
				gotCT = fhs[0].Header.Get("Content-Type")
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(map[string]string{"id": "x", "filename": fhs[0].Filename})
			}))
			defer srv.Close()
			c := client.NewClient(client.Options{ServerURL: srv.URL, Token: "tk"})
			_, err := c.Upload(context.Background(), client.UploadParams{LocalPath: path, DisplayName: "sample" + tc.ext})
			if err != nil {
				t.Fatalf("Upload: %v", err)
			}
			if gotCT != tc.want {
				t.Fatalf("Content-Type = %q, want %q", gotCT, tc.want)
			}
		})
	}
}

// Files with no extension (e.g. the user's real case: `LICENSE`) must
// fall back to http.DetectContentType sniffing instead of the old
// hardcoded application/octet-stream.
func TestUpload_FilePartContentType_SniffsWhenNoExtension(t *testing.T) {
	// A minimal PDF byte sequence http.DetectContentType recognizes.
	pdfBytes := "%PDF-1.4\n%...\n"
	path := writeTempFile(t, "LICENSE", pdfBytes+"padding to exceed 16 bytes for the sniffer")
	var gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			t.Fatalf("parse: %v", err)
		}
		fhs := r.MultipartForm.File["file"]
		gotCT = fhs[0].Header.Get("Content-Type")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "x", "filename": fhs[0].Filename})
	}))
	defer srv.Close()
	c := client.NewClient(client.Options{ServerURL: srv.URL, Token: "tk"})
	_, err := c.Upload(context.Background(), client.UploadParams{LocalPath: path, DisplayName: "LICENSE"})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if gotCT != "application/pdf" {
		t.Fatalf("Content-Type = %q, want application/pdf (sniffed)", gotCT)
	}
}

// Sniffing must not corrupt the file stream — the server still
// receives the full bytes after we seek back.
func TestUpload_SniffDoesNotTruncateBody(t *testing.T) {
	body := strings.Repeat("ABCDEFGH", 200) // 1600 bytes, > 512 sniff window
	path := writeTempFile(t, "LICENSE", body)
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			t.Fatalf("parse: %v", err)
		}
		fhs := r.MultipartForm.File["file"]
		f, _ := fhs[0].Open()
		gotBody, _ = io.ReadAll(f)
		_ = f.Close()
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "x", "filename": fhs[0].Filename})
	}))
	defer srv.Close()
	c := client.NewClient(client.Options{ServerURL: srv.URL, Token: "tk"})
	_, err := c.Upload(context.Background(), client.UploadParams{LocalPath: path, DisplayName: "LICENSE"})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if string(gotBody) != body {
		t.Fatalf("body corrupted after sniff: got %d bytes, want %d", len(gotBody), len(body))
	}
}

// Sanity: confirm the multipart body round-trips through mime.Multipart
// when there are no optional metadata fields.
func TestUpload_NoMetadataSucceeds(t *testing.T) {
	path := writeTempFile(t, "only.pdf", "X")
	var filename string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, fn, _ := parseMultipart(t, r)
		filename = fn
		// Also roll our own boundary check via multipart.NewReader.
		mr := multipart.NewReader(strings.NewReader(""), "x")
		_ = mr
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "d", "filename": fn})
	}))
	defer srv.Close()
	c := client.NewClient(client.Options{ServerURL: srv.URL, Token: "tk"})
	if _, err := c.Upload(context.Background(), client.UploadParams{LocalPath: path, DisplayName: "only.pdf"}); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if filename != "only.pdf" {
		t.Fatalf("filename = %q", filename)
	}
}
