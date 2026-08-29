# Development

[Back to the README](../README.md)

## Local Checks

```bash
go test -race ./...
go vet ./...
staticcheck ./...
dupl -plumbing -t 100 .
```

Go rejects unused imports and local variables during compilation. `staticcheck` detects unused private declarations and broader static issues. `dupl` reports structurally duplicated code; CI fails when it finds a clone group of at least 100 tokens.

## Live Transfer Check

The `live LFS transfer` workflow is a manual, privileged end-to-end check. It creates a uniquely named private target repository, pushes Git refs without LFS objects, verifies missing-object negotiation, performs a real upload, compares exact OID/size inventories after a fresh pull, verifies an idempotent rerun, and deletes the target. Reports are retained as a workflow artifact.

A required environment reviewer pauses the job before GitHub releases the App secrets. The repository currently requires approval from `cvega`; self-review is allowed so the person dispatching the manual workflow can approve their own run.

Create a protected `live-lfs` GitHub Actions environment with these variables:

- `LIVE_LFS_SOURCE_APP_CLIENT_ID`: Client ID of the read-only source GitHub App.
- `LIVE_LFS_TARGET_APP_CLIENT_ID`: Client ID of the target GitHub App.
- `LIVE_LFS_SOURCE_ORGANIZATION`: organization containing the permanent fixture.
- `LIVE_LFS_SOURCE_REPOSITORIES`: newline-separated fixture repository names.
- `LIVE_LFS_TARGET_ORGANIZATION`: organization in which disposable private repositories may be created.

Add the private keys as `LIVE_LFS_SOURCE_APP_PRIVATE_KEY` and `LIVE_LFS_TARGET_APP_PRIVATE_KEY` environment secrets. The source App needs Contents read access and should be installed only on the fixture repositories. The target App needs Administration write plus Contents write access and must be installed for all repositories because each disposable repository is created after its installation token is issued.

Configure the Client IDs and upload PEM files directly from disk:

```bash
repository=mona-actions/gh-migrate-lfs
gh variable set LIVE_LFS_SOURCE_APP_CLIENT_ID --repo "$repository" --env live-lfs --body "<source-client-id>"
gh variable set LIVE_LFS_TARGET_APP_CLIENT_ID --repo "$repository" --env live-lfs --body "<target-client-id>"
gh secret set LIVE_LFS_SOURCE_APP_PRIVATE_KEY --repo "$repository" --env live-lfs < path/to/source-app.pem
gh secret set LIVE_LFS_TARGET_APP_PRIVATE_KEY --repo "$repository" --env live-lfs < path/to/target-app.pem
```

Never commit the PEM files or pass their contents as command-line arguments.

### Provision Source Fixtures

Preview the default repository names without making API calls:

```bash
LIVE_LFS_SOURCE_ORGANIZATION=source-org \
LIVE_LFS_FIXTURE_DRY_RUN=true \
  bash .github/scripts/seed-live-lfs-fixtures.sh
```

Create or refresh three private repositories containing twelve deterministic 64, 128, and 256 KiB LFS objects each:

```bash
export LIVE_LFS_SOURCE_ORGANIZATION=source-org
GH_TOKEN="$(gh auth token)" \
  bash .github/scripts/seed-live-lfs-fixtures.sh > live-lfs-repositories.txt
```

Copy the newline-separated output into `LIVE_LFS_SOURCE_REPOSITORIES`. Existing repositories are updated only when they contain the committed `.github/gh-migrate-lfs-fixture` ownership marker; the script refuses to modify an unmarked repository. The repository and object counts can be changed with `LIVE_LFS_FIXTURE_REPOSITORIES` and `LIVE_LFS_FIXTURE_OBJECTS`.

Run the workflow from the Actions tab when live transfer behavior needs verification. It is intentionally separate from pull request CI: privileged credentials are never exposed to untrusted pull request code, and the workflow always checks out the default branch. The default live run exercises three repository workers, four upload workers per repository, and five-object batches.