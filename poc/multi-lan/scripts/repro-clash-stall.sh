#!/bin/sh
# Re-runs the two actions that preceded the "Google via Clash" timeouts in
# the kernel-TUN test, entirely in user space (no interface, route or DNS
# change on this Mac), plus a control that moves the same 20 MB over the LAN
# without Deneb. Probes Google through Clash and a domestic site every second
# and records mihomo's warnings through its local socket (GET only: nothing
# in Clash is switched, tested or reloaded).
#
# Needs: remote nodes running (scripts/start-remote.sh), the Mac node running
# in user space (scripts/start-mac.sh -bind-if en0), and a 20 MB file on the
# Windows box at deneb-poc\blob20m.bin.
cd "$(dirname "$0")/.."
. scripts/env.sh
out=results/raw/repro-$(date +%H%M%S)
SOCK=/var/run/clash-verge-service/users/501/verge-mihomo.sock
now() { python3 -c 'import time;print(f"{time.time():.3f}")'; }
mark() { echo "$(now) $*" >> "$out-actions.log"; echo "[$(date +%H:%M:%S)] $*"; }

probe() { # $1 name, $2 url
  while :; do
    t=$(now)
    r=$(curl -s -o /dev/null --max-time 4 -w '%{http_code} %{time_total}' "$2")
    echo "$t $1 $r" >> "$out-probe.log"
    sleep 1
  done
}
probe google https://www.google.com/generate_204 & P1=$!
probe baidu https://www.baidu.com/ & P2=$!
curl -sN --unix-socket "$SOCK" 'http://localhost/logs?level=warning' \
  | python3 -u -c 'import sys,time
for l in sys.stdin: print(f"{time.time():.3f} {l.strip()}", flush=True)' > "$out-mihomo-warn.log" &
P3=$!
trap 'kill $P1 $P2 $P3 2>/dev/null; pkill -f "logs[?]level=warning" 2>/dev/null' EXIT

quiet() { mark "quiet $1s"; sleep "$1"; }
pull_deneb() {
  mark "A: 20MB from box.jie through Deneb (LAN direct path)"
  ./scripts/api.sh mac "/pull?to=box.jie&bytes=20000000" | python3 -c 'import json,sys;d=json.load(sys.stdin);print("   ", d.get("pathAtStart"), round(d.get("mbps",0),1), "Mbps")'
  mark "A done"
}
pull_plain() {
  mark "B: 20MB from the Windows box over the LAN with scp, no Deneb"
  scp -q -o BatchMode=yes -o ProxyCommand=none -o IdentitiesOnly=yes -i "$HOME/.ssh/id_ed25519" \
    -o UserKnownHostsFile=/tmp/denebpoc/known_hosts -o StrictHostKeyChecking=accept-new \
    "Administrator@192.168.1.17:deneb-poc/blob20m.bin" /tmp/denebpoc/blob20m.bin
  mark "B done ($(stat -f %z /tmp/denebpoc/blob20m.bin 2>/dev/null) bytes)"
}
pause_studio() {
  mark "C: pause studio"
  ./scripts/api.sh mac -X POST "/lan?id=studio&on=0" > /dev/null
  sleep 10
  ./scripts/api.sh mac -X POST "/lan?id=studio&on=1" > /dev/null
  mark "C done (studio resumed)"
}

quiet 60
for round in 1 2; do
  pull_deneb; quiet 60
  pull_plain; quiet 60
  pause_studio; quiet 60
done
mark "end"
echo "$out"
