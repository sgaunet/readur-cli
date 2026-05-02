package cli

import cerrors "github.com/sgaunet/readur-cli/internal/errors"

// usageErr wraps a CodeUsage CLIError. Commands use it for argv/flag
// violations detected after cobra has parsed successfully but the
// semantic interpretation fails.
func usageErr(msg string) error {
	return cerrors.New(cerrors.CodeUsage, msg, nil)
}
