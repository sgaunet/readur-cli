package integration_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// End-to-end: seed the fake server with labels, run `readur labels
// list`, confirm the table matches and is sorted by name.
func TestLabelsList_Human_TableSortedByName(t *testing.T) {
	srv := NewFakeServer(t)
	srv.Labels = []map[string]any{
		{"id": "b2", "name": "Zeppelin", "document_count": 1},
		{"id": "a1", "name": "Apples", "color": "#FF0000", "document_count": 10, "description": "red fruit"},
		{"id": "c3", "name": "Invoices", "color": "#00AA00", "document_count": 42},
	}
	cfg := writeProfile(t, srv.URL(), srv.Token, false)

	r := runCLI(t, []string{"--config", cfg, "labels", "list"}, nil)
	if r.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
	lines := strings.Split(strings.TrimRight(r.Stdout, "\n"), "\n")
	if len(lines) < 4 {
		t.Fatalf("expected header + 3 rows, got %d lines:\n%s", len(lines), r.Stdout)
	}
	// Header sanity
	for _, col := range []string{"NAME", "COUNT", "COLOR", "ID", "DESCRIPTION"} {
		if !strings.Contains(lines[0], col) {
			t.Errorf("header missing %q", col)
		}
	}
	// Row order: Apples (a), Invoices (i), Zeppelin (z).
	if !strings.HasPrefix(lines[1], "Apples") {
		t.Fatalf("line 1 should start with Apples, got %q", lines[1])
	}
	if !strings.HasPrefix(lines[2], "Invoices") {
		t.Fatalf("line 2 should start with Invoices, got %q", lines[2])
	}
	if !strings.HasPrefix(lines[3], "Zeppelin") {
		t.Fatalf("line 3 should start with Zeppelin, got %q", lines[3])
	}
}

// --sort count puts highest document_count first.
func TestLabelsList_SortByCount(t *testing.T) {
	srv := NewFakeServer(t)
	srv.Labels = []map[string]any{
		{"id": "a", "name": "A", "document_count": 3},
		{"id": "b", "name": "B", "document_count": 99},
		{"id": "c", "name": "C", "document_count": 42},
	}
	cfg := writeProfile(t, srv.URL(), srv.Token, false)

	r := runCLI(t, []string{"--config", cfg, "labels", "list", "--sort", "count"}, nil)
	if r.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
	lines := strings.Split(strings.TrimRight(r.Stdout, "\n"), "\n")
	// After header: B (99) then C (42) then A (3).
	if !strings.HasPrefix(lines[1], "B") || !strings.HasPrefix(lines[2], "C") || !strings.HasPrefix(lines[3], "A") {
		t.Fatalf("sort by count wrong: %v", lines[1:])
	}
}

// JSON mode shape: labels array, total, exit_code.
func TestLabelsList_JSON_Shape(t *testing.T) {
	srv := NewFakeServer(t)
	srv.Labels = []map[string]any{
		{"id": "1", "name": "Work", "color": "#222222", "document_count": 5, "description": "job stuff", "created_by": "admin", "created_at": "2026-04-20T10:00:00Z"},
	}
	cfg := writeProfile(t, srv.URL(), srv.Token, false)

	r := runCLI(t, []string{"--config", cfg, "--json", "labels", "list"}, nil)
	if r.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
	var got struct {
		Labels []struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			Color         string `json:"color"`
			Description   string `json:"description"`
			DocumentCount int    `json:"document_count"`
			CreatedBy     string `json:"created_by"`
			CreatedAt     string `json:"created_at"`
		} `json:"labels"`
		Total    int `json:"total"`
		ExitCode int `json:"exit_code"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(r.Stdout)), &got); err != nil {
		t.Fatalf("stdout not JSON: %v\n%q", err, r.Stdout)
	}
	if got.ExitCode != 0 || got.Total != 1 || len(got.Labels) != 1 {
		t.Fatalf("shape: %+v", got)
	}
	l := got.Labels[0]
	if l.ID != "1" || l.Name != "Work" || l.Color != "#222222" || l.Description != "job stuff" ||
		l.DocumentCount != 5 || l.CreatedBy != "admin" || l.CreatedAt != "2026-04-20T10:00:00Z" {
		t.Fatalf("label content: %+v", l)
	}
}

// Empty list renders a human-friendly placeholder, not a bare header.
func TestLabelsList_EmptyList(t *testing.T) {
	srv := NewFakeServer(t)
	srv.Labels = nil
	cfg := writeProfile(t, srv.URL(), srv.Token, false)

	r := runCLI(t, []string{"--config", cfg, "labels", "list"}, nil)
	if r.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "(no labels defined)") {
		t.Fatalf("empty placeholder missing: %q", r.Stdout)
	}
}

// Auth failure from the server → exit 3 and no stdout table.
func TestLabelsList_InvalidToken_IsAUTH(t *testing.T) {
	srv := NewFakeServer(t)
	cfg := writeProfile(t, srv.URL(), "WRONG", false)
	_ = filepath.Dir(cfg)
	r := runCLI(t, []string{"--config", cfg, "labels", "list"}, nil)
	if r.ExitCode != 3 {
		t.Fatalf("exit = %d, want 3 AUTH; stderr=%q", r.ExitCode, r.Stderr)
	}
	if strings.Contains(r.Stdout, "NAME") {
		t.Fatalf("stdout should not contain table on auth failure: %q", r.Stdout)
	}
}
