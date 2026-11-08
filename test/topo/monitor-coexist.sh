#!/bin/bash
# monitor-coexist.sh: acceptance item 12. While Deneb runs on this Mac,
# watch what it must not disturb, about once every 2 s:
#   - the Internet through Clash (an overseas URL) and a domestic one;
#   - Tailscale's routes: a switch from its one 100.64/10 route to per-peer
#     /32 routes would mean Tailscale reacted to Deneb (multi-lan-tech §3.3);
#   - a live SSH session over Tailscale to the Windows test machine that
#     prints a heartbeat every 0.5 s, stamped on arrival here.
# It only reads; it changes nothing on the machine.
#
#   test/topo/monitor-coexist.sh [out-dir]   (default test/topo/.run/coexist)
#   stop: pkill -f monitor-coexist
#   then: test/topo/monitor-coexist.sh --summary [out-dir] [from to]
#   (from, to: Unix seconds, to sum up one stretch, such as the minutes a
#   kernel-mode utun was up; kernel-round.sh prints them)
set -u
out=${1:-test/topo/.run/coexist}
summary=0
if [ "${1:-}" = --summary ]; then
	summary=1
	out=${2:-test/topo/.run/coexist}
	from=${3:-0}
	to=${4:-9999999999}
fi

if [ $summary = 1 ]; then
	http=$(mktemp)
	sshl=$(mktemp)
	trap 'rm -f "$http" "$sshl"' EXIT
	awk -v f="$from" -v t="$to" '$1 >= f && $1 < t' "$out/http.log" >"$http"
	awk -v f="$from" -v t="$to" '$1 >= f && $1 < t' "$out/ssh.log" >"$sshl"
	echo "samples: $(wc -l <"$http")"
	echo "google failures: $(awk '$2 != "google=204"' "$http" | wc -l)"
	echo "baidu failures:  $(awk '$4 != "baidu=200"' "$http" | wc -l)"
	echo "tailscale route changes: $(awk '{print $6, $7}' "$http" | uniq | wc -l) distinct states: $(awk '{print $6, $7}' "$http" | sort -u | tr '\n' ';')"
	# Longest silence of the SSH heartbeat, from arrival stamps.
	awk 'NR > 1 { d = $1 - p; if (d > m) { m = d; at = $1 } } { p = $1 } END { printf "ssh heartbeat: %d lines, longest gap %.2f s (ending %.3f)\n", NR, m, at }' "$sshl"
	exit 0
fi

mkdir -p "$out"
ts() { python3 -c 'import time; print(f"{time.time():.3f}")'; }
# Tailscale's interface: the utun holding a 100.64/10 address.
tsif=$(ifconfig | awk '/^utun/ { sub(":", "", $1); i = $1 } /inet 100\./ && i { print i; exit }')
echo "tailscale interface: ${tsif:-none found}" | tee "$out/info.txt"

ssh -o BatchMode=yes -o ConnectTimeout=15 -o ProxyCommand=none -o IdentitiesOnly=yes -i "$HOME/.ssh/id_ed25519" Administrator@100.122.56.85 \
	'powershell -NoProfile -Command "while($true){[Console]::Out.WriteLine((Get-Date).ToString(\"HH:mm:ss.fff\")); [Console]::Out.Flush(); Start-Sleep -Milliseconds 500}"' </dev/null 2>&1 |
	python3 -u -c 'import sys, time
for l in sys.stdin: print(f"{time.time():.3f} {l.strip()}", flush=True)' >"$out/ssh.log" &

while :; do
	t=$(ts)
	g=$(curl -s -o /dev/null --max-time 4 -w '%{http_code} %{time_total}' https://www.google.com/generate_204)
	b=$(curl -s -o /dev/null --max-time 4 -w '%{http_code} %{time_total}' https://www.baidu.com/)
	r=0
	w=0
	if [ -n "$tsif" ]; then
		r=$(netstat -rn -f inet | awk -v i="$tsif" '$NF == i' | wc -l | tr -d ' ')
		w=$(netstat -rn -f inet | awk -v i="$tsif" '$1 == "100.64/10" && $NF == i' | wc -l | tr -d ' ')
	fi
	echo "$t google=${g% *} ${g#* } baidu=${b% *} ${b#* } ts_routes=$r ts_cgnat_route=$w" >>"$out/http.log"
	sleep 2
done
