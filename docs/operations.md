# Operations Reference

[Back to the README](../README.md)

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