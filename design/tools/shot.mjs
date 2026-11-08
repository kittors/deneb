#!/usr/bin/env node
// Takes still screenshots of a page at a given moment, for pixel review.
//
// It loads the page with ?record so the page's virtual clock stands still,
// steps the clock to each requested time, and saves a PNG. Steady time
// matters for review: two shots of the same moment must be identical, so a
// difference between them is a real change, not animation noise.
//
// usage: node design/tools/shot.mjs <page.html|url> --out a.png
//        [--at 1200[,2400,...]] [--theme dark|light] [--scale 2]
//        [--w 1440 --h 900] [--clip x,y,w,h] [--query k=v&k2=v2]

import { spawn } from 'node:child_process';
import { mkdtempSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

const argv = process.argv.slice(2);
const opt = (k, d) => {
  const i = argv.indexOf('--' + k);
  if (i < 0) return d;
  const v = argv[i + 1];
  return v == null || v.startsWith('--') ? true : v;
};
const target = argv.find((a) => !a.startsWith('--') && (a.endsWith('.html') || a.startsWith('http')));
const theme = opt('theme', 'dark');
const scale = Number(opt('scale', 2));
const W = Number(opt('w', 1440));
const H = Number(opt('h', 900));
const at = String(opt('at', '1000')).split(',').map(Number);
const out = resolve(opt('out', join(tmpdir(), 'shot.png')));
const clip = opt('clip', null);
const query = opt('query', '');
const CHROME = '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome';
const profile = mkdtempSync(join(tmpdir(), 'deneb-shot-'));
const errors = [];

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
        if (m.method === 'Runtime.consoleAPICalled' && m.params.type === 'error') {
          errors.push(m.params.args.map((a) => a.value ?? a.description).join(' '));
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

const proc = spawn(CHROME, [
  '--headless=new',
  '--remote-debugging-port=0',
  `--user-data-dir=${profile}`,
  '--no-first-run',
  '--no-default-browser-check',
  '--hide-scrollbars',
  '--force-color-profile=srgb',
  '--allow-file-access-from-files',
  '--enable-gpu',
  '--use-angle=metal',
  `--window-size=${W},${H}`,
  'about:blank',
]);
const ws = await new Promise((res, rej) => {
  let buf = '';
  proc.stderr.on('data', (d) => {
    buf += d;
    const m = buf.match(/DevTools listening on (ws:\/\/\S+)/);
    if (m) res(m[1]);
  });
  proc.on('exit', (c) => rej(new Error('chrome exited ' + c + '\n' + buf)));
});
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

try {
  await S('Page.enable');
  await S('Runtime.enable');
  await S('Emulation.setDeviceMetricsOverride', { width: W, height: H, deviceScaleFactor: scale, mobile: false });
  const base = target.startsWith('http') ? target : pathToFileURL(resolve(target)).href;
  // --raw loads a page as it is (a reference site), without the demo's
  // recording switches.
  const raw = opt('raw', false);
  const url = raw ? base : base + (base.includes('?') ? '&' : '?') + `record=1&theme=${theme}` + (query ? '&' + query : '');
  const loaded = cdp.once('Page.loadEventFired');
  await S('Page.navigate', { url });
  await loaded;
  for (let i = 0; i < 100 && !(await evaluate('!!window.__ready')); i++) await new Promise((r) => setTimeout(r, 50));
  await evaluate('document.fonts.ready.then(() => true)');
  const info = await evaluate('window.__info ? window.__info() : null');
  if (info) console.log('page:', JSON.stringify(info));
  // --scroll N: N screens down the page, one shot each, for studying a
  // long reference page at desktop width.
  const scrollN = Number(opt('scroll', 0));
  if (scrollN) {
    await new Promise((r) => setTimeout(r, 2500));
    for (let i = 0; i < scrollN; i++) {
      await evaluate(`window.scrollTo(0, ${i} * innerHeight * 0.92)`);
      await new Promise((r) => setTimeout(r, 900));
      const { data } = await S('Page.captureScreenshot', { format: 'jpeg', quality: 80 });
      const file = out.replace(/\.(png|jpg)$/, `_${String(i).padStart(2, '0')}.jpg`);
      writeFileSync(file, Buffer.from(data, 'base64'));
      console.log('saved', file);
    }
    at.length = 0;
  }
  let now = 0;
  for (let i = 0; i < at.length; i++) {
    // Step in frames, as the recorder does, so springs and timers fire in
    // the same order they would in a recording.
    while (now < at[i]) {
      const step = Math.min(1000 / 60, at[i] - now);
      await evaluate(`window.__advance(${step})`);
      now += step;
    }
    await evaluate('new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)))');
    const params = { format: 'png', captureBeyondViewport: false };
    if (clip) {
      const [x, y, w, h] = String(clip).split(',').map(Number);
      params.clip = { x, y, width: w, height: h, scale: 1 };
    }
    const { data } = await S('Page.captureScreenshot', params);
    const file = at.length > 1 ? out.replace(/\.png$/, `_${String(at[i]).padStart(5, '0')}.png`) : out;
    writeFileSync(file, Buffer.from(data, 'base64'));
    console.log('saved', file);
  }
} finally {
  if (errors.length) console.log('PAGE ERRORS:\n' + errors.join('\n'));
  proc.kill();
  await new Promise((r) => (proc.exitCode != null ? r() : proc.once('exit', r)));
  setTimeout(() => rmSync(profile, { recursive: true, force: true, maxRetries: 5 }), 300);
  cdp.ws.close();
  setTimeout(() => process.exit(0), 600);
}
