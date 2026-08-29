# gh-migrate-lfs

[![build](https://github.com/mona-actions/gh-migrate-lfs/actions/workflows/build.yml/badge.svg)](https://github.com/mona-actions/gh-migrate-lfs/actions/workflows/build.yml)
[![GitHub Release](https://img.shields.io/github/v/release/mona-actions/gh-migrate-lfs)](https://github.com/mona-actions/gh-migrate-lfs/releases)

A GitHub CLI extension for migrating Git LFS objects between GitHub organizations and GitHub Enterprise Server instances.

The extension discovers repositories using LFS, downloads their source objects, and uploads only objects missing from the destination.

## Requirements

- GitHub CLI
- Git and Git LFS for `migrate` and `pull`
- Existing destination repositories and refs
- Source and destination tokens with repository access

`export` and `sync` do not require Git LFS.

## Install

```bash
gh extension install mona-actions/gh-migrate-lfs
```

Upgrade an existing installation:

```bash
gh extension upgrade gh-migrate-lfs
```

## Quick Start

```bash
gh migrate-lfs migrate \
  --source-organization source-org \
  --source-token "$SOURCE_TOKEN" \
  --target-organization target-org \
  --target-token "$TARGET_TOKEN" \
  --work-dir ./lfs-migration
```

The command:

1. Exports repositories containing LFS configuration.
2. Creates or updates bare source mirrors and runs `git lfs fetch --all`.
3. Uploads missing objects directly through the destination LFS Batch API.
4. Reconciles destination state and writes a run report.

See [Usage](docs/usage.md) for staged commands, dry runs, output modes, and performance controls.

## Why Direct Sync

Unlike `git lfs push --all`, which walks Git refs and history to discover referenced objects, `sync` scans the local LFS object store and negotiates those OIDs directly with the destination. This avoids ref traversal and transfers only missing LFS content; it does not push commits, branches, or tags.

The destination is authoritative. There are no local completion checkpoints: rerunning safely renegotiates all OIDs and skips objects already present remotely.

## Documentation

- [Usage](docs/usage.md): end-to-end and staged workflows, output, dry runs, and performance tuning.
- [Operations reference](docs/operations.md): reports, recovery, manifests, configuration, proxies, and safety.
- [Development](docs/development.md): local checks and privileged live-transfer CI setup.

## License

[MIT](LICENSE) © [Mona Actions](https://github.com/mona-actions)