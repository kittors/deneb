#!/usr/bin/env python3
"""Line up probe failures and mihomo warnings with the repro actions.

usage: analyze-repro.py results/raw/repro-HHMMSS
For every action (A: 20 MB through Deneb, B: 20 MB without Deneb, C: pause
studio) count probe failures in the 60 s after it starts, next to the
failure rate over the whole run.
"""
import re
import sys
from datetime import datetime

base = sys.argv[1]
probes = []
for line in open(base + "-probe.log"):
    t, name, code, secs = line.split()[:4]
    probes.append((float(t), name, code, float(secs)))
actions = []
for line in open(base + "-actions.log"):
    t, rest = line.split(" ", 1)
    actions.append((float(t), rest.strip()))
warns = []
try:
    for line in open(base + "-mihomo-warn.log"):
        t, rest = line.split(" ", 1)
        warns.append((float(t), rest.strip()))
except FileNotFoundError:
    pass

def hhmmss(t):
    return datetime.fromtimestamp(t).strftime("%H:%M:%S")

def fails(a, b, name):
    rs = [p for p in probes if a <= p[0] < b and p[1] == name]
    bad = [p for p in rs if p[2] not in ("204", "200")]
    return len(bad), len(rs), bad

t0, t1 = probes[0][0], probes[-1][0]
for name in ("google", "baidu"):
    n_bad, n, _ = fails(t0, t1 + 1, name)
    print(f"whole run {hhmmss(t0)}-{hhmmss(t1)}: {name} {n_bad}/{n} failed")

print("\nper action (60 s after it starts):")
for t, what in actions:
    if not re.match(r"[ABC]:", what):
        continue
    g_bad, g_n, bad = fails(t, t + 60, "google")
    b_bad, b_n, _ = fails(t, t + 60, "baidu")
    w = [x for x in warns if t <= x[0] < t + 60]
    print(f"  {hhmmss(t)} {what[:58]:58} google {g_bad}/{g_n} failed, baidu {b_bad}/{b_n}, mihomo warnings {len(w)}")
    for p in bad:
        print(f"      google fail at +{p[0]-t:4.1f}s code={p[2]} {p[3]:.1f}s")

print("\nall google failures:")
for p in probes:
    if p[1] == "google" and p[2] != "204":
        print(f"  {hhmmss(p[0])} code={p[2]} {p[3]:.1f}s")

print("\nmihomo warnings mentioning Hy2/google/timeout (first 15):")
shown = 0
for t, w in warns:
    if re.search(r"Hy2|hysteria|google|timeout|deadline", w, re.I):
        print(f"  {hhmmss(t)} {w[:200]}")
        shown += 1
        if shown >= 15:
            break
print(f"  ({len(warns)} warnings in total)")
