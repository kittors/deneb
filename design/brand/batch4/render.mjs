#!/usr/bin/env node
// Batch renderer for the fourth logo batch: one headless Chrome, many
// shots. Each job loads an SVG or HTML file into a viewport of w x h CSS
// pixels at the given device scale and saves a PNG, transparent where the
// page draws nothing.
//
// usage: node design/brand/batch4/render.mjs jobs.json
//   jobs.json: [{"src": "a.svg", "w": 1024, "h": 1024, "scale": 1, "out": "a.png"}, ...]
//
// Chrome runs on a profile of its own in a temporary folder, so the user's
// Chrome is not touched.

import { spawn } from 'node:child_process';
import { mkdtempSync, readFileSync, writeFileSync, rmSync, mkdirSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

const jobs = JSON.parse(readFileSync(process.argv[2], 'utf8'));
const CHROME = '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome';
const profile = mkdtempSync(join(tmpdir(), 'deneb-logo4-'));

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
  '--headless=new', '--remote-debugging-port=0', `--user-data-dir=${profile}`,
  '--no-first-run', '--no-default-browser-check', '--hide-scrollbars',
  '--force-color-profile=srgb', '--allow-file-access-from-files', 'about:blank',
]);
const wsURL = await new Promise((res, rej) => {
  let buf = '';
  proc.stderr.on('data', (d) => {
    buf += d;
    const m = buf.match(/DevTools listening on (ws:\/\/\S+)/);
    if (m) res(m[1]);
  });
  proc.on('exit', (c) => rej(new Error('chrome exited ' + c + '\n' + buf)));
});
const cdp = new CDP(wsURL);
await cdp.open();
const { targetId } = await cdp.send('Target.createTarget', { url: 'about:blank' });
const { sessionId: sid } = await cdp.send('Target.attachToTarget', { targetId, flatten: true });
const S = (m, p) => cdp.send(m, p, sid);

let failed = 0;
try {
  await S('Page.enable');
  await S('Runtime.enable');
  await S('Emulation.setDefaultBackgroundColorOverride', { color: { r: 0, g: 0, b: 0, a: 0 } });
  for (const j of jobs) {
    // h "auto": as tall as the page's content.
    const auto = j.h === 'auto';
    await S('Emulation.setDeviceMetricsOverride', { width: j.w, height: auto ? 600 : j.h, deviceScaleFactor: j.scale || 1, mobile: false });
    const loaded = cdp.once('Page.loadEventFired');
    await S('Page.navigate', { url: pathToFileURL(resolve(j.src)).href });
    await loaded;
    await S('Runtime.evaluate', { expression: 'document.fonts ? document.fonts.ready.then(() => 1) : 1', awaitPromise: true });
    if (auto) {
      const r = await S('Runtime.evaluate', { expression: 'Math.ceil(document.body.firstElementChild ? document.body.firstElementChild.getBoundingClientRect().bottom : document.documentElement.scrollHeight)', returnByValue: true });
      j.h = r.result.value;
      await S('Emulation.setDeviceMetricsOverride', { width: j.w, height: j.h, deviceScaleFactor: j.scale || 1, mobile: false });
    }
    await new Promise((r) => setTimeout(r, j.wait || 60));
    const { data } = await S('Page.captureScreenshot', {
      format: 'png', captureBeyondViewport: false,
      clip: { x: 0, y: 0, width: j.w, height: j.h, scale: 1 },
    });
    mkdirSync(dirname(resolve(j.out)), { recursive: true });
    writeFileSync(resolve(j.out), Buffer.from(data, 'base64'));
  }
} catch (e) {
  console.error(e);
  failed = 1;
} finally {
  const gone = new Promise((r) => proc.once('exit', r));
  proc.kill();
  await gone;
  rmSync(profile, { recursive: true, force: true, maxRetries: 5, retryDelay: 200 });
}
console.log(`rendered ${jobs.length} file(s)`);
process.exit(failed);
