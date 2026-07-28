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

permissions="$(
  awk '
    $0 == "permissions:" { in_permissions = 1; next }
    in_permissions && /^[^ ]/ { exit }
    in_permissions && NF { print }
  ' "$workflow"
)"
test "$permissions" = $'  contents: read\n  packages: write' || fail
grep -Eq '^  (id-token|actions|deployments|secrets):|^  packages: delete$' "$workflow" && fail

grep -Fq 'RELEASE_SHA: ${{ inputs.release_sha }}' "$workflow" || fail
grep -Fq '[[ ! "$RELEASE_SHA" =~ ^[0-9a-f]{40}$ ]]' "$workflow" || fail
grep -Fq 'git cat-file -e "${RELEASE_SHA}^{commit}"' "$workflow" || fail
grep -Fq 'git checkout --detach "$RELEASE_SHA"' "$workflow" || fail

grep -Fq 'GHCR_USER: ${{ github.actor }}' "$workflow" || fail
grep -Fq 'GHCR_TOKEN: ${{ secrets.GITHUB_TOKEN }}' "$workflow" || fail
grep -Fq 'docker login ghcr.io --username "$GHCR_USER" --password-stdin >/dev/null 2>&1' \
  "$workflow" || fail
grep -Fq 'image="ghcr.io/${owner}/jobcron:sha-${RELEASE_SHA:0:12}"' "$workflow" || fail
grep -Fq 'docker buildx imagetools inspect "$image" >/dev/null 2>&1' "$workflow" || fail
test "$(grep -Fxc '            exit 1' "$workflow")" -ge 1 || fail

grep -Fq -- '--platform linux/arm64' "$workflow" || fail
grep -Fq -- '--file deploy/production/Dockerfile' "$workflow" || fail
grep -Fq -- ' --push ' "$workflow" || fail
grep -Fq ' >"$build_log" 2>&1' "$workflow" || fail
grep -Fq 'chmod 600 "$build_log" "$metadata_file"' "$workflow" || fail
grep -Fq 'trap '\''rm -f "$build_log" "$metadata_file" "$package_response"'\'' EXIT' \
  "$workflow" || fail

grep -Fq 'curl --fail --silent --show-error' "$workflow" || fail
grep -Fq -- '--output "$package_response"' "$workflow" || fail
grep -Fq 'jq -e '\''.visibility == "private"'\'' "$package_response" >/dev/null' \
  "$workflow" || fail

grep -Eq '\b(cat|head|tail|less|more) "\$(build_log|metadata_file|package_response)"' \
  "$workflow" && fail
grep -Eiq 'digest|sha256|GITHUB_OUTPUT|upload-artifact|::(notice|warning|error)' \
  "$workflow" && fail
grep -Fqx '          echo "Commit: ${RELEASE_SHA}"' "$workflow" || fail
grep -Fqx '          echo "Platform: linux/arm64"' "$workflow" || fail
grep -Fqx '          echo "Visibility: private"' "$workflow" || fail

printf 'Production image workflow contract verified\n'
