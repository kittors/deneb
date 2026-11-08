#!/bin/sh
# Watches the things the kernel-TUN test must not disturb, once per ~2 s:
#   - internet through Clash (an overseas URL) and a domestic URL,
#   - Tailscale's routes (a switch from one 100.64/10 route to per-peer /32
#     routes would mean Tailscale reacted to our interface),
#   - a live SSH session over Tailscale that prints a heartbeat every 0.5 s.
# Output: results/raw/mon-*.log. Stop with: pkill -f monitor-coexist
cd "$(dirname "$0")/.."
. scripts/env.sh
out=results/raw
ts() { python3 -c 'import time;print(f"{time.time():.3f}")'; }
# SSH heartbeat over Tailscale; each line is stamped on arrival on this Mac.
ssh $WIN_OPTS "$WIN_HOST" 'powershell -NoProfile -Command "while($true){[Console]::Out.WriteLine((Get-Date).ToString(\"HH:mm:ss.fff\")); [Console]::Out.Flush(); Start-Sleep -Milliseconds 500}"' < /dev/null 2>&1 \
  | python3 -u -c 'import sys,time
for l in sys.stdin: print(f"{time.time():.3f} {l.strip()}", flush=True)' > $out/mon-ts-ssh.log &
while :; do
  t=$(ts)
  g=$(curl -s -o /dev/null --max-time 4 -w '%{http_code} %{time_total}' https://www.google.com/generate_204)
  b=$(curl -s -o /dev/null --max-time 4 -w '%{http_code} %{time_total}' https://www.baidu.com/)
  r=$(netstat -rn -f inet | awk '$NF=="utun8"' | wc -l | tr -d ' ')
  w=$(netstat -rn -f inet | awk '$1=="100.64/10" && $NF=="utun8"' | wc -l | tr -d ' ')
  echo "$t google=$g baidu=$b ts_routes=$r ts_cgnat_route=$w" >> $out/mon-http.log
  sleep 2
done
