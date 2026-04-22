package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/sgaunet/readur-cli/internal/client"
	"github.com/sgaunet/readur-cli/internal/config"
	cerrors "github.com/sgaunet/readur-cli/internal/errors"
)

// LoginJSON is the JSON shape emitted by `readur login --json` on
// success. Matches contracts/json-output.md §login.
type LoginJSON struct {
	Profile        string `json:"profile"`
	ServerURL      string `json:"server_url"`
	Username       string `json:"username"`
	TokenExpiresAt string `json:"token_expires_at,omitempty"`
	ExitCode       int    `json:"exit_code"`
}

func newLoginCommand(g *Globals) *cobra.Command {
	var (
		serverFlag   string
		usernameFlag string
		passwordStdin bool
		profileName   string
	)
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate to a Readur server and save the token.",
		Long: `Obtain a bearer token from the Readur server and save it to the local
config file.

The password is read from standard input when --password-stdin is set,
or prompted for (with echo disabled) when the CLI is attached to a
terminal. There is deliberately no --password flag — passwords must
never appear in argv, shell history, or environment variables.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Determine the profile name we're creating/updating. Precedence:
			// login's local --profile flag (if set) > global --profile > env > "default".
			name := profileName
			if name == "" {
				name = g.ProfileFlag
			}
			if name == "" {
				name = os.Getenv(config.EnvProfile)
			}
			if name == "" {
				name = "default"
			}

			// Resolve server URL: flag > existing profile's server_url.
			paths, err := config.Resolve(g.ConfigPath)
			if err != nil {
				return err
			}
			store := config.NewStore(paths)
			profiles, defaultProfile, err := store.Load()
			if err != nil {
				return err
			}

			serverURL := strings.TrimRight(serverFlag, "/")
			if serverURL == "" {
				if existing, ok := profiles[name]; ok {
					serverURL = existing.ServerURL
				}
			}
			if serverURL == "" {
				return usageErr("--server is required (no existing profile to inherit from)")
			}

			// Resolve username: flag > existing profile's username > prompt.
			username := strings.TrimSpace(usernameFlag)
			if username == "" {
				if existing, ok := profiles[name]; ok {
					username = existing.Username
				}
			}
			if username == "" {
				u, err := promptLine(g.Writer.Stderr, cmd.InOrStdin(), "Username: ")
				if err != nil {
					return err
				}
				username = strings.TrimSpace(u)
			}
			if username == "" {
				return usageErr("username is required")
			}

			// Read password from stdin or the terminal.
			password, err := readPassword(cmd.InOrStdin(), g.Writer.Stderr, passwordStdin)
			if err != nil {
				return err
			}
			if password == "" {
				return usageErr("password is required")
			}

			// Construct a temporary HTTP client (no token yet).
			httpClient := client.NewClient(client.Options{
				ServerURL:          serverURL,
				InsecureSkipVerify: g.InsecureSkipVerify,
				WarnOut:            g.Writer.Stderr,
				ProfileName:        name,
			})

			res, loginErr := httpClient.Login(cmd.Context(), client.LoginRequest{
				Username: username,
				Password: password,
			})
			// Scrub the in-memory password aggressively.
			password = ""
			if loginErr != nil {
				return loginErr
			}

			// Build or update the profile.
			if profiles == nil {
				profiles = map[string]*config.Profile{}
			}
			existing := profiles[name]
			if existing == nil {
				existing = &config.Profile{
					Name: name,
				}
				profiles[name] = existing
			}
			existing.Name = name
			existing.ServerURL = serverURL
			existing.Username = res.Username
			existing.Token = res.Token
			existing.ObtainedAt = time.Now().UTC()
			if !res.ExpiresAt.IsZero() {
				existing.TokenExpiry = res.ExpiresAt
			}
			// --insecure-skip-tls-verify on the login invocation persists
			// into the profile so subsequent non-login commands keep the
			// same posture without the user repeating the flag.
			if g.InsecureSkipVerify {
				existing.InsecureSkipVerify = true
			}

			// Set default_profile if none was set (first login).
			if defaultProfile == "" {
				defaultProfile = name
			}

			if err := store.Save(profiles, defaultProfile); err != nil {
				return err
			}

			if g.JSON {
				out := LoginJSON{
					Profile:   name,
					ServerURL: serverURL,
					Username:  res.Username,
					ExitCode:  0,
				}
				if !res.ExpiresAt.IsZero() {
					out.TokenExpiresAt = res.ExpiresAt.UTC().Format(time.RFC3339)
				}
				return g.Writer.Primary(out)
			}
			return g.Writer.Primary(fmt.Sprintf("logged in as %s to %s (profile=%s)",
				res.Username, serverURL, name))
		},
	}

	cmd.Flags().StringVar(&serverFlag, "server", "", "Readur server URL (required if the profile does not already exist)")
	cmd.Flags().StringVar(&usernameFlag, "username", "", "username (prompted if omitted)")
	cmd.Flags().BoolVar(&passwordStdin, "password-stdin", false, "read password from stdin (exactly one line, no echo)")
	cmd.Flags().StringVar(&profileName, "profile", "", "profile name to create/update (defaults to the active profile or \"default\")")
	return cmd
}

// promptLine writes a prompt to out and reads a single line from in,
// trimming the trailing newline. Safe for non-TTY input.
func promptLine(out io.Writer, in io.Reader, prompt string) (string, error) {
	_, _ = fmt.Fprint(out, prompt)
	br := bufio.NewReader(in)
	line, err := br.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// readPassword gets the password from stdin (if passwordStdin is true)
// or from the controlling terminal with echo disabled. Returns a
// USAGE error if neither channel is available.
func readPassword(in io.Reader, errw io.Writer, passwordStdin bool) (string, error) {
	if passwordStdin {
		br := bufio.NewReader(in)
		line, err := br.ReadString('\n')
		if err != nil && err != io.EOF {
			return "", cerrors.New(cerrors.CodeUsage,
				"failed to read password from stdin", err)
		}
		return strings.TrimRight(line, "\r\n"), nil
	}

	// No --password-stdin. Require a terminal so the password isn't
	// captured in argv, env, or pipe buffers.
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", cerrors.New(cerrors.CodeUsage,
			"stdin is not a terminal; re-run with --password-stdin and pipe the password", nil)
	}
	_, _ = fmt.Fprint(errw, "Password: ")
	raw, err := term.ReadPassword(fd)
	_, _ = fmt.Fprintln(errw) // newline after the silent read
	if err != nil {
		return "", cerrors.New(cerrors.CodeUsage,
			"failed to read password from terminal", err)
	}
	return string(raw), nil
}
