# codex-auth agent notes

## Purpose

`codex-auth` is a standalone Go TUI for managing local Codex auth profiles stored under `~/.codex`.

The app is intentionally:

- single-binary
- Homebrew-installable
- terminal-theme-aware
- focused on one interactive entrypoint instead of subcommands

## Local Development

Prefer local builds for development work instead of the Homebrew-installed binary.

Useful commands:

```bash
go test ./...
go build ./cmd/codex-auth
go run ./cmd/codex-auth
```

Homebrew should be treated as a release verification path, not the primary development workflow.

## Key Behavior Constraints

- The UI should respect the terminal theme. Avoid introducing a hardcoded custom color palette.
- Keep row layout stable while quota refresh is happening.
- Show last-known quota immediately when available, then refresh live data in the background.
- The current account should be auto-saved if it is not already managed.
- The app should stay single-command: `codex-auth` opens the interactive picker.

## Quota Notes

- Live quota currently comes from `https://chatgpt.com/backend-api/wham/usage`.
- The upstream field is `used_percent`; the UI displays remaining quota (`100 - used_percent`).
- If live refresh is unavailable, the UI should still render cleanly and avoid disruptive layout shifts.

## Release Notes

- Source repo: `Alexs7zzh/codex-auth`
- Homebrew tap repo: `Alexs7zzh/homebrew-tap`
- Public install path: `brew install Alexs7zzh/tap/codex-auth`

Release flow:

1. Push `main`.
2. Create and push a tag like `v0.1.0`.
3. GitHub Actions builds release tarballs and publishes a GitHub Release.
4. The release also renders `codex-auth.rb`.
5. The formula in `Alexs7zzh/homebrew-tap` should match the latest release assets.

## Repo Hygiene

- Keep the public `README.md` user-focused.
- Put maintainer and agent workflow notes here rather than expanding the README.
- Keep changes scoped; avoid refactoring unrelated code during UI tweaks.

## Package Layout Conventions

- Keep the current domain-based package split under `internal/`.
- Do not flatten everything into one `internal` directory just because some packages are still single-file.
- A single-file package is acceptable when it represents a clear boundary like `tui`, `store`, `quota`, or `authfile`.
- Avoid generic catch-all packages or filenames such as `utils`, `helpers`, or `all`.
- Prefer adding files to an existing domain package before introducing a new package.
