#!/bin/sh
set -eu
umask 077

fail() {
	printf '%s\n' "production recovery operation failed" >&2
	exit 1
}

bucket=${JOBCRON_RECOVERY_BUCKET:-}
prefix=${JOBCRON_RECOVERY_PREFIX:-}
recovery_dir=${JOBCRON_RECOVERY_DIR:-}

printf '%s\n' "$bucket" | grep -Eq '^[a-z0-9][a-z0-9.-]+[a-z0-9]$' || fail
printf '%s\n' "$prefix" | grep -Eq '^jobcron/[0-9]{8}T[0-9]{6}Z$' || fail
[ -n "$recovery_dir" ] || fail
aws sts get-caller-identity >/dev/null 2>&1 || fail

mkdir -p "$recovery_dir"
chmod 700 "$recovery_dir"
archive_dir=$recovery_dir/${prefix##*/}
mkdir -p "$archive_dir"
chmod 700 "$archive_dir"
keys_file=$(mktemp "$recovery_dir/.keys.XXXXXX")
keys_raw=$(mktemp "$recovery_dir/.keys-raw.XXXXXX")
cleanup() {
	rm -f "$keys_file" "$keys_raw"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

if ! aws s3api list-objects-v2 \
	--bucket "$bucket" \
	--prefix "$prefix/" \
	--query 'Contents[].Key' \
	--output text >"$keys_raw" 2>/dev/null; then
	fail
fi
tr '[:space:]' '\n' <"$keys_raw" | sed '/^$/d' >"$keys_file"
rm -f "$keys_raw"

for name in database.dump database.dump.sha256 jobcron.log jobcron.log.sha256 caddy.log caddy.log.sha256; do
	[ "$(grep -Fxc "$prefix/$name" "$keys_file")" = 1 ] || fail
done

key_count=0
while IFS= read -r key; do
	case $key in
	"$prefix/database.dump" | "$prefix/database.dump.sha256" | \
		"$prefix/jobcron.log" | "$prefix/jobcron.log.sha256" | \
		"$prefix/caddy.log" | "$prefix/caddy.log.sha256") ;;
	*) fail ;;
	esac
	key_count=$((key_count + 1))
	target=$archive_dir/${key##*/}
	if [ ! -f "$target" ]; then
		aws s3 cp "s3://$bucket/$key" "$target" >/dev/null 2>&1 || fail
	fi
	chmod 600 "$target"
done <"$keys_file"
[ "$key_count" = 6 ] || fail

for name in database.dump jobcron.log caddy.log; do
	(cd "$archive_dir" && sha256sum -c "$name.sha256" >/dev/null 2>&1) || fail
done

while IFS= read -r key; do
	aws s3api put-object-tagging \
		--bucket "$bucket" \
		--key "$key" \
		--tagging 'TagSet=[{Key=macbook-copy,Value=verified}]' \
		>/dev/null 2>&1 || fail
done <"$keys_file"

cleanup
trap - EXIT HUP INT TERM
printf '%s\n' "recovery_verified=true"
