package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/sgaunet/readur-cli/internal/client"
	"github.com/sgaunet/readur-cli/internal/config"
	cerrors "github.com/sgaunet/readur-cli/internal/errors"
	"github.com/sgaunet/readur-cli/internal/upload"
)

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

func newUploadCommand(g *Globals) *cobra.Command {
	var (
		title    string
		labels   []string
		ocr      bool
		noOCR    bool
		language string
	)
	cmd := &cobra.Command{
		Use:   "upload <path>",
		Short: "Upload a single document to the Readur server.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if ocr && noOCR {
				return usageErr("--ocr and --no-ocr are mutually exclusive")
			}
			req, err := upload.NewFromPath(args[0])
			if err != nil {
				return err
			}
			req.SetTitle(title)
			req.Labels = labels
			req.SetLanguage(language)
			switch {
			case ocr:
				t := true
				req.SetOCR(&t)
			case noOCR:
				f := false
				req.SetOCR(&f)
			}
			if err := req.Validate(); err != nil {
				return err
			}

			profile, err := loadActiveProfile(g)
			if err != nil {
				return err
			}
			httpClient := buildHTTPClient(g, profile)

			start := time.Now()
			res, err := httpClient.Upload(cmd.Context(), client.UploadParams{
				LocalPath:   req.LocalPath,
				DisplayName: req.DisplayName,
				Title:       req.Title,
				Labels:      req.Labels,
				OCREnabled:  req.OCREnabled,
				Language:    req.Language,
			})
			if err != nil {
				return err
			}
			elapsed := time.Since(start).Milliseconds()

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
		},
	}

	cmd.Flags().StringVar(&title, "title", "", "optional document title")
	cmd.Flags().StringSliceVar(&labels, "label", nil, "label to attach (repeatable)")
	cmd.Flags().BoolVar(&ocr, "ocr", false, "enable OCR (overrides server default)")
	cmd.Flags().BoolVar(&noOCR, "no-ocr", false, "disable OCR (overrides server default)")
	cmd.Flags().StringVar(&language, "language", "", "OCR language, ISO 639-2 code")
	return cmd
}

// loadActiveProfile loads config.toml and selects the profile per flag
// > env > default_profile precedence. Returns a CLIError on failure.
func loadActiveProfile(g *Globals) (*config.Profile, error) {
	paths, err := config.Resolve(g.ConfigPath)
	if err != nil {
		return nil, err
	}
	store := config.NewStore(paths)
	profiles, def, err := store.Load()
	if err != nil {
		return nil, err
	}
	if len(profiles) == 0 {
		return nil, cerrors.New(cerrors.CodeConfig,
			"no profiles configured (run `readur login` first)", nil)
	}
	return config.ResolveProfile(profiles, def, g.ProfileFlag)
}

// buildHTTPClient constructs a client wired with the profile's token,
// server URL, and TLS posture (honoring both the profile config and
// the --insecure-skip-tls-verify global flag).
func buildHTTPClient(g *Globals, p *config.Profile) *client.Client {
	return client.NewClient(client.Options{
		ServerURL:          p.ServerURL,
		Token:              p.Token,
		InsecureSkipVerify: p.InsecureSkipVerify || g.InsecureSkipVerify,
		WarnOut:            g.Writer.Stderr,
		ProfileName:        p.Name,
	})
}
