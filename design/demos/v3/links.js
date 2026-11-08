// Connections (DESIGN.md §3.5): a faint track with light flowing in it.
//
// The track is a 3px line in a barely-there grey. Along it run short beams
// of light whose color shades from one owner's color to the other's as
// they travel, with a bright head and a fading tail. Through a relay a beam
// stops at the relay for a moment, then goes on. A link that is still
// trying is a row of small dots drifting along; a link that cannot connect
// is the same dots, still. Plain Canvas 2D strokes and gradients; in MyGo
// the same is StrokePathGradient over Path.Trim(s0, s1) in a Draw callback.
(function () {
  const N = (window.Net = window.Net || {});
  const qAt = (a, c, b, t) => {
    const s = 1 - t;
    return { x: s * s * a.x + 2 * s * t * c.x + t * t * b.x, y: s * s * a.y + 2 * s * t * c.y + t * t * b.y };
  };
  const hex = (h) => {
    const n = parseInt(h.slice(1), 16);
    return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
  };
  const mixc = (a, b, k) => a.map((v, i) => Math.round(v + (b[i] - v) * k));
  const rgba = (c, a) => `rgba(${c[0]},${c[1]},${c[2]},${Math.max(0, Math.min(1, a)).toFixed(3)})`;
  const rand = (a, b) => a + Math.random() * (b - a);
  N.hex = hex;
  N.rgba = rgba;
  N.mixc = mixc;

  // The control point of a curve from its ends and its bend: the bend
  // pushes the middle sideways by a share of the length.
  const ctl = (a, b, bend) => {
    const dx = b.x - a.x, dy = b.y - a.y;
    return { x: (a.x + b.x) / 2 - dy * bend, y: (a.y + b.y) / 2 + dx * bend };
  };
  N.ctl = ctl;
  N.qAt = qAt;

  // One quadratic curve, measured so positions go by length, not by t.
  class Seg {
    constructor(a, b, c) {
      this.a = a;
      this.b = b;
      this.c = c;
      this.pts = [];
      this.cum = [0];
      const n = 48;
      for (let i = 0; i <= n; i++) this.pts.push(qAt(a, this.c, b, i / n));
      for (let i = 1; i <= n; i++) this.cum.push(this.cum[i - 1] + Math.hypot(this.pts[i].x - this.pts[i - 1].x, this.pts[i].y - this.pts[i - 1].y));
      this.len = this.cum[n];
    }
    at(s) {
      s = Math.max(0, Math.min(this.len, s));
      let i = 1;
      while (i < this.cum.length - 1 && this.cum[i] < s) i++;
      const k = (s - this.cum[i - 1]) / (this.cum[i] - this.cum[i - 1] || 1);
      const p = this.pts[i - 1], q = this.pts[i];
      return { x: p.x + (q.x - p.x) * k, y: p.y + (q.y - p.y) * k };
    }
  }

  // Soft rope (DESIGN.md §6.2): a link's ends stay on its nodes, but its
  // middle (each curve's control point) follows on a spring. A node being
  // dragged pulls the curve along a moment late and it sways once when the
  // node stops; a change of bend (to get around a node) eases over instead
  // of flipping. In MyGo: the control point is a Spring animated value.
  // A touch of give, not a wobble: a bouncier rope reads as jitter.
  const ROPE = { duration: 420, bounce: 0.08 };

  class Link {
    constructor(o) {
      this.o = o;
      // on: track and light; dash: dots (moving while `trying`); grow:
      // track drawn out from both ends (or one end); retract: pulled back.
      this.state = o.state || 'on';
      this.grow = 1;
      this.gfrom = o.growFrom || 'both';
      this.trying = !!o.trying;
      this.flow = o.flow !== false;
      this.alpha = 1;
      this.alphaTo = 1;
      this.beams = [];
      this.next = [D.now() + rand(0, 700), D.now() + rand(300, 1200)];
      this.build();
    }
    build() {
      const { a, b, via, bend = 0.08 } = this.o;
      const ends = via ? [[a, via], [via, b]] : [[a, b]];
      if (!this.ropes || this.ropes.length !== ends.length) this.ropes = ends.map(() => null);
      this.segs = ends.map(([p, q], i) => {
        const t = ctl(p, q, bend);
        if (this.o.soft === false) return new Seg(p, q, t);
        const r = this.ropes[i];
        if (!r) {
          this.ropes[i] = new D.Spring2(t.x, t.y, ROPE);
          return new Seg(p, q, t);
        }
        r.to(t.x, t.y);
        return new Seg(p, q, r.value);
      });
      this.len = this.segs.reduce((s, g) => s + g.len, 0);
    }
    // Still swinging: the frame loop rebuilds it until it rests (within a
    // twentieth of a pixel, too slow to see), then once more exactly.
    loose() {
      if (this.o.soft === false || !this.ropes) return false;
      const now = D.now();
      const moving = this.ropes.some((r) => {
        if (!r) return false;
        const sx = r.x.sample(now), sy = r.y.sample(now);
        return Math.abs(sx.x - r.x.target) + Math.abs(sy.x - r.y.target) > 0.05 || Math.abs(sx.v) + Math.abs(sy.v) > 1;
      });
      if (!moving && this.swung) {
        this.swung = false;
        for (const r of this.ropes) if (r) r.jump(r.x.target, r.y.target);
        this.build();
      }
      if (moving) this.swung = true;
      return moving;
    }
    set(o) {
      Object.assign(this.o, o);
      this.build();
    }
    at(s) {
      for (const g of this.segs) {
        if (s <= g.len) return g.at(s);
        s -= g.len;
      }
      const g = this.segs[this.segs.length - 1];
      return g.at(g.len);
    }
    mid() {
      return this.at(this.len / 2);
    }
    colorAt(s) {
      return mixc(hex(this.o.ca), hex(this.o.cb), Math.max(0, Math.min(1, s / this.len)));
    }
  }

  class Layer {
    constructor(host, o = {}) {
      this.host = host;
      this.zoom = o.zoom || (() => 1);
      this.canvas = document.createElement('canvas');
      this.canvas.className = 'links';
      host.prepend(this.canvas);
      this.ctx = this.canvas.getContext('2d');
      this.links = [];
      this.tendrils = [];
      this.pops = [];
      this.speed = 150;
      new ResizeObserver(() => this.resize()).observe(host);
      this.resize();
      D.onFrame((now) => this.frame(now));
    }
    resize() {
      this.w = this.host.clientWidth;
      this.h = this.host.clientHeight;
      this.dpr = (window.devicePixelRatio || 1) * this.zoom();
      this.canvas.width = Math.round(this.w * this.dpr);
      this.canvas.height = Math.round(this.h * this.dpr);
      this.canvas.style.width = this.w + 'px';
      this.canvas.style.height = this.h + 'px';
    }
    add(o) {
      const l = new Link(o);
      this.links.push(l);
      return l;
    }
    remove(l) {
      this.links = this.links.filter((x) => x !== l);
    }
    // Draw the track out (from both ends to the middle, or from one end),
    // then let the light flow.
    grow(l, dur = 460, from = 'both') {
      l.state = 'grow';
      l.gfrom = from;
      l.grow = 0;
      l.beams = [];
      const t0 = D.now();
      return new Promise((res) => {
        const off = D.onFrame((now) => {
          const p = Math.min(1, (now - t0) / dur);
          l.grow = D.Ease.inOut(p);
          if (p >= 1) {
            off();
            l.state = 'on';
            l.next = [now, now + 260];
            if (l.o.onJoin) l.o.onJoin(l);
            res();
          }
        });
      });
    }
    // Pull the track back into one end ('a' or 'b'), or into the middle
    // ('center'), and drop the link.
    // glow: the retreating end carries a bright head, as a growing one
    // does, so "the light goes back" reads even on the faint track.
    retract(l, into = 'a', dur = 420, o = {}) {
      l.state = 'retract';
      l.gfrom = into;
      l.glow = !!o.glow;
      l.grow = 1;
      const t0 = D.now();
      return new Promise((res) => {
        const off = D.onFrame((now) => {
          const p = Math.min(1, (now - t0) / dur);
          l.grow = 1 - D.Ease.inOut(p);
          if (p >= 1) {
            off();
            this.remove(l);
            res();
          }
        });
      });
    }
    // One light that runs once from a to b along a soft arc, with no
    // track under it: something handed from one place to another (scene
    // 4, a shared device travelling to the person's own Deneb). Resolves
    // when the head arrives; the tail is absorbed into b after.
    shoot(a, b, o = {}) {
      const l = this.add({ a, b, bend: o.bend == null ? 0.2 : o.bend, ca: o.ca, cb: o.cb || o.ca });
      l.flow = false;
      l.track = false;
      l.speed = o.speed || 560;
      return new Promise((res) => {
        // Bolder than a link's own light: it crosses open space, between
        // windows, where nothing else leads the eye.
        l.beams.push({ s: 0, dir: 1, hold: 0, held: true, onArrive: res, once: true, tail: 130, width: 3, glow: 14 });
      });
    }
    // A refused request: a short red light that runs at the device and
    // breaks up just before it. from: the end it starts at.
    refuse(l, from = 'a', color = '#EF4444') {
      l.beams.push({ s: from === 'a' ? 0 : l.len, dir: from === 'a' ? 1 : -1, hold: 0, held: true, color: hex(color), stopAt: from === 'a' ? l.len - 40 : 40 });
    }
    path(l, s0, s1) {
      const { ctx } = this;
      const n = Math.max(2, Math.ceil((s1 - s0) / 4));
      ctx.beginPath();
      for (let i = 0; i <= n; i++) {
        const p = l.at(s0 + ((s1 - s0) * i) / n);
        i ? ctx.lineTo(p.x, p.y) : ctx.moveTo(p.x, p.y);
      }
    }
    frame(now) {
      const { ctx } = this;
      const dark = document.documentElement.dataset.theme === 'dark';
      const css = getComputedStyle(document.documentElement);
      const track = css.getPropertyValue('--track').trim();
      const dots = css.getPropertyValue('--dots').trim();
      ctx.setTransform(this.dpr, 0, 0, this.dpr, 0, 0);
      ctx.clearRect(0, 0, this.w, this.h);
      ctx.lineCap = 'round';
      ctx.lineJoin = 'round';
      const dt = this.last ? Math.min(50, now - this.last) : 16;
      this.last = now;
      for (const l of this.links) {
        if (l.loose()) l.build();
        l.alpha += (l.alphaTo - l.alpha) * Math.min(1, dt / 120);
        ctx.globalAlpha = l.alpha;
        if (l.state === 'dash') {
          this.path(l, 0, l.len);
          ctx.setLineDash([0.1, 7]);
          ctx.lineDashOffset = l.trying ? -now * 0.03 : 0;
          ctx.strokeStyle = dots;
          ctx.lineWidth = 2.6;
          ctx.stroke();
          ctx.setLineDash([]);
          continue;
        }
        ctx.strokeStyle = track;
        ctx.lineWidth = 3;
        if (l.state === 'grow' || l.state === 'retract') {
          const ends = [];
          if (l.gfrom === 'both') {
            const h = (l.len / 2) * l.grow;
            this.path(l, 0, h);
            ctx.stroke();
            this.path(l, l.len - h, l.len);
            ctx.stroke();
            ends.push(h, l.len - h);
          } else if (l.gfrom === 'center') {
            // Out of (or back into) the middle: a relay reaching for the
            // two devices, or pulling its links back in.
            const c = l.o.via ? l.segs[0].len : l.len / 2;
            const s0 = c - c * l.grow, s1 = c + (l.len - c) * l.grow;
            this.path(l, s0, s1);
            ctx.stroke();
            ends.push(s0, s1);
          } else if (l.gfrom === 'a') {
            const h = l.len * l.grow;
            this.path(l, 0, h);
            ctx.stroke();
            ends.push(h);
          } else {
            const h = l.len * (1 - l.grow);
            this.path(l, h, l.len);
            ctx.stroke();
            ends.push(h);
          }
          if (l.state === 'grow' || l.glow)
            for (const s of ends) {
              const p = l.at(s), c = l.colorAt(s);
              const g = ctx.createRadialGradient(p.x, p.y, 0, p.x, p.y, 7);
              g.addColorStop(0, rgba(mixc(c, [255, 255, 255], 0.5), 0.95));
              g.addColorStop(1, rgba(c, 0));
              ctx.fillStyle = g;
              ctx.beginPath();
              ctx.arc(p.x, p.y, 7, 0, Math.PI * 2);
              ctx.fill();
            }
          continue;
        }
        if (l.track !== false) {
          this.path(l, 0, l.len);
          ctx.stroke();
        }

        // Beams: spawn, move, draw.
        const relay = l.o.via ? 1 : 0;
        if (l.flow)
          for (const dir of [0, 1]) {
            if (now >= l.next[dir]) {
              l.beams.push({ s: dir ? l.len : 0, dir: dir ? -1 : 1, hold: 0, held: false });
              l.next[dir] = now + (relay ? rand(1300, 2300) : rand(900, 1800)) / (l.o.busy || 1);
            }
          }
        const mid = relay ? l.segs[0].len : -1;
        const keep = [];
        for (const bm of l.beams) {
          if (bm.fade != null) {
            bm.fade -= dt / 200;
            if (bm.fade <= 0) continue;
          } else if (bm.hold > 0) bm.hold -= dt;
          else {
            const ns = bm.s + bm.dir * (l.speed || this.speed) * (dt / 1000);
            if (bm.onArrive && ns >= l.len) {
              bm.onArrive();
              bm.onArrive = null;
            }
            if (bm.stopAt != null && (ns - bm.stopAt) * bm.dir >= 0) {
              bm.s = bm.stopAt;
              bm.fade = 1;
              const p = l.at(bm.s);
              this.pops.push({ x: p.x, y: p.y, t0: now, c: bm.color });
            } else if (relay && !bm.held && (bm.s - mid) * (ns - mid) <= 0) {
              // Store and forward: a moment's stop inside the relay.
              bm.s = mid;
              bm.held = true;
              bm.hold = 200;
              if (l.o.onRelay) l.o.onRelay(l.colorAt(mid));
            } else bm.s = ns;
          }
          const tail = bm.tail || 74;
          const head = bm.s;
          const back = head - bm.dir * tail;
          if (bm.dir > 0 ? back >= l.len : back <= 0) {
            if (bm.once) this.remove(l);
            continue;
          }
          keep.push(bm);
          const s0 = Math.max(0, Math.min(head, back)), s1 = Math.min(l.len, Math.max(head, back));
          if (s1 - s0 < 1) continue;
          const ph = l.at(Math.max(0, Math.min(l.len, head))), pt = l.at(Math.max(0, Math.min(l.len, back)));
          const ch = bm.color || l.colorAt(head);
          const fa = bm.fade != null ? bm.fade : 1;
          const g = ctx.createLinearGradient(pt.x, pt.y, ph.x, ph.y);
          g.addColorStop(0, rgba(ch, 0));
          g.addColorStop(0.75, rgba(ch, 0.85 * fa));
          g.addColorStop(1, rgba(mixc(ch, [255, 255, 255], 0.55), fa));
          this.path(l, s0, s1);
          ctx.strokeStyle = g;
          ctx.lineWidth = bm.width || 2;
          ctx.shadowColor = rgba(ch, (dark ? 0.9 : 0.6) * fa);
          ctx.shadowBlur = (bm.glow || 8) * this.dpr;
          ctx.stroke();
          ctx.shadowBlur = 0;
        }
        l.beams = keep;
      }
      ctx.globalAlpha = 1;
      // A refused light breaking up: a small ring that opens and fades.
      this.pops = this.pops.filter((p) => now - p.t0 < 420);
      for (const p of this.pops) {
        const k = (now - p.t0) / 420;
        ctx.strokeStyle = rgba(p.c, 0.9 * (1 - k));
        ctx.lineWidth = 2;
        ctx.beginPath();
        ctx.arc(p.x, p.y, 3 + 9 * D.Ease.out(k), 0, Math.PI * 2);
        ctx.stroke();
      }
      // Tendrils: dotted curves reaching for something being dragged near.
      for (const t of this.tendrils) {
        if (t.alpha <= 0.01) continue;
        const dx = t.to.x - t.from.x, dy = t.to.y - t.from.y;
        const c = { x: (t.from.x + t.to.x) / 2 - dy * 0.12, y: (t.from.y + t.to.y) / 2 + dx * 0.12 };
        ctx.beginPath();
        ctx.moveTo(t.from.x, t.from.y);
        ctx.quadraticCurveTo(c.x, c.y, t.to.x, t.to.y);
        ctx.setLineDash([0.1, 7]);
        ctx.lineDashOffset = -now * 0.03;
        ctx.strokeStyle = rgba(hex(t.color), t.alpha);
        ctx.lineWidth = 2.6;
        ctx.stroke();
        ctx.setLineDash([]);
      }
    }
  }
  N.Layer = Layer;
})();
