#!/bin/sh

set -eu

fail() {
	printf '%s\n' "production Compose contract failed: $1" >&2
	exit 1
}

[ -n "${JOBCRON_IMAGE:-}" ] || fail "required variable JOBCRON_IMAGE"
[ -n "${DATABASE_URL:-}" ] || fail "required variable DATABASE_URL"
[ -n "${SESSION_SECRET:-}" ] || fail "required variable SESSION_SECRET"
[ -n "${JOBCRON_CREDENTIAL_ENCRYPTION_KEY:-}" ] ||
	fail "required variable JOBCRON_CREDENTIAL_ENCRYPTION_KEY"
[ -n "${JOBCRON_PROXY_SECRET:-}" ] || fail "required variable JOBCRON_PROXY_SECRET"

command -v docker >/dev/null 2>&1 || fail "docker compose command"
command -v jq >/dev/null 2>&1 || fail "jq structured Compose inspector"

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(CDPATH= cd -- "$script_dir/.." && pwd)
compose_file="$repo_root/deploy/production/compose.yaml"
[ -f "$compose_file" ] || fail "deploy/production/compose.yaml"

umask 077
rendered_compose=$(mktemp "${TMPDIR:-/tmp}/jobcron-production-compose.XXXXXX" 2>/dev/null) ||
	fail "private rendered Compose file"
