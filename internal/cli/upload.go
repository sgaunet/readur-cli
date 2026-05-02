package cli

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/spf13/cobra"

	"github.com/sgaunet/readur-cli/internal/client"
	"github.com/sgaunet/readur-cli/internal/config"
	cerrors "github.com/sgaunet/readur-cli/internal/errors"
	"github.com/sgaunet/readur-cli/internal/upload"
)

// uuidRe matches the canonical 8-4-4-4-12 hex UUID form. Inputs that
// match are treated as label IDs and passed through to the server
// unchanged; everything else is treated as a label name and resolved
// via /api/labels.
var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// profileContext bundles the config store, the loaded profile map, the
// file-level default, and the selected active profile. buildHTTPClient
// needs all four to wire a token-rotation callback that persists the
// refreshed token back to config.toml.
type profileContext struct {
	store    *config.Store
	profiles map[string]*config.Profile
	def      string
	active   *config.Profile
}

// UploadJSON is the JSON shape emitted by `readur upload --json` on
// success (see contracts/json-output.md §upload).
type UploadJSON struct {
	LocalPath  string   `json:"local_path"`
	DocumentID string   `json:"document_id"`
	SizeBytes  int64    `json:"size_bytes"`
	Labels     []string `json:"labels"`
	Title      string   `json:"title,omitempty"`
	OCREnabled *bool    `json:"ocr_enabled,omitempty"`
	Language   string   `json:"language,omitempty"`
	DurationMs int64    `json:"duration_ms"`
	ExitCode   int      `json:"exit_code"`
}

// uploadArgs bundles the flag values for the upload subcommand so they
// can be passed as a single argument to runUpload.
type uploadArgs struct {
	title    string
	labels   []string
	ocr      bool
	noOCR    bool
	language string
}

func newUploadCommand(g *Globals) *cobra.Command {
	var ua uploadArgs
	cmd := &cobra.Command{
		Use:   "upload <path>",
		Short: "Upload a single document to the Readur server.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUpload(cmd, g, ua, args[0])
		},
	}

	cmd.Flags().StringVar(&ua.title, "title", "", "optional document title")
	cmd.Flags().StringSliceVar(&ua.labels, "label", nil, "label to attach (repeatable)")
	cmd.Flags().BoolVar(&ua.ocr, "ocr", false, "enable OCR (overrides server default)")
	cmd.Flags().BoolVar(&ua.noOCR, "no-ocr", false, "disable OCR (overrides server default)")
	cmd.Flags().StringVar(&ua.language, "language", "", "OCR language, ISO 639-2 code")
	return cmd
}

// runUpload is the implementation of the upload subcommand, extracted
// from the cobra RunE closure to reduce cyclomatic complexity.
func runUpload(cmd *cobra.Command, g *Globals, ua uploadArgs, path string) error {
	if ua.ocr && ua.noOCR {
		return usageErr("--ocr and --no-ocr are mutually exclusive")
	}

	req, err := upload.NewFromPath(path)
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}
	req.SetTitle(ua.title)
	req.Labels = ua.labels
	req.SetLanguage(ua.language)
	applyOCRFlags(req, ua.ocr, ua.noOCR)
	err = req.Validate()
	if err != nil {
		return fmt.Errorf("validate request: %w", err)
	}

	pctx, err := loadProfileContext(g)
	if err != nil {
		return err
	}
	httpClient := buildHTTPClient(g, pctx)

	// Resolve label names → UUIDs BEFORE uploading so an unknown label
	// fails fast and never leaves an unlabeled orphan document on the
	// server.
	labelIDs, err := resolveLabels(cmd.Context(), httpClient, req.Labels)
	if err != nil {
		return err
	}

	start := time.Now()
	res, err := httpClient.Upload(cmd.Context(), client.UploadParams{
		LocalPath:   req.LocalPath,
		DisplayName: req.DisplayName,
		Title:       req.Title,
		OCREnabled:  req.OCREnabled,
		Language:    req.Language,
	})
	if err != nil {
		return fmt.Errorf("upload: %w", err)
	}

	// The Readur upload endpoint ignores any "labels" multipart field;
	// label assignment is a separate PUT call. SetDocumentLabels is a
	// no-op on empty input, so we can call it unconditionally.
	err = httpClient.SetDocumentLabels(cmd.Context(), res.DocumentID, labelIDs)
	if err != nil {
		return fmt.Errorf("attach labels to %s: %w", res.DocumentID, err)
	}
	elapsed := time.Since(start).Milliseconds()

	return emitUploadOutput(g, req, res, elapsed)
}

