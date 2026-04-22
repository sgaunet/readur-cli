package upload_test

import (
	"os"
	"path/filepath"
	"testing"

	cerrors "github.com/sgaunet/readur-cli/internal/errors"
	"github.com/sgaunet/readur-cli/internal/upload"
)

func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	tmp := t.TempDir()
	p := filepath.Join(tmp, "doc.pdf")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
}

func TestNewFromPath_Happy(t *testing.T) {
	p := writeTempFile(t, "hello pdf")
	r, err := upload.NewFromPath(p)
	if err != nil {
		t.Fatalf("NewFromPath: %v", err)
	}
	if r.DisplayName != "doc.pdf" {
		t.Fatalf("DisplayName = %q", r.DisplayName)
	}
	if r.SizeBytes != int64(len("hello pdf")) {
		t.Fatalf("Size = %d", r.SizeBytes)
	}
	if !filepath.IsAbs(r.LocalPath) {
		t.Fatalf("LocalPath not absolute: %s", r.LocalPath)
	}
}

func TestNewFromPath_Missing_Is_NOINPUT(t *testing.T) {
	_, err := upload.NewFromPath("/nonexistent/path/file.pdf")
	if err == nil {
		t.Fatalf("expected error")
	}
	if cerrors.Classify(err) != cerrors.CodeNoInput {
		t.Fatalf("code = %d, want NOINPUT (66)", cerrors.Classify(err))
	}
}

func TestNewFromPath_Directory_Is_NOINPUT(t *testing.T) {
	_, err := upload.NewFromPath(t.TempDir())
	if err == nil {
		t.Fatalf("expected error for directory")
	}
	if cerrors.Classify(err) != cerrors.CodeNoInput {
		t.Fatalf("code = %d, want NOINPUT", cerrors.Classify(err))
	}
}

func TestNewFromPath_Empty_Is_NOINPUT(t *testing.T) {
	p := writeTempFile(t, "")
	_, err := upload.NewFromPath(p)
	if err == nil {
		t.Fatalf("expected error")
	}
	if cerrors.Classify(err) != cerrors.CodeNoInput {
		t.Fatalf("code = %d, want NOINPUT", cerrors.Classify(err))
	}
}

func TestValidate_LanguageRegex(t *testing.T) {
	p := writeTempFile(t, "x")
	r, err := upload.NewFromPath(p)
	if err != nil {
		t.Fatalf("NewFromPath: %v", err)
	}
	bad := "english"
	r.Language = &bad
	if err := r.Validate(); err == nil {
		t.Fatalf("expected error for bad language")
	}
	good := "eng"
	r.Language = &good
	if err := r.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidate_LabelRegex(t *testing.T) {
	p := writeTempFile(t, "x")
	r, _ := upload.NewFromPath(p)
	r.Labels = []string{"ok-label", "also_ok", "bad label"}
	if err := r.Validate(); err == nil {
		t.Fatalf("expected error for space in label")
	}
	if cerrors.Classify(r.Validate()) != cerrors.CodeUsage {
		t.Fatalf("label err should be USAGE")
	}
}

func TestSetters(t *testing.T) {
	p := writeTempFile(t, "x")
	r, _ := upload.NewFromPath(p)
	r.SetTitle("")
	if r.Title != nil {
		t.Fatalf("empty title should be nil")
	}
	r.SetTitle("Hello")
	if r.Title == nil || *r.Title != "Hello" {
		t.Fatalf("title not set: %+v", r.Title)
	}
	r.SetLanguage("")
	if r.Language != nil {
		t.Fatalf("empty lang should be nil")
	}
	r.SetLanguage("fra")
	if r.Language == nil || *r.Language != "fra" {
		t.Fatalf("lang not set")
	}
	f := false
	r.SetOCR(&f)
	if r.OCREnabled == nil || *r.OCREnabled {
		t.Fatalf("ocr setter broken")
	}
}
