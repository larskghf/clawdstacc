<!--
Thanks for contributing! A few quick checks before you hit submit.
-->

## What

One- or two-sentence description of the change.

## Why

What problem does this solve? Link the issue if there is one (`Fixes #123`).

## How

Brief notes on the approach. Anything reviewers should pay particular attention to?

## Checklist

- [ ] `go vet ./...` and `go test ./...` are green (in the repo root)
- [ ] `shellcheck install.sh` is green
- [ ] `CHANGELOG.md` updated under `[Unreleased]` if user-visible
- [ ] Docs updated if config keys / paths / commands changed
- [ ] No commits include secrets, `clawdstacc.conf`, or other gitignored files
