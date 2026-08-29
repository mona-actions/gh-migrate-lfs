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

## Migrate End to End

Run discovery, source download, and destination upload with one command:

```bash
gh migrate-lfs migrate \
  --source-organization source-org \
  --source-token "$SOURCE_TOKEN" \
  --target-organization target-org \
  --target-token "$TARGET_TOKEN" \
  --work-dir ./lfs-migration
```

This command:

1. Exports repositories containing LFS configuration.
2. Creates or updates bare source mirrors and runs `git lfs fetch --all`.
3. Uploads missing objects directly through the destination LFS Batch API.
4. Reconciles destination state and writes a run report.

The generated manifest is retained at:

```text
./lfs-migration/source-org_lfs.csv
```

To use an existing or curated manifest, pass `--file` instead of `--source-organization`:

```bash
gh migrate-lfs migrate \
  --file ./lfs-migration/source-org_lfs.csv \
  --source-token "$SOURCE_TOKEN" \
  --target-organization target-org \
  --target-token "$TARGET_TOKEN" \
  --work-dir ./lfs-migration
```

`--file` and `--source-organization` are mutually exclusive when both are passed as flags.

## How Sync Works

Sync does not invoke `git` or `git lfs`. It:

- Streams canonical objects from `.git/lfs/objects` or `lfs/objects` in bounded batches.
- Negotiates every local OID with the destination.
- Uploads only objects for which the server returns an upload action.
- Reconciles every processed object after upload.

The destination is authoritative. There are no local completion checkpoints: rerunning safely renegotiates all OIDs and skips objects already present remotely.

With `--check-hashes`, corrupt objects are reported and excluded while healthy objects continue processing.

## Staged Workflow

Each phase can also run independently.

### Export

```bash
gh migrate-lfs export \
  --source-organization source-org \
  --source-token "$SOURCE_TOKEN" \
  --search-depth 2
```

Writes `source-org_lfs.csv` in the current directory.

### Pull

```bash
gh migrate-lfs pull \
  --file source-org_lfs.csv \
  --source-token "$SOURCE_TOKEN" \
  --work-dir ./lfs-migration \
  --workers 4
```

Creates or updates bare mirrors and downloads all source LFS objects.

### Sync

```bash
gh migrate-lfs sync \
  --file source-org_lfs.csv \
  --target-organization target-org \
  --target-token "$TARGET_TOKEN" \
  --work-dir ./lfs-migration \
  --workers 2 \
  --upload-parallel 16
```

Use `gh migrate-lfs <command> --help` for the complete flag reference.

## Terminal Output

Interactive commands keep one progress line at the bottom of the terminal and update it in place. Repository findings and completed repository results are printed permanently, so every repository remains visible in the transcript.

When output is redirected or the terminal is not interactive, progress becomes stable plain-text lines with no cursor control sequences. Long-running status updates are limited to one line every 30 seconds, while repository findings, completions, and failures are always printed.

Output controls are available on every command:

- `--json`: write one final structured document to standard output; progress and diagnostics remain on standard error.
- `--quiet`, `-q`: suppress progress and human-readable summaries; errors are still reported. When combined with `--json`, the JSON document is still written.
- `--verbose`: include retry and diagnostic details.

`GH_FORCE_TTY` forces interactive progress rendering. `TERM=dumb` disables it unless `GH_FORCE_TTY` is set.

For example, capture a machine-readable migration result without mixing progress into the document:

```bash
gh migrate-lfs migrate \
  --file source-org_lfs.csv \
  --source-token "$SOURCE_TOKEN" \
  --target-organization target-org \
  --target-token "$TARGET_TOKEN" \
  --work-dir ./lfs-migration \
  --json > migration-result.json
```

## Performance Controls

- `--workers`: repositories processed concurrently.
- `--upload-parallel`: concurrent object uploads per repository.
- `--batch-size`: OIDs per Batch API request, from 1 to 10,000.

Maximum concurrent uploads are approximately `workers * upload-parallel`. The default batch size of 100 is conservative; test 1,000 or 5,000 against the destination before a large migration.

## Dry Run

For `sync`, `--dry-run` negotiates destination objects without uploading or writing sync state.

For `migrate`, export and pull still run and write local data; only destination upload and sync-state writes are skipped.

## Reports and Recovery

Sync state defaults to `.lfs-migrate` and is grouped by destination:

```text
.lfs-migrate/targets/<target-id>/
  errors-current.tsv
  errors-history.tsv
  last-run.json
  sync.lock
```

