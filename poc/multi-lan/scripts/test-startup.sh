#!/bin/sh
# Restart this Mac's node and ping both LANs from the first instant, to see
# "usable first, fast later": answers via the relay before a direct path
# exists, then the switch to direct.
# usage: scripts/test-startup.sh <label> [node args...]   e.g. test-startup.sh bound -bind-if en0
cd "$(dirname "$0")/.."
label=$1; shift
now_ms() { python3 -c 'import time;print(int(time.time()*1000))'; }
pkill -f 'deneb-poc node -config configs/mac.me.json'; sleep 1
out=results/raw/startup-$label-$(date +%H%M%S)
S=$(now_ms)
./scripts/start-mac.sh "$@" > "$out-node.log" 2>&1 &
# Wait only for the local API socket, not for any network state.
until curl -s -o /dev/null http://127.0.0.1:47777/status; do sleep 0.05; done
for peer in box.lin box.jie nas.jie; do
  ./scripts/api.sh mac "/ping?to=$peer&n=100&interval=100ms&timeout=3s" > "$out-$peer.json" &
done
# Only the three pings are waited for; the node keeps running.
wait $(jobs -p | tail -3) 2>/dev/null; sleep 1
for peer in box.lin box.jie nas.jie; do echo "== $peer"; python3 scripts/analyze.py "$out-$peer.json" --mark=start=$S | sed -n '1,4p;$p'; done
echo "== node log"; grep -E 'stun|relay|path|switch' "$out-node.log" | head -8
