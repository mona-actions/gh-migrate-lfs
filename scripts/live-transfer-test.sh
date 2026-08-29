#!/usr/bin/env bash

set -Eeuo pipefail
set +x

required_variables=(
  LIVE_LFS_SOURCE_ORGANIZATION
  LIVE_LFS_SOURCE_REPOSITORY
  LIVE_LFS_SOURCE_TOKEN
  LIVE_LFS_TARGET_ORGANIZATION
  LIVE_LFS_TARGET_TOKEN
)

for variable in "${required_variables[@]}"; do
  if [[ -z "${!variable:-}" ]]; then
    printf 'Required environment variable is not set: %s\n' "$variable" >&2
    exit 2
  fi
done

for command in gh git go; do
  if ! command -v "$command" >/dev/null 2>&1; then
    printf 'Required command is not available: %s\n' "$command" >&2
    exit 2
  fi
done

if ! git lfs version >/dev/null 2>&1; then
  printf 'Git LFS is required.\n' >&2
  exit 2
fi

artifact_dir="${LIVE_LFS_ARTIFACT_DIR:-live-transfer-artifacts}"
mkdir -p "$artifact_dir"
artifact_dir="$(cd "$artifact_dir" && pwd)"
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/gh-migrate-lfs-live.XXXXXX")"
target_repository="${LIVE_LFS_TARGET_REPOSITORY:-}"
target_created=false

if [[ -z "$target_repository" ]]; then
  run_id="${GITHUB_RUN_ID:-local}"
  run_attempt="${GITHUB_RUN_ATTEMPT:-1}"
  target_repository="ghmlfs-live-${run_id}-${run_attempt}-$(date -u +%Y%m%d%H%M%S)-$$"
fi
target_repository="$(printf '%s' "$target_repository" | tr '[:upper:]_' '[:lower:]-' | tr -cd 'a-z0-9.-' | cut -c1-100)"
if [[ -z "$target_repository" || "$target_repository" == "." || "$target_repository" == ".." ]]; then
  printf 'Invalid target repository name.\n' >&2
  exit 2
fi

printf '%s\n' "$target_repository" >"$artifact_dir/target-repository.txt"

cleanup() {
  status=$?
  trap - EXIT
  rm -rf "$work_dir"
  if [[ "$target_created" == true ]]; then
    if GH_TOKEN="$LIVE_LFS_TARGET_TOKEN" gh api \
      --silent \
      --method DELETE \
      "repos/$LIVE_LFS_TARGET_ORGANIZATION/$target_repository"; then
      printf 'deleted\n' >"$artifact_dir/cleanup-status.txt"
    else
      printf 'failed\n' >"$artifact_dir/cleanup-status.txt"
      status=1
    fi
  fi
  exit "$status"
}
trap cleanup EXIT

object_inventory() {
  repository_path=$1
  output_path=$2
  object_root="$repository_path/lfs/objects"
  : >"$output_path"
  if [[ ! -d "$object_root" ]]; then
    return
  fi

  while IFS= read -r -d '' object_path; do
    oid=${object_path##*/}
    if [[ "$oid" =~ ^[0-9a-f]{64}$ ]]; then
      size=$(wc -c <"$object_path" | tr -d '[:space:]')
      printf '%s\t%s\n' "$oid" "$size" >>"$output_path"
    fi
  done < <(find "$object_root" -type f -print0)
  LC_ALL=C sort -o "$output_path" "$output_path"
}

printf 'Creating private target %s/%s\n' "$LIVE_LFS_TARGET_ORGANIZATION" "$target_repository"
GH_TOKEN="$LIVE_LFS_TARGET_TOKEN" gh api \
  --method POST \
  "orgs/$LIVE_LFS_TARGET_ORGANIZATION/repos" \
  -f "name=$target_repository" \
  -F private=true \
  -F has_issues=false \
  -F has_projects=false \
  -F has_wiki=false \
  >"$artifact_dir/create-repository.json"
target_created=true

binary="$work_dir/gh-migrate-lfs"
report_assertion="$work_dir/assert-sync-report"
go build -o "$binary" .
go build -o "$report_assertion" ./scripts/assert-sync-report.go

source_manifest="$work_dir/source.csv"
printf 'Repository,GitAttributesPaths,CloneURL\n%s,.gitattributes,https://github.com/%s/%s.git\n' \
  "$target_repository" \
  "$LIVE_LFS_SOURCE_ORGANIZATION" \
  "$LIVE_LFS_SOURCE_REPOSITORY" \
  >"$source_manifest"

source_work_dir="$work_dir/source"
"$binary" --quiet pull \
  --file "$source_manifest" \
  --source-token "$LIVE_LFS_SOURCE_TOKEN" \
  --work-dir "$source_work_dir"

source_inventory="$artifact_dir/source-objects.tsv"
object_inventory "$source_work_dir/$target_repository" "$source_inventory"
expected_objects=$(wc -l <"$source_inventory" | tr -d '[:space:]')
if [[ "$expected_objects" -eq 0 ]]; then
  printf 'Source fixture contains no Git LFS objects.\n' >&2
  exit 1
fi

askpass="$work_dir/git-askpass.sh"
cat >"$askpass" <<'EOF'
#!/usr/bin/env bash
case "$1" in
  *Username*) printf '%s\n' 'x-access-token' ;;
  *) printf '%s\n' "$GHMLFS_GIT_TOKEN" ;;
