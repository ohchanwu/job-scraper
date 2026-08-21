#!/bin/sh
set -eu
umask 077

fail() {
	printf '%s\n' "reviewed Jobcron user build failed" >&2
	exit 1
}

[ "$#" -eq 3 ] || fail
repo_root=$1
reviewed_sha=$2
output=$3

printf '%s\n' "$reviewed_sha" | grep -Eq '^[0-9a-f]{40}$' || fail
[ "$(git -C "$repo_root" rev-parse --is-inside-work-tree 2>/dev/null)" = true ] || fail
case $output in
/*) ;;
*) fail ;;
esac
[ -d "$(dirname "$output")" ] || fail
[ ! -e "$output" ] || fail
resolved_sha=$(git -C "$repo_root" rev-parse --verify "$reviewed_sha^{commit}" 2>/dev/null) || fail
[ "$resolved_sha" = "$reviewed_sha" ] || fail

build_dir=$(mktemp -d) || fail
cleanup() {
	rm -rf -- "$build_dir"
}
cleanup_failure() {
	rm -f -- "$output"
	cleanup
}
trap cleanup_failure EXIT
trap 'exit 1' HUP INT TERM

archive_file=$build_dir/source.tar
source_dir=$build_dir/source
mkdir -m 700 "$source_dir" || fail

git -C "$repo_root" archive --format=tar --output="$archive_file" "$reviewed_sha" || fail
tar -xf "$archive_file" -C "$source_dir" || fail
rm -f -- "$archive_file" || fail
(
	cd "$source_dir"
	GOENV=off GOFLAGS= GOWORK=off GO111MODULE=on \
		go build -mod=readonly -trimpath -o "$output" ./cmd/jobcron-user
) || fail
chmod 500 "$output"
cleanup
trap - EXIT HUP INT TERM
