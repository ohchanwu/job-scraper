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
  OLD="$old" NEW="$new" perl -0pi -e 's/\Q$ENV{OLD}\E/$ENV{NEW}/g' "$fixture"
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
  insert_after \
  '          chmod 600 "$build_log" "$metadata_file" "$package_response" "$manifest_lookup"' \
  '          cat "$build_log"' || failures=$((failures + 1))
expect_rejected "exported build metadata" \
  insert_after \
  '          chmod 600 "$build_log" "$metadata_file" "$package_response" "$manifest_lookup"' \
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
expect_rejected "missing release concurrency" \
  remove_line '  group: publish-production-image-${{ inputs.release_sha }}' ||
  failures=$((failures + 1))
expect_rejected "cancelled in-progress release" \
  replace_all "  cancel-in-progress: false" "  cancel-in-progress: true" ||
  failures=$((failures + 1))
expect_rejected "discarded manifest lookup output" \
  replace_all ' >"$manifest_lookup" 2>&1' ' >/dev/null 2>&1' ||
  failures=$((failures + 1))
expect_rejected "permissive manifest lookup failure" \
  replace_all \
  'elif grep -Fqx "ERROR: ${image}: not found" "$manifest_lookup"; then' \
  'elif test "$?" -ne 0; then' || failures=$((failures + 1))
expect_rejected "retained manifest lookup output" \
  remove_line '          rm -f "$manifest_lookup"' ||
  failures=$((failures + 1))
expect_rejected "missing registry logout" \
  replace_all 'docker logout ghcr.io >/dev/null 2>&1 || true; ' "" ||
  failures=$((failures + 1))

publish_script="$fixture_root/publish.sh"
awk '
  $0 == "      - name: Publish immutable private image" { in_step = 1 }
  in_step && $0 == "        run: |" { emit = 1; next }
  emit { sub(/^          /, ""); print }
' "$workflow" >"$publish_script"
chmod +x "$publish_script"

fake_bin="$fixture_root/bin"
mkdir "$fake_bin"
cat >"$fake_bin/git" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
cat >"$fake_bin/docker" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$FAKE_DOCKER_LOG"
case "${1:-}" in
  login)
    cat >/dev/null
    ;;
  logout)
    ;;
  buildx)
    if [[ "${2:-}" == "imagetools" && "${3:-}" == "inspect" ]]; then
      case "$FAKE_INSPECT_MODE" in
        absent)
          printf 'ERROR: %s: not found\n' "$4" >&2
          exit 1
          ;;
        network)
          printf 'ERROR: failed to do request: temporary network failure\n' >&2
          exit 1
          ;;
        existing)
          printf '{"manifest":"exists"}\n'
          ;;
      esac
    elif [[ "${2:-}" != "build" ]]; then
      exit 64
    fi
    ;;
  *)
    exit 64
    ;;
esac
EOF
cat >"$fake_bin/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
while (($#)); do
  if [[ "$1" == "--output" ]]; then
    printf '{"visibility":"private"}\n' >"$2"
    exit 0
  fi
  shift
done
exit 64
EOF
cat >"$fake_bin/jq" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
chmod +x "$fake_bin"/*

run_publish() {
  local mode="$1"
  local case_root="$fixture_root/run-$mode"
  mkdir "$case_root"
  : >"$case_root/docker.log"
  : >"$case_root/summary"
  PATH="$fake_bin:$PATH" \
    FAKE_DOCKER_LOG="$case_root/docker.log" \
    FAKE_INSPECT_MODE="$mode" \
    GHCR_TOKEN="synthetic-token" \
    GHCR_USER="synthetic-user" \
    GITHUB_REPOSITORY_OWNER="synthetic-owner" \
    GITHUB_STEP_SUMMARY="$case_root/summary" \
    RELEASE_SHA="0123456789abcdef0123456789abcdef01234567" \
    RUNNER_TEMP="$case_root" \
    bash "$publish_script" >"$case_root/output" 2>&1
}

if ! run_publish absent; then
  printf 'FAIL: rejected a narrowly recognized absent manifest\n' >&2
  failures=$((failures + 1))
elif ! grep -Fq "buildx build" "$fixture_root/run-absent/docker.log"; then
  printf 'FAIL: absent manifest did not reach the build\n' >&2
  failures=$((failures + 1))
elif ! grep -Fq "logout ghcr.io" "$fixture_root/run-absent/docker.log"; then
  printf 'FAIL: successful publication did not log out of GHCR\n' >&2
  failures=$((failures + 1))
fi

if run_publish network; then
  printf 'FAIL: transient manifest lookup failure reached publication\n' >&2
  failures=$((failures + 1))
elif grep -Fq "buildx build" "$fixture_root/run-network/docker.log"; then
  printf 'FAIL: transient manifest lookup failure invoked the build\n' >&2
  failures=$((failures + 1))
elif ! grep -Fq "logout ghcr.io" "$fixture_root/run-network/docker.log"; then
  printf 'FAIL: failed lookup did not log out of GHCR\n' >&2
  failures=$((failures + 1))
fi

if run_publish existing; then
  printf 'FAIL: existing immutable tag reached publication\n' >&2
  failures=$((failures + 1))
elif grep -Fq "buildx build" "$fixture_root/run-existing/docker.log"; then
  printf 'FAIL: existing immutable tag invoked the build\n' >&2
  failures=$((failures + 1))
elif ! grep -Fq "logout ghcr.io" "$fixture_root/run-existing/docker.log"; then
  printf 'FAIL: existing manifest path did not log out of GHCR\n' >&2
  failures=$((failures + 1))
fi

if ((failures > 0)); then
  printf 'FAIL: %d workflow mutation(s) accepted\n' "$failures" >&2
  exit 1
fi

printf 'PASS: production image workflow mutation contract\n'
