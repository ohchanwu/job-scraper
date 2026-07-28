#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
checker="$repo_root/scripts/check-production-image-workflow.sh"
workflow="$repo_root/.github/workflows/publish-production-image.yml"
fixture_root="$(mktemp -d)"
trap 'rm -rf "$fixture_root"' EXIT

test -x "$checker"
test -f "$workflow"

fixture="$fixture_root/publish-production-image.yml"
output="$fixture_root/checker.out"

reset_fixture() {
  cp "$workflow" "$fixture"
}

replace_all() {
  local old="$1"
  local new="$2"
  perl -0pi -e "s/\\Q$old\\E/$new/g" "$fixture"
}

remove_line() {
  local line="$1"
  awk -v line="$line" '$0 != line' "$fixture" >"$fixture.tmp"
  mv "$fixture.tmp" "$fixture"
}

insert_after() {
  local line="$1"
  local addition="$2"
  awk -v line="$line" -v addition="$addition" '
    { print }
    !inserted && $0 == line {
      print addition
      inserted = 1
    }
    END { if (!inserted) exit 1 }
  ' "$fixture" >"$fixture.tmp"
  mv "$fixture.tmp" "$fixture"
}

run_checker() {
  "$checker" "$fixture" >"$output" 2>&1
}

expect_rejected() {
  local name="$1"
  shift
  reset_fixture
  "$@"
  if run_checker; then
    printf 'FAIL: accepted %s\n' "$name" >&2
    return 1
  fi
  printf 'PASS: rejected %s\n' "$name"
}

failures=0

reset_fixture
if ! run_checker; then
  printf 'FAIL: rejected the unmodified workflow\n' >&2
  cat "$output" >&2
  exit 1
fi

expect_rejected "missing packages write permission" \
  remove_line "  packages: write" || failures=$((failures + 1))
expect_rejected "broader contents permission" \
  replace_all "contents: read" "contents: write" || failures=$((failures + 1))
expect_rejected "id-token permission" \
  insert_after "  packages: write" "  id-token: write" || failures=$((failures + 1))
expect_rejected "push trigger" \
  insert_after "on:" "  push:" || failures=$((failures + 1))
expect_rejected "pull request trigger" \
  insert_after "on:" "  pull_request:" || failures=$((failures + 1))
expect_rejected "wrong platform" \
  replace_all "linux/arm64" "linux/amd64" || failures=$((failures + 1))
expect_rejected "missing push flag" \
  replace_all " --push " " " || failures=$((failures + 1))
expect_rejected "visible build output" \
  replace_all ' >"$build_log" 2>&1' "" || failures=$((failures + 1))
expect_rejected "printed build output" \
  insert_after '          chmod 600 "$build_log" "$metadata_file"' \
  '          cat "$build_log"' || failures=$((failures + 1))
expect_rejected "exported build metadata" \
  insert_after '          chmod 600 "$build_log" "$metadata_file"' \
  '          echo "metadata=$metadata_file" >>"$GITHUB_OUTPUT"' ||
  failures=$((failures + 1))
expect_rejected "digest in summary" \
  insert_after '          echo "Visibility: private"' \
  '          echo "Digest: private"' || failures=$((failures + 1))
expect_rejected "missing private visibility check" \
  remove_line \
  '          jq -e '"'"'.visibility == "private"'"'"' "$package_response" >/dev/null' ||
  failures=$((failures + 1))
expect_rejected "immutable tag overwrite" \
  remove_line "            exit 1" || failures=$((failures + 1))
expect_rejected "repository publication secret" \
  replace_all '${{ secrets.GITHUB_TOKEN }}' '${{ secrets.GHCR_TOKEN }}' ||
  failures=$((failures + 1))

if ((failures > 0)); then
  printf 'FAIL: %d workflow mutation(s) accepted\n' "$failures" >&2
  exit 1
fi

printf 'PASS: production image workflow mutation contract\n'
