// Command readur is the CLI entry point for the Readur upload client.
package main

import (
	"os"

	"github.com/sgaunet/readur-cli/internal/cli"
	cerrors "github.com/sgaunet/readur-cli/internal/errors"
)

func main() {
	err := cli.Run(os.Args[1:], os.Stdout, os.Stderr)
	os.Exit(cerrors.Classify(err))
}
