#!/bin/sh
set -eu
umask 077
PATH=/usr/bin:/bin
export PATH

fail() {
	printf '%s\n' "reviewed Jobcron user build failed" >&2
	exit 1
}

[ "$#" -eq 4 ] || fail
repo_root=$1
reviewed_sha=$2
output=$3
go_binary=$4

printf '%s\n' "$reviewed_sha" | grep -Eq '^[0-9a-f]{40}$' || fail
[ "$(git -C "$repo_root" rev-parse --is-inside-work-tree 2>/dev/null)" = true ] || fail
case $output in
/*) ;;
*) fail ;;
esac
[ -d "$(dirname "$output")" ] || fail
[ ! -e "$output" ] || fail
case $go_binary in
/*) ;;
*) fail ;;
esac
[ -f "$go_binary" ] && [ -x "$go_binary" ] || fail
git_binary=$(command -v git) || fail
run_git() {
	env -i \
		PATH="$PATH" \
		GIT_CONFIG_NOSYSTEM=1 \
		GIT_CONFIG_GLOBAL=/dev/null \
		GIT_NO_REPLACE_OBJECTS=1 \
		"$git_binary" "$@"
}
resolved_sha=$(run_git -C "$repo_root" rev-parse --verify "$reviewed_sha^{commit}" 2>/dev/null) || fail
[ "$resolved_sha" = "$reviewed_sha" ] || fail

build_dir=$(mktemp -d) || fail
cleanup() {
	chmod -R u+w "$build_dir" 2>/dev/null || true
	rm -rf -- "$build_dir"
}
cleanup_failure() {
	rm -f -- "$output"
	cleanup
}
trap cleanup_failure EXIT
trap 'exit 1' HUP INT TERM

source_dir=$build_dir/source
run_git -c core.attributesFile=/dev/null \
	clone --quiet --local --no-hardlinks --no-checkout --no-tags "$repo_root" "$source_dir" || fail
clone_sha=$(run_git -C "$source_dir" rev-parse --verify "$reviewed_sha^{commit}" 2>/dev/null) || fail
[ "$clone_sha" = "$reviewed_sha" ] || fail
run_git -C "$source_dir" checkout --quiet --detach "$reviewed_sha" || fail
[ "$(run_git -C "$source_dir" rev-parse HEAD)" = "$reviewed_sha" ] || fail
rm -rf -- "$source_dir/.git"

home_dir=$build_dir/home
cache_dir=$build_dir/cache
module_cache=$build_dir/modcache
go_path=$build_dir/gopath
tmp_dir=$build_dir/tmp
mkdir -m 700 "$home_dir" "$cache_dir" "$module_cache" "$go_path" "$tmp_dir" || fail
go_dir=${go_binary%/*}
[ -n "$go_dir" ] || go_dir=/
trusted_path=$go_dir:/usr/bin:/bin
run_go() {
	env -i \
		HOME="$home_dir" \
		PATH="$trusted_path" \
		TMPDIR="$tmp_dir" \
		GOENV=off \
		GOFLAGS= \
		GOWORK=off \
		GO111MODULE=on \
		GOTOOLCHAIN=local \
		CGO_ENABLED=0 \
		GOCACHE="$cache_dir" \
		GOMODCACHE="$module_cache" \
		GOPATH="$go_path" \
		GOTMPDIR="$tmp_dir" \
		GOPROXY=https://proxy.golang.org,direct \
		GOSUMDB=sum.golang.org \
		GOPRIVATE= \
		GONOPROXY= \
		GONOSUMDB= \
		GOTELEMETRY=off \
		GIT_CONFIG_NOSYSTEM=1 \
		GIT_CONFIG_GLOBAL=/dev/null \
		GIT_NO_REPLACE_OBJECTS=1 \
		GIT_TERMINAL_PROMPT=0 \
		"$go_binary" "$@"
}
(
	cd "$source_dir"
	run_go mod download all
	run_go mod verify
	run_go build -mod=readonly -trimpath -o "$output" ./cmd/jobcron-user
) || fail
chmod 500 "$output"
cleanup
trap - EXIT HUP INT TERM
