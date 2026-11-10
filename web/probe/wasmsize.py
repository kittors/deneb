# Sum wasm code bytes per Go package using the "name" custom section.
import sys, collections

def leb(b, i):
    r = s = 0
    while True:
        x = b[i]; i += 1
        r |= (x & 0x7f) << s; s += 7
        if x < 0x80: return r, i

def main(path, top=40):
    b = open(path, 'rb').read()
    i = 8; nimports = 0; bodies = []; names = {}; secs = collections.Counter()
    while i < len(b):
        sid = b[i]; i += 1
        size, i = leb(b, i); end = i + size
        if sid == 2:
            n, j = leb(b, i)
            for _ in range(n):
                l, j = leb(b, j); j += l
                l, j = leb(b, j); j += l
                kind = b[j]; j += 1
                if kind == 0: _, j = leb(b, j); nimports += 1
                elif kind == 1: j += 1; fl, j = leb(b, j); _, j = leb(b, j); j = leb(b, j)[1] if fl & 1 else j
                elif kind == 2: fl, j = leb(b, j); _, j = leb(b, j); j = leb(b, j)[1] if fl & 1 else j
                elif kind == 3: j += 2
        if sid == 10:
            n, j = leb(b, i)
            for _ in range(n):
                l, j = leb(b, j); bodies.append(l); j += l
        if sid == 0:
            l, j = leb(b, i); nm = b[j:j+l].decode(); j += l
            secs['custom:' + nm] += size
            if nm == 'name':
                while j < end:
                    sub = b[j]; j += 1
                    sl, j = leb(b, j); se = j + sl
                    if sub == 1:
                        n, j = leb(b, j)
                        for _ in range(n):
                            idx, j = leb(b, j); l, j = leb(b, j)
                            names[idx] = b[j:j+l].decode('utf8', 'replace'); j += l
                    j = se
        else:
            secs[{1:'type',2:'import',3:'func',5:'mem',6:'global',7:'export',10:'code',11:'data'}.get(sid, str(sid))] += size
        i = end
    import re
    pk = collections.Counter()
    for k, l in enumerate(bodies):
        n = names.get(nimports + k, '?')
        m = re.match(r'^[a-z0-9-]+(\.[a-z0-9-]+)+_', n)
        dot = n.find('.', m.end() if m else 0)
        pk[(n[:dot] if dot > 0 else n).replace('_', '/')] += l
    print('sections:', {k: round(v / 1e6, 2) for k, v in secs.items()})
    groups = collections.Counter()
    for p, v in pk.items():
        g = p
        for pre in ('github.com/egoist/mygo/ui', 'github.com/egoist/mygo', 'github.com/kittors/deneb/engine', 'github.com/kittors/deneb', 'golang.org/x', 'github.com/go-text', 'crypto', 'net/http', 'vendor', 'internal', 'runtime', 'reflect', 'encoding', 'unicode', 'image', 'text', 'compress'):
            if p == pre or p.startswith(pre + '/') or p.startswith(pre + '.'):
                g = pre; break
        groups[g] += v
    print('code by group (MB):')
    for g, v in groups.most_common(top): print(f'  {v/1e6:6.2f}  {g}')
    if len(sys.argv) > 2:
        print('code by package (MB):')
        for p, v in pk.most_common(int(sys.argv[2])): print(f'  {v/1e6:6.3f}  {p}')

main(sys.argv[1])
