// Deneb demo motion engine.
//
// Mirrors MyGo's model: the view sets targets, values ease toward them, and
// the engine owns the frame clock. Owning the clock lets the recorder step
// time deterministically (60 exact frames a second) instead of sampling a
// live page, so recordings show the motion as designed, not as the headless
// GPU happened to render it.
(function () {
  const D = (window.D = window.D || {});

  // ---- Clock ---------------------------------------------------------------
  const Clock = {
    manual: false,
    t: 0,
    now() {
      return this.manual ? this.t : performance.now();
    },
  };
  D.Clock = Clock;
  D.now = () => Clock.now();

  // ---- Easing (CSS definitions, as MyGo's ui.EaseIn/EaseOut/EaseInOut) -------
  function cubicBezier(x1, y1, x2, y2) {
    // Newton-Raphson on x(t), then y(t); same approach browsers use.
    const cx = 3 * x1, bx = 3 * (x2 - x1) - cx, ax = 1 - cx - bx;
    const cy = 3 * y1, by = 3 * (y2 - y1) - cy, ay = 1 - cy - by;
    const sx = (t) => ((ax * t + bx) * t + cx) * t;
    const sy = (t) => ((ay * t + by) * t + cy) * t;
    const dx = (t) => (3 * ax * t + 2 * bx) * t + cx;
    return (x) => {
      if (x <= 0) return 0;
      if (x >= 1) return 1;
      let t = x;
      for (let i = 0; i < 8; i++) {
        const e = sx(t) - x;
        if (Math.abs(e) < 1e-5) break;
        const d = dx(t);
        if (Math.abs(d) < 1e-6) break;
        t -= e / d;
      }
      return sy(Math.min(1, Math.max(0, t)));
    };
  }
  const Ease = {
    linear: (t) => t,
    in: cubicBezier(0.42, 0, 1, 1),
    out: cubicBezier(0, 0, 0.58, 1),
    inOut: cubicBezier(0.42, 0, 0.58, 1),
    // Strong decelerate for things arriving (a la "emphasized decelerate").
    outExpo: cubicBezier(0.16, 1, 0.3, 1),
    bezier: cubicBezier,
  };
  D.Ease = Ease;
  D.clamp = (v, a, b) => Math.min(b, Math.max(a, v));
  D.lerp = (a, b, t) => a + (b - a) * t;

  // ---- Springs (SwiftUI/MyGo parameters: perceptual duration + bounce) -------
  const Smooth = { duration: 500, bounce: 0 };
  const Snappy = { duration: 400, bounce: 0.15 };
  const Bouncy = { duration: 550, bounce: 0.32 };
  D.Smooth = Smooth;
  D.Snappy = Snappy;
  D.Bouncy = Bouncy;

  function springParams(o) {
    const omega = (2 * Math.PI) / (o.duration / 1000);
    const b = o.bounce || 0;
    const zeta = b >= 0 ? 1 - b : 1 / (1 + b);
    return { omega, zeta };
  }

  // Spring solves the damped oscillator exactly (as MyGo does), so a slow
  // frame never changes the path, and retargeting keeps the velocity.
  class Spring {
    constructor(value, opts = Smooth) {
      this.target = value;
      this.x0 = value;
      this.v0 = 0;
      this.t0 = Clock.now();
      this.opts = opts;
      this.delay = 0;
    }
    sample(now = Clock.now()) {
      let t = (now - this.t0 - this.delay) / 1000;
      if (t <= 0) return { x: this.x0, v: this.v0 };
      const { omega, zeta } = springParams(this.opts);
      const x0 = this.x0 - this.target;
      const v0 = this.v0;
      let x, v;
      if (zeta < 1) {
        const wd = omega * Math.sqrt(1 - zeta * zeta);
        const e = Math.exp(-zeta * omega * t);
        const A = x0;
        const B = (v0 + zeta * omega * x0) / wd;
        const c = Math.cos(wd * t), s = Math.sin(wd * t);
        x = e * (A * c + B * s);
        v = e * (-zeta * omega * (A * c + B * s) + (-A * wd * s + B * wd * c));
      } else if (zeta === 1) {
        const e = Math.exp(-omega * t);
        const B = v0 + omega * x0;
        x = e * (x0 + B * t);
        v = e * (B - omega * (x0 + B * t));
      } else {
        const r1 = -omega * (zeta - Math.sqrt(zeta * zeta - 1));
        const r2 = -omega * (zeta + Math.sqrt(zeta * zeta - 1));
        const c2 = (v0 - r1 * x0) / (r2 - r1);
        const c1 = x0 - c2;
        x = c1 * Math.exp(r1 * t) + c2 * Math.exp(r2 * t);
        v = c1 * r1 * Math.exp(r1 * t) + c2 * r2 * Math.exp(r2 * t);
      }
      return { x: this.target + x, v };
    }
    get value() {
      return this.sample().x;
    }
    // to retargets from where the value shows now, keeping its velocity.
    to(target, opts, delay = 0) {
      if (target === this.target && !opts && !delay) return this;
      const now = Clock.now();
      const { x, v } = this.sample(now);
      this.x0 = x;
      // A delayed start holds the value still, then springs from rest, so a
      // staggered entrance never lurches with a stale velocity.
      this.v0 = delay ? 0 : v;
      this.t0 = now;
      this.target = target;
      this.delay = delay;
      if (opts) this.opts = opts;
      return this;
    }
    // jump sets the value at once (pointer following), tracking velocity.
    jump(value, velocity = 0) {
      this.x0 = value;
      this.target = value;
      this.v0 = velocity;
      this.t0 = Clock.now();
      this.delay = 0;
      return this;
    }
    settled(now = Clock.now()) {
      const { x, v } = this.sample(now);
      return Math.abs(x - this.target) < 0.001 && Math.abs(v) < 0.01;
    }
  }
  D.Spring = Spring;

  class Spring2 {
    constructor(x, y, opts = Smooth) {
      this.x = new Spring(x, opts);
      this.y = new Spring(y, opts);
    }
    to(x, y, opts, delay) {
      this.x.to(x, opts, delay);
      this.y.to(y, opts, delay);
      return this;
    }
    jump(x, y, vx = 0, vy = 0) {
      this.x.jump(x, vx);
      this.y.jump(y, vy);
      return this;
    }
    get value() {
      return { x: this.x.value, y: this.y.value };
    }
    settled() {
      return this.x.settled() && this.y.settled();
    }
  }
  D.Spring2 = Spring2;

  // Tween is a curve animation toward a target over a duration, as MyGo's
  // Animate/AnimateWith. Retargeting starts from the value shown.
  class Tween {
    constructor(value, duration = 160, ease = Ease.out) {
      this.from = value;
      this.target = value;
      this.t0 = -1e9;
      this.duration = duration;
      this.ease = ease;
      this.delay = 0;
    }
    to(target, duration, ease, delay = 0) {
      if (target === this.target && duration == null) return this;
      const now = Clock.now();
      this.from = this.sample(now);
      this.target = target;
      this.t0 = now;
      this.delay = delay;
      if (duration != null) this.duration = duration;
      if (ease) this.ease = ease;
      return this;
    }
    set(value) {
      this.from = this.target = value;
      this.t0 = -1e9;
      return this;
    }
    sample(now = Clock.now()) {
      const p = D.clamp((now - this.t0 - this.delay) / this.duration, 0, 1);
      return this.from + (this.target - this.from) * this.ease(p);
    }
    get value() {
      return this.sample();
    }
    settled(now = Clock.now()) {
      return now - this.t0 - this.delay >= this.duration;
    }
  }
  D.Tween = Tween;

  // ---- Frame loop and timers -----------------------------------------------
  const frameFns = new Set();
  let timers = [];
  let lastFrame = null;

  function runFrame(now) {
    const dt = lastFrame == null ? 16.67 : Math.min(64, Math.max(0, now - lastFrame));
    lastFrame = now;
    if (timers.length) {
      const due = timers.filter((t) => t.at <= now && !t.cancelled);
      timers = timers.filter((t) => t.at > now && !t.cancelled);
      due.sort((a, b) => a.at - b.at);
      for (const t of due) t.fn();
    }
    for (const fn of frameFns) fn(now, dt);
  }

  function loop() {
    if (Clock.manual) return;
    runFrame(Clock.now());
    requestAnimationFrame(loop);
  }

  D.onFrame = (fn) => {
    frameFns.add(fn);
    return () => frameFns.delete(fn);
  };

  // after runs fn once the clock passes ms from now; returns a handle.
  D.after = (ms, fn) => {
    const t = { at: Clock.now() + ms, fn, cancelled: false };
    timers.push(t);
    return { cancel: () => (t.cancelled = true) };
  };

  // Script schedules steps relative to its start, and cancels them together,
  // so a replay or a mode switch never leaves stray steps behind.
  class Script {
    constructor() {
      this.handles = [];
      this.start = Clock.now();
    }
    at(ms, fn) {
      const t = { at: this.start + ms, fn, cancelled: false };
      timers.push(t);
      this.handles.push(t);
      return this;
    }
    cancel() {
      for (const h of this.handles) h.cancelled = true;
      this.handles = [];
    }
  }
  D.Script = Script;

  // ---- Recorder hooks ------------------------------------------------------
  // CSS transitions and animations follow the virtual clock too: each frame
  // pauses them and sets their time, so hover fades and spinners record at
  // their designed speed.
  function syncCSSAnimations(t) {
    for (const a of document.getAnimations()) {
      // First seen: it starts now, at its first frame. Whatever it ran on
      // real time before (between an input event and this frame, or
      // while a screenshot was taken) is not part of the design.
      if (a.__vt0 == null) {
        a.__vt0 = t;
        a.pause();
      }
      const local = t - a.__vt0;
      a.currentTime = local;
      const end = a.effect && a.effect.getComputedTiming().endTime;
      if (end != null && isFinite(end) && local >= end) a.finish();
    }
  }

  window.__advance = async (ms) => {
    if (!Clock.manual) {
      Clock.manual = true;
      Clock.t = performance.now();
    }
    Clock.t += ms;
    syncCSSAnimations(Clock.t);
    runFrame(Clock.t);
    // Let code waiting on this frame finish (what comes after an await in
    // a frame callback), then catch every animation started in this frame
    // at its first frame. Otherwise they run on real time until the next
    // frame and jump ahead by however long the screenshot took: a jolt in
    // the recording that no real browser shows.
    await new Promise((r) => setTimeout(r, 0));
    syncCSSAnimations(Clock.t);
    for (const fn of sampleFns) fn(Clock.t);
  };
  // Recording only: called once a frame is final (for frame-by-frame logs).
  const sampleFns = new Set();
  D.onSample = (fn) => sampleFns.add(fn);

  D.recording = new URLSearchParams(location.search).has('record');
  if (D.recording) {
    Clock.manual = true;
    Clock.t = 0;
  } else {
    requestAnimationFrame(loop);
  }
})();
