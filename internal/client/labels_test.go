package client_test

import (
	"context"
	"net/http"
	"net/http/httptest"
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
