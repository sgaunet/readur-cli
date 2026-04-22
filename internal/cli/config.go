package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sgaunet/readur-cli/internal/config"
)

// ConfigShowJSON is the JSON shape emitted by `readur config show --json`.
type ConfigShowJSON struct {
	ConfigPath string             `json:"config_path"`
	StateDir   string             `json:"state_dir"`
	Exists     bool               `json:"exists"`
	DefaultIs  string             `json:"default_profile,omitempty"`
	Profiles   []ConfigProfileRow `json:"profiles"`
	Example    string             `json:"example_config"`
	ExitCode   int                `json:"exit_code"`
}

// ConfigProfileRow is a redacted view of a profile for display. The
// token is never included.
type ConfigProfileRow struct {
	Name               string `json:"name"`
	ServerURL          string `json:"server_url"`
	Username           string `json:"username"`
	ObtainedAt         string `json:"obtained_at"`
	TokenExpiry        string `json:"token_expiry,omitempty"`
	InsecureSkipVerify bool   `json:"insecure_skip_verify"`
	HasToken           bool   `json:"has_token"`
}

// exampleConfigTOML is the template emitted by `config show`. Keeping
// it as a raw constant (rather than hand-building from struct tags)
// lets us include explanatory comments, which are the whole point of
// the subcommand.
const exampleConfigTOML = `# readur-cli config.toml
#
# File location (macOS):   ~/Library/Application Support/readur-cli/config.toml
# File location (Linux):   $XDG_CONFIG_HOME/readur-cli/config.toml
#                          (defaults to ~/.config/readur-cli/config.toml)
# File location (Windows): %APPDATA%\readur-cli\config.toml
# Override: --config <path> or READUR_CONFIG=<path>
#
# Permissions: 0600 on POSIX (enforced by ` + "`readur login`" + `). The token
# is treated as a secret — never log it, never commit this file to VCS.

# Name of the profile used when --profile and READUR_PROFILE are unset.
default_profile = "default"

[profiles.default]
# Server URL. Must start with http:// or https://.
server_url = "https://readur.example.com"

# Username that the token belongs to.
username = "alice"

# Server-issued JWT bearer token (obtained via ` + "`readur login`" + `).
# Treat as a secret; do not share this file.
token = "eyJhbGciOiJIUzI1NiIs..."

# Timestamp of when the token was obtained (RFC 3339 / ISO 8601 UTC).
obtained_at = 2026-04-20T10:00:00Z

# OPTIONAL — expiry, if the server returned one. Used by ` + "`readur status`" + `
# to warn when the token is close to expiring.
# token_expiry = 2026-05-20T12:00:00Z

# OPTIONAL — disables TLS certificate verification for this profile.
# Use only with self-signed internal CAs or in trusted test environments.
# The CLI emits a stderr warning on every request while this is true.
# insecure_skip_verify = false


# You can define multiple profiles in the same file, one per Readur
# server. Select one with --profile <name> or READUR_PROFILE=<name>.
#
# [profiles.work]
# server_url = "https://readur.work.example.com"
# username   = "bob"
# token      = "..."
# obtained_at = 2026-04-20T11:00:00Z
`

func newConfigCommand(g *Globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect and document the on-disk config.",
	}
	cmd.AddCommand(newConfigShowCommand(g))
	cmd.AddCommand(newConfigPathCommand(g))
	return cmd
}

func newConfigPathCommand(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print the resolved config file path and exit.",
		RunE: func(_ *cobra.Command, _ []string) error {
			paths, err := config.Resolve(g.ConfigPath)
			if err != nil {
				return err
			}
			if g.JSON {
				return g.Writer.Primary(map[string]any{
					"config_path": paths.ConfigFile,
					"state_dir":   paths.StateDir,
					"exit_code":   0,
				})
			}
			return g.Writer.Primary(paths.ConfigFile)
		},
	}
}

