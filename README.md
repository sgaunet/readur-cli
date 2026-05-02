[![GitHub release](https://img.shields.io/github/release/sgaunet/readur-cli.svg)](https://github.com/sgaunet/readur-cli/releases/latest)
![GitHub Downloads](https://img.shields.io/github/downloads/sgaunet/readur-cli/total)
[![Go Report Card](https://goreportcard.com/badge/github.com/sgaunet/readur-cli)](https://goreportcard.com/report/github.com/sgaunet/readur-cli)
![Test Coverage](https://raw.githubusercontent.com/wiki/sgaunet/readur-cli/coverage-badge.svg)
[![linter](https://github.com/sgaunet/readur-cli/actions/workflows/linter.yml/badge.svg)](https://github.com/sgaunet/readur-cli/actions/workflows/linter.yml)
[![coverage](https://github.com/sgaunet/readur-cli/actions/workflows/coverage.yml/badge.svg)](https://github.com/sgaunet/readur-cli/actions/workflows/coverage.yml)
[![Snapshot Build](https://github.com/sgaunet/readur-cli/actions/workflows/snapshot.yml/badge.svg)](https://github.com/sgaunet/readur-cli/actions/workflows/snapshot.yml)
[![Release Build](https://github.com/sgaunet/readur-cli/actions/workflows/release.yml/badge.svg)](https://github.com/sgaunet/readur-cli/actions/workflows/release.yml)
[![GoDoc](https://godoc.org/github.com/sgaunet/readur-cli?status.svg)](https://godoc.org/github.com/sgaunet/readur-cli)
[![License](https://img.shields.io/github/license/sgaunet/readur-cli.svg)](LICENSE)

# readur-cli

`readur-cli` is a command-line client for the [Readur](https://github.com/readur) document ingestion server. Log in once, then upload single files or whole directories from the shell — the binary handles authentication, streaming multipart upload, bounded retry with jittered backoff, and dual human/JSON output for scripted pipelines.

## Highlights

- **Profile-based config**: Multiple Readur servers in one TOML file, switchable via `--profile` or `READUR_PROFILE`. Stored at the XDG config location with `0600` permissions.
- **No password in argv**: Passwords are read from a TTY prompt (echo-off) or `--password-stdin` only — never from a flag, environment variable, or shell history.
- **Silent token refresh**: Opt-in `--save-password` lets the client refresh expired tokens proactively (within 60 s of expiry) and reactively (on a single 401) without re-prompting.
- **Bounded retries**: 1 initial + 3 retries with 1 s → 10 s exponential backoff, ±25% jitter, and `Retry-After` honored on 429/503. The whole budget caps at ~24 s of waiting per request.
- **Strict TLS by default**: `--insecure-skip-tls-verify` (or the per-profile equivalent) is supported but emits a stderr warning on every request while active.
- **Dual output**: Every command honors `--json` for machine-readable output. Stdout carries data, stderr carries diagnostics — pipelines stay clean.
- **Stable exit codes**: 9 distinct codes (`OK`, `USAGE`, `AUTH`, `NETWORK`, `NOINPUT`, `CANTCREAT`, `CONFIG`, `PARTIAL`, `GENERIC`) pinned by contract tests.

## Quick Start

```bash
# 1. Authenticate against your Readur server (interactive prompt)
readur login --server https://readur.example.com --username alice

# 2. Upload a document
readur upload ~/Documents/invoice.pdf

# 3. Attach labels at upload time
readur upload contract.pdf --label legal --label q2-2026

# 4. List labels defined on the server
readur labels list
```

## Commands

```
readur login        Authenticate to a Readur server and save the token
readur upload       Upload a single document
readur labels list  List labels known to the server
readur config show  Print the expected config shape and current profiles
readur config path  Print the resolved config file path
readur version      Print version, commit, build date, and Go toolchain
readur completion   Generate shell completion scripts (bash, zsh, fish, powershell)
```

### Global flags

| Flag                          | Effect                                                       |
|-------------------------------|--------------------------------------------------------------|
| `--profile <name>`            | Select a profile (overrides `READUR_PROFILE` and the default).|
| `--config <path>`             | Use a specific config.toml path (overrides XDG and `READUR_CONFIG`).|
| `--json`                      | Emit machine-readable JSON on stdout.                        |
| `--quiet`                     | Suppress non-essential stderr diagnostics.                   |
| `--verbose`                   | Emit debug-level diagnostics on stderr.                      |
| `--no-color`                  | Disable ANSI color in human output.                          |
| `--insecure-skip-tls-verify`  | Disable TLS verification (per-request stderr warning).       |

## Examples

```bash
# Authenticate, reading the password from stdin (CI-friendly)
$ printf '%s' "$READUR_PASS" | readur login \
    --server https://readur.example.com \
    --username alice \
    --password-stdin
logged in to https://readur.example.com as alice

# Persist the password so future commands silently refresh expired tokens
$ printf '%s' "$READUR_PASS" | readur login \
    --server https://readur.example.com \
    --username alice \
    --password-stdin \
    --save-password

# Upload with title, OCR enabled, and language hint
$ readur upload scan.pdf --title "Q2 invoice" --ocr --language fra
uploaded scan.pdf as 0d8b3c5a-... (2 MB in 1.4 s)

# Upload and attach labels in a single command
$ readur upload contract.pdf --label legal --label q2-2026
uploaded contract.pdf as 9f1c... (labels: legal, q2-2026)

# Machine-readable upload output for scripts
$ readur upload report.pdf --json | jq .
{
  "document_id": "9f1c...",
  "path": "report.pdf",
  "size": 412034,
  "duration_ms": 850,
  "exit_code": 0
}

# List labels sorted by document count, JSON output
$ readur labels list --json --sort count | jq '.labels[0:3]'
[
  { "id": "...", "name": "invoices",  "document_count": 412, ... },
  { "id": "...", "name": "contracts", "document_count": 188, ... },
  { "id": "...", "name": "scans",     "document_count":  53, ... }
]

# Inspect where the config lives and what profiles are stored
$ readur config show

# Switch profiles transparently — works for any subcommand
$ readur --profile staging upload doc.pdf
$ READUR_PROFILE=staging readur labels list

# Verbose mode prints exit-code reason on the final stderr line
$ readur upload missing.pdf --verbose 2>&1 | tail -1
exit=66 reason=NOINPUT
```

## Configuration

The config file is a small TOML document with one section per profile:

```toml
default_profile = "prod"

[profiles.prod]
server_url    = "https://readur.example.com"
username      = "alice"
token         = "..."           # set by `readur login`
token_expiry  = "2026-05-20T12:00:00Z"
# password    = "..."           # only present when --save-password was used
# insecure_skip_verify = false  # opt-in TLS bypass for self-signed dev servers

[profiles.staging]
server_url = "https://readur.staging.example.com"
username   = "alice"
token      = "..."
```

Resolution order:

1. `--config <path>` flag (highest priority)
2. `READUR_CONFIG` env var
3. `$XDG_CONFIG_HOME/readur-cli/config.toml` (default)

Profile selection: `--profile` flag → `READUR_PROFILE` env var → `default_profile` key.

## Exit Codes

Stable, scripted-friendly exit codes (mirrored in JSON output as `exit_code`):

| Code | Constant     | Cause                                                          |
|------|--------------|----------------------------------------------------------------|
| 0    | `OK`         | Success.                                                       |
| 1    | `GENERIC`    | Unexpected runtime error.                                      |
| 2    | `USAGE`      | Bad argv: unknown subcommand, mutex flags, missing required.   |
| 3    | `AUTH`       | Server 401/403, missing or expired token, login rejected.      |
| 4    | `NETWORK`    | All retries exhausted (5xx, dial errors, TLS handshake, EOF).  |
| 5    | `PARTIAL`    | Bulk strict mode: at least one file failed.                    |
| 66   | `NOINPUT`    | Input file or directory does not exist or is not readable.     |
| 73   | `CANTCREAT`  | Cannot create or write config / state file.                    |
| 78   | `CONFIG`     | Config file malformed, or required profile missing.            |

## Install

### Option 1: Download Release

* Download the latest release from [GitHub Releases](https://github.com/sgaunet/readur-cli/releases/latest)
* Install the binary in `/usr/local/bin` or your preferred location

### Option 2: Homebrew

```bash
brew tap sgaunet/homebrew-tools
brew install sgaunet/tools/readur-cli
```

### Option 3: Go Install

```bash
go install github.com/sgaunet/readur-cli/cmd/readur@latest
```

### Option 4: Build from source

```bash
git clone https://github.com/sgaunet/readur-cli.git
cd readur-cli
task build      # → bin/readur
```

## Shell Completion

`readur-cli` supports shell completion for bash, zsh, fish, and powershell.

### Bash

```bash
source <(readur completion bash)

# To persist, add to ~/.bashrc or install system-wide:
readur completion bash > /etc/bash_completion.d/readur
```

### Zsh

```bash
source <(readur completion zsh)

# To persist:
readur completion zsh > "${fpath[1]}/_readur"
```

### Fish

```bash
readur completion fish | source

# To persist:
readur completion fish > ~/.config/fish/completions/readur.fish
```

### Homebrew

If installed via Homebrew, completions are automatically installed for bash, zsh, and fish.

## Development

```bash
task              # default: lint + test + build
task build        # → bin/readur with version/commit/date ldflags
task test         # go test -race -timeout 120s ./...
task lint         # golangci-lint run ./...
task cover        # race test with coverage profile
task fmt          # gofmt + goimports -local github.com/sgaunet/readur-cli
task cross        # goreleaser snapshot across linux/darwin/windows × amd64/arm64
```

## License

[MIT](LICENSE)
