#!/usr/bin/env bash

set -Eeuo pipefail
set +x

organization="${LIVE_LFS_SOURCE_ORGANIZATION:-}"
prefix="${LIVE_LFS_FIXTURE_PREFIX:-ghmlfs-live-fixture}"
repository_count="${LIVE_LFS_FIXTURE_REPOSITORIES:-3}"
object_count="${LIVE_LFS_FIXTURE_OBJECTS:-12}"
dry_run="${LIVE_LFS_FIXTURE_DRY_RUN:-false}"

if [[ -z "$organization" ]]; then
  printf 'Required environment variable is not set: LIVE_LFS_SOURCE_ORGANIZATION\n' >&2
  exit 2
fi
if [[ ! "$organization" =~ ^[A-Za-z0-9][A-Za-z0-9-]*$ ]]; then
  printf 'Invalid source organization: %s\n' "$organization" >&2
  exit 2
fi
if [[ ! "$prefix" =~ ^[a-z0-9][a-z0-9.-]*$ || ${#prefix} -gt 80 ]]; then
  printf 'Invalid fixture repository prefix: %s\n' "$prefix" >&2
  exit 2
fi
if [[ ! "$repository_count" =~ ^[0-9]+$ || "$repository_count" -lt 1 || "$repository_count" -gt 10 ]]; then
  printf 'LIVE_LFS_FIXTURE_REPOSITORIES must be between 1 and 10.\n' >&2
  exit 2
fi
if [[ ! "$object_count" =~ ^[0-9]+$ || "$object_count" -lt 1 || "$object_count" -gt 100 ]]; then
  printf 'LIVE_LFS_FIXTURE_OBJECTS must be between 1 and 100.\n' >&2
  exit 2
fi
if [[ "$dry_run" != true && "$dry_run" != false ]]; then
  printf 'LIVE_LFS_FIXTURE_DRY_RUN must be true or false.\n' >&2
  exit 2
fi

repository_names=()
for ((repository_index = 1; repository_index <= repository_count; repository_index++)); do
  repository_names+=("$(printf '%s-%02d' "$prefix" "$repository_index")")
done

if [[ "$dry_run" == true ]]; then
  printf '%s\n' "${repository_names[@]}"
  exit 0
fi

if [[ -z "${GH_TOKEN:-}" ]]; then
  printf 'Required environment variable is not set: GH_TOKEN\n' >&2
  exit 2
fi
for command in gh git; do
  if ! command -v "$command" >/dev/null 2>&1; then
    printf 'Required command is not available: %s\n' "$command" >&2
    exit 2
  fi
done
if ! git lfs version >/dev/null 2>&1; then
  printf 'Git LFS is required.\n' >&2
  exit 2
fi

work_dir="$(mktemp -d "${TMPDIR:-/tmp}/gh-migrate-lfs-fixtures.XXXXXX")"
cleanup() {
  status=$?
  trap - EXIT
  rm -rf "$work_dir"
  exit "$status"
}
trap cleanup EXIT

askpass="$work_dir/git-askpass.sh"
cat >"$askpass" <<'EOF'
#!/usr/bin/env bash
case "$1" in
  *Username*) printf '%s\n' 'x-access-token' ;;
  *) printf '%s\n' "$GHMLFS_GIT_TOKEN" ;;
esac
EOF
chmod 700 "$askpass"

git_with_auth() {
  GIT_ASKPASS="$askpass" \
  GIT_TERMINAL_PROMPT=0 \
  GHMLFS_GIT_TOKEN="$GH_TOKEN" \
    git "$@"
}

for repository_index in "${!repository_names[@]}"; do
  repository="${repository_names[$repository_index]}"
  repository_url="https://github.com/$organization/$repository.git"
  created=false

  if ! GH_TOKEN="$GH_TOKEN" gh api --silent "repos/$organization/$repository" 2>/dev/null; then
    printf 'Creating private fixture %s/%s\n' "$organization" "$repository" >&2
    GH_TOKEN="$GH_TOKEN" gh api \
      --silent \
      --method POST \
      "orgs/$organization/repos" \
      -f "name=$repository" \
      -f 'description=Deterministic Git LFS fixture managed by gh-migrate-lfs' \
      -F private=true \
      -F has_issues=false \
      -F has_projects=false \
      -F has_wiki=false
    created=true
  else
    printf 'Refreshing fixture %s/%s\n' "$organization" "$repository" >&2
  fi

  repository_dir="$work_dir/$repository"
  git_with_auth clone "$repository_url" "$repository_dir" >&2

  marker="$repository_dir/.github/gh-migrate-lfs-fixture"
  if [[ "$created" == false ]] && ! grep -Fxq 'managed-by=gh-migrate-lfs' "$marker" 2>/dev/null; then
    if git -C "$repository_dir" rev-parse --verify HEAD >/dev/null 2>&1; then
      printf 'Refusing to modify unmarked repository: %s/%s\n' "$organization" "$repository" >&2
      exit 1
    fi
  fi

  git -C "$repository_dir" checkout -B main >&2
  git -C "$repository_dir" lfs install --local >&2
  rm -rf "$repository_dir/fixtures"
  mkdir -p "$repository_dir/.github"
  printf 'managed-by=gh-migrate-lfs\nfixture-version=1\n' >"$marker"
  printf 'fixtures/** filter=lfs diff=lfs merge=lfs -text\n' >"$repository_dir/.gitattributes"

  for ((object_index = 1; object_index <= object_count; object_index++)); do
    set_index=$(((object_index - 1) % 3 + 1))
    case "$set_index" in
      1) size_kib=64 ;;
      2) size_kib=128 ;;
      3) size_kib=256 ;;
    esac
    fixture_dir="$repository_dir/fixtures/set-$(printf '%02d' "$set_index")"
    fixture_path="$fixture_dir/object-$(printf '%03d' "$object_index").bin"
    mkdir -p "$fixture_dir"
    seed="$organization/$repository/object-$object_index"
    printf '%s' "$seed" >"$fixture_path"
    seed_size=$(wc -c <"$fixture_path" | tr -d '[:space:]')
    remaining=$((size_kib * 1024 - seed_size))
    full_blocks=$((remaining / 1024))
    tail_bytes=$((remaining % 1024))
    if [[ "$full_blocks" -gt 0 ]]; then
      dd if=/dev/zero bs=1024 count="$full_blocks" >>"$fixture_path" 2>/dev/null
    fi
    if [[ "$tail_bytes" -gt 0 ]]; then
      dd if=/dev/zero bs=1 count="$tail_bytes" >>"$fixture_path" 2>/dev/null
    fi
  done

  git -C "$repository_dir" config user.name 'gh-migrate-lfs fixture automation'
  git -C "$repository_dir" config user.email '41898282+github-actions[bot]@users.noreply.github.com'
  git -C "$repository_dir" add .gitattributes .github/gh-migrate-lfs-fixture fixtures
  if ! git -C "$repository_dir" diff --cached --quiet; then
    git -C "$repository_dir" commit -m 'Refresh deterministic LFS fixtures' >&2
    git_with_auth -C "$repository_dir" push --set-upstream origin main >&2
  fi

  tracked_objects=$(git -C "$repository_dir" lfs ls-files --all --name-only | wc -l | tr -d '[:space:]')
  if [[ "$tracked_objects" -ne "$object_count" ]]; then
    printf '%s/%s has %s LFS objects, expected %s.\n' \
      "$organization" "$repository" "$tracked_objects" "$object_count" >&2
    exit 1
  fi
  printf '%s\n' "$repository"
done