legacy_volume_probe=""
cleanup() {
	rm -f "$rendered_compose" >/dev/null 2>&1 || :
	if [ -n "$legacy_volume_probe" ]; then
		rm -f "$legacy_volume_probe" >/dev/null 2>&1 || :
	fi
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

if ! docker compose -f "$compose_file" config --format json 2>/dev/null >"$rendered_compose"; then
	fail "rendered Compose configuration"
fi

# Compose drops unused top-level volumes from its normalized model. Reference the
# retired volume from a private override: a successful render proves the base
# file still declares it, while an undefined-volume failure proves it is absent.
legacy_volume_probe=$(mktemp "${TMPDIR:-/tmp}/jobcron-production-compose.legacy.XXXXXX" 2>/dev/null) ||
	fail "private legacy-volume probe"
if ! cat 2>/dev/null >"$legacy_volume_probe" <<'EOF'
services:
  production_contract_probe:
    image: scratch
    volumes:
      - jobcron_config:/contract-probe
EOF
then
	fail "private legacy-volume probe"
fi
if docker compose -f "$compose_file" -f "$legacy_volume_probe" config --quiet \
	>/dev/null 2>&1; then
	fail "volumes.jobcron_config must be absent"
fi

check_contract() {
	contract=$1
	filter=$2
	if ! jq -e "$filter" "$rendered_compose" >/dev/null 2>&1; then
		fail "$contract"
	fi
}

check_contract "services.app" '.services.app | type == "object"'
check_contract "services.caddy" '.services.caddy | type == "object"'
check_contract "services.app.volumes must be absent" \
	'((.services.app.volumes // []) | length) == 0'
check_contract "services.app.ports must bind only loopback 7777" '
	(.services.app.ports // []) as $ports |
	($ports | length) == 1 and
	$ports[0].host_ip == "127.0.0.1" and
	($ports[0].published | tostring) == "7777" and
	$ports[0].target == 7777 and
	$ports[0].protocol == "tcp"
'
check_contract "services.caddy.ports must bind only loopback 8443 to 443" '
	(.services.caddy.ports // []) as $ports |
	($ports | length) == 1 and
	$ports[0].host_ip == "127.0.0.1" and
	($ports[0].published | tostring) == "8443" and
	$ports[0].target == 443 and
	$ports[0].protocol == "tcp"
'
check_contract "networks.runtime must be internal and shared only by app and Caddy" '
	.networks.runtime.internal == true and
	(.services.app.networks | has("runtime")) and
	(.services.caddy.networks | has("runtime"))
'
check_contract "services.caddy.volumes must mount transient Origin CA read-only" '
	[.services.caddy.volumes[] |
	 select(.type == "bind" and
	        .source == "/run/jobcron/caddy" and
	        .target == "/run/jobcron/caddy" and
	        .read_only == true)] | length == 1
'
check_contract "services.app.logging must rotate local JSON logs" '
	.services.app.logging.driver == "json-file" and
	.services.app.logging.options["max-size"] == "10m" and
	.services.app.logging.options["max-file"] == "3"
'
check_contract "services.caddy.logging must rotate local JSON logs" '
	.services.caddy.logging.driver == "json-file" and
	.services.caddy.logging.options["max-size"] == "10m" and
	.services.caddy.logging.options["max-file"] == "3"
'

check_contract "services.app.environment.DATABASE_URL" \
	'.services.app.environment.DATABASE_URL == env.DATABASE_URL'
check_contract "services.app.environment.SESSION_SECRET" \
	'.services.app.environment.SESSION_SECRET == env.SESSION_SECRET'
check_contract "services.app.environment.JOBCRON_CREDENTIAL_ENCRYPTION_KEY" \
	'.services.app.environment.JOBCRON_CREDENTIAL_ENCRYPTION_KEY == env.JOBCRON_CREDENTIAL_ENCRYPTION_KEY'
check_contract "services.app.environment.JOBCRON_ENV must be production" \
	'.services.app.environment.JOBCRON_ENV == "production"'
check_contract "services.app.environment.JOBCRON_HOST must be 0.0.0.0" \
	'.services.app.environment.JOBCRON_HOST == "0.0.0.0"'
check_contract "services.app.environment.JOBCRON_PORT must be 7777" \
	'.services.app.environment.JOBCRON_PORT == "7777"'
check_contract "services.app.environment.JOBCRON_NO_OPEN must be 1" \
	'.services.app.environment.JOBCRON_NO_OPEN == "1"'
check_contract "services.app.environment.AWS_EC2_METADATA_DISABLED must be true" \
	'.services.app.environment.AWS_EC2_METADATA_DISABLED == "true"'
check_contract "services.app.environment.JOBCRON_DEMO must be absent" \
	'(.services.app.environment | has("JOBCRON_DEMO")) | not'
check_contract "services.app.environment.JOBCRON_ADMIN_TOKEN must be absent" \
	'(.services.app.environment | has("JOBCRON_ADMIN_TOKEN")) | not'
check_contract "services.app.environment.JOBCRON_WORKNET_KEY must be absent" \
	'(.services.app.environment | has("JOBCRON_WORKNET_KEY")) | not'
check_contract "services.app.environment.JOBCRON_PROXY_SECRET" \
	'.services.app.environment.JOBCRON_PROXY_SECRET == env.JOBCRON_PROXY_SECRET'
check_contract "services.caddy.environment.JOBCRON_PROXY_SECRET" \
	'.services.caddy.environment.JOBCRON_PROXY_SECRET == env.JOBCRON_PROXY_SECRET'
check_contract "services.caddy.environment.AWS_EC2_METADATA_DISABLED must be true" \
	'.services.caddy.environment.AWS_EC2_METADATA_DISABLED == "true"'
check_contract "services.app.environment.JOBCRON_SCHEDULER_ENABLED must be 1" \
	'.services.app.environment.JOBCRON_SCHEDULER_ENABLED == "1"'
check_contract "services.app.environment.JOBCRON_DAILY_SCRAPE_TIME must preserve the same-name input or default to 05:00" '
	(env.JOBCRON_DAILY_SCRAPE_TIME // "") as $daily_time |
	.services.app.environment.JOBCRON_DAILY_SCRAPE_TIME ==
	(if $daily_time == "" then "05:00" else $daily_time end)
'
check_contract "services.app.command must enforce no-open host and port" '
	.services.app.command == ["--no-open", "--host", "0.0.0.0", "--port", "7777"]
'
check_contract "services.app.image must equal JOBCRON_IMAGE" \
	'.services.app.image == env.JOBCRON_IMAGE'
check_contract "services.app.image must use private GHCR sha256 digest" \
	'(.services.app.image | type == "string") and
	 (.services.app.image |
	  test("^ghcr[.]io/[a-z0-9._-]+/jobcron@sha256:[0-9a-f]{64}$"))'

printf '%s\n' "production Compose contract verified"