// resolveLabels turns user-provided --label values into the server's
// UUIDs. Inputs already in canonical UUID form are passed through;
// every other value is looked up by exact-match Name in the server's
// labels list. A single ListLabels call covers all name lookups in a
// single command invocation.
//
// Unknown names yield CodeUsage with a hint to consult `labels list`,
// failing fast before the file is uploaded so the user never ends up
// with an unlabeled orphan document.
func resolveLabels(ctx context.Context, c *client.Client, raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	// Detect whether any input needs a name → UUID lookup before paying
	// for the /api/labels round-trip.
	needsLookup := false
	for _, v := range raw {
		if !uuidRe.MatchString(v) {
			needsLookup = true
			break
		}
	}

	var byName map[string]string
	if needsLookup {
		labels, err := c.ListLabels(ctx)
		if err != nil {
			return nil, fmt.Errorf("list labels for resolution: %w", err)
		}
		byName = make(map[string]string, len(labels))
		for _, l := range labels {
			byName[l.Name] = l.ID
		}
	}

	// Walk once in input order so the multipart body the server stores
	// (and any future ordered display) reflects what the user typed.
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if uuidRe.MatchString(v) {
			out = append(out, v)
			continue
		}
		id, ok := byName[v]
		if !ok {
			return nil, cerrors.New(cerrors.CodeUsage,
				fmt.Sprintf("label %q not found on server (run `readur labels list` to see available labels)", v), nil)
		}
		out = append(out, id)
	}
	return out, nil
}

// applyOCRFlags sets the OCR flag on req based on the --ocr / --no-ocr
// boolean flags. Both being true is already rejected by the caller.
func applyOCRFlags(req *upload.DocumentUploadRequest, ocr, noOCR bool) {
	switch {
	case ocr:
		t := true
		req.SetOCR(&t)
	case noOCR:
		f := false
		req.SetOCR(&f)
	}
}

// emitUploadOutput writes the upload success result to the writer in
// either JSON or human mode.
func emitUploadOutput(g *Globals, req *upload.DocumentUploadRequest, res *client.UploadResult, elapsed int64) error {
	if g.JSON {
		titleOut := ""
		if req.Title != nil {
			titleOut = *req.Title
		}
		langOut := ""
		if req.Language != nil {
			langOut = *req.Language
		}
		return g.Writer.Primary(UploadJSON{
			LocalPath:  req.LocalPath,
			DocumentID: res.DocumentID,
			SizeBytes:  req.SizeBytes,
			Labels:     req.Labels,
			Title:      titleOut,
			OCREnabled: req.OCREnabled,
			Language:   langOut,
			DurationMs: elapsed,
			ExitCode:   0,
		})
	}
	return g.Writer.Primary(fmt.Sprintf("uploaded: %s  %s", res.DocumentID, req.LocalPath))
}

// loadProfileContext loads config.toml, selects the active profile per
// --profile > READUR_PROFILE > default_profile, and returns enough
// state for buildHTTPClient to wire a rotation-aware callback.
func loadProfileContext(g *Globals) (*profileContext, error) {
	paths, err := config.Resolve(g.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("resolve config paths: %w", err)
	}
	store := config.NewStore(paths)
	profiles, def, err := store.Load()
	if err != nil {
		return nil, fmt.Errorf("load profiles: %w", err)
	}
	if len(profiles) == 0 {
		return nil, cerrors.New(cerrors.CodeConfig,
			"no profiles configured (run `readur login` first)", nil)
	}
	active, err := config.ResolveProfile(profiles, def, g.ProfileFlag)
	if err != nil {
		return nil, fmt.Errorf("resolve active profile: %w", err)
	}
	return &profileContext{store: store, profiles: profiles, def: def, active: active}, nil
}

// buildHTTPClient constructs a client wired with the profile's token,
// server URL, TLS posture, and — when a password is saved — the
// credentials and rotation callback that together let the client
// silently refresh an expired or revoked token.
func buildHTTPClient(g *Globals, pctx *profileContext) *client.Client {
	p := pctx.active
	opts := client.Options{
		ServerURL:          p.ServerURL,
		Token:              p.Token,
		InsecureSkipVerify: p.InsecureSkipVerify || g.InsecureSkipVerify,
		WarnOut:            g.Writer.Stderr,
		ProfileName:        p.Name,
		Username:           p.Username,
		Password:           p.Password,
		TokenExpiry:        p.TokenExpiry,
	}
	if p.Password != "" {
		opts.OnTokenRotate = func(newToken string, expiresAt time.Time) {
			p.Token = newToken
			p.TokenExpiry = expiresAt
			p.ObtainedAt = time.Now().UTC()
			err := pctx.store.Save(pctx.profiles, pctx.def)
			if err != nil {
				_, _ = fmt.Fprintf(g.Writer.Stderr,
					"warning: failed to persist rotated token: %v\n", err)
			}
		}
	}
	return client.NewClient(opts)
}
