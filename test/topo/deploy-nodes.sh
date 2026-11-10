#!/usr/bin/env bash
# deploy-nodes.sh: move the three real test machines' engines between M1 and
# M2 (test builds, user-space mode, the production control server).
#
#   test/topo/deploy-nodes.sh prepare               build M2 and copy it next to the M1 engines; nothing restarts
#   test/topo/deploy-nodes.sh swap [build|nas|desktop ...]      stop M1, start M2 (seconds of downtime each)
#   test/topo/deploy-nodes.sh rollback [build|nas|desktop ...]  stop M2, start M1 again
#   test/topo/deploy-nodes.sh previous [build|nas|desktop ...]  back to what ran when the last prepare ran
#   test/topo/deploy-nodes.sh status                which engine each machine runs
# With no machine named, swap and rollback do all three.
#
# Run swap only after telling the main session (plan: control server, then
# these engines, then this Mac's, then the client). Each machine keeps its
# state (same device, still logged in) and the exact flags M1 ran with:
#   build    hk-155    Docker deneb-node, host network, UDP 41720
#   nas      hk-spare  Docker deneb-node, host network, UDP 41721
#   desktop  Windows   %USERPROFILE%\deneb\denebd.exe via start.ps1, UDP 41722
# Nothing here touches the servers' networking: the container runs with the
# host network exactly as before, on the same high port.
set -euo pipefail
cd "$(dirname "$0")/../.."
OUT=test/topo/.accept/m2-nodes
WIN=(ssh -o BatchMode=yes -o ConnectTimeout=15 -o ProxyCommand=none -o IdentitiesOnly=yes -i "$HOME/.ssh/id_ed25519" Administrator@100.122.56.85)
WINSCP=(scp -q -o BatchMode=yes -o ConnectTimeout=15 -o ProxyCommand=none -o IdentitiesOnly=yes -i "$HOME/.ssh/id_ed25519")
SERVERS=(hk-155 hk-spare)

prepare() {
	local v
	v="m2-$(date +%m%d%H%M)"
	mkdir -p "$OUT"
	echo "== build $v (test build)"
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags denebtest -ldflags "-X main.version=$v" -o "$OUT/denebd" ./cmd/denebd
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -tags denebtest -ldflags "-X main.version=$v" -o "$OUT/denebd.exe" ./cmd/denebd
	(cd "$OUT" && COPYFILE_DISABLE=1 tar -czf denebd-m2-win.tgz denebd.exe)
	echo "$v" >"$OUT/version"
	for h in "${SERVERS[@]}"; do
		echo "== $h: image deneb-node:m2"
		scp -q "$OUT/denebd" "$h":/tmp/deneb-node/denebd-m2
		# What runs now becomes "previous" (prepare twice without a swap
		# in between still keeps the one running).
		ssh "$h" 'cd /tmp/deneb-node && docker tag "$(docker inspect deneb-node --format "{{.Image}}")" deneb-node:m2-prev &&
			mkdir -p m2 && mv denebd-m2 m2/denebd && cp ca-certificates.crt m2/ &&
			printf "FROM scratch\nCOPY denebd /denebd\nCOPY ca-certificates.crt /etc/ssl/certs/ca-certificates.crt\nENTRYPOINT [\"/denebd\"]\n" > m2/Dockerfile &&
			docker build -q -t deneb-node:m2 m2 >/dev/null && echo "     built"'
	done
	echo "== desktop: upload (slow over Tailscale, a few minutes)"
	"${WINSCP[@]}" "$OUT/denebd-m2-win.tgz" 'Administrator@100.122.56.85:deneb/denebd-m2-win.tgz'
	"${WIN[@]}" 'cd /d %USERPROFILE%\deneb && (if not exist m2 mkdir m2) & (copy /y denebd.exe m2\denebd-prev.exe >nul) & tar -xzf denebd-m2-win.tgz -C m2 && dir m2\denebd.exe | findstr denebd'
	echo "prepared $v; next: $0 swap"
}

