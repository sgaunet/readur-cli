package config_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/sgaunet/readur-cli/internal/config"
)

func newStore(t *testing.T) *config.Store {
	t.Helper()
	tmp := t.TempDir()
	p, err := config.Resolve(filepath.Join(tmp, "config.toml"))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return config.NewStore(p)
}

func validProfile() *config.Profile {
	return &config.Profile{
		Name:       "work",
		ServerURL:  "https://readur.example.com",
		Username:   "alice",
		Token:      "jwt.token.value",
		ObtainedAt: time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC),
	}
}

func TestProfile_Roundtrip(t *testing.T) {
	s := newStore(t)
	p := validProfile()

	if err := s.Save(map[string]*config.Profile{"work": p}, "work"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	profiles, def, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if def != "work" {
		t.Fatalf("default = %q, want work", def)
	}
	loaded, ok := profiles["work"]
	if !ok {
		t.Fatalf("work profile missing after reload")
	}
	if loaded.ServerURL != p.ServerURL || loaded.Username != p.Username || loaded.Token != p.Token {
		t.Fatalf("fields corrupted: %+v", loaded)
	}
	if loaded.Name != "work" {
		t.Fatalf("Name not populated on load: %q", loaded.Name)
	}
	if loaded.InsecureSkipVerify {
		t.Fatalf("InsecureSkipVerify should default false, got true")
	}
}

func TestProfile_Save_EnforcesMode0600_POSIX(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX-specific")
	}
	s := newStore(t)
	if err := s.Save(map[string]*config.Profile{"work": validProfile()}, "work"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(s.Paths.ConfigFile)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("mode = %o, want 0600", mode)
	}
}

func TestProfile_InsecureSkipVerify_Persists(t *testing.T) {
	s := newStore(t)
	p := validProfile()
	p.InsecureSkipVerify = true

	if err := s.Save(map[string]*config.Profile{"work": p}, "work"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	profiles, _, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !profiles["work"].InsecureSkipVerify {
		t.Fatalf("InsecureSkipVerify not persisted")
	}
}

func TestProfile_Validate_RejectsBadName(t *testing.T) {
	cases := []string{"", "BadName", "has space", "has/slash", "_leading", "-leading", "toolong-profile-name-that-exceeds-32chrs"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			p := validProfile()
			p.Name = name
			if err := p.Validate(); err == nil {
				t.Fatalf("expected error for name %q", name)
			}
		})
	}
}

func TestProfile_Validate_RejectsBadScheme(t *testing.T) {
	p := validProfile()
	p.ServerURL = "ftp://example.com"
	if err := p.Validate(); err == nil {
		t.Fatalf("expected error for non-http scheme")
	}
}

func TestProfile_Load_MissingFileReturnsEmpty(t *testing.T) {
	s := newStore(t)
	profiles, def, err := s.Load()
	if err != nil {
		t.Fatalf("Load on missing file: %v", err)
	}
	if len(profiles) != 0 || def != "" {
		t.Fatalf("expected empty, got %+v default=%q", profiles, def)
	}
}

func TestResolveProfile_Precedence(t *testing.T) {
	profiles := map[string]*config.Profile{
		"work":     validProfile(),
		"personal": validProfile(),
	}
	profiles["personal"].Name = "personal"

	// file default only
	p, err := config.ResolveProfile(profiles, "work", "")
	if err != nil || p.Name != "work" {
		t.Fatalf("default: %v %v", p, err)
	}

	// env overrides file default
	t.Setenv(config.EnvProfile, "personal")
	p, err = config.ResolveProfile(profiles, "work", "")
	if err != nil || p.Name != "personal" {
		t.Fatalf("env: %v %v", p, err)
	}

	// flag beats env
	p, err = config.ResolveProfile(profiles, "work", "work")
	if err != nil || p.Name != "work" {
		t.Fatalf("flag: %v %v", p, err)
	}
}

func TestResolveProfile_NoneConfigured(t *testing.T) {
	t.Setenv(config.EnvProfile, "")
	_, err := config.ResolveProfile(map[string]*config.Profile{}, "", "")
	if err == nil {
		t.Fatalf("expected error when no profile configured")
	}
}

func TestResolveProfile_UnknownName(t *testing.T) {
	t.Setenv(config.EnvProfile, "")
	_, err := config.ResolveProfile(map[string]*config.Profile{"work": validProfile()}, "", "missing")
	if err == nil {
		t.Fatalf("expected error for unknown profile")
	}
}

func TestProfile_TOMLRoundtrip_WithPassword(t *testing.T) {
	s := newStore(t)
	p := validProfile()
	p.Password = "s3cret-on-disk"

	if err := s.Save(map[string]*config.Profile{"work": p}, "work"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	profiles, _, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := profiles["work"]
	if got.Password != "s3cret-on-disk" {
		t.Fatalf("Password not persisted, got %q", got.Password)
	}

	// Sanity: absent password stays empty on reload.
	p2 := validProfile()
	p2.Name = "personal"
	if err := s.Save(map[string]*config.Profile{"work": p, "personal": p2}, "work"); err != nil {
		t.Fatalf("Save 2: %v", err)
	}
	profiles, _, err = s.Load()
	if err != nil {
		t.Fatalf("Load 2: %v", err)
	}
	if profiles["personal"].Password != "" {
		t.Fatalf("unset Password should load as empty, got %q", profiles["personal"].Password)
	}
}

func TestProfile_Save_AtomicReplace(t *testing.T) {
	s := newStore(t)

	// first write
	if err := s.Save(map[string]*config.Profile{"work": validProfile()}, "work"); err != nil {
		t.Fatalf("first save: %v", err)
	}

	// overwrite with updated token
	p := validProfile()
	p.Token = "new-token"
	if err := s.Save(map[string]*config.Profile{"work": p}, "work"); err != nil {
		t.Fatalf("second save: %v", err)
	}

	profiles, _, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if profiles["work"].Token != "new-token" {
		t.Fatalf("token not updated: %+v", profiles["work"])
	}

	// No temp files left behind.
	entries, err := os.ReadDir(s.Paths.ConfigDir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".toml" {
			continue
		}
		if e.Name()[0] == '.' && filepath.Ext(e.Name()) == ".toml" {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}
