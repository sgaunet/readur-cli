package cli

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

// VersionInfo is the JSON shape emitted by `readur version --json`.
type VersionInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	Go        string `json:"go"`
	ExitCode  int    `json:"exit_code"`
}

// newVersionCommand builds the `readur version` subcommand.
func newVersionCommand(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version, commit, build date, and Go toolchain.",
		RunE: func(_ *cobra.Command, _ []string) error {
			if g.JSON {
				return g.Writer.Primary(VersionInfo{
					Version:   version,
					Commit:    commit,
					BuildDate: buildDate,
					Go:        runtime.Version(),
					ExitCode:  0,
				})
			}
			return g.Writer.Primary(fmt.Sprintf("readur %s %s %s %s",
				version, commit, buildDate, runtime.Version()))
		},
	}
}
