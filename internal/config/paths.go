package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/adrg/xdg"

	cerrors "github.com/sgaunet/readur-cli/internal/errors"
)

// Env variables honored when resolving paths.
const (
	EnvConfig = "READUR_CONFIG"

	appName    = "readur-cli"
	configFile = "config.toml"
	stateDir   = "state"
)

// Paths resolves the filesystem locations used by the CLI.
type Paths struct {
	// ConfigDir is the directory holding config.toml and the state/
	// subdirectory. It is created on demand.
	ConfigDir string
	// ConfigFile is the TOML profile file path.
	ConfigFile string
	// StateDir holds per-batch resume state files.
	StateDir string
}

// Resolve returns the Paths to use, honoring an explicit override
// (typically --config or READUR_CONFIG) when non-empty. An override
// points directly at the config.toml file; its directory is taken as
// ConfigDir.
//
// When no override is supplied the function uses xdg.ConfigHome
// (which resolves correctly on Linux, macOS, and Windows).
func Resolve(override string) (Paths, error) {
	if override == "" {
		override = os.Getenv(EnvConfig)
	}

	if override != "" {
		abs, err := filepath.Abs(override)
		if err != nil {
			return Paths{}, cerrors.New(cerrors.CodeConfig,
				fmt.Sprintf("cannot resolve config path %q", override), err)
		}
		dir := filepath.Dir(abs)
		return Paths{
			ConfigDir:  dir,
			ConfigFile: abs,
			StateDir:   filepath.Join(dir, stateDir),
		}, nil
	}

	base := filepath.Join(xdg.ConfigHome, appName)
	return Paths{
		ConfigDir:  base,
		ConfigFile: filepath.Join(base, configFile),
		StateDir:   filepath.Join(base, stateDir),
	}, nil
}

// EnsureDir creates dir with 0700 permissions if it does not exist. On
// Windows the perm bits are advisory; the per-user AppData directory's
// default ACL already restricts access.
func EnsureDir(dir string) error {
	if dir == "" {
		return cerrors.New(cerrors.CodeConfig, "empty directory path", nil)
	}
	err := os.MkdirAll(dir, 0o700)
	if err != nil {
		return cerrors.New(cerrors.CodeCantCreat,
			"cannot create "+dir, err)
	}
	return nil
}
