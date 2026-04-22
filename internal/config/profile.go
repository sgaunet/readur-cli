package config

import (
	"fmt"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	cerrors "github.com/sgaunet/readur-cli/internal/errors"
)

// EnvProfile names the profile to use, overriding the file-level
// default_profile. Precedence: --profile flag > READUR_PROFILE env
// var > file default_profile.
const EnvProfile = "READUR_PROFILE"

// profileNameRe enforces the data-model.md Profile.name rule.
var profileNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// Profile is a named, per-user record of how to reach one Readur server.
// Field names match the TOML keys exactly.
type Profile struct {
	Name               string    `toml:"-"`
	ServerURL          string    `toml:"server_url"`
	Username           string    `toml:"username"`
	Token              string    `toml:"token"`
	TokenExpiry        time.Time `toml:"token_expiry,omitempty"`
	ObtainedAt         time.Time `toml:"obtained_at"`
	InsecureSkipVerify bool      `toml:"insecure_skip_verify,omitempty"`
}

// Validate checks required fields. Called before save.
func (p *Profile) Validate() error {
	if p == nil {
		return cerrors.New(cerrors.CodeConfig, "nil profile", nil)
	}
	if !profileNameRe.MatchString(p.Name) {
		return cerrors.New(cerrors.CodeConfig,
			fmt.Sprintf("invalid profile name %q (must match %s)", p.Name, profileNameRe.String()), nil)
	}
	if p.ServerURL == "" {
		return cerrors.New(cerrors.CodeConfig, "server_url is required", nil)
	}
	if !strings.HasPrefix(p.ServerURL, "http://") && !strings.HasPrefix(p.ServerURL, "https://") {
		return cerrors.New(cerrors.CodeConfig,
			fmt.Sprintf("server_url must start with http:// or https://, got %q", p.ServerURL), nil)
	}
	if p.Username == "" {
		return cerrors.New(cerrors.CodeConfig, "username is required", nil)
	}
	if p.Token == "" {
		return cerrors.New(cerrors.CodeConfig, "token is required", nil)
	}
	return nil
}

// file is the on-disk config.toml layout.
type file struct {
	DefaultProfile string              `toml:"default_profile,omitempty"`
	Profiles       map[string]*Profile `toml:"profiles"`
}

// Store is the read/write handle for the config file. It is cheap to
// construct and safe to use from a single goroutine.
type Store struct {
	Paths Paths
}

// NewStore builds a Store from a resolved Paths.
func NewStore(p Paths) *Store { return &Store{Paths: p} }

// Load reads config.toml and returns all profiles along with the
// file-level default_profile name (possibly empty if none is set).
// If the file does not exist, Load returns an empty result with no
// error — a fresh install has no profiles yet.
func (s *Store) Load() (profiles map[string]*Profile, defaultProfile string, err error) {
	data, rerr := os.ReadFile(s.Paths.ConfigFile)
	if rerr != nil {
		if os.IsNotExist(rerr) {
			return map[string]*Profile{}, "", nil
		}
		return nil, "", cerrors.New(cerrors.CodeConfig,
			fmt.Sprintf("cannot read %s", s.Paths.ConfigFile), rerr)
	}

	var f file
	if _, derr := toml.Decode(string(data), &f); derr != nil {
		return nil, "", cerrors.New(cerrors.CodeConfig,
			fmt.Sprintf("cannot parse %s", s.Paths.ConfigFile), derr)
	}
	if f.Profiles == nil {
		f.Profiles = map[string]*Profile{}
	}
	for name, p := range f.Profiles {
		p.Name = name
	}
	return f.Profiles, f.DefaultProfile, nil
}

// Save atomically writes profiles + defaultProfile back to disk with
// mode 0600 on POSIX. Atomicity is via tempfile + rename in the same
// directory.
func (s *Store) Save(profiles map[string]*Profile, defaultProfile string) error {
	if err := EnsureDir(s.Paths.ConfigDir); err != nil {
		return err
	}

	// Validate every profile before touching the filesystem.
	names := make([]string, 0, len(profiles))
	for name, p := range profiles {
		p.Name = name
		if err := p.Validate(); err != nil {
			return err
		}
		names = append(names, name)
	}
	sort.Strings(names)

	if defaultProfile != "" {
		if _, ok := profiles[defaultProfile]; !ok {
			return cerrors.New(cerrors.CodeConfig,
				fmt.Sprintf("default_profile %q does not exist", defaultProfile), nil)
		}
	}

	f := file{
		DefaultProfile: defaultProfile,
		Profiles:       profiles,
	}

	tmp, err := os.CreateTemp(s.Paths.ConfigDir, ".config-*.toml")
	if err != nil {
		return cerrors.New(cerrors.CodeCantCreat,
			fmt.Sprintf("cannot create temp file in %s", s.Paths.ConfigDir), err)
	}
	tmpName := tmp.Name()
	// Ensure cleanup on failure paths.
	defer func() { _ = os.Remove(tmpName) }()

	if runtime.GOOS != "windows" {
		if err := tmp.Chmod(0o600); err != nil {
			_ = tmp.Close()
			return cerrors.New(cerrors.CodeCantCreat, "cannot chmod temp file", err)
		}
	}

	enc := toml.NewEncoder(tmp)
	if err := enc.Encode(f); err != nil {
		_ = tmp.Close()
		return cerrors.New(cerrors.CodeCantCreat, "cannot encode config", err)
	}
	if err := tmp.Close(); err != nil {
		return cerrors.New(cerrors.CodeCantCreat, "cannot close temp file", err)
	}

	if err := os.Rename(tmpName, s.Paths.ConfigFile); err != nil {
		return cerrors.New(cerrors.CodeCantCreat,
			fmt.Sprintf("cannot rename into place %s", s.Paths.ConfigFile), err)
	}
	return nil
}

// ResolveProfile selects the active profile from a loaded map following
// the precedence flag > env > defaultProfile. Returns CodeConfig if no
// profile can be resolved.
func ResolveProfile(profiles map[string]*Profile, defaultProfile, flagProfile string) (*Profile, error) {
	name := flagProfile
	if name == "" {
		name = os.Getenv(EnvProfile)
	}
	if name == "" {
		name = defaultProfile
	}
	if name == "" {
		return nil, cerrors.New(cerrors.CodeConfig,
			"no profile configured (run `readur login`)", nil)
	}
	p, ok := profiles[name]
	if !ok {
		return nil, cerrors.New(cerrors.CodeConfig,
			fmt.Sprintf("profile %q not found", name), nil)
	}
	return p, nil
}

