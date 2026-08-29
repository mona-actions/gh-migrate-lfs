#!/usr/bin/env bash

set -Eeuo pipefail
set +x

required_variables=(
  LIVE_LFS_SOURCE_ORGANIZATION
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

source_repository_list="${LIVE_LFS_SOURCE_REPOSITORIES:-${LIVE_LFS_SOURCE_REPOSITORY:-}}"
if [[ -z "$source_repository_list" ]]; then
  printf 'Required environment variable is not set: LIVE_LFS_SOURCE_REPOSITORIES\n' >&2
  exit 2
fi
source_repositories=()
declare -A seen_source_repositories=()
while IFS= read -r source_repository; do
  source_repository="${source_repository//$'\r'/}"
  if [[ -z "$source_repository" ]]; then
    continue
  fi
  if [[ ! "$source_repository" =~ ^[A-Za-z0-9_.-]+$ || "$source_repository" == "." || "$source_repository" == ".." ]]; then
    printf 'Invalid source repository name: %s\n' "$source_repository" >&2
    exit 2
  fi
  if [[ -n "${seen_source_repositories[$source_repository]+set}" ]]; then
    printf 'Duplicate source repository name: %s\n' "$source_repository" >&2
    exit 2
  fi
  seen_source_repositories[$source_repository]=1
  source_repositories+=("$source_repository")
done < <(printf '%s\n' "$source_repository_list" | tr ', ' '\n\n')
if [[ "${#source_repositories[@]}" -eq 0 ]]; then
  printf 'LIVE_LFS_SOURCE_REPOSITORIES contains no repository names.\n' >&2
  exit 2
fi

repository_workers="${LIVE_LFS_WORKERS:-${#source_repositories[@]}}"
upload_parallel="${LIVE_LFS_UPLOAD_PARALLEL:-4}"
batch_size="${LIVE_LFS_BATCH_SIZE:-5}"
for setting in repository_workers upload_parallel batch_size; do
  if [[ ! "${!setting}" =~ ^[0-9]+$ || "${!setting}" -lt 1 ]]; then
    printf '%s must be a positive integer.\n' "$setting" >&2
    exit 2
  fi
done
if [[ "$repository_workers" -gt 10 ]]; then
  printf 'repository_workers must not exceed 10.\n' >&2
  exit 2
fi
if [[ "$upload_parallel" -gt 512 ]]; then
  printf 'upload_parallel must not exceed 512.\n' >&2
  exit 2
fi
if [[ "$batch_size" -gt 10000 ]]; then
  printf 'batch_size must not exceed 10000.\n' >&2
  exit 2
fi

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
target_prefix="${LIVE_LFS_TARGET_PREFIX:-}"
if [[ -n "${LIVE_LFS_TARGET_REPOSITORY:-}" ]]; then
  if [[ "${#source_repositories[@]}" -ne 1 ]]; then
    printf 'LIVE_LFS_TARGET_REPOSITORY is only valid with one source repository.\n' >&2
    exit 2
  fi
  target_prefix="$LIVE_LFS_TARGET_REPOSITORY"
elif [[ -z "$target_prefix" ]]; then
  run_id="${GITHUB_RUN_ID:-local}"
  run_attempt="${GITHUB_RUN_ATTEMPT:-1}"
  target_prefix="ghmlfs-live-${run_id}-${run_attempt}-$(date -u +%Y%m%d%H%M%S)-$$"
fi
target_prefix="$(printf '%s' "$target_prefix" | tr '[:upper:]_' '[:lower:]-' | tr -cd 'a-z0-9.-' | cut -c1-96)"
if [[ -z "$target_prefix" || "$target_prefix" == "." || "$target_prefix" == ".." ]]; then
  printf 'Invalid target repository name.\n' >&2
  exit 2
fi

target_repositories=()
target_created=()
for source_index in "${!source_repositories[@]}"; do
  if [[ -n "${LIVE_LFS_TARGET_REPOSITORY:-}" ]]; then
    target_repository="$target_prefix"
  else
    target_repository="$(printf '%s-%02d' "$target_prefix" "$((source_index + 1))")"
  fi
  target_repositories+=("$target_repository")
  target_created+=(false)
done
printf '%s\n' "${target_repositories[@]}" >"$artifact_dir/target-repositories.txt"
if [[ "${#target_repositories[@]}" -eq 1 ]]; then
  printf '%s\n' "${target_repositories[0]}" >"$artifact_dir/target-repository.txt"
fi

cleanup() {
  status=$?
  trap - EXIT
  rm -rf "$work_dir"
  : >"$artifact_dir/cleanup-status.txt"
  for target_index in "${!target_repositories[@]}"; do
    if [[ "${target_created[$target_index]}" == true ]]; then
      target_repository="${target_repositories[$target_index]}"
      if GH_TOKEN="$LIVE_LFS_TARGET_TOKEN" gh api \
        --silent \
        --method DELETE \
        "repos/$LIVE_LFS_TARGET_ORGANIZATION/$target_repository"; then
        printf '%s\tdeleted\n' "$target_repository" >>"$artifact_dir/cleanup-status.txt"
      else
        printf '%s\tfailed\n' "$target_repository" >>"$artifact_dir/cleanup-status.txt"
        status=1
      fi
    fi
  done
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

: >"$artifact_dir/create-repositories.jsonl"
for target_index in "${!target_repositories[@]}"; do
  target_repository="${target_repositories[$target_index]}"
  printf 'Creating private target %s/%s\n' "$LIVE_LFS_TARGET_ORGANIZATION" "$target_repository"
  GH_TOKEN="$LIVE_LFS_TARGET_TOKEN" gh api \
    --method POST \
    "orgs/$LIVE_LFS_TARGET_ORGANIZATION/repos" \
    -f "name=$target_repository" \
    -F private=true \
    -F has_issues=false \
    -F has_projects=false \
    -F has_wiki=false \
    >>"$artifact_dir/create-repositories.jsonl"
  target_created[$target_index]=true
done

binary="$work_dir/gh-migrate-lfs"
report_assertion="$work_dir/assert-sync-report"
go build -o "$binary" .
go build -o "$report_assertion" ./scripts/assert-sync-report.go

source_manifest="$work_dir/source.csv"
printf 'Repository,GitAttributesPaths,CloneURL\n' >"$source_manifest"
for source_index in "${!source_repositories[@]}"; do
  printf '%s,.gitattributes,https://github.com/%s/%s.git\n' \
    "${target_repositories[$source_index]}" \
    "$LIVE_LFS_SOURCE_ORGANIZATION" \
    "${source_repositories[$source_index]}" \
    >>"$source_manifest"
done

source_work_dir="$work_dir/source"
"$binary" --quiet pull \
  --file "$source_manifest" \
  --source-token "$LIVE_LFS_SOURCE_TOKEN" \
  --work-dir "$source_work_dir" \
  --workers "$repository_workers"

source_inventory="$artifact_dir/source-objects.tsv"
: >"$source_inventory"
for target_repository in "${target_repositories[@]}"; do
  repository_inventory="$work_dir/$target_repository-source.tsv"
  object_inventory "$source_work_dir/$target_repository" "$repository_inventory"
  while IFS=$'\t' read -r oid size; do
    printf '%s\t%s\t%s\n' "$target_repository" "$oid" "$size" >>"$source_inventory"
  done <"$repository_inventory"
done
LC_ALL=C sort -o "$source_inventory" "$source_inventory"
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

for target_repository in "${target_repositories[@]}"; do
  printf 'Pushing Git refs without LFS objects for %s\n' "$target_repository"
  GIT_ASKPASS="$askpass" \
  GIT_LFS_SKIP_PUSH=1 \
  GIT_TERMINAL_PROMPT=0 \
  GHMLFS_GIT_TOKEN="$LIVE_LFS_TARGET_TOKEN" \
    git -C "$source_work_dir/$target_repository" push \
      --mirror \
      "https://github.com/$LIVE_LFS_TARGET_ORGANIZATION/$target_repository.git"
done

common_sync_args=(
  --file "$source_manifest"
  --target-organization "$LIVE_LFS_TARGET_ORGANIZATION"
  --target-token "$LIVE_LFS_TARGET_TOKEN"
  --work-dir "$source_work_dir"
  --check-hashes
  --workers "$repository_workers"
  --upload-parallel "$upload_parallel"
  --batch-size "$batch_size"
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
printf 'Repository,GitAttributesPaths,CloneURL\n' >"$target_manifest"
for target_repository in "${target_repositories[@]}"; do
  printf '%s,.gitattributes,https://github.com/%s/%s.git\n' \
    "$target_repository" \
    "$LIVE_LFS_TARGET_ORGANIZATION" \
    "$target_repository" \
    >>"$target_manifest"
done

target_work_dir="$work_dir/target"
"$binary" --quiet pull \
  --file "$target_manifest" \
  --source-token "$LIVE_LFS_TARGET_TOKEN" \
  --work-dir "$target_work_dir" \
  --workers "$repository_workers"

target_inventory="$artifact_dir/target-objects.tsv"
: >"$target_inventory"
for target_repository in "${target_repositories[@]}"; do
  repository_inventory="$work_dir/$target_repository-target.tsv"
  object_inventory "$target_work_dir/$target_repository" "$repository_inventory"
  while IFS=$'\t' read -r oid size; do
    printf '%s\t%s\t%s\n' "$target_repository" "$oid" "$size" >>"$target_inventory"
  done <"$repository_inventory"
done
LC_ALL=C sort -o "$target_inventory" "$target_inventory"
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

printf 'Verified %s objects across %s repositories through fresh fetch and idempotent rerun.\n' \
  "$expected_objects" "${#source_repositories[@]}"