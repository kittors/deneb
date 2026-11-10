#!/bin/sh
# Refuses a release that holds a test build. A build with the denebtest tag
# serves the test API (/v1/test/...: inject packets, turn the filter off),
# and as the Windows service it takes unsigned updates, which would let any
# user allowed on it run a program as SYSTEM (design/plan/m7/
# engine-security.md). It reads each Go executable's build settings (`go
# version -m`) without running it; other files are skipped, and a folder
# is read whole. Exits 1, naming every test build it found.
#
#   deploy/check-release.sh FILE|FOLDER...
set -eu
bad=0
check() {
	info=$(go version -m "$1" 2>/dev/null) || return 0 # not a Go executable
	tags=$(printf '%s\n' "$info" | sed -n 's/^[[:space:]]*build[[:space:]]*-tags=\([^[:space:]]*\).*/\1/p')
	case ",$tags," in
	*,denebtest,*)
		echo "check-release: $1 is a test build (-tags=$tags): not for a release" >&2
		bad=1
		;;
	esac
}
for arg in "$@"; do
	if [ -d "$arg" ]; then
		for f in $(find "$arg" -type f); do
			check "$f"
		done
	else
		check "$arg"
	fi
done
exit $bad
