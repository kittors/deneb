#!/usr/bin/env node
// Records a demo scene frame by frame for review.
//
// It starts its own headless Chrome (a throwaway profile, so the user's
// browser is never touched), loads the scene with ?record, and drives the
// page's virtual clock: each frame it sends the scripted pointer events,
// advances the clock by 1/60 s, and captures the page. The result is an
// exact 60 fps video of the motion as designed, a contact sheet for review,
// and full-resolution stills at the moments the script marks.
//
// usage: node design/tools/record.mjs <scene.html> [--theme dark|light]
//        [--scale 1|2] [--out dir] [--deliver] [--sheet N] [--from ms --to ms]

import { spawn, execFileSync } from 'node:child_process';
import { mkdtempSync, mkdirSync, writeFileSync, rmSync, readdirSync, copyFileSync } from 'node:fs';
import { tmpdir, homedir } from 'node:os';
import { join, resolve, basename } from 'node:path';
import { pathToFileURL } from 'node:url';

const argv = process.argv.slice(2);
const opt = (k, d) => {
  const i = argv.indexOf('--' + k);
  if (i < 0) return d;
  const v = argv[i + 1];
  return v == null || v.startsWith('--') ? true : v;
};
const scene = resolve(argv.find((a) => !a.startsWith('--') && a.endsWith('.html')));
const theme = opt('theme', 'dark');
const scale = Number(opt('scale', 1));
const deliver = opt('deliver', false);
const sheetN = Number(opt('sheet', 12));
const W = 1440, H = 900, FPS = 60, STEP = 1000 / FPS;
const name = basename(scene, '.html');
const outDir = resolve(opt('out', join(tmpdir(), 'deneb-rec', `${name}-${theme}`)));
const CHROME = '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome';

rmSync(outDir, { recursive: true, force: true });
mkdirSync(join(outDir, 'frames'), { recursive: true });
const profile = mkdtempSync(join(tmpdir(), 'deneb-chrome-'));

// Uncaught page errors, reported with the result: a demo that throws
// mid-frame would otherwise just look frozen in the video.
const errors = [];

// ---- CDP over the browser's WebSocket ------------------------------------------
class CDP {
  constructor(url) {
    this.ws = new WebSocket(url);
    this.id = 0;
    this.pending = new Map();
    this.waiters = [];
    this.ws.onmessage = (e) => {
      const m = JSON.parse(e.data);
      if (m.id && this.pending.has(m.id)) {
        const { res, rej } = this.pending.get(m.id);
        this.pending.delete(m.id);
        m.error ? rej(new Error(m.error.message)) : res(m.result);
      } else if (m.method) {
        if (m.method === 'Runtime.exceptionThrown') {
          const d = m.params.exceptionDetails;
          errors.push((d.exception && d.exception.description) || d.text);
        }
        this.waiters = this.waiters.filter((w) => !(w.method === m.method && (w.fn(m.params), true)));
      }
    };
  }
  open() {
    return new Promise((res, rej) => {
      this.ws.onopen = res;
      this.ws.onerror = rej;
    });
  }
  send(method, params = {}, sessionId) {
    const id = ++this.id;
    this.ws.send(JSON.stringify({ id, method, params, sessionId }));
    return new Promise((res, rej) => this.pending.set(id, { res, rej }));
  }
  once(method) {
    return new Promise((fn) => this.waiters.push({ method, fn }));
  }
}

function launch() {
  return new Promise((res, rej) => {
    const p = spawn(CHROME, [
      '--headless=new',
      '--remote-debugging-port=0',
      `--user-data-dir=${profile}`,
      '--no-first-run',
      '--no-default-browser-check',
      '--hide-scrollbars',
      '--mute-audio',
      '--force-color-profile=srgb',
      '--allow-file-access-from-files',
      `--window-size=${W},${H}`,
      'about:blank',
    ]);
    let buf = '';
    p.stderr.on('data', (d) => {
      buf += d;
      const m = buf.match(/DevTools listening on (ws:\/\/\S+)/);
      if (m) res({ proc: p, ws: m[1] });
    });
    p.on('exit', (c) => rej(new Error('chrome exited ' + c + '\n' + buf)));
  });
}

const { proc, ws } = await launch();
const cdp = new CDP(ws);
await cdp.open();
const { targetId } = await cdp.send('Target.createTarget', { url: 'about:blank' });
const { sessionId: sid } = await cdp.send('Target.attachToTarget', { targetId, flatten: true });
const S = (m, p) => cdp.send(m, p, sid);
const evaluate = async (expression) => {
  const r = await S('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true });
  if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description || r.exceptionDetails.text);
  return r.result.value;
};

