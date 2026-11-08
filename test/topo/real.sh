#!/bin/bash
# real.sh: the engine line's check on the three real machines (plan §3 M1,
# step 6 of the engine brief). It needs the three nodes deployed and logged
# in: build on hk-155 (kittors), nas on hk-spare and desktop on the Windows
# test machine (阿杰).
#
# It mirrors the colima topology with build in the middle: 阿杰 makes two
# LANs, engine-test-a with desktop and engine-test-b with nas, and invites
# kittors to both; build joins both. desktop and nas share nothing, so
# neither may reach the other, not even through build. Both LANs are
# deleted at the end, so the acceptance starts from a clean state.
#
#   test/topo/real.sh            set up, check, clean up
#   test/topo/real.sh cleanup    only delete the two LANs
#   test/topo/real.sh links      print the current login links
set -u
WIN_SSH=(ssh -o BatchMode=yes -o ConnectTimeout=15 -o ProxyCommand=none -o IdentitiesOnly=yes -i "$HOME/.ssh/id_ed25519" Administrator@100.122.56.85)

# d <node> <deneb args...>: run the command line on one machine.
d() {
	local n=$1
	shift
	case $n in
	build) ssh hk-155 docker exec deneb-node /denebd "$@" ;;
	nas) ssh hk-spare docker exec deneb-node /denebd "$@" ;;
	desktop) "${WIN_SSH[@]}" "%USERPROFILE%\\deneb\\denebd.exe $*" | tr -d '\r' ;;
	esac
}

pass=0
fail=0
ok() { echo "ok   $*"; pass=$((pass + 1)); }
bad() { echo "FAIL $*"; fail=$((fail + 1)); }

addr() { d "$1" status --json | jq -r '.self.addrs[0]'; }
peers() { d "$1" status --json | jq -r '[.peers[].name] | sort | join(",")'; }
path() { d "$1" status --json | jq -r --arg p "$2" '(.peers[] | select(.name == $p) | .id) as $id | .paths[] | select(.deviceID == $id) | "\(.kind) \(.rttMs)ms \(.endpoint // .relay)"'; }

cleanup() {
	d desktop lan delete engine-test-a >/dev/null 2>&1 && echo "deleted engine-test-a"
	d nas lan delete engine-test-b >/dev/null 2>&1 && echo "deleted engine-test-b"
}

if [ "${1:-}" = cleanup ]; then
	cleanup
	exit 0
fi
if [ "${1:-}" = links ]; then
	# The current login link of each machine that is not logged in yet.
	for n in build nas desktop; do
		echo "$n: $(d $n status --json | jq -r 'if .loggedIn then "logged in" else .loginURL end')"
	done
	exit 0
fi

for n in build nas desktop; do
	if [ "$(d $n status --json | jq -r .loggedIn)" != true ]; then
		echo "$n is not logged in: run deneb login there and log in in a browser"
		exit 1
	fi
done

echo "== setting up"
d desktop lan create engine-test-a
d nas lan create engine-test-b
link_a=$(d desktop lan invite engine-test-a | grep -o 'https://[^ ]*')
link_b=$(d nas lan invite engine-test-b | grep -o 'https://[^ ]*')
d build lan join "$link_a"
d build lan join "$link_b"
d desktop service engine-test-a other tcp/7000 on --name tick >/dev/null
d nas service engine-test-b other tcp/7000 on --name tick >/dev/null
sleep 3

echo "== checks"
[ "$(peers build)" = "desktop,nas" ] && ok "build sees desktop and nas" || bad "build sees $(peers build)"
[ "$(peers desktop)" = "build" ] && ok "desktop sees only build" || bad "desktop sees $(peers desktop)"
[ "$(peers nas)" = "build" ] && ok "nas sees only build" || bad "nas sees $(peers nas)"

for p in "build desktop" "build nas" "desktop build" "nas build"; do
	set -- $p
	out=$(d $1 ping $2 -c 5 -i 300ms 2>&1)
	if echo "$out" | grep -q '收 [1-9]'; then ok "$1 -> $2: $(echo "$out" | tail -1)"; else bad "$1 -> $2: $out"; fi
done
for p in "desktop nas" "nas desktop"; do
	set -- $p
	out=$(d $1 ping $2 -c 1 2>&1)
	if echo "$out" | grep -q '找不到'; then ok "$1 -> $2: not found, as it must be"; else bad "$1 -> $2 should be not found: $out"; fi
done
sleep 4
for p in "build desktop" "build nas" "desktop build" "nas build"; do
	set -- $p
	echo "     path $1 -> $2: $(path $1 $2)"
done

nas_ip=$(addr nas)
desktop_ip=$(addr desktop)
build_ip=$(addr build)
for a in "desktop $nas_ip" "nas $desktop_ip"; do
	set -- $a
	before=$(d build debug drops | jq '.drops["in:transit-in"] // 0')
	d $1 debug attack build "$2/32" >/dev/null
	got=$(d $1 debug ping-addr "$2" | jq .received)
	d $1 debug unattack build >/dev/null
	after=$(d build debug drops | jq '.drops["in:transit-in"] // 0')
	if [ "$got" = 0 ] && [ "$after" -gt "$before" ]; then ok "$1 cannot reach $2 through build (build dropped $((after - before)))"; else bad "transit from $1 to $2: received=$got drops $before -> $after"; fi
done

since=$(date -u +%Y-%m-%dT%H:%M:%SZ)
d build debug inject "$desktop_ip" "$nas_ip" 80 >/dev/null
sleep 2
if ssh hk-spare "docker logs --since $since deneb-node 2>&1" | grep -q 'disallowed source'; then ok "nas's WireGuard dropped a packet forged as desktop"; else bad "nas did not log the forged packet"; fi

echo "== UDP between build and desktop blocked for 8 s, with a long connection open"
(d build debug tick desktop 20 >/tmp/real-tick.json) &
sleep 3
d build debug block desktop >/dev/null
t0=$(date +%s)
for i in $(seq 20); do [ "$(path build desktop | cut -d' ' -f1)" = relay ] && break; sleep 0.5; done
echo "     after blocking: $(path build desktop) ($(($(date +%s) - t0))s)"
sleep 8
d build debug unblock desktop >/dev/null
for i in $(seq 20); do [ "$(path build desktop | cut -d' ' -f1)" = direct ] && break; sleep 0.5; done
echo "     after unblocking: $(path build desktop)"
wait
tick=$(cat /tmp/real-tick.json)
if [ "$(echo "$tick" | jq .broken)" = false ]; then ok "long connection survived: $tick"; else bad "long connection: $tick"; fi

echo "== two long connections at once, 15 s"
(d build debug tick desktop 15 >/tmp/real-tick-a.json) &
(d build debug tick nas 15 >/tmp/real-tick-b.json) &
wait
for f in /tmp/real-tick-a.json /tmp/real-tick-b.json; do
	if [ "$(jq .broken $f)" = false ] && [ "$(jq .lines $f)" -ge 120 ]; then ok "$(basename $f .json): $(cat $f)"; else bad "$(basename $f .json): $(cat $f)"; fi
done

echo "== cleaning up"
cleanup
echo
echo "$pass passed, $fail failed"
[ "$fail" = 0 ]
