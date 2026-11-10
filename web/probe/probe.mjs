// First-screen probe: loads a page in a throwaway headless Chrome under given
// network and CPU conditions, with an empty cache, and reads the page's
// timing hooks (probe.js): wasm downloaded, instantiated, Go running, first
// WebGL draw, first frame on screen.
// usage: node probe.mjs <url> [--runs 3] [--shot out.png] [--gpu]
import { spawn } from 'node:child_process';
import { mkdtempSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const argv = process.argv.slice(2);
const opt = (k, d) => { const i = argv.indexOf('--' + k); return i < 0 ? d : (argv[i + 1] && !argv[i + 1].startsWith('--') ? argv[i + 1] : true); };
const url = argv[0];
const runs = Number(opt('runs', 3));
const shot = opt('shot', '');
const only = opt('only', '');
const warm = opt('warm', false);
const W = 1280, H = 800;
const CHROME = '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome';
const profile = mkdtempSync(join(tmpdir(), 'deneb-probe-'));

class CDP {
  constructor(u) { this.ws = new WebSocket(u); this.id = 0; this.p = new Map(); this.ev = [];
    this.ws.onmessage = (e) => { const m = JSON.parse(e.data);
      if (m.id && this.p.has(m.id)) { const { res, rej } = this.p.get(m.id); this.p.delete(m.id); m.error ? rej(new Error(m.error.message)) : res(m.result); }
      else if (m.method) { this.ev.push(m); } }; }
  open() { return new Promise((r, j) => { this.ws.onopen = r; this.ws.onerror = j; }); }
  send(method, params = {}, sessionId) { const id = ++this.id; this.ws.send(JSON.stringify({ id, method, params, sessionId })); return new Promise((res, rej) => this.p.set(id, { res, rej })); }
}
const args = ['--headless=new', '--remote-debugging-port=0', `--user-data-dir=${profile}`, '--no-first-run', '--no-default-browser-check', '--hide-scrollbars', '--mute-audio', '--force-color-profile=srgb', `--window-size=${W},${H}`];
if (opt('gpu', false)) args.push('--enable-gpu', '--use-angle=metal', '--ignore-gpu-blocklist');
args.push('about:blank');
const proc = spawn(CHROME, args);
const ws = await new Promise((res, rej) => { let b = ''; proc.stderr.on('data', (d) => { b += d; const m = b.match(/DevTools listening on (ws:\/\/\S+)/); if (m) res(m[1]); }); proc.on('exit', (c) => rej(new Error('chrome exited ' + c + b))); });
const cdp = new CDP(ws); await cdp.open();

const conds = [
  { name: '本机', lat: 0, kbps: 0, cpu: 1 },
  { name: '宽带 50M/30ms', lat: 30, kbps: 50000, cpu: 1 },
  { name: '20M/50ms', lat: 50, kbps: 20000, cpu: 1 },
  { name: '10M/80ms', lat: 80, kbps: 10000, cpu: 1 },
  { name: '慢 4G 1.6M/150ms', lat: 150, kbps: 1600, cpu: 1 },
  { name: '10M/80ms + CPU 4x', lat: 80, kbps: 10000, cpu: 4 },
].filter((c) => !only || only.split(',').includes(c.name));

const med = (a) => { const s = [...a].sort((x, y) => x - y); return s[Math.floor(s.length / 2)]; };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const out = [];
let gpu = '';
for (const c of conds) {
  const rows = [];
  for (let r = 0; r < runs; r++) {
    const { targetId } = await cdp.send('Target.createTarget', { url: 'about:blank' });
    const { sessionId: sid } = await cdp.send('Target.attachToTarget', { targetId, flatten: true });
    const S = (m, p) => cdp.send(m, p, sid);
    const ev = async (e) => { const x = await S('Runtime.evaluate', { expression: e, returnByValue: true, awaitPromise: true }); return x.result.value; };
    await S('Network.enable'); await S('Page.enable'); await S('Runtime.enable');
    if (!warm) { await S('Network.clearBrowserCache'); await S('Network.setCacheDisabled', { cacheDisabled: true }); }
    else {
      // Open the page once, unthrottled, so that the wasm and its compiled code are cached; then measure a second visit.
      await S('Page.navigate', { url });
      for (let i = 0; i < 400; i++) { await sleep(50); if (await ev('!!(window.__t && window.__t.firstFrame)')) break; }
      await sleep(1500);
      await S('Page.navigate', { url: 'about:blank' }); await sleep(300);
    }
    await S('Emulation.setDeviceMetricsOverride', { width: W, height: H, deviceScaleFactor: 2, mobile: false });
    await S('Network.emulateNetworkConditions', { offline: false, latency: c.lat, downloadThroughput: c.kbps ? c.kbps * 1000 / 8 : -1, uploadThroughput: c.kbps ? c.kbps * 1000 / 8 : -1 });
    await S('Emulation.setCPUThrottlingRate', { rate: c.cpu });
    await S('Page.navigate', { url });
    let t = null;
    const deadline = Date.now() + 60000;
    while (Date.now() < deadline) { await sleep(50); t = await ev('window.__t && (window.__t.firstFrame || window.__t.error) ? JSON.stringify(window.__t) : null'); if (t) break; }
    t = t ? JSON.parse(t) : { timeout: true };
    const res = JSON.parse(await ev(`JSON.stringify(performance.getEntriesByType('resource').filter(e => e.name.endsWith('.wasm')).map(e => ({ end: e.responseEnd, start: e.requestStart, size: e.transferSize, body: e.decodedBodySize })))`) || '[]');
    const nav = JSON.parse(await ev(`JSON.stringify(performance.getEntriesByType('navigation')[0] || {})`) || '{}');
    if (!gpu) gpu = await ev(`(() => { const g = document.createElement('canvas').getContext('webgl2'); if (!g) return 'no webgl2'; const e = g.getExtension('WEBGL_debug_renderer_info'); return g.getParameter(e ? e.UNMASKED_RENDERER_WEBGL : g.RENDERER); })()`);
    await sleep(2000);
    const after = JSON.parse(await ev('JSON.stringify(window.__t)'));
    const mem = await ev(`(() => { try { return Math.round(go._inst.exports.mem.buffer.byteLength / 1e6) } catch (e) { return -1 } })()`);
    if (shot && r === 0 && c === conds[0]) { const s = await S('Page.captureScreenshot', { format: 'png' }); writeFileSync(shot, Buffer.from(s.data, 'base64')); }
    rows.push({ html: nav.responseEnd, wasm: res[0]?.end, inst: t.instantiated, ran: t.ran, draw: t.firstDraw, frame: t.firstFrame, err: t.error, timeout: t.timeout, frames2s: after.frames - (t.frames || 0), mem, size: res[0]?.size });
    await cdp.send('Target.closeTarget', { targetId });
  }
  const pick = (k) => med(rows.map((x) => x[k]).filter((v) => v != null));
  const row = { 条件: c.name, wasm到齐: pick('wasm'), 编译完: pick('inst'), 首次绘制: pick('draw'), 首帧上屏: pick('frame'), 之后2秒帧数: pick('frames2s'), 内存MB: pick('mem'), 传输KB: Math.round((pick('size') || 0) / 1024), 出错: rows.find((x) => x.err || x.timeout) ? JSON.stringify(rows.map((x) => x.err || x.timeout)) : '' };
  for (const k of ['wasm到齐', '编译完', '首次绘制', '首帧上屏']) if (row[k] != null) row[k] = Math.round(row[k]);
  out.push(row);
  console.log(JSON.stringify(row));
}
console.log('GPU:', gpu);
proc.kill(); await sleep(500); try { rmSync(profile, { recursive: true, force: true }); } catch {}
