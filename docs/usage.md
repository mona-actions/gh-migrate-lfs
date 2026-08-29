# Usage

[Back to the README](../README.md)

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

The generated manifest is retained at `./lfs-migration/source-org_lfs.csv`.

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

## Direct Sync Behavior

Sync does not invoke `git` or `git lfs`. It:

- Streams canonical objects from `.git/lfs/objects` or `lfs/objects` in bounded batches.
- Negotiates every local OID with the destination.
- Uploads only objects for which the server returns an upload action.
- Reconciles every processed object after upload.

The destination is authoritative. There are no local completion checkpoints: rerunning safely renegotiates all OIDs and skips objects already present remotely.

With `--check-hashes`, corrupt objects are reported and excluded while healthy objects continue processing.

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