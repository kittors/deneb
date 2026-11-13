// Minimal CDP driver over a throwaway headless Chrome (never the user's).
import { spawn } from 'node:child_process';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
const CHROME = '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome';
export const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
export async function open({ W = 1280, H = 800, scale = 2, dark = null } = {}) {
  const profile = mkdtempSync(join(tmpdir(), 'deneb-cdp-'));
  const proc = spawn(CHROME, ['--headless=new', '--remote-debugging-port=0', `--user-data-dir=${profile}`, '--no-first-run', '--no-default-browser-check', '--hide-scrollbars', '--mute-audio', '--force-color-profile=srgb', `--window-size=${W},${H}`, 'about:blank']);
  const ws = await new Promise((res, rej) => { let b = ''; proc.stderr.on('data', (d) => { b += d; const m = b.match(/DevTools listening on (ws:\/\/\S+)/); if (m) res(m[1]); }); proc.on('exit', (c) => rej(new Error('chrome exited ' + c + b))); });
  const sock = new WebSocket(ws); let id = 0; const pend = new Map(); const events = [];
  sock.onmessage = (e) => { const m = JSON.parse(e.data); if (m.id && pend.has(m.id)) { const { res, rej } = pend.get(m.id); pend.delete(m.id); m.error ? rej(new Error(m.error.message)) : res(m.result); } else if (m.method) events.push(m); };
  await new Promise((r, j) => { sock.onopen = r; sock.onerror = j; });
  const send = (method, params = {}, sessionId) => { const i = ++id; sock.send(JSON.stringify({ id: i, method, params, sessionId })); return new Promise((res, rej) => pend.set(i, { res, rej })); };
  // A page: the first, and with tab() another of the same browser (its
  // cookies, its BroadcastChannel), for what passes between tabs.
  const attach = async () => {
    const { targetId } = await send('Target.createTarget', { url: 'about:blank' });
    const { sessionId } = await send('Target.attachToTarget', { targetId, flatten: true });
    const S = (m, p) => send(m, p, sessionId);
    await S('Page.enable'); await S('Runtime.enable');
    await S('Emulation.setDeviceMetricsOverride', { width: W, height: H, deviceScaleFactor: scale, mobile: false });
    if (dark != null) await S('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value: dark ? 'dark' : 'light' }] });
    const ev = async (e) => { const x = await S('Runtime.evaluate', { expression: e, returnByValue: true, awaitPromise: true }); if (x.exceptionDetails) throw new Error(x.exceptionDetails.exception?.description || x.exceptionDetails.text); return x.result.value; };
    const shot = async (file, clip) => { const s = await S('Page.captureScreenshot', { format: 'png', ...(clip ? { clip: { ...clip, scale: 1 } } : {}) }); (await import('node:fs')).writeFileSync(file, Buffer.from(s.data, 'base64')); };
    const mouse = async (type, x, y, extra = {}) => S('Input.dispatchMouseEvent', { type, x, y, button: 'left', buttons: type === 'mouseReleased' ? 0 : 1, clickCount: 1, ...extra });
    const click = async (x, y) => { await S('Input.dispatchMouseEvent', { type: 'mouseMoved', x, y }); await mouse('mousePressed', x, y); await mouse('mouseReleased', x, y); };
    const key = async (k, code, vk) => { await S('Input.dispatchKeyEvent', { type: 'rawKeyDown', key: k, code, windowsVirtualKeyCode: vk }); await S('Input.dispatchKeyEvent', { type: 'keyUp', key: k, code, windowsVirtualKeyCode: vk }); };
    return { S, ev, shot, click, mouse, key };
  };
  const first = await attach();
  const close = async () => { proc.kill(); await sleep(400); try { rmSync(profile, { recursive: true, force: true }); } catch {} };
  return { ...first, close, events, tab: attach };
}
