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

replace_action_pin() {
  local action="$1"
  local pin="$2"
  ACTION="$action" PIN="$pin" perl -0pi -e \
    's#(\Q$ENV{ACTION}\E@)[0-9a-f]{40}#$1$ENV{PIN}#g' "$fixture"
}

remove_line() {
  local line="$1"
  awk -v line="$line" '$0 != line' "$fixture" >"$fixture.tmp"
  mv "$fixture.tmp" "$fixture"
}

remove_first_line() {
  local line="$1"
  awk -v line="$line" '
    !removed && $0 == line { removed = 1; next }
    { print }
  ' "$fixture" >"$fixture.tmp"
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

move_package_gate_after_build() {
  awk '
    $0 == "          curl --fail --silent --show-error \\" { capture = 1 }
    capture {
      block = block $0 ORS
      if ($0 ~ /^          jq -e /) capture = 0
      next
    }
    { print }
    $0 == "          printf '\''Image publication succeeded\\n'\''" {
      printf "%s", block
    }
  ' "$fixture" >"$fixture.tmp"
  mv "$fixture.tmp" "$fixture"
}

move_package_output_after_url() {
  local target="$1"
  awk -v target="$target" '
    $0 == "            --output \"$package_response\" \\" {
      outputs++
    }
    outputs == target && !moved &&
      $0 == "            --output \"$package_response\" \\" {
      output = $0
      moved = 1
      next
    }
    { print }
    moved == 1 &&
      $0 == "            \"https://api.github.com/users/${GITHUB_REPOSITORY_OWNER}/packages/container/jobcron\"" {
      print output
      moved = 2
    }
  ' "$fixture" >"$fixture.tmp"
  mv "$fixture.tmp" "$fixture"
}

restore_stale_package_response_before_post_gate() {
  awk '
    $0 == "          jq -e '\''.visibility == \"private\"'\'' \"$package_response\" >/dev/null" {
      gates++
      if (gates == 1) {
        print
        print "          cp \"$package_response\" \"$package_response.saved\""
        next
      }
      if (gates == 2) {
        print "          cp \"$package_response.saved\" \"$package_response\""
      }
    }
    { print }
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
expect_rejected "unapproved checkout action pin" \
  replace_action_pin "actions/checkout" \
  "0000000000000000000000000000000000000000" || failures=$((failures + 1))
expect_rejected "unapproved Buildx action pin" \
  replace_action_pin "docker/setup-buildx-action" \
  "1111111111111111111111111111111111111111" || failures=$((failures + 1))
expect_rejected "alternate YAML uses key spelling" \
  insert_after \
  '        uses: docker/setup-buildx-action@bb05f3f5519dd87d3ba754cc423b652a5edd6d2c' \
  '        uses : example.invalid/unapproved@2222222222222222222222222222222222222222' ||
  failures=$((failures + 1))
expect_rejected "quoted YAML uses key" \
  insert_after \
  '        uses: docker/setup-buildx-action@bb05f3f5519dd87d3ba754cc423b652a5edd6d2c' \
  '        "uses": example.invalid/unapproved@3333333333333333333333333333333333333333' ||
  failures=$((failures + 1))
expect_rejected "flow-style YAML uses key" \
  insert_after \
  '        uses: docker/setup-buildx-action@bb05f3f5519dd87d3ba754cc423b652a5edd6d2c' \
  '      - {uses: example.invalid/unapproved@4444444444444444444444444444444444444444}' ||
  failures=$((failures + 1))
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
expect_rejected "missing one package visibility fetch" \
  remove_first_line \
  '          curl --fail --silent --show-error \' ||
  failures=$((failures + 1))
expect_rejected "extra package visibility fetch" \
  insert_after \
  '          curl --fail --silent --show-error \' \
  '          curl --fail --silent --show-error \' ||
  failures=$((failures + 1))
expect_rejected "missing one package response output" \
  remove_first_line \
  '            --output "$package_response" \' ||
  failures=$((failures + 1))
expect_rejected "extra package response output" \
  insert_after \
  '            --output "$package_response" \' \
  '            --output "$package_response" \' ||
  failures=$((failures + 1))
expect_rejected "package response output after endpoint URL" \
  move_package_output_after_url 1 || failures=$((failures + 1))
expect_rejected "post-push package response output after endpoint URL" \
  move_package_output_after_url 2 || failures=$((failures + 1))
expect_rejected "missing one private visibility gate" \
  remove_first_line \
  '          jq -e '"'"'.visibility == "private"'"'"' "$package_response" >/dev/null' ||
  failures=$((failures + 1))
expect_rejected "extra private visibility gate" \
  insert_after \
  '          jq -e '"'"'.visibility == "private"'"'"' "$package_response" >/dev/null' \
  '          jq -e '"'"'.visibility == "private"'"'"' "$package_response" >/dev/null' ||
  failures=$((failures + 1))
expect_rejected "late private visibility check" \
  move_package_gate_after_build || failures=$((failures + 1))
expect_rejected "stale pre-push package response restored before post gate" \
  restore_stale_package_response_before_post_gate || failures=$((failures + 1))
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
printf 'docker %s\n' "$*" >>"$FAKE_EVENT_LOG"
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
call_count="$(($(cat "$FAKE_CURL_COUNT_FILE") + 1))"
printf '%s\n' "$call_count" >"$FAKE_CURL_COUNT_FILE"
printf 'curl %s\n' "$call_count" >>"$FAKE_EVENT_LOG"
package_mode="$FAKE_PACKAGE_MODE_PRE"
if ((call_count == 2)); then
  package_mode="$FAKE_PACKAGE_MODE_POST"
fi
while (($#)); do
  if [[ "$1" == "--output" ]]; then
    case "$package_mode" in
      private)
        printf '{"visibility":"private"}\n' >"$2"
        exit 0
        ;;
      public)
        printf '{"visibility":"public"}\n' >"$2"
        exit 0
        ;;
      missing)
        exit 22
        ;;
    esac
  fi
  shift
done
exit 64
EOF
cat >"$fake_bin/jq" <<'EOF'
#!/usr/bin/env bash
printf 'jq %s\n' "$(cat "$FAKE_CURL_COUNT_FILE")" >>"$FAKE_EVENT_LOG"
grep -Fqx '{"visibility":"private"}' "${@: -1}"
EOF
chmod +x "$fake_bin"/*

run_publish() {
  local mode="$1"
  local package_mode_pre="${2:-private}"
  local package_mode_post="${3:-private}"
  local case_root="$fixture_root/run-$mode"
  if [[ "$package_mode_pre" != "private" ]]; then
    case_root="$case_root-$package_mode_pre"
  fi
  if [[ "$package_mode_post" != "private" ]]; then
    case_root="$case_root-post-$package_mode_post"
  fi
  mkdir "$case_root"
  : >"$case_root/docker.log"
  : >"$case_root/event.log"
  printf '0\n' >"$case_root/curl-count"
  : >"$case_root/summary"
  PATH="$fake_bin:$PATH" \
    FAKE_DOCKER_LOG="$case_root/docker.log" \
    FAKE_EVENT_LOG="$case_root/event.log" \
    FAKE_CURL_COUNT_FILE="$case_root/curl-count" \
    FAKE_INSPECT_MODE="$mode" \
    FAKE_PACKAGE_MODE_PRE="$package_mode_pre" \
    FAKE_PACKAGE_MODE_POST="$package_mode_post" \
    GHCR_TOKEN="synthetic-token" \
    GHCR_USER="synthetic-user" \
    GITHUB_REPOSITORY_OWNER="synthetic-owner" \
    GITHUB_STEP_SUMMARY="$case_root/summary" \
    RELEASE_SHA="0123456789abcdef0123456789abcdef01234567" \
    RUNNER_TEMP="$case_root" \
    bash "$publish_script" >"$case_root/output" 2>&1
}

if run_publish absent missing; then
  printf 'FAIL: missing package reached publication\n' >&2
  failures=$((failures + 1))
elif grep -Eq "imagetools inspect|buildx build" \
  "$fixture_root/run-absent-missing/docker.log"; then
  printf 'FAIL: missing package reached manifest lookup or build\n' >&2
  failures=$((failures + 1))
fi

if run_publish absent public; then
  printf 'FAIL: public package reached publication\n' >&2
  failures=$((failures + 1))
elif grep -Eq "imagetools inspect|buildx build" \
  "$fixture_root/run-absent-public/docker.log"; then
  printf 'FAIL: public package reached manifest lookup or build\n' >&2
  failures=$((failures + 1))
fi

if ! run_publish absent; then
  printf 'FAIL: rejected a narrowly recognized absent manifest\n' >&2
  failures=$((failures + 1))
elif ! grep -Fq "buildx build" "$fixture_root/run-absent/docker.log"; then
  printf 'FAIL: absent manifest did not reach the build\n' >&2
  failures=$((failures + 1))
elif ! grep -Fq "logout ghcr.io" "$fixture_root/run-absent/docker.log"; then
  printf 'FAIL: successful publication did not log out of GHCR\n' >&2
  failures=$((failures + 1))
elif [[ "$(cat "$fixture_root/run-absent/curl-count")" != "2" ]]; then
  printf 'FAIL: successful publication did not fetch package visibility exactly twice\n' >&2
  failures=$((failures + 1))
elif ! awk '
  $0 == "curl 1" && state == 0 { state = 1; next }
  $0 == "jq 1" && state == 1 { state = 2; next }
  /^docker buildx imagetools inspect / && state == 2 { state = 3; next }
  /^docker buildx build / && state == 3 { state = 4; next }
  $0 == "curl 2" && state == 4 { state = 5; next }
  $0 == "jq 2" && state == 5 { state = 6; next }
  END { exit state == 6 ? 0 : 1 }
' "$fixture_root/run-absent/event.log"; then
  printf 'FAIL: package visibility gates are not ordered around manifest lookup and build\n' >&2
  failures=$((failures + 1))
fi

if run_publish absent private public; then
  printf 'FAIL: public package after publication was accepted\n' >&2
  failures=$((failures + 1))
elif ! grep -Fq "buildx build" "$fixture_root/run-absent-post-public/docker.log"; then
  printf 'FAIL: post-public package check failed before publication\n' >&2
  failures=$((failures + 1))
fi

if run_publish absent private missing; then
  printf 'FAIL: missing package after publication was accepted\n' >&2
  failures=$((failures + 1))
elif ! grep -Fq "buildx build" "$fixture_root/run-absent-post-missing/docker.log"; then
  printf 'FAIL: post-missing package check failed before publication\n' >&2
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
