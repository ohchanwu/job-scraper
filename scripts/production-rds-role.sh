#!/bin/sh
set -eu
umask 077

fail() {
	printf '%s\n' "production RDS role operation failed" >&2
	exit 1
}

mode() {
	stat -c '%a' "$1" 2>/dev/null || stat -f '%Lp' "$1"
}

master_url=${JOBCRON_MASTER_DATABASE_URL:-}
private_endpoint=${JOBCRON_PRIVATE_DATABASE_ENDPOINT:-}
app_user=${JOBCRON_APP_DATABASE_USER:-}
role_env=${JOBCRON_DATABASE_ROLE_ENV:-}
runtime_secret=${JOBCRON_RUNTIME_SECRET_JSON:-}

printf '%s\n' "$master_url" |
	grep -Eq '^postgres://[A-Za-z_][A-Za-z0-9_]*@127\.0\.0\.1:[0-9]+/[A-Za-z_][A-Za-z0-9_]*\?sslmode=require$' ||
	fail
printf '%s\n' "$private_endpoint" |
	grep -Eq '^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+\.rds\.amazonaws\.com:[0-9]+$' ||
	fail
private_port=${private_endpoint##*:}
[ "$private_port" -ge 1 ] 2>/dev/null && [ "$private_port" -le 65535 ] 2>/dev/null || fail
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
tls_query=${master_url#*\?}
escaped_application_password=$(printf '%s' "$application_password" | sed "s/'/''/g")
encoded_application_password=$(printf '%s' "$application_password" | jq -sRr @uri)
application_url="postgres://$app_user:$encoded_application_password@$private_endpoint/$database?$tls_query"
case $application_url in
*127.0.0.1* | *localhost*) fail ;;
esac
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
		CREATE ROLE $app_user LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS;
	END IF;
END
\$jobcron\$;
ALTER ROLE $app_user LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS PASSWORD '$escaped_application_password';
ALTER ROLE $app_user RESET ALL;
DO \$jobcron\$
BEGIN
	IF EXISTS (
		SELECT 1
		FROM pg_auth_members membership
		JOIN pg_roles member_role ON member_role.oid = membership.member
		WHERE member_role.rolname = '$app_user'
	) THEN
		RAISE EXCEPTION 'application role has inherited role membership';
	END IF;
	IF EXISTS (
		SELECT 1 FROM pg_roles
		WHERE rolname = '$app_user'
		  AND (NOT rolcanlogin OR rolsuper OR rolcreatedb OR rolcreaterole OR rolinherit OR rolreplication OR rolbypassrls)
	) THEN
		RAISE EXCEPTION 'application role attributes are not restrictive';
	END IF;
	IF EXISTS (
		SELECT 1 FROM pg_database
		WHERE datname = current_database() AND pg_get_userbyid(datdba) = '$app_user'
	) OR EXISTS (
		SELECT 1 FROM pg_namespace
		WHERE nspname = 'public' AND pg_get_userbyid(nspowner) = '$app_user'
	) OR EXISTS (
		SELECT 1
		FROM pg_class relation
		JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace
		WHERE namespace.nspname = 'public' AND pg_get_userbyid(relation.relowner) = '$app_user'
	) THEN
		RAISE EXCEPTION 'application role owns production database objects';
	END IF;
END
\$jobcron\$;
GRANT CONNECT ON DATABASE $database TO $app_user;
GRANT USAGE ON SCHEMA public TO $app_user;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO $app_user;
REVOKE INSERT, UPDATE, DELETE ON TABLE public.schema_migrations FROM $app_user;
REVOKE INSERT, UPDATE, DELETE ON TABLE public.schema_migrations FROM PUBLIC;
GRANT SELECT ON TABLE public.schema_migrations TO $app_user;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO $app_user;
ALTER DEFAULT PRIVILEGES IN SCHEMA public REVOKE INSERT, UPDATE, DELETE ON TABLES FROM PUBLIC;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO $app_user;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT USAGE, SELECT ON SEQUENCES TO $app_user;
DO \$jobcron\$
BEGIN
	IF NOT has_database_privilege('$app_user', current_database(), 'CONNECT')
		OR NOT has_schema_privilege('$app_user', 'public', 'USAGE')
		OR NOT has_table_privilege('$app_user', 'public.schema_migrations', 'SELECT')
		OR has_table_privilege('$app_user', 'public.schema_migrations', 'INSERT')
		OR has_table_privilege('$app_user', 'public.schema_migrations', 'UPDATE')
		OR has_table_privilege('$app_user', 'public.schema_migrations', 'DELETE') THEN
		RAISE EXCEPTION 'application role effective migration-ledger privileges are unsafe';
	END IF;
END
\$jobcron\$;
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
