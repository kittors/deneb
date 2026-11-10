#!/usr/bin/env bash
# Runs `go test` on the packages given (default ./...), then once more the
# tests that failed, alone. A few of the engine's tests depend on timing
# (a lossy line, idle timers: design/plan/m2/engine.md, "已知的限制") and
# now and then fail on a busy machine. A test that passes the second time
# leaves a warning on the run, naming it, and the step passes; one that
# fails again fails it. Nothing is retried when a package failed without
# naming a test (it did not build, panicked outside a test, or timed out),
# or when more than a few tests failed: that is breakage, not timing.
#
#   .github/scripts/go-test.sh [packages...]
#
# GOTESTFLAGS adds flags to both runs, e.g. GOTESTFLAGS=-timeout=8m.
# Works with the bash of macOS (3.2).
set -u
[ $# -gt 0 ] || set -- ./...
# shellcheck disable=SC2206 # GOTESTFLAGS is a list of flags
flags=(-count=1 ${GOTESTFLAGS:-})
log=$(mktemp)
trap 'rm -f "$log"' EXIT

go test "${flags[@]}" "$@" 2>&1 | tee "$log"
status=${PIPESTATUS[0]}
[ "$status" -eq 0 ] && exit 0

# One line per failed package: "<package> <Test1|Test2>", or "<package> -"
# when it named no failed test. go test prints each package's output in
# one piece, its "--- FAIL: TestX" lines (top-level tests: no indent)
# before its "FAIL<tab><package>" line.
failed=$(awk '
	/^--- FAIL: / { tests = tests (tests == "" ? "" : "|") $3; next }
	$1 == "FAIL" && NF >= 2 {
		if (tests == "" || $3 ~ /^\[/) print $2, "-"; else print $2, tests
		tests = ""
		next
	}
	$1 == "ok" { tests = "" }
' "$log")
if [ -z "$failed" ]; then
	exit "$status"
fi
if printf '%s\n' "$failed" | grep -q ' -$'; then
	echo "go-test: not retrying: a package failed without naming a test:" >&2
	printf '%s\n' "$failed" | grep ' -$' >&2
	exit "$status"
fi
n=$(printf '%s\n' "$failed" | cut -d' ' -f2 | tr '|' '\n' | grep -c .)
if [ "$n" -gt 3 ]; then
	echo "go-test: $n tests failed: not retrying" >&2
	exit "$status"
fi

echo
echo "go-test: running the $n failed test(s) again, alone"
again=0
while read -r pkg tests; do
	go test "${flags[@]}" -run "^(${tests})\$" "$pkg" || again=1
done <<EOF
$failed
EOF
if [ "$again" -ne 0 ]; then
	echo "go-test: failed again" >&2
	exit 1
fi
while read -r pkg tests; do
	echo "::warning title=Flaky test::${pkg}: ${tests//|/, } failed, then passed when run again alone"
done <<EOF
$failed
EOF
exit 0
