// First screen of the TypeScript web app (implementation.md §2.5's line:
// 3 seconds), as web/probe/probe.mjs measured MyGo's: a throwaway headless
// Chrome, an empty cache, throttled network and CPU, three runs each, the
// median. Two pages: an invite link opened logged out (a new person's first
// stop: until the invite card shows), and a LAN logged in (until the first
// device tile shows). Against deneb-web (-mock or a real control server).
// usage: node web/probe/fp-web.mjs <origin> <invite path> <lan path> <email> <code>
import { open, sleep } from './cdp.mjs';
const [origin, invitePath, lanPath, email, code] = process.argv.slice(2);
const conds = [
  ['本机', 0, 0, 1], ['宽带 50M/30ms', 30, 50000, 1], ['20M/50ms', 50, 20000, 1], ['10M/80ms', 80, 10000, 1], ['慢 4G 1.6M/150ms', 150, 1600, 1], ['10M/80ms + CPU 4x', 80, 10000, 4], ['慢 4G + CPU 4x', 150, 1600, 4],
];
const HOOK = `window.__fp = {}; new MutationObserver(() => { const t = performance.now(); if (!__fp.card && document.querySelector('.join-card .jc-title, .join-card h1')) requestAnimationFrame(() => (__fp.card = performance.now())); if (!__fp.dev && document.querySelector('.canvas .dev .tile')) requestAnimationFrame(() => (__fp.dev = performance.now())); }).observe(document, { childList: true, subtree: true });`;
const med = (a) => { const s = a.filter((x) => x != null).sort((x, y) => x - y); return s.length ? Math.round(s[Math.floor(s.length / 2)]) : null; };
async function run(b, path, key, c) {
  const [, lat, kbps, cpu] = c;
  await b.S('Network.enable');
  await b.S('Network.setCacheDisabled', { cacheDisabled: true });
  await b.S('Network.emulateNetworkConditions', { offline: false, latency: lat, downloadThroughput: kbps ? kbps * 125 : -1, uploadThroughput: kbps ? kbps * 125 : -1 });
  await b.S('Emulation.setCPUThrottlingRate', { rate: cpu });
  await b.S('Page.navigate', { url: origin + path });
  let t = null;
  for (let i = 0; i < 600 && !t; i++) { await sleep(50); t = await b.ev(`window.__fp && window.__fp.${key}`).catch(() => null); }
  const bytes = await b.ev(`performance.getEntriesByType('resource').reduce((s, e) => s + e.transferSize, (performance.getEntriesByType('navigation')[0] || {}).transferSize || 0)`);
  const fcp = await b.ev(`(performance.getEntriesByName('first-contentful-paint')[0] || {}).startTime || null`);
  return { t, bytes, fcp };
}
const rows = [];
for (const c of conds) {
  const res = { invite: [], lan: [], bytesI: 0, bytesL: 0 };
  for (let r = 0; r < 3; r++) {
    // Logged out: the invite page.
    let b = await open({ W: 1280, H: 800, scale: 2 });
    await b.S('Page.addScriptToEvaluateOnNewDocument', { source: HOOK });
    const i = await run(b, invitePath, 'card', c);
    res.invite.push(i.t); res.bytesI = i.bytes;
    await b.close();
    // Logged in (unthrottled), then the LAN with an empty cache.
    b = await open({ W: 1280, H: 800, scale: 2 });
    await b.S('Page.navigate', { url: origin + '/login' }); await sleep(600);
    await b.ev(`(async () => { const h = { 'Content-Type': 'application/json' }; await fetch('/api/v1/login/email', { method: 'POST', headers: h, body: JSON.stringify({ email: ${JSON.stringify(email)} }) }); await fetch('/api/v1/login/code', { method: 'POST', headers: h, body: JSON.stringify({ email: ${JSON.stringify(email)}, code: ${JSON.stringify(code)} }) }); })()`);
    await b.S('Network.enable'); await b.S('Network.clearBrowserCache');
    await b.S('Page.addScriptToEvaluateOnNewDocument', { source: HOOK });
    const l = await run(b, lanPath, 'dev', c);
    res.lan.push(l.t); res.bytesL = l.bytes;
    await b.close();
  }
  const row = { 条件: c[0], 邀请页: med(res.invite), 局域网页: med(res.lan), 邀请页传输KB: Math.round(res.bytesI / 1024), 局域网页传输KB: Math.round(res.bytesL / 1024) };
  rows.push(row);
  console.log(JSON.stringify(row));
}