esac
EOF
chmod 700 "$askpass"

printf 'Pushing Git refs without LFS objects\n'
GIT_ASKPASS="$askpass" \
GIT_LFS_SKIP_PUSH=1 \
GIT_TERMINAL_PROMPT=0 \
GHMLFS_GIT_TOKEN="$LIVE_LFS_TARGET_TOKEN" \
  git -C "$source_work_dir/$target_repository" push \
    --mirror \
    "https://github.com/$LIVE_LFS_TARGET_ORGANIZATION/$target_repository.git"

common_sync_args=(
  --file "$source_manifest"
  --target-organization "$LIVE_LFS_TARGET_ORGANIZATION"
  --target-token "$LIVE_LFS_TARGET_TOKEN"
  --work-dir "$source_work_dir"
  --check-hashes
)

printf 'Verifying fresh-target negotiation\n'
"$binary" --json --quiet sync \
  "${common_sync_args[@]}" \
  --dry-run \
  >"$artifact_dir/dry-run.json"
"$report_assertion" "$artifact_dir/dry-run.json" "$expected_objects" "$expected_objects" 0 0 0

printf 'Uploading %s objects\n' "$expected_objects"
"$binary" --json --quiet sync \
  "${common_sync_args[@]}" \
  --state "$artifact_dir/first-sync-state" \
  >"$artifact_dir/first-sync.json"
"$report_assertion" "$artifact_dir/first-sync.json" "$expected_objects" "$expected_objects" "$expected_objects" 0 "$expected_objects"

target_manifest="$work_dir/target.csv"
printf 'Repository,GitAttributesPaths,CloneURL\n%s,.gitattributes,https://github.com/%s/%s.git\n' \
  "$target_repository" \
  "$LIVE_LFS_TARGET_ORGANIZATION" \
  "$target_repository" \
  >"$target_manifest"

target_work_dir="$work_dir/target"
"$binary" --quiet pull \
  --file "$target_manifest" \
  --source-token "$LIVE_LFS_TARGET_TOKEN" \
  --work-dir "$target_work_dir"

target_inventory="$artifact_dir/target-objects.tsv"
object_inventory "$target_work_dir/$target_repository" "$target_inventory"
if ! diff -u "$source_inventory" "$target_inventory" >"$artifact_dir/object-inventory.diff"; then
  printf 'Fresh target object inventory does not match the source.\n' >&2
  exit 1
fi

printf 'Verifying idempotent rerun\n'
"$binary" --json --quiet sync \
  "${common_sync_args[@]}" \
  --state "$artifact_dir/second-sync-state" \
  >"$artifact_dir/second-sync.json"
"$report_assertion" "$artifact_dir/second-sync.json" "$expected_objects" 0 0 "$expected_objects" "$expected_objects"

printf 'Verified %s objects from upload through fresh fetch and idempotent rerun.\n' "$expected_objects"