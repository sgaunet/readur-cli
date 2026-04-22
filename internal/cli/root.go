package cli

import (
	stderrors "errors"
	"os"

	"github.com/spf13/cobra"

	cerrors "github.com/sgaunet/readur-cli/internal/errors"
	"github.com/sgaunet/readur-cli/internal/output"
)

// Build-time metadata. Populated via -ldflags.
var (
	version   = "dev"
	commit    = "none"
	buildDate = "unknown"
)

// Globals carries the resolved persistent-flag state after Cobra
// parses the root command. Commands read from this.
type Globals struct {
	ConfigPath         string
	ProfileFlag        string
	JSON               bool
	Quiet              bool
	Verbose            bool
	NoColor            bool
	InsecureSkipVerify bool

	Writer *output.Writer
}

// BuildRoot constructs the top-level `readur` cobra.Command and
// registers every subcommand. The returned Globals pointer is shared
// with the subcommands via closure; callers should not mutate it.
func BuildRoot() (*cobra.Command, *Globals) {
	g := &Globals{
		Writer: output.New(),
	}

	root := &cobra.Command{
		Use:           "readur",
		Short:         "Upload documents to a Readur server.",
		Long:          "readur is a command-line client for the Readur document ingestion API. It uploads one or many documents from the local filesystem, manages named server profiles, and supports human-readable and JSON output for scripted pipelines.",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			// Populate stdin/out/err from Cobra's configured streams so tests
			// can inject buffers.
			g.Writer.Stdout = cmd.OutOrStdout()
			g.Writer.Stderr = cmd.ErrOrStderr()
			return g.Writer.ApplyFlags(g.JSON, g.Quiet, g.Verbose, g.NoColor)
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			// `readur` with no subcommand: print help to stderr and exit 2.
			cmd.SetOut(cmd.ErrOrStderr())
			_ = cmd.Help()
			return usageErr("no subcommand specified")
		},
	}

	fs := root.PersistentFlags()
	fs.StringVar(&g.ConfigPath, "config", "", "path to config.toml (overrides XDG default and READUR_CONFIG)")
	fs.StringVar(&g.ProfileFlag, "profile", "", "profile name (overrides READUR_PROFILE and default_profile)")
	fs.BoolVar(&g.JSON, "json", false, "emit machine-readable JSON on stdout")
	fs.BoolVar(&g.Quiet, "quiet", false, "suppress non-essential stderr output")
	fs.BoolVar(&g.Verbose, "verbose", false, "emit debug-level diagnostics on stderr")
	fs.BoolVar(&g.NoColor, "no-color", false, "disable ANSI color in human output")
	fs.BoolVar(&g.InsecureSkipVerify, "insecure-skip-tls-verify", false, "disable server TLS certificate verification (warning printed per request)")

	root.AddCommand(newVersionCommand(g))
	root.AddCommand(newUploadCommand(g))
	root.AddCommand(newConfigCommand(g))
	root.AddCommand(newLoginCommand(g))
	root.AddCommand(newLabelsCommand(g))

	return root, g
}

// Run is the single entry point invoked from main. It executes the
// cobra command tree, renders any error through the configured Writer
// (respecting --json), and returns the final error. Mapping error →
// exit code is the caller's responsibility (main.go).
func Run(args []string, stdout, stderr *os.File) error {
	root, g := BuildRoot()
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	err := root.Execute()
	if err != nil {
		err = promoteParseErrors(err)
		g.Writer.RenderError(err)
	}
	return err
}

// promoteParseErrors wraps cobra/pflag argv parsing errors as CodeUsage
// so Classify returns exit code 2. Any already-typed CLIError passes
// through unchanged.
func promoteParseErrors(err error) error {
	var cli *cerrors.CLIError
	if stderrors.As(err, &cli) {
		return err
	}
	// Cobra uses these sentinel error types for argv parsing failures.
	// We detect by message prefix, which is stable in published versions.
	msg := err.Error()
	parseMarkers := []string{
		"unknown command",
		"unknown flag",
		"unknown shorthand flag",
		"flag needs an argument",
		"invalid argument",
		"required flag",
		"requires at least",
		"accepts at most",
		"accepts ",
	}
	for _, m := range parseMarkers {
		if startsWith(msg, m) {
			return cerrors.New(cerrors.CodeUsage, msg, err)
		}
	}
	return err
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
