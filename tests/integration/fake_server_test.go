package integration_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// UploadRecord captures what the fake server received on a single-file
// upload so tests can assert metadata correctness.
type UploadRecord struct {
	Filename    string
	Title       string
	Labels      []string
	OCREnabled  *bool
	Language    string
	BodyBytes   int64
	ContentType string
}

// BulkRecord captures a bulk-upload invocation.
type BulkRecord struct {
	Files         []string
	DefaultLabels []string
}

// FakeServer is a stand-in for the Readur HTTP API. Behavior can be
// driven by test code via the exposed Policy fields.
type FakeServer struct {
	mu sync.Mutex

	// Policy knobs (mutate before test dispatch):
	Token          string // expected bearer; "" disables auth check
	Username       string // echoed in login and users/profile responses
	Expiry         string // token_expires_at in login response
	Uploads5xxN    int32  // N upload attempts return 503 before succeeding
	Upload429N     int32  // N upload attempts return 429 before succeeding
	UploadOversize bool   // upload returns 413 always
	UploadInvalid  bool   // upload returns 400 always
	RetryAfter     string // Retry-After header value when injecting 429
	NextDocID      func() string

	// TokenRevokedUntilRelogin, when true, makes every protected
	// endpoint return 401 regardless of the bearer token. The next
	// successful /api/auth/login handler invocation atomically resets
	// it to false, modelling "the server revoked the outstanding
	// token; the user's credentials still work".
	TokenRevokedUntilRelogin atomic.Bool

	// LoginRejectPassword, when non-empty, forces /api/auth/login to
	// return 401 whenever the request body's password equals this
	// value. Models "the saved password is now wrong" while leaving
	// all other passwords acceptable.
	LoginRejectPassword string

	// Captured traffic:
	Uploads          []UploadRecord
	Bulks            []BulkRecord
	Labels           []map[string]any    // seeded labels for GET /api/labels
	DocumentLabels   map[string][]string // captured PUT /api/labels/documents/{id} bodies
	LoginHits        atomic.Int32
	ProfileHits      atomic.Int32
	UploadHits       atomic.Int32
	BulkHits         atomic.Int32
	LabelsHits       atomic.Int32
	AttachLabelsHits atomic.Int32
	AuthFailures     atomic.Int32

	srv *httptest.Server
}

