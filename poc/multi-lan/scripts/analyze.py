#!/usr/bin/env python3
"""Summarize /ping results: loss, RTT percentiles per path, and outage windows.

usage: analyze.py ping.json [--mark name=unix_ms ...]
Pattern legend: '.' answered while sending direct, 'r' answered via relay, 'x' lost.
"""
import json, sys, statistics as st
from datetime import datetime

def ts(s):
    # Go RFC3339Nano -> seconds; trim to microseconds for Python.
    s = s.replace('Z', '+00:00')
    head, _, tail = s.partition('.')
    if tail:
        frac, sign, tz = tail[:6], '', ''
        for c in ('+', '-'):
            if c in tail:
                frac = tail.split(c)[0][:6]; tz = c + tail.split(c)[1]
        s = head + '.' + frac.ljust(6, '0') + tz
    return datetime.fromisoformat(s).timestamp()

def main():
    d = json.load(open(sys.argv[1]))
    marks = {}
    for a in sys.argv[2:]:
        if a.startswith('--mark='):
            k, v = a[7:].split('=')
            marks[k] = float(v) / 1000
    ps = d['pings']
    pat = ''.join('x' if p['rttMs'] < 0 else ('.' if p['pathAtSend'] == 'direct' else 'r') for p in ps)
    print('pattern:', pat)
    for path in ('direct', 'relay'):
        r = sorted(p['rttMs'] for p in ps if p['rttMs'] >= 0 and p['pathAtSend'] == path)
        if r:
            print(f'  {path:6}: n={len(r)} min={r[0]:.1f} p50={st.median(r):.1f} avg={st.mean(r):.1f} p95={r[max(0,int(len(r)*0.95)-1)]:.1f} max={r[-1]:.1f} ms')
    lost = [p for p in ps if p['rttMs'] < 0]
    print(f'  lost: {len(lost)}/{len(ps)}')
    # Longest run of consecutive losses = the outage a user would feel.
    best, cur, start = (0, None, None), 0, None
    for p in ps:
        if p['rttMs'] < 0:
            if cur == 0: start = ts(p['sentAt'])
            cur += 1
            if cur > best[0]: best = (cur, start, ts(p['sentAt']))
        else:
            cur = 0
    if best[0]:
        print(f'  longest outage: {best[0]} pings, {best[2]-best[1]:.2f}s of sends ({datetime.fromtimestamp(best[1]).strftime("%H:%M:%S.%f")[:-3]} .. {datetime.fromtimestamp(best[2]).strftime("%H:%M:%S.%f")[:-3]})')
    for name, t in marks.items():
        after = [p for p in ps if ts(p['sentAt']) >= t]
        first_ok = next((p for p in after if p['rttMs'] >= 0), None)
        first_relay = next((p for p in after if p['rttMs'] >= 0 and p['pathAtSend'] == 'relay'), None)
        first_direct = next((p for p in after if p['rttMs'] >= 0 and p['pathAtSend'] == 'direct'), None)
        def rel(p): return f'{ts(p["sentAt"]) - t:+.2f}s (seq {p["seq"]})' if p else 'never'
        print(f'  after {name}: first answered {rel(first_ok)}, first via relay {rel(first_relay)}, first direct {rel(first_direct)}')

main()
