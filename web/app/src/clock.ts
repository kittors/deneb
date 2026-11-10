// The app's clock and springs, as design/demos/shared/motion.js has them
// (which mirrors MyGo's model): the view sets targets, values spring toward
// them, and one frame loop drives everything. Owning the clock lets the
// recorder (design/tools/record.mjs, with ?record) step time exactly: it
// calls window.__advance(ms) for each frame, and CSS and Web animations
// follow the same clock. The springs' parameters are the desktop's (MyGo's
// ui.Smooth, ui.Snappy, ui.Bouncy, read from the generated tokens).
import tokens from './tokens.js';
import type { SpringOptions } from './tokens.js';

export type { SpringOptions };

export const Clock = {
  manual: false,
  t: 0,
  now(): number {
    return this.manual ? this.t : performance.now();
  },
};
export const now = (): number => Clock.now();

// ---- Easing (CSS's curves, as MyGo's EaseIn/EaseOut/EaseInOut) ----------------------------
export type Easing = (t: number) => number;

export function cubicBezier(x1: number, y1: number, x2: number, y2: number): Easing {
  const cx = 3 * x1, bx = 3 * (x2 - x1) - cx, ax = 1 - cx - bx;
  const cy = 3 * y1, by = 3 * (y2 - y1) - cy, ay = 1 - cy - by;
  const sx = (t: number) => ((ax * t + bx) * t + cx) * t;
  const sy = (t: number) => ((ay * t + by) * t + cy) * t;
  const dx = (t: number) => (3 * ax * t + 2 * bx) * t + cx;
  return (x: number) => {
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

export const Ease = {
  linear: ((t: number) => t) as Easing,
  in: cubicBezier(0.42, 0, 1, 1),
  out: cubicBezier(0, 0, 0.58, 1),
  inOut: cubicBezier(0.42, 0, 0.58, 1),
  outExpo: cubicBezier(0.16, 1, 0.3, 1),
  bezier: cubicBezier,
};
export const clamp = (v: number, a: number, b: number): number => Math.min(b, Math.max(a, v));
export const lerp = (a: number, b: number, t: number): number => a + (b - a) * t;

// ---- Springs (perceptual duration and bounce, as SwiftUI's and MyGo's) ---------------------
export const Smooth: SpringOptions = tokens.spring.Smooth;
export const Snappy: SpringOptions = tokens.spring.Snappy;
export const Bouncy: SpringOptions = tokens.spring.Bouncy;

function springParams(o: SpringOptions): { omega: number; zeta: number } {
  const omega = (2 * Math.PI) / (o.duration / 1000);
  const b = o.bounce || 0;
  const zeta = b >= 0 ? 1 - b : 1 / (1 + b);
  return { omega, zeta };
}

// Spring solves the damped oscillator exactly, so a slow frame never
// changes the path, and a new target keeps the current velocity.
export class Spring {
  target: number;
  x0: number;
  v0: number;
  t0: number;
  opts: SpringOptions;
  delay: number;

  constructor(value: number, opts: SpringOptions = Smooth) {
    this.target = value;
    this.x0 = value;
    this.v0 = 0;
    this.t0 = Clock.now();
    this.opts = opts;
    this.delay = 0;
  }

  sample(at: number = Clock.now()): { x: number; v: number } {
    const t = (at - this.t0 - this.delay) / 1000;
    if (t <= 0) return { x: this.x0, v: this.v0 };
    const { omega, zeta } = springParams(this.opts);
    const x0 = this.x0 - this.target;
    const v0 = this.v0;
    let x: number, v: number;
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

  get value(): number {
    return this.sample().x;
  }

  // to retargets from where the value shows now, keeping its velocity; a
  // delayed start holds still, then springs from rest.
  to(target: number, opts?: SpringOptions, delay = 0): this {
    if (target === this.target && !opts && !delay) return this;
    const t = Clock.now();
    const { x, v } = this.sample(t);
    this.x0 = x;
    this.v0 = delay ? 0 : v;
    this.t0 = t;
    this.target = target;
    this.delay = delay;
    if (opts) this.opts = opts;
    return this;
  }

  // jump sets the value at once (following the pointer), with a velocity.
  jump(value: number, velocity = 0): this {
    this.x0 = value;
    this.target = value;
    this.v0 = velocity;
    this.t0 = Clock.now();
    this.delay = 0;
    return this;
  }

  settled(at: number = Clock.now()): boolean {
    const { x, v } = this.sample(at);
    return Math.abs(x - this.target) < 0.001 && Math.abs(v) < 0.01;
  }
}

export class Spring2 {
  x: Spring;
  y: Spring;
  constructor(x: number, y: number, opts: SpringOptions = Smooth) {
    this.x = new Spring(x, opts);
    this.y = new Spring(y, opts);
  }
  to(x: number, y: number, opts?: SpringOptions, delay?: number): this {
    this.x.to(x, opts, delay);
    this.y.to(y, opts, delay);
    return this;
  }
  jump(x: number, y: number, vx = 0, vy = 0): this {
    this.x.jump(x, vx);
    this.y.jump(y, vy);
    return this;
  }
  get value(): { x: number; y: number } {
    return { x: this.x.value, y: this.y.value };
  }
  settled(): boolean {
    return this.x.settled() && this.y.settled();
  }
}

// Tween is a curve toward a target over a duration (MyGo's Animate); a new
// target starts from the value shown.
export class Tween {
  from: number;
  target: number;
  t0: number;
  duration: number;
  ease: Easing;
  delay: number;

  constructor(value: number, duration = 160, ease: Easing = Ease.out) {
    this.from = value;
    this.target = value;
    this.t0 = -1e9;
    this.duration = duration;
    this.ease = ease;
    this.delay = 0;
  }
  to(target: number, duration?: number, ease?: Easing, delay = 0): this {
    if (target === this.target && duration == null) return this;
    const t = Clock.now();
    this.from = this.sample(t);
    this.target = target;
    this.t0 = t;
    this.delay = delay;
    if (duration != null) this.duration = duration;
    if (ease) this.ease = ease;
    return this;
  }
  set(value: number): this {
    this.from = this.target = value;
    this.t0 = -1e9;
    return this;
  }
  sample(at: number = Clock.now()): number {
    const p = clamp((at - this.t0 - this.delay) / this.duration, 0, 1);
    return this.from + (this.target - this.from) * this.ease(p);
  }
  get value(): number {
    return this.sample();
  }
  settled(at: number = Clock.now()): boolean {
    return at - this.t0 - this.delay >= this.duration;
  }
}

// ---- Frame loop and timers ---------------------------------------------------------------
type FrameFn = (now: number, dt: number) => void;
interface Timer {
  at: number;
  fn: () => void;
  cancelled: boolean;
}
const frameFns = new Set<FrameFn>();
let timers: Timer[] = [];
let lastFrame: number | null = null;

function runFrame(t: number): void {
  const dt = lastFrame == null ? 16.67 : Math.min(64, Math.max(0, t - lastFrame));
  lastFrame = t;
  if (timers.length) {
    const due = timers.filter((x) => x.at <= t && !x.cancelled);
    timers = timers.filter((x) => x.at > t && !x.cancelled);
    due.sort((a, b) => a.at - b.at);
    for (const x of due) x.fn();
  }
  for (const fn of frameFns) fn(t, dt);
}

function loop(): void {
  if (Clock.manual) return;
  runFrame(Clock.now());
  requestAnimationFrame(loop);
}

export function onFrame(fn: FrameFn): () => void {
  frameFns.add(fn);
  return () => frameFns.delete(fn);
}

// after runs fn once the clock passes ms from now.
export function after(ms: number, fn: () => void): { cancel(): void } {
  const t: Timer = { at: Clock.now() + ms, fn, cancelled: false };
  timers.push(t);
  return { cancel: () => (t.cancelled = true) };
}

export const wait = (ms: number): Promise<void> => new Promise((r) => after(ms, r));

// Script schedules steps from its start and cancels them together.
export class Script {
  handles: Timer[];
  start: number;
  constructor() {
    this.handles = [];
    this.start = Clock.now();
  }
  at(ms: number, fn: () => void): this {
    const t: Timer = { at: this.start + ms, fn, cancelled: false };
    timers.push(t);
    this.handles.push(t);
    return this;
  }
  cancel(): void {
    for (const h of this.handles) h.cancelled = true;
    this.handles = [];
  }
}

// ---- Recorder hooks ---------------------------------------------------------------------
// Web animations follow the virtual clock too: each frame pauses them and
// sets their time, so they record at their designed speed.
type Tracked = Animation & { __vt0?: number };

function syncAnimations(t: number): void {
  for (const a of document.getAnimations() as Tracked[]) {
    if (a.__vt0 == null) {
      a.__vt0 = t;
      a.pause();
    }
    const local = t - a.__vt0;
    a.currentTime = local;
    const end = a.effect?.getComputedTiming().endTime;
    if (typeof end === 'number' && isFinite(end) && local >= end) a.finish();
  }
}

const sampleFns = new Set<(t: number) => void>();
// onSample runs once a recorded frame is final (frame-by-frame logs).
export function onSample(fn: (t: number) => void): void {
  sampleFns.add(fn);
}

export const recording: boolean = new URLSearchParams(location.search).has('record');

declare global {
  interface Window {
    __advance?: (ms: number) => Promise<void>;
    __ready?: boolean;
    __trace?: unknown[];
    RECORDING?: unknown;
  }
}

window.__advance = async (ms: number): Promise<void> => {
  if (!Clock.manual) {
    Clock.manual = true;
    Clock.t = performance.now();
  }
  Clock.t += ms;
  syncAnimations(Clock.t);
  runFrame(Clock.t);
  // Let what waits on this frame finish, then catch the animations it
  // started at their first frame.
  await new Promise((r) => setTimeout(r, 0));
  syncAnimations(Clock.t);
  for (const fn of sampleFns) fn(Clock.t);
};

if (recording) {
  Clock.manual = true;
  Clock.t = 0;
} else {
  requestAnimationFrame(loop);
}

// D gathers the clock under the demos' names, for code ported from them.
export const D = { Clock, now, Ease, clamp, lerp, Smooth, Snappy, Bouncy, Spring, Spring2, Tween, onFrame, after, wait, Script, onSample, recording };