// NewFakeServer returns a running fake server. Call Close() when done.
func NewFakeServer(t *testing.T) *FakeServer {
	t.Helper()
	f := &FakeServer{
		Token:    "test-jwt-token",
		Username: "alice",
		Expiry:   time.Now().Add(7 * 24 * time.Hour).UTC().Format(time.RFC3339),
	}
	seq := int64(0)
	f.NextDocID = func() string {
		n := atomic.AddInt64(&seq, 1)
		return fmt.Sprintf("doc-%04d", n)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/login", f.login)
	mux.HandleFunc("/api/users/profile", f.profile)
	// Real Readur deployments route single-file upload to POST
	// /api/documents and listing to GET /api/documents — same path,
	// different methods. Mirror that behavior here.
	mux.HandleFunc("/api/documents", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			f.upload(w, r)
		case http.MethodGet:
			f.listDocuments(w, r)
		default:
			w.Header().Set("Allow", "POST, GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/documents/bulk-upload", f.bulkUpload)
	mux.HandleFunc("/api/labels", f.labels)
	// PUT /api/labels/documents/{document_id} — replace document labels.
	// http.ServeMux dispatches by longest prefix; this handler owns
	// any path starting with /api/labels/documents/.
	mux.HandleFunc("/api/labels/documents/", f.attachDocumentLabels)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// URL returns the base URL (without trailing slash).
func (f *FakeServer) URL() string { return f.srv.URL }

// Close shuts the fake server down.
func (f *FakeServer) Close() { f.srv.Close() }

// --- handlers -------------------------------------------------------

func (f *FakeServer) login(w http.ResponseWriter, r *http.Request) {
	f.LoginHits.Add(1)
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	// Simulate credential validation: accept exact username, any non-empty
	// password; special value "BAD_PASSWORD" forces 401. Tests that want
	// to model "the saved password is now wrong" set LoginRejectPassword
	// to the password string that should now fail.
	if body.Password == "BAD_PASSWORD" || body.Password == "" {
		f.AuthFailures.Add(1)
		http.Error(w, `{"error":"invalid credentials"}`, http.StatusUnauthorized)
		return
	}
	if f.LoginRejectPassword != "" && body.Password == f.LoginRejectPassword {
		f.AuthFailures.Add(1)
		http.Error(w, `{"error":"invalid credentials"}`, http.StatusUnauthorized)
		return
	}
	// Successful login clears any outstanding "token revoked" flag: the
	// server considers this session freshly authenticated.
	f.TokenRevokedUntilRelogin.Store(false)
	resp := map[string]any{
		"token":      f.Token,
		"user":       map[string]string{"username": body.Username},
		"expires_at": f.Expiry,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func (f *FakeServer) requireAuth(w http.ResponseWriter, r *http.Request) bool {
	if f.TokenRevokedUntilRelogin.Load() {
		f.AuthFailures.Add(1)
		http.Error(w, `{"error":"token revoked"}`, http.StatusUnauthorized)
		return false
	}
	if f.Token == "" {
		return true
	}
	got := r.Header.Get("Authorization")
	if got != "Bearer "+f.Token {
		f.AuthFailures.Add(1)
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return false
	}
	return true
}

func (f *FakeServer) profile(w http.ResponseWriter, r *http.Request) {
	f.ProfileHits.Add(1)
	if !f.requireAuth(w, r) {
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"username": f.Username,
		"email":    f.Username + "@example.com",
	})
}

func (f *FakeServer) upload(w http.ResponseWriter, r *http.Request) {
	f.UploadHits.Add(1)
	if !f.requireAuth(w, r) {
		return
	}

	// Inject 503s before success if configured.
	if remaining := atomic.LoadInt32(&f.Uploads5xxN); remaining > 0 {
		atomic.AddInt32(&f.Uploads5xxN, -1)
		http.Error(w, "transient", http.StatusServiceUnavailable)
		return
	}
	if remaining := atomic.LoadInt32(&f.Upload429N); remaining > 0 {
		atomic.AddInt32(&f.Upload429N, -1)
		if f.RetryAfter != "" {
			w.Header().Set("Retry-After", f.RetryAfter)
		}
		http.Error(w, "slow down", http.StatusTooManyRequests)
		return
	}

	if f.UploadOversize {
		http.Error(w, `{"error":"file exceeds size limit"}`, http.StatusRequestEntityTooLarge)
		return
	}
	if f.UploadInvalid {
		http.Error(w, `{"error":"invalid document"}`, http.StatusBadRequest)
		return
	}

	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	files := r.MultipartForm.File["file"]
	if len(files) != 1 {
		http.Error(w, "missing file", http.StatusBadRequest)
		return
	}
	fh := files[0]
	file, err := fh.Open()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer func() { _ = file.Close() }()
	body, _ := io.Copy(io.Discard, file)

	rec := UploadRecord{
		Filename:    fh.Filename,
		Title:       r.FormValue("title"),
		BodyBytes:   body,
		ContentType: fh.Header.Get("Content-Type"),
		Language:    r.FormValue("language"),
	}
	if lbl := r.FormValue("labels"); lbl != "" {
		rec.Labels = strings.Split(lbl, ",")
	}
	if ocr := r.FormValue("ocr_enabled"); ocr != "" {
		b := ocr == "true"
		rec.OCREnabled = &b
	}
	f.mu.Lock()
	f.Uploads = append(f.Uploads, rec)
	f.mu.Unlock()

	resp := map[string]any{
		"id":       f.NextDocID(),
		"filename": fh.Filename,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}

func (f *FakeServer) bulkUpload(w http.ResponseWriter, r *http.Request) {
	f.BulkHits.Add(1)
	if !f.requireAuth(w, r) {
		return
	}
	if err := r.ParseMultipartForm(128 << 20); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		http.Error(w, "no files", http.StatusBadRequest)
		return
	}
	if len(files) > 10 {
		http.Error(w, "too many files (max 10)", http.StatusBadRequest)
		return
	}
	names := make([]string, 0, len(files))
	for _, fh := range files {
		names = append(names, fh.Filename)
	}
	rec := BulkRecord{Files: names}
	if dl := r.FormValue("default_labels"); dl != "" {
		rec.DefaultLabels = strings.Split(dl, ",")
	}
	f.mu.Lock()
	f.Bulks = append(f.Bulks, rec)
	f.mu.Unlock()

	results := make([]map[string]any, 0, len(files))
	for _, fh := range files {
		results = append(results, map[string]any{
			"id":       f.NextDocID(),
			"filename": fh.Filename,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{"documents": results})
}

func (f *FakeServer) listDocuments(w http.ResponseWriter, r *http.Request) {
	if !f.requireAuth(w, r) {
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"documents": []any{}, "total": 0})
}

// attachDocumentLabels handles PUT /api/labels/documents/{document_id}.
// It records the label_ids body keyed by document id so tests can
// assert post-upload label assignment without parsing wire bytes.
func (f *FakeServer) attachDocumentLabels(w http.ResponseWriter, r *http.Request) {
	f.AttachLabelsHits.Add(1)
	if !f.requireAuth(w, r) {
		return
	}
	if r.Method != http.MethodPut {
		w.Header().Set("Allow", "PUT, GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	const prefix = "/api/labels/documents/"
	docID := strings.TrimPrefix(r.URL.Path, prefix)
	if docID == "" || strings.Contains(docID, "/") {
		http.Error(w, "missing or invalid document id", http.StatusNotFound)
		return
	}
	var body struct {
		LabelIDs []string `json:"label_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	if f.DocumentLabels == nil {
		f.DocumentLabels = map[string][]string{}
	}
	f.DocumentLabels[docID] = append([]string(nil), body.LabelIDs...)
	f.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// labels handles GET /api/labels. It returns whatever has been seeded
// into f.Labels (nil → empty list).
func (f *FakeServer) labels(w http.ResponseWriter, r *http.Request) {
	f.LabelsHits.Add(1)
	if !f.requireAuth(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	labels := f.Labels
	if labels == nil {
		labels = []map[string]any{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"labels": labels})
}

// --- helpers --------------------------------------------------------

// ReadUploadBody is a small helper for tests that want to assert what
// was uploaded; it re-parses captured multipart form data (for cases
// where we snapshot raw bodies in the future). Currently unused but
// kept to guide future tests.
func ReadUploadBody(_ *testing.T, body []byte, _ string) *multipart.Form {
	_ = bytes.NewReader(body)
	return nil
}

// Assert the fake server compiles without importing this file from
// production code.
var _ = NewFakeServer
