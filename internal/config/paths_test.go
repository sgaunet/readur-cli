package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sgaunet/readur-cli/internal/config"
)

func TestResolve_NoOverrideUsesXDG(t *testing.T) {
	t.Setenv(config.EnvConfig, "")

	p, err := config.Resolve("")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if p.ConfigDir == "" || p.ConfigFile == "" || p.StateDir == "" {
		t.Fatalf("empty path in %+v", p)
	}
	if !strings.HasSuffix(p.ConfigFile, "config.toml") {
		t.Fatalf("ConfigFile = %q, want suffix config.toml", p.ConfigFile)
	}
	if filepath.Dir(p.ConfigFile) != p.ConfigDir {
		t.Fatalf("ConfigFile dir %q != ConfigDir %q", filepath.Dir(p.ConfigFile), p.ConfigDir)
	}
	if filepath.Base(p.ConfigDir) != "readur-cli" {
		t.Fatalf("ConfigDir base = %q, want readur-cli", filepath.Base(p.ConfigDir))
	}
}

func TestResolve_ExplicitOverrideWins(t *testing.T) {
	tmp := t.TempDir()
	custom := filepath.Join(tmp, "alt.toml")

	p, err := config.Resolve(custom)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if p.ConfigFile != custom {
		t.Fatalf("ConfigFile = %q, want %q", p.ConfigFile, custom)
	}
	if p.ConfigDir != tmp {
		t.Fatalf("ConfigDir = %q, want %q", p.ConfigDir, tmp)
	}
	if p.StateDir != filepath.Join(tmp, "state") {
		t.Fatalf("StateDir = %q, want %q", p.StateDir, filepath.Join(tmp, "state"))
	}
}

func TestResolve_EnvOverride(t *testing.T) {
	tmp := t.TempDir()
	custom := filepath.Join(tmp, "from-env.toml")
	t.Setenv(config.EnvConfig, custom)

	p, err := config.Resolve("")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if p.ConfigFile != custom {
		t.Fatalf("env not honored: got %q, want %q", p.ConfigFile, custom)
	}
}

func TestResolve_FlagBeatsEnv(t *testing.T) {
	tmp := t.TempDir()
	flagPath := filepath.Join(tmp, "flag.toml")
	envPath := filepath.Join(tmp, "env.toml")
	t.Setenv(config.EnvConfig, envPath)

	p, err := config.Resolve(flagPath)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if p.ConfigFile != flagPath {
		t.Fatalf("flag did not beat env: got %q", p.ConfigFile)
	}
}

func TestEnsureDir_CreatesNested(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "a", "b", "c")

	if err := config.EnsureDir(target); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("not a dir")
	}
}

func TestEnsureDir_EmptyRejected(t *testing.T) {
	if err := config.EnsureDir(""); err == nil {
		t.Fatalf("expected error for empty path")
	}
}
