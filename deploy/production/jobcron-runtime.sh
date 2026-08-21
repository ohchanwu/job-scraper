#!/bin/sh
set -eu
umask 077

run_dir=${JOBCRON_RUN_DIR:-/run/jobcron}
etc_dir=${JOBCRON_ETC_DIR:-/etc/jobcron}
deploy_dir=${JOBCRON_DEPLOY_DIR:-/opt/jobcron}
docker_config=$run_dir/docker
export DOCKER_CONFIG=$docker_config
secret_id_file=$etc_dir/runtime-secret-id

fail() {
	printf '%s\n' "jobcron runtime operation failed" >&2
	exit 1
}

mode() {
	stat -f '%Lp' "$1" 2>/dev/null || stat -c '%a' "$1"
}

owner() {
	stat -f '%u' "$1" 2>/dev/null || stat -c '%u' "$1"
}

remove_runtime_outputs() {
	rm -f "$run_dir/compose.env"
	rm -f "$run_dir/caddy/origin.crt" "$run_dir/caddy/origin.key"
	rmdir "$run_dir/caddy" 2>/dev/null || true
}

cleanup() {
	remove_runtime_outputs
	rm -rf -- "$run_dir/docker" "$run_dir/archive"
	rmdir "$run_dir" 2>/dev/null || true
}

prepare() {
	remove_runtime_outputs
	[ -f "$secret_id_file" ] || fail
	[ "$(mode "$secret_id_file")" = 600 ] || fail
	[ "$(owner "$secret_id_file")" = "$(id -u)" ] || fail
	[ "$(awk 'END { print NR }' "$secret_id_file")" = 1 ] || fail
	secret_id=$(sed -n '1p' "$secret_id_file")
	[ -n "$secret_id" ] || fail

	mkdir -p "$run_dir"
	chmod 700 "$run_dir"
	tmp_dir=$(mktemp -d "$run_dir/.prepare.XXXXXX")
	trap 'rm -f "$tmp_dir/secret.json" "$tmp_dir/compose.env" "$tmp_dir/origin.crt" "$tmp_dir/origin.key"; rmdir "$tmp_dir" 2>/dev/null || true' EXIT HUP INT TERM

	if ! aws secretsmanager get-secret-value \
		--secret-id "$secret_id" \
		--query SecretString \
		--output text >"$tmp_dir/secret.json" 2>/dev/null; then
		fail
	fi

	if ! jq -e '
		keys == [
			"DATABASE_URL",
			"JOBCRON_CREDENTIAL_ENCRYPTION_KEY",
			"JOBCRON_IMAGE",
			"JOBCRON_PROXY_SECRET",
			"JOBCRON_SIGNUP_ACCESS_CODE",
			"JOBCRON_STAGE1_SPONSOR_USER_ID",
			"ORIGIN_CA_CERT",
			"ORIGIN_CA_KEY",
			"SESSION_SECRET"
		]
		and all(.[]; type == "string" and length > 0)
		and all(to_entries[];
			(.key | startswith("ORIGIN_CA_")) or
			((.value | contains("\n") or contains("\r")) | not)
		)
		and (.JOBCRON_IMAGE | test("^ghcr\\.io/[a-z0-9]([a-z0-9-]{0,37}[a-z0-9])?/jobcron@sha256:[0-9a-f]{64}$"))
		and (.ORIGIN_CA_CERT | startswith("-----BEGIN CERTIFICATE-----"))
		and (.ORIGIN_CA_KEY | test("^-----BEGIN ([A-Z ]+ )?PRIVATE KEY-----"))
	' "$tmp_dir/secret.json" >/dev/null 2>&1; then
		fail
	fi

	jq -r '
		to_entries[]
		| select(.key != "ORIGIN_CA_CERT" and .key != "ORIGIN_CA_KEY")
		| "\(.key)=\(.value)"
	' "$tmp_dir/secret.json" >"$tmp_dir/compose.env"
	jq -r '.ORIGIN_CA_CERT' "$tmp_dir/secret.json" >"$tmp_dir/origin.crt"
	jq -r '.ORIGIN_CA_KEY' "$tmp_dir/secret.json" >"$tmp_dir/origin.key"
	chmod 600 "$tmp_dir/compose.env" "$tmp_dir/origin.crt" "$tmp_dir/origin.key"

	mkdir -p "$run_dir/caddy"
	chmod 700 "$run_dir/caddy"
	mv "$tmp_dir/compose.env" "$run_dir/compose.env"
	mv "$tmp_dir/origin.crt" "$run_dir/caddy/origin.crt"
	mv "$tmp_dir/origin.key" "$run_dir/caddy/origin.key"
	rm -f "$tmp_dir/secret.json"
	rmdir "$tmp_dir"
	trap - EXIT HUP INT TERM
}