- `errors-current.tsv`: issues from the latest run.
- `errors-history.tsv`: append-only issue history with repository, OID, stage, and message.
- `last-run.json`: atomic machine-readable repository results and aggregate counters.

Only one process may write state for a destination at a time. The operating system releases lock ownership after a crash, so a remaining `sync.lock` file does not require manual cleanup.

Interrupted runs are resumed by rerunning the same command. Source mirrors are updated, all local OIDs are rescanned, and remotely present objects are skipped.

## Manifest Format

```csv
Repository,GitAttributesPaths,CloneURL
example-repo,.gitattributes,https://github.com/source-org/example-repo.git
another-repo,path/to/.gitattributes,https://github.com/source-org/another-repo.git
```

Repository names must be unique path segments. Duplicate rows are ignored.

## Configuration

The extension loads `.env` from the current directory. Precedence is:

1. Command-line flags
2. Environment variables or `.env`
3. Command defaults

```dotenv
GHMLFS_SOURCE_ORGANIZATION=source-org
GHMLFS_SOURCE_HOSTNAME=
GHMLFS_SOURCE_TOKEN=
GHMLFS_TARGET_ORGANIZATION=target-org
GHMLFS_TARGET_HOSTNAME=
GHMLFS_TARGET_TOKEN=
GHMLFS_WORK_DIR=./lfs-migration
GHMLFS_FILE=
GHMLFS_WORKERS=1
GHMLFS_SEARCH_DEPTH=1
GHMLFS_BATCH_SIZE=100
GHMLFS_UPLOAD_PARALLEL=8
GHMLFS_CHECK_HASHES=false
GHMLFS_DRY_RUN=false
GHMLFS_NO_FINAL_CHECK=false
GHMLFS_RETRY_MAX=3
GHMLFS_RETRY_DELAY=1s
GHMLFS_STATE_DIR=.lfs-migrate
```

Leave `GHMLFS_FILE` blank for end-to-end discovery. Set it to use an existing manifest.

For GitHub Enterprise Server, set `--source-hostname` and/or `--target-hostname` to the instance hostname.

## Proxy Support

Standard proxy variables are supported:

```bash
export HTTPS_PROXY=https://proxy.example.com:8080
export NO_PROXY=github.internal.example
```

Equivalent flags are `--http-proxy`, `--https-proxy`, and `--no-proxy`.

## Safety

- Tokens are sent in authorization headers and are not embedded in clone URLs.
- Upload credentials are not forwarded to different storage hosts.
- HTTPS is required except for localhost testing.
- Pull failure prevents sync in the end-to-end workflow.
- Run summaries and logs use restricted file permissions.

## Development Checks

```bash
go test -race ./...
go vet ./...
staticcheck ./...
dupl -plumbing -t 100 .
```

Go rejects unused imports and local variables during compilation. `staticcheck` detects unused private declarations and broader static issues. `dupl` reports structurally duplicated code; CI fails when it finds a clone group of at least 100 tokens.

### Live Transfer Check

The `live LFS transfer` workflow is a manual, privileged end-to-end check. It creates a uniquely named private target repository, pushes Git refs without LFS objects, verifies missing-object negotiation, performs a real upload, compares exact OID/size inventories after a fresh pull, verifies an idempotent rerun, and deletes the target. Reports are retained as a workflow artifact.

Create a protected `live-lfs` GitHub Actions environment with these variables:

- `LIVE_LFS_APP_CLIENT_ID`: Client ID of the GitHub App used by `actions/create-github-app-token` v3.
- `LIVE_LFS_SOURCE_ORGANIZATION`: organization containing the permanent fixture.
- `LIVE_LFS_SOURCE_REPOSITORY`: small repository containing committed LFS pointers and objects.
- `LIVE_LFS_TARGET_ORGANIZATION`: organization in which disposable private repositories may be created.

Add the App private key as the `LIVE_LFS_APP_PRIVATE_KEY` environment secret. The App needs Contents read access in the source organization and Administration write plus Contents write access in the target organization. Restrict the source installation to the fixture repository. The target installation must cover all repositories because each disposable repository is created after its installation token is issued. Environment reviewers are recommended.

Run the workflow from the Actions tab when live transfer behavior needs verification. It is intentionally separate from pull request CI: privileged credentials are never exposed to untrusted pull request code, and the workflow always checks out the default branch. For routine use, keep the fixture small; three deterministic objects of roughly 64-256 KB each are sufficient.

## License

[MIT](LICENSE) © [Mona Actions](https://github.com/mona-actions)