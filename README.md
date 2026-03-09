# codex-auth

`codex-auth` is a standalone Go TUI for switching between multiple local Codex authentication snapshots in `~/.codex`.

## What It Does

- Discovers managed accounts from `~/.codex/accounts/*.json`
- Detects the active `~/.codex/auth.json`
- Auto-saves the active account if it is not already managed
- Switches accounts from a single interactive screen
- Renames accounts inline
- Deletes managed accounts inline
- Shows quota immediately from local state and refreshes live data in the background

## Install

### Homebrew

Preferred install path, once you publish your tap:

```bash
brew install Alexs7zzh/homebrew-tap/codex-auth
```

Or:

```bash
brew tap Alexs7zzh/homebrew-tap
brew install codex-auth
```

### GitHub Release Download

Every tagged release uploads macOS tarballs to GitHub Releases:

- `codex-auth_v0.1.0_darwin_arm64.tar.gz`
- `codex-auth_v0.1.0_darwin_amd64.tar.gz`

You can unpack one manually and move `codex-auth` somewhere on your `PATH`, for example:

```bash
tar -xzf codex-auth_v0.1.0_darwin_arm64.tar.gz
mv codex-auth_v0.1.0_darwin_arm64/codex-auth ~/.local/bin/codex-auth
```

### Build From Source

```bash
go build -o codex-auth ./cmd/codex-auth
```

## Usage

```bash
codex-auth
```

## Keys

- `Up/Down` or `j/k`: move
- `Space`: switch
- `Enter`: confirm switch, rename, delete, or exit
- `e`: rename
- `d`: delete selected managed account
- `Esc` or `q`: close

## Release Flow

Tagging the repository with `v*` triggers [.github/workflows/release.yml](.github/workflows/release.yml), which:

- runs `go test ./...`
- builds macOS `arm64` and `amd64` tarballs
- injects the tag into `main.version`
- generates `checksums.txt`
- renders a Homebrew formula file for the release
- publishes a GitHub Release with those assets attached

## Homebrew Tap Setup

This repository ships the binary release assets. The Homebrew formula should live in a separate tap repository, typically named `homebrew-tap`.

Recommended setup:

```bash
brew tap-new Alexs7zzh/homebrew-tap
```

Then push that tap repository to GitHub and add a formula under `Formula/codex-auth.rb`.

The template for that formula lives at [packaging/homebrew/codex-auth.rb.tmpl](packaging/homebrew/codex-auth.rb.tmpl). Each tagged release will also attach a rendered `codex-auth.rb` file to the GitHub Release so you can copy it directly into the tap repository.
