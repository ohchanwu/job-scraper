#!/bin/sh
set -eu
umask 077

fail() {
	printf '%s\n' "production RDS role operation failed" >&2
	exit 1
}

mode() {
	stat -f '%Lp' "$1" 2>/dev/null || stat -c '%a' "$1"
}

master_url=${JOBCRON_MASTER_DATABASE_URL:-}
app_user=${JOBCRON_APP_DATABASE_USER:-}
role_env=${JOBCRON_DATABASE_ROLE_ENV:-}
runtime_secret=${JOBCRON_RUNTIME_SECRET_JSON:-}

printf '%s\n' "$master_url" |
	grep -Eq '^postgres://[A-Za-z_][A-Za-z0-9_]*@127\.0\.0\.1:[0-9]+/[A-Za-z_][A-Za-z0-9_]*\?sslmode=(verify-full|require)$' ||
	fail
printf '%s\n' "$app_user" | grep -Eq '^[A-Za-z_][A-Za-z0-9_]*$' || fail
[ -n "$role_env" ] || fail
[ -f "$runtime_secret" ] || fail
[ "$(mode "$runtime_secret")" = 600 ] || fail

if [ -t 0 ]; then
	trap 'stty echo 2>/dev/null || true' EXIT
	trap 'exit 1' HUP INT TERM
	stty -echo
fi
IFS= read -r master_password || fail
IFS= read -r application_password || fail
if [ -t 0 ]; then
	stty echo
	trap - EXIT HUP INT TERM
	printf '\n' >&2
fi
[ -n "$master_password" ] || fail
[ -n "$application_password" ] || fail

database_url=${master_url%%\?*}
database=${database_url##*/}
connection=${master_url#postgres://}
connection=${connection#*@}
escaped_application_password=$(printf '%s' "$application_password" | sed "s/'/''/g")
encoded_application_password=$(printf '%s' "$application_password" | jq -sRr @uri)
application_url="postgres://$app_user:$encoded_application_password@$connection"
runtime_tmp=$(mktemp "$runtime_secret.XXXXXX")
role_tmp=$(mktemp "$role_env.XXXXXX")
cleanup() {
	rm -f "$runtime_tmp" "$role_tmp"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
if ! APP_DATABASE_URL=$application_url jq \
	'.DATABASE_URL = env.APP_DATABASE_URL' "$runtime_secret" >"$runtime_tmp"; then
	fail
fi
printf '%s\n' "DATABASE_ROLE_READY=true" >"$role_tmp"
chmod 600 "$runtime_tmp" "$role_tmp"

if ! PGPASSWORD=$master_password psql "$master_url" -X -q -v ON_ERROR_STOP=1 \
	>/dev/null 2>&1 <<SQL
BEGIN;
DO \$jobcron\$
BEGIN
	IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '$app_user') THEN
		CREATE ROLE $app_user LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION;
	END IF;
END
\$jobcron\$;
ALTER ROLE $app_user PASSWORD '$escaped_application_password';
GRANT CONNECT ON DATABASE $database TO $app_user;
GRANT USAGE ON SCHEMA public TO $app_user;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO $app_user;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO $app_user;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO $app_user;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT USAGE, SELECT ON SEQUENCES TO $app_user;
COMMIT;
SQL
then
	fail
fi

mv "$runtime_tmp" "$runtime_secret"
mv "$role_tmp" "$role_env"
trap - EXIT HUP INT TERM
unset master_password application_password escaped_application_password application_url
printf '%s\n' "database_role_ready=true"
