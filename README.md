# codex-auth

`codex-auth` is a standalone Go TUI for switching between multiple local Codex authentication snapshots in `~/.codex`.

## Features

- Discovers saved accounts from `~/.codex/accounts/*.json`
- Detects the active unmanaged `auth.json` and shows it as an unsaved row
- Saves or renames accounts inline with `e` or `i`
- Deletes saved accounts inline with `d`
- Switches accounts with `Space` plus `Enter`
- Loads cached quota immediately and attempts best-effort background refresh

## Run locally

```bash
go run ./cmd/codex-auth
```

## Keys

- `Up/Down` or `j/k`: move
- `Space`: mark switch target
- `Enter`: confirm switch, save, delete, or exit
- `e` or `i`: edit/save account name
- `d`: delete selected saved account
- `Esc` or `q`: close

## Release

Tagging the repository with `v*` triggers `.github/workflows/release.yml`, which:

- runs `go test ./...`
- builds macOS `arm64` and `amd64` archives
- generates `checksums.txt`
- publishes a GitHub Release

`packaging/homebrew/codex-auth.rb.tmpl` is a separate-tap formula template with repo and SHA placeholders.