await S('Page.enable');
await S('Runtime.enable');
await S('Emulation.setDeviceMetricsOverride', { width: W, height: H, deviceScaleFactor: scale, mobile: false });
await S('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-reduced-motion', value: 'no-preference' }] });
const url = pathToFileURL(scene).href + `?record=1&theme=${theme}`;
const loaded = cdp.once('Page.loadEventFired');
await S('Page.navigate', { url });
await loaded;
for (let i = 0; i < 100 && !(await evaluate('!!window.__ready')); i++) await new Promise((r) => setTimeout(r, 50));
// Fonts must be in before the first frame, or text reflows mid-video.
await evaluate('document.fonts.ready.then(() => true)');
const plan = await evaluate('window.RECORDING || { duration: 8000, actions: [] }');

// ---- Expand the script into per-frame pointer events --------------------------------
// Targets: [x, y] in the first window's design coordinates; {sel} for an
// element's center; {sel, x, y} for a point inside an element.
const resolveTarget = async (t) => {
  if (Array.isArray(t)) return evaluate(`__point('.window', ${t[0]}, ${t[1]})`);
  if (t.sel && t.x != null) return evaluate(`__point(${JSON.stringify(t.sel)}, ${t.x}, ${t.y})`);
  return evaluate(`__point(${JSON.stringify(t.sel)})`);
};
const ease = (p) => (p < 0.5 ? 4 * p * p * p : 1 - Math.pow(-2 * p + 2, 3) / 2);

let mouse = { x: W / 2, y: H - 120 };
let buttons = 0;
const glides = []; // active pointer glides: {t0, dur, from, to}
const steps = []; // {at, run}
for (const a of plan.actions) {
  const at = a.at || 0;
  if (a.move) steps.push({ at, run: async () => moveTo(await resolveTarget(a.move)) });
  if (a.glide) steps.push({ at, run: async () => glides.push({ t0: at, dur: a.dur || 600, from: { ...mouse }, to: await resolveTarget(a.glide), arc: a.arc || 0 }) });
  if (a.down) steps.push({ at, run: () => press('mousePressed') });
  if (a.up) steps.push({ at, run: () => press('mouseReleased') });
  if (a.key) steps.push({ at, run: () => key(a.key) });
  if (a.js) steps.push({ at, run: () => evaluate(a.js) });
  if (a.shot) steps.push({ at, shot: a.shot, clean: !!a.clean });
}
steps.sort((p, q) => p.at - q.at);

async function moveTo(p) {
  mouse = { x: p.x, y: p.y };
  await S('Input.dispatchMouseEvent', { type: 'mouseMoved', x: p.x, y: p.y, button: buttons ? 'left' : 'none', buttons });
}
async function press(type) {
  buttons = type === 'mousePressed' ? 1 : 0;
  await S('Input.dispatchMouseEvent', { type, x: mouse.x, y: mouse.y, button: 'left', buttons, clickCount: 1 });
  // Headless Chrome can leave :hover on a stale element after a press until
  // the pointer moves again; a move in place refreshes it, as a real hand's
  // tremor would.
  await S('Input.dispatchMouseEvent', { type: 'mouseMoved', x: mouse.x, y: mouse.y, button: buttons ? 'left' : 'none', buttons });
}
async function key(k) {
  const codes = { Escape: 27, Enter: 13 };
  await S('Input.dispatchKeyEvent', { type: 'keyDown', key: k, code: k, windowsVirtualKeyCode: codes[k] || 0 });
  await S('Input.dispatchKeyEvent', { type: 'keyUp', key: k, code: k, windowsVirtualKeyCode: codes[k] || 0 });
}

