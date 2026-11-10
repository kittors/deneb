#!/bin/bash
# m2-realworld.sh: M2 acceptance item 8 done for real, with no hand on the
# keyboard, ON A SEPARATE TEST MAC (liang的MacBook Air, 2026-11-08): Wi-Fi
# really goes off and on, then the Mac really sleeps for 10 minutes and
# wakes by a scheduled wake (pmset). After each, it times how long the
# engine takes to be back: Wi-Fi up, the control server connected, all
# three test machines on a path again (direct or relay), each answering a
# ping, and fresh long-lived connections clean; and that the Internet came
# back.
#
# NEVER on the user's own Mac (Kittors的MacBook Air): it must not lose its
# network or sleep (user, 2026-11-08: "这个mac不能断网的"). The script
# refuses to switch anything off there; `check` alone is fine anywhere.
#
#   On the test Mac, from ~/deneb:
#   DENEB_BIN=~/deneb/denebd INTERNET_URL=https://www.baidu.com INTERNET_CODE=200 \
#     TAILSCALE_PEER= SUDO='sudo -n' ./m2-realworld.sh [check|net|sleep|all]
#
# Wi-Fi is the test Mac's uplink, so it is offline while Wi-Fi is off; the
# script runs on its own there (start it with nohup) and always turns Wi-Fi
# back on: at the end, on Ctrl-C and on any failure. Scheduling the wake
# needs root for pmset (a sudoers rule for pmset alone on the test Mac).
# Nothing else is changed: no routes, DNS, proxies or engine settings.
set -u
ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." 2>/dev/null && pwd)

WIFI=en0
SLEEP_SECS=${SLEEP_SECS:-600}
OFF_SECS=${OFF_SECS:-15}
PEERS=(build desktop nas)
# Overridable, so the same script runs on a separate test Mac (whose
# network may go, unlike this one's): where the engine is, where results
# go, what proves the Internet is back, whether Tailscale is checked, and
# how sudo gets the right to schedule a wake.
if [ -z "${OUT:-}" ]; then
	if [ -d "$ROOT/test/topo" ]; then
		OUT=$ROOT/test/topo/.accept/realworld-$(date +%m%d-%H%M%S)
	else
		OUT=$HOME/deneb/realworld-$(date +%m%d-%H%M%S)
	fi
