package client_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sgaunet/readur-cli/internal/client"
	cerrors "github.com/sgaunet/readur-cli/internal/errors"
)

func TestListLabels_EnvelopeShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/labels" {
			http.Error(w, "wrong path", http.StatusNotFound)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "wrong method", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"labels":[
			{"id":"1","name":"Invoices","color":"#FF5733","document_count":42,"description":"billing"},
			{"id":"2","name":"Receipts","color":"#00AA00","document_count":5}
		]}`))
	}))
	defer srv.Close()

	c := client.NewClient(client.Options{ServerURL: srv.URL, Token: "tk"})
	got, err := c.ListLabels(context.Background())
	if err != nil {
		t.Fatalf("ListLabels: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("count = %d, want 2", len(got))
	}
	if got[0].ID != "1" || got[0].Name != "Invoices" || got[0].Color != "#FF5733" || got[0].DocumentCount != 42 {
		t.Fatalf("first label wrong: %+v", got[0])
	}
	if got[0].Description != "billing" {
		t.Fatalf("description lost: %q", got[0].Description)
	}
}

func TestListLabels_BareArrayShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"a","name":"Work"},{"id":"b","name":"Home"}]`))
	}))
	defer srv.Close()

	c := client.NewClient(client.Options{ServerURL: srv.URL, Token: "tk"})
	got, err := c.ListLabels(context.Background())
	if err != nil {
		t.Fatalf("ListLabels: %v", err)
	}
	if len(got) != 2 || got[1].Name != "Home" {
		t.Fatalf("parsed wrong: %+v", got)
	}
}

func TestListLabels_EmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"labels":[]}`))
	}))
	defer srv.Close()
	c := client.NewClient(client.Options{ServerURL: srv.URL, Token: "tk"})
	got, err := c.ListLabels(context.Background())
	if err != nil {
		t.Fatalf("ListLabels: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty slice, got %d items", len(got))
	}
}

func TestListLabels_401_Is_AUTH(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := client.NewClient(client.Options{ServerURL: srv.URL, Token: "tk"})
	_, err := c.ListLabels(context.Background())
	if err == nil {
		t.Fatalf("expected AUTH error")
	}
	if cerrors.Classify(err) != cerrors.CodeAuth {
		t.Fatalf("code = %d, want AUTH", cerrors.Classify(err))
	}
}

func TestSetDocumentLabels_EmptyIsNoOp(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
	}))
	defer srv.Close()
	c := client.NewClient(client.Options{ServerURL: srv.URL, Token: "tk"})
	if err := c.SetDocumentLabels(context.Background(), "doc-1", nil); err != nil {
		t.Fatalf("nil labels: %v", err)
	}
	if err := c.SetDocumentLabels(context.Background(), "doc-1", []string{}); err != nil {
		t.Fatalf("empty labels: %v", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("server hit %d times, want 0", hits.Load())
	}
}

func TestSetDocumentLabels_HappyPathPutsLabelIDs(t *testing.T) {
	var (
		gotMethod string
		gotPath   string
		gotBody   []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := client.NewClient(client.Options{ServerURL: srv.URL, Token: "tk"})
	err := c.SetDocumentLabels(context.Background(), "doc-1",
		[]string{"d7b9d5d7-1539-4042-a19c-059423d25436", "00000000-0000-0000-0000-000000000002"})
	if err != nil {
		t.Fatalf("SetDocumentLabels: %v", err)
	}

	if gotMethod != http.MethodPut {
		t.Fatalf("method = %q, want PUT", gotMethod)
	}
	if gotPath != "/api/labels/documents/doc-1" {
		t.Fatalf("path = %q", gotPath)
	}
	var parsed struct {
		LabelIDs []string `json:"label_ids"`
	}
	if err := json.Unmarshal(gotBody, &parsed); err != nil {
		t.Fatalf("body not JSON: %v: %q", err, gotBody)
	}
	if strings.Join(parsed.LabelIDs, ",") !=
		"d7b9d5d7-1539-4042-a19c-059423d25436,00000000-0000-0000-0000-000000000002" {
		t.Fatalf("label_ids = %v", parsed.LabelIDs)
	}
}

func TestSetDocumentLabels_404_Is_GENERIC(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"document not found"}`, http.StatusNotFound)
	}))
	defer srv.Close()
	c := client.NewClient(client.Options{ServerURL: srv.URL, Token: "tk"})
	err := c.SetDocumentLabels(context.Background(), "missing", []string{"id-1"})
	if err == nil {
		t.Fatalf("expected error")
	}
	if cerrors.Classify(err) != cerrors.CodeGeneric {
		t.Fatalf("code = %d, want GENERIC", cerrors.Classify(err))
	}
}

func TestSetDocumentLabels_401_Is_AUTH(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := client.NewClient(client.Options{ServerURL: srv.URL, Token: "tk"})
	err := c.SetDocumentLabels(context.Background(), "doc-1", []string{"id-1"})
	if err == nil {
		t.Fatalf("expected AUTH error")
	}
	if cerrors.Classify(err) != cerrors.CodeAuth {
		t.Fatalf("code = %d, want AUTH", cerrors.Classify(err))
	}
}

func TestSetDocumentLabels_EmptyDocumentID_Errors(t *testing.T) {
	c := client.NewClient(client.Options{ServerURL: "http://nowhere", Token: "tk"})
	err := c.SetDocumentLabels(context.Background(), "", []string{"id-1"})
	if err == nil {
		t.Fatalf("expected error for empty doc id")
	}
}

func TestListLabels_MalformedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()
	c := client.NewClient(client.Options{ServerURL: srv.URL, Token: "tk"})
	_, err := c.ListLabels(context.Background())
	if err == nil {
		t.Fatalf("expected decode error")
	}
}
