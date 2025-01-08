# gh-migrate-lfs

![build](https://github.com/mona-actions/gh-migrate-lfs/actions/workflows/build.yml/badge.svg)
![GitHub Release](https://img.shields.io/github/v/release/mona-actions/gh-migrate-lfs)

`gh-migrate-lfs` is a [GitHub CLI](https://cli.github.com) extension to assist in the migration of Git LFS files between GitHub organizations. While [GitHub Enterprise Importer](https://github.com/github/gh-gei) handles many aspects of organization migration, there can be challenges with large Git LFS files. This extension helps ensure all your LFS content is properly migrated. Whether you're consolidating organizations, setting up new environments, or need to replicate repositories with LFS content, this extension can help.

## Install

```bash
gh extension install mona-actions/gh-migrate-lfs
```

## Usage: Export

Export a list of repositories containing Git LFS files to a CSV file.

```bash
Usage:
  migrate-lfs export [flags]

Flags:
  -h, --help                         help for export
  -s, --search-depth string          Search depth for .gitattributes file
  -n, --source-hostname string       GitHub Enterprise Server hostname URL (optional)
  -o, --source-organization string   Organization (required)
  -t, --source-token string          GitHub token (required)
```

### Example Export Command

```bash
gh migrate-lfs export \
  --source-organization mona-actions \
  --source-token ghp_xxxxxxxxxxxx \
  --search-depth 2
```

This will create a file named `{organization}_lfs.csv` containing all repositories with LFS files. The export process provides additional feedback:

```
📊 Export Summary:
Total repositories found: 2
✅ Successfully processed: 23 repositories
❌ Failed to process: 0 repositories
🔍 Maximum search depth: 1
🔍 Repositories with LFS: 2
📁 Output file: mona-actions_lfs.csv
🕐 Total time: 13s
```

## Usage: Pull

Clones repositories and download their LFS objects. If the repo already exists in the `--work-dir` it will pull the latest commits and lfs objects. 

```bash
Usage:
  migrate-lfs pull [flags]

Flags:
  -f, --file string              Exported LFS repos file path, csv format (required)
  -h, --help                     help for pull
  -n, --source-hostname string   GitHub Enterprise Server hostname URL (optional)
  -t, --source-token string      GitHub token with repo scope (required)
  -d, --work-dir string          Working directory with cloned repositories (required)
  -w, --workers int              Number of concurrent GIT workers to use (default 1)
```

### Example Pull Command

```bash
gh migrate-lfs pull \
  --file mona-actions_lfs.csv \
  --work-dir ./lfs_repos \
  --source-token ghp_xxxxxxxxxxxx \
  --workers 4
```

The pull process provides feedback:

```
📊 Summary:
✅ Successfully processed: 2 repositories
❌ Failed: 0 repositories
📁 Output directory: lfs_repos/
🕐 Total time: 3s

✅ Pull completed successfully!
```

## Usage: Sync

Push LFS content to repositories in the target organization.

```bash
Usage:
  migrate-lfs sync [flags]

Flags:
  -f, --file string                  Exported LFS repos file path, csv format (required)
  -h, --help                         help for sync
  -n, --target-hostname string       GitHub Enterprise Server hostname URL (optional)
  -o, --target-organization string   GitHub Organization (required)
  -t, --target-token string          GitHub token with repo scope (required)
  -d, --work-dir string              Working directory with cloned repositories (required)
  -w, --workers int                  Number of concurrent GIT workers to use (default 1)
```

### Example Sync Command

```bash
gh migrate-lfs sync \
  --file mona-actions_lfs.csv \
  --target-organization mona-emu \
  --target-token ghp_xxxxxxxxxxxx \
  --work-dir lfs_repos/
```

The sync process provides feedback:

```
📊 Summary:
✅ Successfully processed: 2 repositories
❌ Failed: 0 repositories
📁 Output directory: lfs_repos/
🕐 Total time: 5s

✅ Sync completed successfully!
```

### LFS CSV Format

The tool exports and imports repository information using the following CSV format:

```csv
Repository,GitAttributesPaths,CloneURL
example-repo,.gitattributes,https://github.com/mona-actions/example-repo.git
another-repo,.gitattributes,https://github.com/mona-actions/another-repo.git
```

- `Repository`: The name of the repository
- `GitAttributesPath`: Path to .gitattributes file containing LFS configurations
- `CloneUrl`: The repository HTTPS URL

## Required Permissions

### For Export, Pull and Sync

- repository contents: `repo`
- clone: `repo`
- git lfs pull: `repo`
- git lfs push: `repo`

## Proxy Support

The tool supports proxy configuration through both command-line flags and environment variables:

### Command-line flags:
```bash
Global Flags:
      --http-proxy string    HTTP proxy (can also use HTTP_PROXY env var)
      --https-proxy string   HTTPS proxy (can also use HTTPS_PROXY env var)
      --no-proxy string      No proxy list (can also use NO_PROXY env var)
```

```bash
# Example usage with proxy:
gh migrate-lfs pull \
  --file mona-actions_lfs.csv \
  --work-dir ./lfs_repos \
  --source-token ghp_xxxxxxxxxxxx \
  --https-proxy https://proxy.example.com:8080
```

```bash
# Example with environment variables:
export HTTPS_PROXY=https://proxy.example.com:8080
export NO_PROXY=github.internal.com
export GHMLFS_TARGET_TOKEN=ghp_...
```

```bash
gh migrate-lfs export \
    --source-organization mona-actions
```

## Environment Variables

The tool supports loading configuration from a `.env` file. This provides an alternative to command-line flags and allows you to store your configuration securely.

### Using a .env file

1. Create a `.env` file in your working directory:

```bash
# GitHub Migration LFS (GHMLFS)
GHMLFS_SOURCE_ORGANIZATION=mona-actions  # Source organization name
GHMLFS_SOURCE_HOSTNAME=                  # Source hostname
GHMLFS_SOURCE_TOKEN=ghp_xxx              # Source token
GHMLFS_TARGET_ORGANIZATION=mona-emu      # Target organization name
GHMLFS_TARGET_HOSTNAME=                  # Target hostname
GHMLFS_TARGET_TOKEN=ghp_yyy              # Target token
GHMLFS_WORKERS=                          # worker count
GHMLFS_WORKDIR=                          # work directory
GHMLFS_FILE=${GHMLFS_SOURCE_ORGANIZATION}_lfs.csv # Input CSV file name
```

2. Run the commands without flags - the tool will automatically load values from the .env file:

```bash
gh migrate-lfs export
```
```bash
gh migrate-lfs pull
```
```bash
gh migrate-lfs sync
```

When both environment variables and command-line flags are provided, the command-line flags take precedence. This allows you to override specific values while still using the .env file for most configuration.

### Example with Mixed Usage

```bash
# Load most values from .env but override the target organization
gh migrate-lfs sync --target-organization different-org
```

## Retry Configuration

The tool includes configurable retry behavior for API calls:

```bash
Global Flags:
    --retry-delay string   Delay between retries (default "1s")
    --retry-max int        Maximum retry attempts (default 3)
```

Example usage with retry configuration:

```bash
gh migrate-lfs export \
    --retry-max 5 \
    --retry-delay 2s
```

This configuration allows you to:
- Adjust the number of retry attempts for failed API calls
- Modify the delay between retry attempts
- Handle temporary API issues or rate limiting more gracefully


## Limitations

- Target repositories must exist in the destination organization before syncing
- Large LFS files may take significant time to download and upload
- Network bandwidth and storage space should be considered when migrating large LFS repositories
- The tool will retry failed operations but may still encounter persistent access or network issues
- Deep directory structures may require adjusting the search depth parameter

## License

- [MIT](./license) (c) [Mona-Actions](https://github.com/mona-actions)
- [Contributing](./contributing.md)