# recreate <host> <image>: the same container, flags and state volume, on
# another image.
recreate() {
	local h=$1 img=$2
	ssh "$h" "set -e
		args=\$(docker inspect deneb-node --format '{{json .Args}}')
		echo \"\$args\" > /tmp/deneb-node/args.json
		python3 -c 'import json,shlex,sys; print(\" \".join(shlex.quote(a) for a in json.load(open(\"/tmp/deneb-node/args.json\"))))' > /tmp/deneb-node/args.sh
		docker rm -f deneb-node >/dev/null
		eval docker run -d --name deneb-node --network host --restart unless-stopped --cpus 1 --memory 256m \
			-v deneb-node-state:/var/lib/deneb $img \$(cat /tmp/deneb-node/args.sh) >/dev/null
		sleep 3
		docker logs deneb-node 2>&1 | grep -m1 'denebd ' || docker logs deneb-node 2>&1 | tail -3"
}

swap_desktop() { # $1 = from (m1|m2|prev: to the M2 before the last prepare)
	if [ "$1" = prev ]; then
		"${WIN[@]}" 'cd /d %USERPROFILE%\deneb && taskkill /im denebd.exe /f >nul 2>&1 & ping -n 3 127.0.0.1 >nul & copy /y m2\denebd-prev.exe denebd.exe >nul && powershell -NoProfile -ExecutionPolicy Bypass -File start.ps1'
	elif [ "$1" = m1 ]; then
		# ping as a 2 s sleep: the killed process lets go of the .exe a moment later
		"${WIN[@]}" 'cd /d %USERPROFILE%\deneb && taskkill /im denebd.exe /f >nul 2>&1 & ping -n 3 127.0.0.1 >nul & (if not exist denebd-m1.exe copy /y denebd.exe denebd-m1.exe >nul) & copy /y m2\denebd.exe denebd.exe >nul && powershell -NoProfile -ExecutionPolicy Bypass -File start.ps1'
	else
		"${WIN[@]}" 'cd /d %USERPROFILE%\deneb && taskkill /im denebd.exe /f >nul 2>&1 & ping -n 3 127.0.0.1 >nul & copy /y denebd-m1.exe denebd.exe >nul && powershell -NoProfile -ExecutionPolicy Bypass -File start.ps1'
	fi
}

status() {
	for h in "${SERVERS[@]}"; do
		printf '%-9s ' "$h"
		ssh "$h" "docker inspect deneb-node --format '{{.Config.Image}} {{.State.Status}}'; docker exec deneb-node /denebd status --json 2>/dev/null | python3 -c 'import json,sys; s=json.load(sys.stdin); print(\"          \", s[\"engine\"][\"version\"], \"loggedIn=%s\" % s.get(\"loggedIn\"))'"
	done
	printf '%-9s ' desktop
	"${WIN[@]}" 'cd /d %USERPROFILE%\deneb && denebd.exe status --json' 2>/dev/null |
		python3 -c 'import json,sys; s=json.load(sys.stdin); print(s["engine"]["version"], "loggedIn=%s" % s.get("loggedIn"))' || echo "no answer"
}

host_of() { case $1 in build) echo hk-155 ;; nas) echo hk-spare ;; esac; }

cmd=${1:-}
shift || true
[ $# -eq 0 ] && set -- build nas desktop
case "$cmd" in
prepare) prepare ;;
swap)
	[ -f "$OUT/version" ] || { echo "run $0 prepare first"; exit 1; }
	echo "== swap to $(cat "$OUT/version"): $*"
	for m in "$@"; do
		echo "-- $m"
		case $m in
		build | nas) recreate "$(host_of "$m")" deneb-node:m2 ;;
		desktop) swap_desktop m1 ;;
		*) echo "unknown machine $m"; exit 2 ;;
		esac
	done
	sleep 5; status
	;;
rollback)
	echo "== back to M1: $*"
	for m in "$@"; do
		echo "-- $m"
		case $m in
		build | nas) recreate "$(host_of "$m")" deneb-node:m1 ;;
		desktop) swap_desktop m2 ;;
		*) echo "unknown machine $m"; exit 2 ;;
		esac
	done
	sleep 5; status
	;;
previous)
	echo "== back to the previous M2: $*"
	for m in "$@"; do
		echo "-- $m"
		case $m in
		build | nas) recreate "$(host_of "$m")" deneb-node:m2-prev ;;
		desktop) swap_desktop prev ;;
		*) echo "unknown machine $m"; exit 2 ;;
		esac
	done
	sleep 5; status
	;;
status) status ;;
*) sed -n 2,18p "$0"; exit 2 ;;
esac
