// First paint and accessibility tree of a plain HTML page, for comparison.
import { open, sleep } from './cdp.mjs';
const url = process.argv[2];
const conds = [['10M/80ms', 80, 10000, 1], ['慢 4G 1.6M/150ms', 150, 1600, 1], ['慢 4G + CPU 4x', 150, 1600, 4]];
for (const [name, lat, kbps, cpu] of conds) {
  const b = await open({ dark: false });
  await b.S('Network.enable'); await b.S('Network.setCacheDisabled', { cacheDisabled: true });
  await b.S('Network.emulateNetworkConditions', { offline: false, latency: lat, downloadThroughput: kbps * 125, uploadThroughput: kbps * 125 });
  await b.S('Emulation.setCPUThrottlingRate', { rate: cpu });
  await b.S('Page.navigate', { url });
  let r = null;
  for (let i = 0; i < 400; i++) { await sleep(50); r = await b.ev(`(() => { const p = performance.getEntriesByName('first-contentful-paint')[0]; const n = performance.getEntriesByType('navigation')[0]; return p && n && n.loadEventEnd ? { fcp: Math.round(p.startTime), load: Math.round(n.loadEventEnd), bytes: performance.getEntriesByType('resource').reduce((s, e) => s + e.transferSize, n.transferSize) } : null })()`); if (r) break; }
  console.log(name, JSON.stringify(r));
  if (name === '10M/80ms') {
    const ax = await b.S('Accessibility.getFullAXTree');
    const live = ax.nodes.filter((n) => !n.ignored);
    const named = live.filter((n) => (n.name?.value || '').trim() && n.role?.value !== 'InlineTextBox');
    console.log('AX not ignored:', live.length, 'named:', named.length, JSON.stringify(named.slice(0, 30).map((n) => n.role.value + ':' + n.name.value)));
  }
  await b.close();
}