// ---- Frame loop ------------------------------------------------------------------
const frames = Math.ceil(plan.duration / STEP);
const from = Number(opt('from', 0)), to = Number(opt('to', plan.duration));
const shots = [];
const ts = new Date();
const stamp = `${ts.getFullYear()}${String(ts.getMonth() + 1).padStart(2, '0')}${String(ts.getDate()).padStart(2, '0')}-${String(ts.getHours()).padStart(2, '0')}${String(ts.getMinutes()).padStart(2, '0')}`;
await evaluate('__advance(0)');
let si = 0;
const t0 = Date.now();
let written = 0;
for (let f = 0; f <= frames; f++) {
  const vt = f * STEP;
  const pendingShots = [];
  while (si < steps.length && steps[si].at <= vt + 0.01) {
    const s = steps[si++];
    if (s.shot) pendingShots.push(s);
    else await s.run();
  }
  for (const g of glides.slice()) {
    const p = Math.min(1, (vt - g.t0) / g.dur);
    const e = ease(p);
    // A slight arc keeps long pointer moves from looking robotic.
    const nx = -(g.to.y - g.from.y), ny = g.to.x - g.from.x;
    const nl = Math.hypot(nx, ny) || 1;
    const bow = Math.sin(Math.PI * e) * g.arc;
    await moveTo({ x: g.from.x + (g.to.x - g.from.x) * e + (nx / nl) * bow, y: g.from.y + (g.to.y - g.from.y) * e + (ny / nl) * bow });
    if (p >= 1) glides.splice(glides.indexOf(g), 1);
  }
  if (f > 0) await evaluate(`__advance(${STEP})`);
  if (vt >= from && vt <= to) {
    const { data } = await S('Page.captureScreenshot', { format: 'jpeg', quality: 88, optimizeForSpeed: true });
    writeFileSync(join(outDir, 'frames', `${String(written++).padStart(5, '0')}.jpg`), Buffer.from(data, 'base64'));
  }
  for (const { shot: label, clean } of pendingShots) {
    // clean: a still without the drawn pointer, for thumbnails and result
    // pictures where the hand has nothing to show.
    if (clean) await evaluate(`document.querySelectorAll('.fake-cursor').forEach((c) => (c.style.visibility = 'hidden'))`);
    // Stills are captured at twice the density, as on a Retina display.
    await S('Emulation.setDeviceMetricsOverride', { width: W, height: H, deviceScaleFactor: 2, mobile: false });
    const { data } = await S('Page.captureScreenshot', { format: 'png' });
    await S('Emulation.setDeviceMetricsOverride', { width: W, height: H, deviceScaleFactor: scale, mobile: false });
    const file = join(outDir, `${name}_${theme}_${label}.png`);
    writeFileSync(file, Buffer.from(data, 'base64'));
    shots.push({ label, file, at: vt });
    if (clean) await evaluate(`document.querySelectorAll('.fake-cursor').forEach((c) => (c.style.visibility = ''))`);
  }
}
const secs = ((Date.now() - t0) / 1000).toFixed(1);
// The page's own frame-by-frame log of what moved (recording mode), for
// design/tools/motion-check.mjs.
try {
  const trace = await evaluate('JSON.stringify(window.__trace || null)');
  if (trace && trace !== 'null') writeFileSync(join(outDir, 'trace.json'), trace);
} catch (_) {}

await cdp.send('Target.closeTarget', { targetId }).catch(() => {});
// Chrome keeps writing its profile until it exits; wait before removing it.
await new Promise((r) => {
  proc.once('exit', r);
  proc.kill();
  setTimeout(r, 4000);
});
try {
  rmSync(profile, { recursive: true, force: true, maxRetries: 5, retryDelay: 200 });
} catch (_) {}

// ---- Video and contact sheet --------------------------------------------------------
const mp4 = join(outDir, `${name}_${theme}.mp4`);
execFileSync('ffmpeg', ['-y', '-loglevel', 'error', '-framerate', String(FPS), '-i', join(outDir, 'frames', '%05d.jpg'), '-vf', 'scale=1920:-2:flags=lanczos', '-c:v', 'libx264', '-preset', 'medium', '-crf', '18', '-pix_fmt', 'yuv420p', '-movflags', '+faststart', mp4]);

const sheet = join(outDir, `${name}_${theme}_sheet.jpg`);
const sheetStep = Math.max(STEP, (to - from) / Math.max(1, sheetN - 1));
execFileSync('python3', ['-I', join(import.meta.dirname, 'sheet.py'), join(outDir, 'frames'), sheet, '--step', String(sheetStep), '--cols', '3', '--width', '720']);

if (deliver) {
  const dest = join(homedir(), 'Desktop', 'Deneb组网demo');
  mkdirSync(dest, { recursive: true });
  const title = plan.title || name;
  // Light is the main look since the third design (DESIGN.md §3); the
  // dark copy carries the suffix, as the sample was delivered.
  const suffix = theme === 'light' ? '' : '_深色';
  copyFileSync(mp4, join(dest, `${stamp}_${title}${suffix}.mp4`));
  // --deliver-shots a,b keeps only those stills, as for a second theme.
  const keep = opt('deliver-shots', null);
  const wanted = keep ? String(keep).split(',') : null;
  for (const s of shots) if (!wanted || wanted.includes(s.label)) copyFileSync(s.file, join(dest, `${stamp}_${title}_${s.label}${suffix}.png`));
}

console.log(JSON.stringify({ outDir, frames: written, seconds: secs, mp4, sheet, shots: shots.map((s) => s.file), errors: [...new Set(errors)].slice(0, 10) }, null, 1));
// The browser socket can outlive Chrome and hold the event loop open.
try {
  cdp.ws.close();
} catch (_) {}
process.exit(0);
