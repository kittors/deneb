#!/bin/sh
# Direct -> relay -> direct on one peer while a ping train (every 100 ms) and
# one long-lived TCP stream run. "Blocking" drops all UDP between this Mac and
# the peer inside the prototype (no firewall is touched), like a network that
# suddenly starts dropping UDP.
# usage: scripts/test-switch.sh box.lin [block_after_s] [unblock_after_s] [total_s]
cd "$(dirname "$0")/.."
peer=${1:-box.lin}; b=${2:-10}; u=${3:-30}; total=${4:-50}
now_ms() { python3 -c 'import time;print(int(time.time()*1000))'; }
out=results/raw/switch-$peer-$(date +%H%M%S)
./scripts/api.sh mac "/tick?to=$peer&secs=$total" > "$out-tick.json" &
./scripts/api.sh mac "/ping?to=$peer&n=$((total*10))&interval=100ms&timeout=2s" > "$out-ping.json" &
sleep "$b"; B=$(now_ms); ./scripts/api.sh mac -X POST "/block?peer=$peer&on=1" > /dev/null
sleep $((u-b)); U=$(now_ms); ./scripts/api.sh mac -X POST "/block?peer=$peer&on=0" > /dev/null
wait
echo "== $out"
python3 scripts/analyze.py "$out-ping.json" --mark=block=$B --mark=unblock=$U
python3 - "$out-tick.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
print(f"  tcp stream: connect {d['connectMs']} ms, {d['lines']} lines, seq holes {d['seqHoles']}, "
      f"max stall {d['maxGapMs']} ms, connection error: {d['connectionError'] or 'none'}")
for s in d['stalls'] or []:
    open_ = ' (still stalled when the test ended)' if s.get('stillStalledAtEnd') else ''
    print(f"    stall {s['gapMs']} ms from {s['at'][11:23]}{open_}")
for e in d['events'] or []:
    if e['kind'] in ('switch', 'test', 'path'):
        print(f"    {e['t'][11:23]} [{e['kind']}] {e.get('peer','')}: {e['detail']}")
PY
