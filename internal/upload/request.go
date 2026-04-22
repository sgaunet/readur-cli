package upload

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	cerrors "github.com/sgaunet/readur-cli/internal/errors"
)

var (
	languageRe = regexp.MustCompile(`^[a-z]{3}$`)
	labelRe    = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

// DocumentUploadRequest is the intent to upload one local file. Field
// names match data-model.md §DocumentUploadRequest.
type DocumentUploadRequest struct {
	LocalPath   string    // resolved absolute path
	DisplayName string    // filename sent to the server
	SizeBytes   int64     // from os.Stat
	MTime       time.Time // from os.Stat
	Title       *string   // nil → server default
	Labels      []string
	OCREnabled  *bool
	Language    *string
}

// NewFromPath builds a request from a filesystem path, statting the
// file to populate size and mtime. Returns a CLIError with CodeNoInput
// if the path does not exist or is not a regular file.
func NewFromPath(path string) (*DocumentUploadRequest, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, cerrors.New(cerrors.CodeNoInput,
			fmt.Sprintf("cannot resolve path %q", path), err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, cerrors.New(cerrors.CodeNoInput,
				fmt.Sprintf("file not found: %s", abs), err)
		}
		if os.IsPermission(err) {
			return nil, cerrors.New(cerrors.CodeNoInput,
				fmt.Sprintf("cannot read %s: permission denied", abs), err)
		}
		return nil, cerrors.New(cerrors.CodeNoInput,
			fmt.Sprintf("cannot stat %s", abs), err)
	}
	if !info.Mode().IsRegular() {
		return nil, cerrors.New(cerrors.CodeNoInput,
			fmt.Sprintf("not a regular file: %s", abs), nil)
	}
	if info.Size() == 0 {
		return nil, cerrors.New(cerrors.CodeNoInput,
			fmt.Sprintf("empty file: %s", abs), nil)
	}
	return &DocumentUploadRequest{
		LocalPath:   abs,
		DisplayName: info.Name(),
		SizeBytes:   info.Size(),
		MTime:       info.ModTime(),
	}, nil
}

// Validate checks optional-field shapes. LocalPath/SizeBytes/MTime are
// guaranteed to be populated by NewFromPath; Validate covers the
// user-supplied fields.
func (r *DocumentUploadRequest) Validate() error {
	if r == nil {
		return cerrors.New(cerrors.CodeUsage, "nil upload request", nil)
	}
	if r.Language != nil && !languageRe.MatchString(*r.Language) {
		return cerrors.New(cerrors.CodeUsage,
			fmt.Sprintf("invalid language %q (expected 3-letter ISO 639-2 code)", *r.Language), nil)
	}
	for _, l := range r.Labels {
		if !labelRe.MatchString(l) {
			return cerrors.New(cerrors.CodeUsage,
				fmt.Sprintf("invalid label %q (letters, digits, _ or - only)", l), nil)
		}
	}
	return nil
}

// SetTitle is a convenience setter that stores s via a pointer, treating
// the empty string as "no title" (nil).
func (r *DocumentUploadRequest) SetTitle(s string) {
	if s == "" {
		r.Title = nil
		return
	}
	r.Title = &s
}

// SetOCR stores the OCR flag via pointer; callers pass nil to omit.
func (r *DocumentUploadRequest) SetOCR(b *bool) { r.OCREnabled = b }

// SetLanguage stores the language via pointer; empty string → nil.
func (r *DocumentUploadRequest) SetLanguage(s string) {
	if s == "" {
		r.Language = nil
		return
	}
	r.Language = &s
}