fi
INTERNET_URL=${INTERNET_URL:-https://www.google.com/generate_204}
INTERNET_CODE=${INTERNET_CODE:-204}
TAILSCALE_PEER=${TAILSCALE_PEER-100.122.56.85} # empty: no Tailscale check
SUDO=${SUDO:-sudo -A}
mkdir -p "$OUT"
exec > >(tee -a "$OUT/run.log") 2>&1

D=${DENEB_BIN:-$HOME/go/bin/denebd}
ms() { perl -MTime::HiRes=time -e 'printf "%d\n", time * 1000'; }
now() { date +%T; }
say() { echo "== $(now) $*"; }

wifi_on() { networksetup -setairportpower "$WIFI" on; }

# Someone keeps using the connections, a ping to each machine every second:
# with no traffic the engine probes nothing, and neither it nor the window
# would show that the network went away and came back.
TRAFFIC=""
traffic_start() {
	(while :; do
		for n in "${PEERS[@]}"; do "$D" ping "$n" -c 1 >/dev/null 2>&1; done
		sleep 1
	done) &
	TRAFFIC=$!
}
traffic_stop() {
	[ -n "$TRAFFIC" ] && kill "$TRAFFIC" 2>/dev/null
	TRAFFIC=""
}

cleanup() {
	traffic_stop
	if [ "$(networksetup -getairportpower "$WIFI" | awk '{print $NF}')" != On ]; then
		wifi_on && echo "$(now) Wi-Fi turned back on (cleanup)"
	fi
}
trap cleanup EXIT
trap 'echo "interrupted"; exit 130' INT TERM

# poll <max ms> <cmd...>: run cmd until it succeeds; print the ms it took.
poll() {
	local max=$1 t0 t
	shift
	t0=$(ms)
	while :; do
		if "$@" >/dev/null 2>&1; then
			echo $(($(ms) - t0))
			return 0
		fi
		t=$(($(ms) - t0))
		[ $t -ge "$max" ] && return 1
		sleep 0.2
	done
}
has_ip() { [ -n "$(ipconfig getifaddr "$WIFI")" ]; }
control_up() { [ "$("$D" status --json 2>/dev/null | jq -r .controlConnected)" = true ]; }
all_paths() {
	local p
	p=$("$D" debug paths 2>/dev/null) || return 1
	for n in "${PEERS[@]}"; do
		k=$(jq -r --arg n "$n" 'to_entries[] | select(.key | startswith($n + ".")) | .value.kind' <<<"$p")
		[ "$k" = direct ] || [ "$k" = relay ] || return 1
	done
}
pings() { "$D" ping "$1" -c 1 2>&1 | grep -q '收 1'; }
internet() { [ "$(curl -s -o /dev/null -m 4 -w '%{http_code}' "$INTERNET_URL")" = "$INTERNET_CODE" ]; }
tailscale_up() { ping -c 1 -t 2 "$TAILSCALE_PEER" >/dev/null 2>&1; }

paths_line() { "$D" debug paths 2>/dev/null | jq -c '[to_entries[] | select(.key | test("^(build|desktop|nas)\\.")) | "\(.key | split(".")[0])=\(.value.kind)"]'; }

# check <what>: time the way back after an event, from the moment it ends.
check() {
	local what=$1 t
	say "back after $what"
	t=$(poll 120000 has_ip) && echo "     Wi-Fi has an address after ${t} ms" || echo "FAIL Wi-Fi has no address after 120 s"
	t=$(poll 120000 control_up) && echo "     control server connected after ${t} ms" || echo "FAIL control server not connected after 120 s"
	t=$(poll 120000 all_paths) && echo "     all three on a path after ${t} ms: $(paths_line)" || echo "FAIL not all on a path after 120 s: $(paths_line)"
	for n in "${PEERS[@]}"; do
		t=$(poll 60000 pings "$n") && echo "     $n answers a ping after ${t} ms" || echo "FAIL $n does not answer a ping in 60 s"
	done
	t=$(poll 60000 internet) && echo "     Internet ($INTERNET_URL) after ${t} ms" || echo "FAIL no Internet ($INTERNET_URL) in 60 s"
	if [ -n "$TAILSCALE_PEER" ]; then
		t=$(poll 60000 tailscale_up) && echo "     Tailscale to $TAILSCALE_PEER after ${t} ms" || echo "FAIL Tailscale does not reach $TAILSCALE_PEER in 60 s"
	fi
	say "fresh long connections, 20 s each"
	# Wait for these three only: a bare wait would also wait for the
	# traffic loop, which never ends.
	local ticks=()
	for n in "${PEERS[@]}"; do
		"$D" debug tick "$n" 20 >"$OUT/tick-$what-$n.json" 2>&1 &
		ticks+=($!)
	done
	wait "${ticks[@]}"
	for n in "${PEERS[@]}"; do echo "     $n: $(jq -c '{lines, maxGapMs, broken}' "$OUT/tick-$what-$n.json" 2>/dev/null || head -c 200 "$OUT/tick-$what-$n.json")"; done
	echo "     paths now: $(paths_line)"
}

net() {
	say "before: $(paths_line); Wi-Fi $(ipconfig getifaddr "$WIFI")"
	traffic_start
	sleep 5
	say "Wi-Fi off for $OFF_SECS s"
	networksetup -setairportpower "$WIFI" off || { echo "FAIL could not turn Wi-Fi off"; return 1; }
	echo "     $(now) off"
	sleep "$OFF_SECS"
	wifi_on || echo "FAIL could not turn Wi-Fi on"
	echo "     $(now) on"
	check net
	traffic_stop
}

sleeptest() {
	say "before: $(paths_line)"
	say "schedule a wake in $SLEEP_SECS s (password for pmset)"
	$SUDO pmset relative wake "$SLEEP_SECS" || { echo "FAIL could not schedule the wake; not sleeping"; return 1; }
	sudo -k
	local t0
	t0=$(date +%s)
	traffic_start
	sleep 5
	say "sleep now"
	pmset sleepnow
	# Wait until the clock shows the Mac slept and woke: a loop of 5 s
	# that took much longer.
	local last now
	last=$(date +%s)
	while :; do
		sleep 5
		now=$(date +%s)
		if [ $((now - last)) -gt 60 ]; then
			echo "     $(now) awake; slept about $((now - last)) s"
			break
		fi
		last=$now
		if [ $((now - t0)) -gt $((SLEEP_SECS + 600)) ]; then
			echo "FAIL the Mac never slept"
			return 1
		fi
	done
	check sleep
	traffic_stop
}

if [ "${1:-all}" != check ] && [ "$(scutil --get ComputerName 2>/dev/null)" = "Kittors的MacBook Air" ]; then
	echo "停下：这是用户自己的 Mac，不能断网、不能睡眠。只在测试 Mac 上跑 net、sleep、all。" >&2
	exit 1
fi
case "${1:-all}" in
check) check now ;; # only the measurements, with nothing switched off
net) net ;;
sleep) sleeptest ;;
all)
	net
	sleep 20
	sleeptest
	;;
esac
say "done; results in $OUT"