func newConfigShowCommand(g *Globals) *cobra.Command {
	var exampleOnly bool
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show the expected config file format and current profiles.",
		Long: `Prints an annotated TOML template showing the expected shape of
config.toml, the resolved location of the file, and — if the file
exists — a redacted summary of each stored profile (the token is
never printed).

Use this subcommand on first run to see where the CLI will look for
configuration, or after editing config.toml by hand to verify the
file parses and every profile is well-formed.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			paths, err := config.Resolve(g.ConfigPath)
			if err != nil {
				return err
			}

			// --example / --format: print only the template, skip loading.
			if exampleOnly {
				if g.JSON {
					return g.Writer.Primary(ConfigShowJSON{
						ConfigPath: paths.ConfigFile,
						StateDir:   paths.StateDir,
						Example:    exampleConfigTOML,
						ExitCode:   0,
					})
				}
				return g.Writer.Primary(strings.TrimRight(exampleConfigTOML, "\n"))
			}

			store := config.NewStore(paths)
			profiles, defaultProfile, loadErr := store.Load()

			// Build the redacted profile rows.
			exists := fileExists(paths.ConfigFile)
			rows := make([]ConfigProfileRow, 0, len(profiles))
			names := make([]string, 0, len(profiles))
			for name := range profiles {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				p := profiles[name]
				row := ConfigProfileRow{
					Name:               name,
					ServerURL:          p.ServerURL,
					Username:           p.Username,
					InsecureSkipVerify: p.InsecureSkipVerify,
					HasToken:           p.Token != "",
				}
				if !p.ObtainedAt.IsZero() {
					row.ObtainedAt = p.ObtainedAt.UTC().Format("2006-01-02T15:04:05Z")
				}
				if !p.TokenExpiry.IsZero() {
					row.TokenExpiry = p.TokenExpiry.UTC().Format("2006-01-02T15:04:05Z")
				}
				rows = append(rows, row)
			}

			if g.JSON {
				out := ConfigShowJSON{
					ConfigPath: paths.ConfigFile,
					StateDir:   paths.StateDir,
					Exists:     exists,
					DefaultIs:  defaultProfile,
					Profiles:   rows,
					Example:    exampleConfigTOML,
					ExitCode:   0,
				}
				// If load returned an error, surface it under the envelope's
				// ExitCode. (Currently Load treats missing file as "empty,
				// no error"; a parse error falls to the cobra error path.)
				if loadErr != nil {
					return loadErr
				}
				return g.Writer.Primary(out)
			}

			// Human mode: template + resolved path + profile summary.
			var b strings.Builder
			b.WriteString(strings.TrimRight(exampleConfigTOML, "\n"))
			b.WriteString("\n\n")
			fmt.Fprintf(&b, "# resolved config path : %s\n", paths.ConfigFile)
			fmt.Fprintf(&b, "# resolved state dir   : %s\n", paths.StateDir)
			if !exists {
				b.WriteString("# file status          : does not exist yet (run `readur login`)\n")
			} else {
				b.WriteString("# file status          : exists\n")
				if defaultProfile != "" {
					fmt.Fprintf(&b, "# default profile     : %s\n", defaultProfile)
				}
				if len(rows) == 0 {
					b.WriteString("# profiles            : none configured\n")
				} else {
					b.WriteString("# profiles            :\n")
					for _, r := range rows {
						token := "absent"
						if r.HasToken {
							token = "present (redacted)"
						}
						insec := ""
						if r.InsecureSkipVerify {
							insec = "  (insecure_skip_verify = true)"
						}
						fmt.Fprintf(&b, "#   - %s\n", r.Name)
						fmt.Fprintf(&b, "#       server_url = %s%s\n", r.ServerURL, insec)
						fmt.Fprintf(&b, "#       username   = %s\n", r.Username)
						fmt.Fprintf(&b, "#       token      = %s\n", token)
						if r.ObtainedAt != "" {
							fmt.Fprintf(&b, "#       obtained_at = %s\n", r.ObtainedAt)
						}
						if r.TokenExpiry != "" {
							fmt.Fprintf(&b, "#       token_expiry = %s\n", r.TokenExpiry)
						}
					}
				}
			}
			if loadErr != nil {
				return loadErr
			}
			return g.Writer.Primary(strings.TrimRight(b.String(), "\n"))
		},
	}
	cmd.Flags().BoolVar(&exampleOnly, "example", false, "print only the annotated template (no path/profile info)")
	return cmd
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
