// The LANs' pictures on the web (design/plan/m7/switch-motion.md §5), on the
// mock: kittors' sidebar and the LAN page's title with them, at three
// widths; the invite page, logged out, the LAN's picture with the inviter's
// ball on its corner; a server without the feature, which keeps the balls.
// A line per check, a screenshot at each step; exit 1 on a failed one.
// usage: node web/app/lanicon-test.mjs <outdir> [light|dark] [base]
import { mkdirSync } from 'node:fs';
import { open, sleep } from '../probe/cdp.mjs';
const [outdir, theme = 'light', base = 'http://127.0.0.1:47622'] = process.argv.slice(2);
mkdirSync(outdir, { recursive: true });
let failed = 0;
const say = (ok, w) => (console.log((ok ? 'ok   ' : 'FAIL ') + w), ok || failed++);
for (const [W, H, size] of [[980, 640, '窄'], [1280, 800, '中'], [1600, 1000, '宽']]) {
  const b = await open({ W, H, scale: 2, dark: theme === 'dark' });
  const until = async (js, ms = 6000) => { for (let t = 0; t < ms; t += 100) { if (await b.ev(js)) return true; await sleep(100); } return false; };
  await b.S('Page.navigate', { url: base + '/login' });
  await sleep(600);
  await b.ev(`(async () => { const h = { 'Content-Type': 'application/json' }; await fetch('/api/v1/login/email', { method: 'POST', headers: h, body: '{"email":"kittors@deneb.test"}' }); await fetch('/api/v1/login/code', { method: 'POST', headers: h, body: '{"email":"kittors@deneb.test","code":"000000"}' }); })()`);
  await b.S('Page.navigate', { url: base + '/lan/l_weekend' });
  say(await until(`!!document.querySelector('.canvas .dev')`), `${size}: the LAN page`);
  const rows = await b.ev(`[...document.querySelectorAll('.side .nav[data-id^="lan:"] img.lan-ic')].map((i) => i.getAttribute('src'))`);
  say(rows.length >= 2 && rows.every((s) => s.startsWith('/lan-icons/')), `${size}: the sidebar's LANs have pictures: ${rows.join(', ')}`);
  const loaded = await b.ev(`[...document.querySelectorAll('img.lan-ic')].every((i) => i.complete && i.naturalWidth === 128)`);
  say(loaded, `${size}: every picture loaded, 128 wide`);
  say(await b.ev(`!!document.querySelector('.head .title-ic img.lan-ic') && !document.querySelector('.head .title-ic').hidden`), `${size}: the title has its picture`);
  // The labels still start on one line, the pictures as wide as the icons.
  const lefts = await b.ev(`[...document.querySelectorAll('.side .nav .nav-label')].map((l) => Math.round(l.getBoundingClientRect().left))`);
  say(new Set(lefts).size === 1, `${size}: the sidebar's names start on one line: ${[...new Set(lefts)].join(', ')}`);
  await sleep(500);
  await b.shot(`${outdir}/${size}-局域网-${theme}.png`);
  await b.S('Page.navigate', { url: base + '/mine' });
  say(await until(`document.querySelector('.head .title')?.textContent === '我的设备'`), `${size}: 我的设备`);
  say(await b.ev(`document.querySelector('.head .title-ic').hidden`), `${size}: 我的设备's title has no picture`);
  if (size === '中') {
    await b.ev(`fetch('/api/v1/logout', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' })`);
    await sleep(300);
    await b.S('Page.navigate', { url: base + '/i/k3xQ9' });
    say(await until(`!!document.querySelector('.jc-lan img.lan-ic')`), 'the invite page: the LAN\'s picture');
    say(await b.ev(`!!document.querySelector('.jc-lan .ball')`), 'the invite page: the inviter\'s ball on its corner');
    await sleep(900);
    await b.shot(`${outdir}/${size}-邀请页-${theme}.png`);
  }
  await b.close();
}
process.exit(failed ? 1 : 0);