compose_value() {
	sed -n "s/^$1=//p" "$run_dir/compose.env"
}

pull() {
	[ -f "$run_dir/compose.env" ] || fail
	image=$(compose_value JOBCRON_IMAGE)
	printf '%s\n' "$image" |
		grep -Eq '^ghcr\.io/[a-z0-9]([a-z0-9-]{0,37}[a-z0-9])?/jobcron@sha256:[0-9a-f]{64}$' || fail
	registry_owner=${image#ghcr.io/}
	registry_owner=${registry_owner%%/jobcron@sha256:*}
	if docker image inspect "$image" >/dev/null 2>&1; then
		return
	fi

	token_file=$run_dir/registry-token
	[ -f "$token_file" ] || fail
	[ "$(mode "$token_file")" = 600 ] || fail
	[ -s "$token_file" ] || fail
	rm -f "$docker_config/config.json"
	rmdir "$docker_config" 2>/dev/null || true
	mkdir "$docker_config"
	chmod 700 "$docker_config"
	cleanup_pull() {
		DOCKER_CONFIG=$docker_config docker logout ghcr.io >/dev/null 2>&1 || true
		rm -f "$docker_config/config.json" "$token_file"
		rmdir "$docker_config" 2>/dev/null || true
	}
	trap 'cleanup_pull' EXIT HUP INT TERM
	DOCKER_CONFIG=$docker_config docker login ghcr.io \
		--username "$registry_owner" --password-stdin <"$token_file" >/dev/null 2>&1 || fail
	DOCKER_CONFIG=$docker_config docker pull "$image" >/dev/null 2>&1 || fail
	cleanup_pull
	trap - EXIT HUP INT TERM
}

sanitize_logs() {
	sed -E \
		-e 's/([Aa]uthorization:[[:space:]]*[Bb]earer[[:space:]]+)[^[:space:]",}]+/\1[redacted]/g' \
		-e 's/([Cc]ookie:[[:space:]]*).*/\1[redacted]/g' \
		-e 's/(([Pp]assword|[Ss]ecret|[Tt]oken):[[:space:]]*)[^[:space:]",}]+/\1[redacted]/g' \
		-e 's/("([Pp]assword|[Ss]ecret|[Tt]oken)"[[:space:]]*:[[:space:]]*)"[^"]*"/\1"[redacted]"/g' \
		-e 's#(postgres(ql)?://)[^[:space:]@]+@#\1[redacted]@#g' \
		-e 's/(([Aa]uthorization|[Cc]ookie|[Pp]assword|[Ss]ecret|[Tt]oken)=[[:space:]]*)[^[:space:]",}]+/\1[redacted]/g'
}

archive() {
	[ -f "$run_dir/compose.env" ] || fail
	[ -n "${JOBCRON_RECOVERY_BUCKET:-}" ] || fail
	database_url=$(compose_value DATABASE_URL)
	[ -n "$database_url" ] || fail
	now=${JOBCRON_NOW:-$(date -u +%Y%m%dT%H%M%SZ)}
	printf '%s\n' "$now" | grep -Eq '^[0-9]{8}T[0-9]{6}Z$' || fail
	archive_dir=$run_dir/archive
	mkdir -p "$archive_dir"
	chmod 700 "$archive_dir"
	jobcron_raw=$(mktemp "$archive_dir/.jobcron.XXXXXX.raw")
	caddy_raw=$(mktemp "$archive_dir/.caddy.XXXXXX.raw")
	chmod 600 "$jobcron_raw" "$caddy_raw"
	cleanup_archive_raw() {
		rm -f "$jobcron_raw" "$caddy_raw"
	}
	trap 'cleanup_archive_raw' EXIT HUP INT TERM

	PGDATABASE=$database_url pg_dump -Fc -f "$archive_dir/database.dump" >/dev/null 2>&1 || fail
	(cd "$deploy_dir" && docker compose --env-file "$run_dir/compose.env" logs --no-color app >"$jobcron_raw") || fail
	(cd "$deploy_dir" && docker compose --env-file "$run_dir/compose.env" logs --no-color caddy >"$caddy_raw") || fail
	sanitize_logs <"$jobcron_raw" >"$archive_dir/jobcron.log"
	sanitize_logs <"$caddy_raw" >"$archive_dir/caddy.log"
	cleanup_archive_raw
	trap - EXIT HUP INT TERM
	chmod 600 "$archive_dir/database.dump" "$archive_dir/jobcron.log" "$archive_dir/caddy.log"

	for name in database.dump jobcron.log caddy.log; do
		(cd "$archive_dir" && sha256sum "$name" >"$name.sha256")
		chmod 600 "$archive_dir/$name.sha256"
	done
	key="s3://$JOBCRON_RECOVERY_BUCKET/jobcron/$now"
	for name in database.dump jobcron.log caddy.log; do
		aws s3 cp "$archive_dir/$name" "$key/$name" >/dev/null 2>&1 || fail
	done
	for name in database.dump.sha256 jobcron.log.sha256 caddy.log.sha256; do
		aws s3 cp "$archive_dir/$name" "$key/$name" >/dev/null 2>&1 || fail
	done
}

bool_mode() {
	if [ -e "$1" ] && [ "$(mode "$1")" = "$2" ]; then
		printf true
	else
		printf false
	fi
}

verify_local_state() {
	compose_env=$run_dir/compose.env
	image=
	[ ! -f "$compose_env" ] || image=$(compose_value JOBCRON_IMAGE)
	current_digest_count=0
	previous_digest_count=0
	if [ -n "$image" ]; then
		images=$(docker image ls --digests --format '{{.Repository}}@{{.Digest}}' 2>/dev/null || true)
		repository=${image%@sha256:*}
		current_digest_count=$(printf '%s\n' "$images" |
			awk -v current="$image" '$0 == current { count++ } END { print count + 0 }')
		previous_digest_count=$(printf '%s\n' "$images" |
			awk -v prefix="$repository@" -v current="$image" \
				'index($0, prefix) == 1 && $0 != current { count++ } END { print count + 0 }')
	fi
	docker_config=$(mktemp "$run_dir/.docker-config.XXXXXX")
	trap 'rm -f "$docker_config"' EXIT HUP INT TERM
	docker_json_file_logging=false
	docker_log_rotation=false
	if (cd "$deploy_dir" && docker compose config --no-interpolate --format json >"$docker_config" 2>/dev/null); then
		if jq -e '[.services.app.logging.driver, .services.caddy.logging.driver] | all(. == "json-file")' \
			"$docker_config" >/dev/null 2>&1; then
			docker_json_file_logging=true
		fi
		if jq -e '
			[.services.app.logging.options, .services.caddy.logging.options]
			| all(."max-size" == "10m" and ."max-file" == "3")
		' "$docker_config" >/dev/null 2>&1; then
			docker_log_rotation=true
		fi
	fi
	rm -f "$docker_config"
	trap - EXIT HUP INT TERM
	disk_free_kib=$(df -Pk "$run_dir" | awk 'NR == 2 { print $4 }')
	disk_free_bytes=$((disk_free_kib * 1024))
	printf 'run_dir_private=%s\n' "$(bool_mode "$run_dir" 700)"
	printf 'compose_env_private=%s\n' "$(bool_mode "$compose_env" 600)"
	printf 'origin_cert_private=%s\n' "$(bool_mode "$run_dir/caddy/origin.crt" 600)"
	printf 'origin_key_private=%s\n' "$(bool_mode "$run_dir/caddy/origin.key" 600)"
	printf 'persistent_docker_credentials=%s\n' "$([ ! -e "${HOME:?}/.docker/config.json" ] && printf false || printf true)"
	printf 'docker_json_file_logging=%s\n' "$docker_json_file_logging"
	printf 'docker_log_rotation=%s\n' "$docker_log_rotation"
	printf 'current_digest_count=%s\n' "$current_digest_count"
	printf 'previous_digest_count=%s\n' "$previous_digest_count"
	printf 'disk_free_bytes=%s\n' "$disk_free_bytes"
}

case ${1:-} in
prepare) prepare ;;
pull) pull ;;
archive) archive ;;
cleanup) cleanup ;;
verify-local-state) verify_local_state ;;
*) fail ;;
esac
