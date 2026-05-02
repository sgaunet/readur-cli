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
	PasswordSaved  bool   `json:"password_saved"`
	ExitCode       int    `json:"exit_code"`
}

// loginArgs bundles the flag values captured by newLoginCommand and
// passed down to runLogin. Keeping them in a struct avoids a long
// parameter list across the extracted helpers.
type loginArgs struct {
	serverFlag     string
	usernameFlag   string
	passwordStdin  bool
	profileName    string
	savePassword   bool
	forgetPassword bool
}

func newLoginCommand(g *Globals) *cobra.Command {
	var la loginArgs
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate to a Readur server and save the token.",
		Long: `Obtain a bearer token from the Readur server and save it to the local
config file.

The password is read from standard input when --password-stdin is set,
or prompted for (with echo disabled) when the CLI is attached to a
terminal. There is deliberately no --password flag — passwords must
never appear in argv, shell history, or environment variables.

Use --save-password to persist the supplied password inside the
profile so later commands can silently refresh an expired token
without a re-prompt; --forget-password clears any previously saved
password. The two flags are mutually exclusive.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runLogin(cmd, g, la)
		},
	}

	cmd.Flags().StringVar(&la.serverFlag, "server", "",
		"Readur server URL (required if the profile does not already exist)")
	cmd.Flags().StringVar(&la.usernameFlag, "username", "", "username (prompted if omitted)")
	cmd.Flags().BoolVar(&la.passwordStdin, "password-stdin", false, "read password from stdin (exactly one line, no echo)")
	cmd.Flags().StringVar(&la.profileName, "profile", "",
		"profile name to create/update (defaults to the active profile or \"default\")")
	cmd.Flags().BoolVar(&la.savePassword, "save-password", false,
		"persist the password in the profile for silent token refresh (excludes --forget-password)")
	cmd.Flags().BoolVar(&la.forgetPassword, "forget-password", false,
		"clear any saved password from the profile (excludes --save-password)")
	return cmd
}

// runLogin is the implementation of the login subcommand, extracted
// from the cobra RunE closure to reduce cognitive complexity.
func runLogin(cmd *cobra.Command, g *Globals, la loginArgs) error {
	if la.savePassword && la.forgetPassword {
		return usageErr("--save-password and --forget-password are mutually exclusive")
	}

	name := resolveLoginProfileName(la.profileName, g.ProfileFlag)

	store, profiles, defaultProfile, err := loadLoginStore(g.ConfigPath)
	if err != nil {
		return err
	}

	serverURL, err := resolveServerURL(la.serverFlag, name, profiles)
	if err != nil {
		return err
	}

	username, err := resolveUsername(la.usernameFlag, name, profiles, g, cmd)
	if err != nil {
		return err
	}

	password, err := readRequiredPassword(cmd.InOrStdin(), g.Writer.Stderr, la.passwordStdin)
	if err != nil {
		return err
	}

	httpClient := client.NewClient(client.Options{
		ServerURL:          serverURL,
		InsecureSkipVerify: g.InsecureSkipVerify,
		WarnOut:            g.Writer.Stderr,
		ProfileName:        name,
	})

	res, err := httpClient.Login(cmd.Context(), client.LoginRequest{
		Username: username,
		Password: password,
	})
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}

	existing := upsertProfile(profiles, name, serverURL, g.InsecureSkipVerify, res)
	applyPasswordFlag(existing, password, la.savePassword, la.forgetPassword)

	if defaultProfile == "" {
		defaultProfile = name
	}
	err = store.Save(profiles, defaultProfile)
	if err != nil {
		return fmt.Errorf("save profile: %w", err)
	}

	return emitLoginOutput(g, name, serverURL, la.savePassword, la.forgetPassword, existing, res)
}

// readRequiredPassword delegates to readPassword and additionally
// returns a usage error when the result is empty, keeping runLogin
// within the cyclomatic complexity budget.
func readRequiredPassword(in io.Reader, errw io.Writer, passwordStdin bool) (string, error) {
	password, err := readPassword(in, errw, passwordStdin)
	if err != nil {
		return "", err
	}
	if password == "" {
		return "", usageErr("password is required")
	}
	return password, nil
}

// resolveLoginProfileName picks the profile name from the local flag,
// global flag, environment, or falls back to "default".
func resolveLoginProfileName(localFlag, globalFlag string) string {
	if localFlag != "" {
		return localFlag
	}
	if globalFlag != "" {
		return globalFlag
	}
	if env := os.Getenv(config.EnvProfile); env != "" {
		return env
	}
	return "default"
}

// loadLoginStore resolves config paths, loads the store, and returns
// the store, profile map, and file-level default profile name.
func loadLoginStore(configPath string) (*config.Store, map[string]*config.Profile, string, error) {
	paths, err := config.Resolve(configPath)
	if err != nil {
		return nil, nil, "", fmt.Errorf("resolve config paths: %w", err)
	}
	store := config.NewStore(paths)
	profiles, defaultProfile, err := store.Load()
	if err != nil {
		return nil, nil, "", fmt.Errorf("load profiles: %w", err)
	}
	return store, profiles, defaultProfile, nil
}

// resolveServerURL returns the server URL from the flag or the existing
// profile. Returns a usage error when neither source supplies a value.
func resolveServerURL(serverFlag, name string, profiles map[string]*config.Profile) (string, error) {
	serverURL := strings.TrimRight(serverFlag, "/")
	if serverURL == "" {
		if existing, ok := profiles[name]; ok {
			serverURL = existing.ServerURL
		}
	}
	if serverURL == "" {
		return "", usageErr("--server is required (no existing profile to inherit from)")
	}
	return serverURL, nil
}

// resolveUsername returns the username from the flag, existing profile,
// or an interactive prompt (in that priority order).
func resolveUsername(
	usernameFlag, name string,
	profiles map[string]*config.Profile,
	g *Globals,
	cmd *cobra.Command,
) (string, error) {
	username := strings.TrimSpace(usernameFlag)
	if username == "" {
		if existing, ok := profiles[name]; ok {
			username = existing.Username
		}
	}
	if username == "" {
		u, err := promptLine(g.Writer.Stderr, cmd.InOrStdin(), "Username: ")
		if err != nil {
			return "", err
		}
		username = strings.TrimSpace(u)
	}
	if username == "" {
		return "", usageErr("username is required")
	}
	return username, nil
}

// upsertProfile creates or updates the named profile in the map with
// the values returned from a successful Login call and returns a pointer
// to the profile entry (which is also stored in the map).
func upsertProfile(
	profiles map[string]*config.Profile,
	name, serverURL string,
	insecure bool,
	res *client.LoginResult,
) *config.Profile {
	if profiles == nil {
		profiles = map[string]*config.Profile{}
	}
	existing := profiles[name]
	if existing == nil {
		existing = &config.Profile{Name: name}
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
	// --insecure-skip-tls-verify on the login invocation persists into
	// the profile so later commands keep the same TLS posture.
	if insecure {
		existing.InsecureSkipVerify = true
	}
	return existing
}

// emitLoginOutput writes the login success result to the writer in
// either JSON or human mode.
func emitLoginOutput(
	g *Globals,
	name, serverURL string,
	savePassword, forgetPassword bool,
	existing *config.Profile,
	res *client.LoginResult,
) error {
	if g.JSON {
		out := LoginJSON{
			Profile:       name,
			ServerURL:     serverURL,
			Username:      res.Username,
			PasswordSaved: existing.Password != "",
			ExitCode:      0,
		}
		if !res.ExpiresAt.IsZero() {
			out.TokenExpiresAt = res.ExpiresAt.UTC().Format(time.RFC3339)
		}
		return g.Writer.Primary(out)
	}
	msg := fmt.Sprintf("logged in as %s to %s (profile=%s)%s",
		res.Username, serverURL, name,
		passwordFlagSuffix(savePassword, forgetPassword))
	return g.Writer.Primary(msg)
}

// applyPasswordFlag updates p's Password according to --save-password /
// --forget-password semantics. If neither flag is set the existing
// Password is left untouched so users who re-login without the flags
// do not surprise themselves by losing their saved credential.
func applyPasswordFlag(p *config.Profile, plaintext string, save, forget bool) {
	switch {
	case save:
		p.Password = plaintext
	case forget:
		p.Password = ""
	}
}

// passwordFlagSuffix returns the human-readable " — password saved/
// forgotten" fragment appended to the login success line when either
// flag was provided.
func passwordFlagSuffix(save, forget bool) string {
	switch {
	case save:
		return " — password saved"
	case forget:
		return " — password forgotten"
	}
	return ""
}

// promptLine writes a prompt to out and reads a single line from in,
// trimming the trailing newline. Safe for non-TTY input.
func promptLine(out io.Writer, in io.Reader, prompt string) (string, error) {
	_, _ = fmt.Fprint(out, prompt)
	br := bufio.NewReader(in)
	line, err := br.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("read line: %w", err)
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
	fd := int(os.Stdin.Fd()) // #nosec G115 — terminal fd is always within int range on every supported platform
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
