#!/bin/sh
# DESIGN.md scene 3: turn one LAN's switch off and on while both LANs carry
# traffic. The other LAN must not lose a single packet.
# usage: scripts/test-pause.sh [lan_to_pause] [pause_at_s] [resume_at_s] [total_s]
cd "$(dirname "$0")/.."
lan=${1:-weekend}; p=${2:-8}; r=${3:-18}; total=${4:-30}
now_ms() { python3 -c 'import time;print(int(time.time()*1000))'; }
out=results/raw/pause-$lan-$(date +%H%M%S)
./scripts/api.sh mac "/tick?to=box.lin&secs=$total" > "$out-tick-lin.json" &
./scripts/api.sh mac "/ping?to=box.lin&n=$((total*10))&interval=100ms&timeout=2s" > "$out-lin.json" &
./scripts/api.sh mac "/ping?to=box.jie&n=$((total*10))&interval=100ms&timeout=2s" > "$out-jie.json" &
./scripts/api.sh mac "/ping?to=nas.jie&n=$((total*10))&interval=100ms&timeout=2s" > "$out-nas.json" &
sleep "$p"; P=$(now_ms); ./scripts/api.sh mac -X POST "/lan?id=$lan&on=0" > /dev/null
sleep $((r-p)); R=$(now_ms); ./scripts/api.sh mac -X POST "/lan?id=$lan&on=1" > /dev/null
wait
for f in lin jie nas; do
  echo "== $f ($out-$f.json)"
  python3 scripts/analyze.py "$out-$f.json" --mark=pause=$P --mark=resume=$R
done
python3 -c "
import json; d=json.load(open('$out-tick-lin.json'))
print(f\"== tcp stream to box.lin: {d['lines']} lines, max stall {d['maxGapMs']} ms, error: {d['connectionError'] or 'none'}\")"
