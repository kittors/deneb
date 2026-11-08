"""tickread.py: read Deneb test tick streams the way an ordinary program
would, through the system's resolver and routes, and report each one.

    python3 -I test/topo/tickread.py <seconds> <host>[:port] ...

A test engine's tick service (port 7000) writes "<seq> <unix ms>" every
100 ms. For each host this prints one JSON object per line:
{"host", "lines", "maxGapMs", "seqGaps", "broken", "err"}; broken means the
stream stopped (closed, or silent for 3 s) before the time was up.
"""
import json
import socket
import sys
import threading
import time


def read(target, seconds, out):
    host, _, port = target.partition(":")
    r = {"host": host, "lines": 0, "maxGapMs": 0, "seqGaps": 0, "broken": False, "err": ""}
    try:
        s = socket.create_connection((host, int(port or 7000)), timeout=5)
        s.settimeout(3)
        f = s.makefile("r")
        end = time.time() + seconds
        last = None
        prev = None
        while time.time() < end:
            line = f.readline()
            if not line:
                r["broken"], r["err"] = True, "closed"
                break
            now = time.time()
            if last is not None:
                r["maxGapMs"] = max(r["maxGapMs"], round((now - last) * 1000))
            last = now
            seq = int(line.split()[0])
            if prev is not None and seq != prev + 1:
                r["seqGaps"] += 1
            prev = seq
            r["lines"] += 1
        s.close()
    except Exception as e:  # a timeout, a refused or unreachable peer
        r["broken"], r["err"] = True, str(e)
    out.append(r)


def main():
    seconds = float(sys.argv[1])
    out = []
    threads = [threading.Thread(target=read, args=(t, seconds, out)) for t in sys.argv[2:]]
    for t in threads:
        t.start()
    for t in threads:
        t.join()
    for r in sorted(out, key=lambda r: r["host"]):
        print(json.dumps(r, ensure_ascii=False))


if __name__ == "__main__":
    main()
