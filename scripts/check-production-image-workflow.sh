#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
workflow="${1:-$repo_root/.github/workflows/publish-production-image.yml}"

fail() {
  printf 'Production image workflow violates the publication contract\n' >&2
  exit 1
}

test -f "$workflow" || fail

grep -Fqx "  workflow_dispatch:" "$workflow" || fail
grep -Fqx "      release_sha:" "$workflow" || fail
grep -Fqx "        required: true" "$workflow" || fail
grep -Fqx "        type: string" "$workflow" || fail
grep -Eq '^  (push|pull_request|schedule|workflow_call):' "$workflow" && fail
grep -Fqx '  group: publish-production-image-${{ inputs.release_sha }}' \
  "$workflow" || fail
grep -Fqx "  cancel-in-progress: false" "$workflow" || fail

permissions="$(
  awk '
    $0 == "permissions:" { in_permissions = 1; next }
    in_permissions && /^[^ ]/ { exit }
    in_permissions && NF { print }
  ' "$workflow"
)"
test "$permissions" = $'  contents: read\n  packages: write' || fail
grep -Eq '^  (id-token|actions|deployments|secrets):|^  packages: delete$' "$workflow" && fail

uses_count="$(grep -Eow 'uses' "$workflow" | wc -l | tr -d '[:space:]')" || fail
test "$uses_count" -eq 2 || fail
grep -Fqx \
  '        uses: actions/checkout@d23441a48e516b6c34aea4fa41551a30e30af803' \
  "$workflow" || fail
grep -Fqx \
  '        uses: docker/setup-buildx-action@bb05f3f5519dd87d3ba754cc423b652a5edd6d2c' \
  "$workflow" || fail

grep -Fq 'RELEASE_SHA: ${{ inputs.release_sha }}' "$workflow" || fail
grep -Fq '[[ ! "$RELEASE_SHA" =~ ^[0-9a-f]{40}$ ]]' "$workflow" || fail
grep -Fq 'git cat-file -e "${RELEASE_SHA}^{commit}"' "$workflow" || fail
grep -Fq 'git checkout --detach "$RELEASE_SHA"' "$workflow" || fail

grep -Fq 'GHCR_USER: ${{ github.actor }}' "$workflow" || fail
grep -Fq 'GHCR_TOKEN: ${{ secrets.GITHUB_TOKEN }}' "$workflow" || fail
grep -Fq 'docker login ghcr.io --username "$GHCR_USER" --password-stdin >/dev/null 2>&1' \
  "$workflow" || fail
grep -Fq 'image="ghcr.io/${owner}/jobcron:sha-${RELEASE_SHA:0:12}"' "$workflow" || fail
grep -Fq 'docker buildx imagetools inspect "$image" >"$manifest_lookup" 2>&1' \
  "$workflow" || fail
grep -Fq \
  'elif grep -Fqx "ERROR: ${image}: not found" "$manifest_lookup"; then' \
  "$workflow" || fail
grep -Fq 'rm -f "$manifest_lookup"' "$workflow" || fail
test "$(grep -Fxc '            exit 1' "$workflow")" -ge 1 || fail

grep -Fq -- '--platform linux/arm64' "$workflow" || fail
grep -Fq -- '--file deploy/production/Dockerfile' "$workflow" || fail
grep -Fq -- ' --push ' "$workflow" || fail
grep -Fq ' >"$build_log" 2>&1' "$workflow" || fail
grep -Fq 'chmod 600 "$build_log" "$metadata_file" "$package_response" "$manifest_lookup"' \
  "$workflow" || fail
grep -Fq 'docker logout ghcr.io >/dev/null 2>&1 || true' "$workflow" || fail
grep -Fq 'rm -f "$build_log" "$metadata_file" "$package_response" "$manifest_lookup"' \
  "$workflow" || fail

grep -Fq 'curl --fail --silent --show-error' "$workflow" || fail
grep -Fq 'jq -e '\''.visibility == "private"'\'' "$package_response" >/dev/null' \
  "$workflow" || fail
test "$(grep -Fc 'curl --fail --silent --show-error' "$workflow")" -eq 2 || fail
test "$(grep -Fxc '            --output "$package_response" \' "$workflow")" -eq 2 ||
  fail
test "$(grep -Fc 'jq -e '\''.visibility == "private"'\'' "$package_response" >/dev/null' \
  "$workflow")" -eq 2 || fail
package_privacy_blocks="$(
  awk '
    $0 == "          curl --fail --silent --show-error \\" {
      getline
      if ($0 != "            --header \"Accept: application/vnd.github+json\" \\") next
      getline
      if ($0 != "            --header \"Authorization: Bearer ${GHCR_TOKEN}\" \\") next
      getline
      if ($0 != "            --header \"X-GitHub-Api-Version: 2022-11-28\" \\") next
      getline
      if ($0 != "            --output \"$package_response\" \\") next
      getline
      if ($0 != "            \"https://api.github.com/users/${GITHUB_REPOSITORY_OWNER}/packages/container/jobcron\"") next
      getline
      if ($0 == "          jq -e '\''.visibility == \"private\"'\'' \"$package_response\" >/dev/null") blocks++
    }
    END { print blocks + 0 }
  ' "$workflow"
)"
test "$package_privacy_blocks" -eq 2 || fail
package_fetch_lines="$(grep -nF 'curl --fail --silent --show-error' "$workflow" | cut -d: -f1)"
package_output_lines="$(
  grep -nFx '            --output "$package_response" \' "$workflow" | cut -d: -f1
)"
package_gate_lines="$(
  grep -nF 'jq -e '\''.visibility == "private"'\'' "$package_response" >/dev/null' \
    "$workflow" | cut -d: -f1
)"
pre_fetch_line="$(printf '%s\n' "$package_fetch_lines" | sed -n '1p')"
post_fetch_line="$(printf '%s\n' "$package_fetch_lines" | sed -n '2p')"
pre_output_line="$(printf '%s\n' "$package_output_lines" | sed -n '1p')"
post_output_line="$(printf '%s\n' "$package_output_lines" | sed -n '2p')"
pre_gate_line="$(printf '%s\n' "$package_gate_lines" | sed -n '1p')"
post_gate_line="$(printf '%s\n' "$package_gate_lines" | sed -n '2p')"
manifest_line="$(
  grep -nF 'docker buildx imagetools inspect "$image"' "$workflow" | cut -d: -f1
)"
build_line="$(grep -nF 'docker buildx build --platform linux/arm64' "$workflow" | cut -d: -f1)"
test "$pre_fetch_line" -lt "$pre_output_line" || fail
test "$pre_output_line" -lt "$pre_gate_line" || fail
test "$pre_gate_line" -lt "$manifest_line" || fail
test "$manifest_line" -lt "$build_line" || fail
test "$build_line" -lt "$post_fetch_line" || fail
test "$post_fetch_line" -lt "$post_output_line" || fail
test "$post_output_line" -lt "$post_gate_line" || fail

grep -Eq '\b(cat|head|tail|less|more) "\$(build_log|metadata_file|package_response)"' \
  "$workflow" && fail
grep -Eiq 'digest|sha256|GITHUB_OUTPUT|upload-artifact|::(notice|warning|error)' \
  "$workflow" && fail
grep -Fqx '          echo "Commit: ${RELEASE_SHA}"' "$workflow" || fail
grep -Fqx '          echo "Platform: linux/arm64"' "$workflow" || fail
grep -Fqx '          echo "Visibility: private"' "$workflow" || fail

printf 'Production image workflow contract verified\n'
