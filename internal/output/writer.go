package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	cerrors "github.com/sgaunet/readur-cli/internal/errors"
)

// Mode selects the primary-output formatter.
type Mode int

const (
	// ModeHuman formats stdout primary output for human readers (default).
	ModeHuman Mode = iota
	// ModeJSON formats stdout primary output as JSON.
	ModeJSON
)

// Verbosity controls stderr diagnostic chatter.
type Verbosity int

const (
	// VerbosityNormal is the default: info+warn on stderr.
	VerbosityNormal Verbosity = iota
	// VerbosityQuiet suppresses progress/info on stderr. Warnings still emitted.
	VerbosityQuiet
	// VerbosityVerbose emits debug-level diagnostics on stderr and
	// includes an "exit=<code> reason=<name>" trailer.
	VerbosityVerbose
)

// Writer is the single object every command writes through. It enforces
// the stdout/stderr split (Constitution III) and handles JSON vs human
// selection (contracts/json-output.md).
type Writer struct {
	// Stdout carries primary data only. Tests may wire a buffer here.
	Stdout io.Writer
	// Stderr carries diagnostics, progress, and prompts.
	Stderr io.Writer
	// Mode selects human vs JSON primary output.
	Mode Mode
	// Verbosity gates the stderr stream.
	Verbosity Verbosity
	// NoColor disables ANSI formatting (currently informational; color
	// is not yet enabled, so this bit is reserved for future use).
	NoColor bool
}

// New returns a Writer wired to os.Stdout/os.Stderr with human output
// and normal verbosity. Commands typically construct one per invocation
// and override fields from persistent flags.
func New() *Writer {
	return &Writer{
		Stdout:    os.Stdout,
		Stderr:    os.Stderr,
		Mode:      ModeHuman,
		Verbosity: VerbosityNormal,
	}
}

// Primary writes v to stdout. In human mode, v must implement fmt.Stringer
// or be a plain string. In JSON mode, v is JSON-marshalled + newline.
func (w *Writer) Primary(v any) error {
	if w.Mode == ModeJSON {
		b, err := json.Marshal(v)
		if err != nil {
			return cerrors.New(cerrors.CodeGeneric, "encode json output", err)
		}
		b = append(b, '\n')
		_, err = w.Stdout.Write(b)
		if err != nil {
			return cerrors.New(cerrors.CodeGeneric, "write stdout", err)
		}
		return nil
	}
	// Human mode: accept string or Stringer.
	switch t := v.(type) {
	case string:
		_, err := fmt.Fprintln(w.Stdout, t)
		if err != nil {
			return fmt.Errorf("write stdout: %w", err)
		}
		return nil
	case fmt.Stringer:
		_, err := fmt.Fprintln(w.Stdout, t.String())
		if err != nil {
			return fmt.Errorf("write stdout: %w", err)
		}
		return nil
	default:
		_, err := fmt.Fprintln(w.Stdout, v)
		if err != nil {
			return fmt.Errorf("write stdout: %w", err)
		}
		return nil
	}
}

// Infof writes a progress/info line to stderr unless quiet.
func (w *Writer) Infof(format string, args ...any) {
	if w.Verbosity == VerbosityQuiet {
		return
	}
	_, _ = fmt.Fprintf(w.Stderr, format+"\n", args...)
}

// Warnf writes a warning line to stderr regardless of quiet.
func (w *Writer) Warnf(format string, args ...any) {
	_, _ = fmt.Fprintf(w.Stderr, "warning: "+format+"\n", args...)
}

// Debugf writes a debug line to stderr only in verbose mode.
func (w *Writer) Debugf(format string, args ...any) {
	if w.Verbosity != VerbosityVerbose {
		return
	}
	_, _ = fmt.Fprintf(w.Stderr, "debug: "+format+"\n", args...)
}

// ExitTrailer emits the verbose exit summary line when verbose is on.
func (w *Writer) ExitTrailer(code int) {
	if w.Verbosity != VerbosityVerbose {
		return
	}
	_, _ = fmt.Fprintf(w.Stderr, "exit=%d reason=%s\n", code, cerrors.Name(code))
}

// ErrorJSON is the wire shape used inside JSON primary output when an
// error is returned instead of a success record.
type ErrorJSON struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ErrorEnvelope is the top-level JSON produced on failure. Callers may
// wrap it with command-specific fields before emitting.
type ErrorEnvelope struct {
	Error    ErrorJSON `json:"error"`
	ExitCode int       `json:"exit_code"`
}

// RenderError writes the human-readable error tree to stderr and, in
// JSON mode, writes the ErrorEnvelope to stdout.
func (w *Writer) RenderError(err error) {
	if err == nil {
		return
	}
	code := cerrors.Classify(err)

	if w.Mode == ModeJSON {
		env := ErrorEnvelope{
			Error:    ErrorJSON{Code: cerrors.Name(code), Message: err.Error()},
			ExitCode: code,
		}
		_ = w.Primary(env)
		w.ExitTrailer(code)
		return
	}

	// Human error: plain-English message to stderr.
	_, _ = fmt.Fprintln(w.Stderr, "error: "+err.Error())
	w.ExitTrailer(code)
}

// ConflictQuietVerbose returns a USAGE CLIError if both quiet and
// verbose are set — enforced by root.go before any I/O.
func ConflictQuietVerbose(quiet, verbose bool) error {
	if quiet && verbose {
		return cerrors.New(cerrors.CodeUsage,
			"--quiet and --verbose are mutually exclusive", nil)
	}
	return nil
}

// ApplyFlags resolves the combined flag state into Writer fields. The
// NO_COLOR environment variable is honored here rather than in each
// command.
func (w *Writer) ApplyFlags(jsonMode, quiet, verbose, noColor bool) error {
	err := ConflictQuietVerbose(quiet, verbose)
	if err != nil {
		return err
	}
	if jsonMode {
		w.Mode = ModeJSON
	} else {
		w.Mode = ModeHuman
	}
	switch {
	case quiet:
		w.Verbosity = VerbosityQuiet
	case verbose:
		w.Verbosity = VerbosityVerbose
	default:
		w.Verbosity = VerbosityNormal
	}
	w.NoColor = noColor || strings.TrimSpace(os.Getenv("NO_COLOR")) != ""
	return nil
}
