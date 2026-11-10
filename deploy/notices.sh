#!/bin/sh
# Writes THIRD_PARTY_NOTICES to stdout: the licenses of everything a build
# links besides Deneb's own code, for the GOOS and GOARCH in the
# environment: Go itself, every Go module the packages import (their
# LICENSE, COPYING, NOTICE and PATENTS files), and the extra files -f
# names (wintun.dll's license). Deneb's own license is LICENSE at the
# root of the repository; packages ship both.
#
#   GOOS=windows GOARCH=amd64 deploy/notices.sh [-tags denebtest] \
#     [-f "Wintun 0.14.1 (wintun.dll)=$HOME/deneb-deps/wintun-0.14.1/wintun/LICENSE.txt"] \
#     ./cmd/denebd [./cmd/deneb] > THIRD_PARTY_NOTICES.txt
set -eu
root=$(cd "$(dirname "$0")/.." && pwd)
tags=
extra=
nl='
'
while [ $# -gt 0 ]; do
	case $1 in
	-tags) tags=$2; shift 2 ;;
	-f) extra="$extra$2$nl"; shift 2 ;;
	*) break ;;
	esac
done
[ $# -gt 0 ] || { echo "usage: $0 [-tags t] [-f 'name=file']... package..." >&2; exit 2; }

rule='================================================================================'
section() { # title, then the files' texts
	title=$1
	shift
	printf '\n%s\n%s\n%s\n' "$rule" "$title" "$rule"
	for f in "$@"; do
		[ $# -gt 1 ] && printf '\n--- %s ---\n' "$(basename "$f")"
		printf '\n'
		cat "$f"
	done
}
licenses() { # the license files at the root of a module's directory
	find "$1" -maxdepth 1 -type f \( -iname 'LICENSE*' -o -iname 'LICENCE*' -o -iname 'COPYING*' -o -iname 'NOTICE*' -o -name PATENTS \) | sort
}

printf 'Deneb (%s/%s) includes the software below, each under its own license.\n' \
	"$(cd "$root" && go env GOOS)" "$(cd "$root" && go env GOARCH)"
printf 'Deneb itself is under the PolyForm Noncommercial License 1.0.0: see LICENSE.\n'

goroot=$(cd "$root" && go env GOROOT)
section "Go $(cd "$root" && go env GOVERSION | sed 's/^go//') (the runtime and standard library)" "$goroot/LICENSE"

# path version dir, for every non-main module the packages import.
(cd "$root" && go list -deps ${tags:+-tags "$tags"} \
	-f '{{with .Module}}{{if not .Main}}{{.Path}} {{.Version}} {{with .Replace}}{{.Dir}}{{else}}{{.Dir}}{{end}}{{end}}{{end}}' "$@") |
	sort -u | while read -r path version dir; do
	[ -n "$path" ] || continue
	files=$(licenses "$dir")
	if [ -z "$files" ]; then
		echo "notices: no license file in $path $version ($dir)" >&2
		exit 1
	fi
	# shellcheck disable=SC2086 # one file per line, no spaces in module cache paths
	section "$path $version" $files
done

printf '%s' "$extra" | while IFS= read -r spec; do
	[ -n "$spec" ] || continue
	section "${spec%%=*}" "${spec#*=}"
done
