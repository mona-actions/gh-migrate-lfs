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

Create a protected `live-lfs` GitHub Actions environment with these variables:

- `LIVE_LFS_APP_CLIENT_ID`: Client ID of the GitHub App used by `actions/create-github-app-token` v3.
- `LIVE_LFS_SOURCE_ORGANIZATION`: organization containing the permanent fixture.
- `LIVE_LFS_SOURCE_REPOSITORY`: small repository containing committed LFS pointers and objects.
- `LIVE_LFS_TARGET_ORGANIZATION`: organization in which disposable private repositories may be created.

Add the App private key as the `LIVE_LFS_APP_PRIVATE_KEY` environment secret. The App needs Contents read access in the source organization and Administration write plus Contents write access in the target organization. Restrict the source installation to the fixture repository. The target installation must cover all repositories because each disposable repository is created after its installation token is issued. Environment reviewers are recommended.

Run the workflow from the Actions tab when live transfer behavior needs verification. It is intentionally separate from pull request CI: privileged credentials are never exposed to untrusted pull request code, and the workflow always checks out the default branch. For routine use, keep the fixture small; three deterministic objects of roughly 64-256 KB each are sufficient.