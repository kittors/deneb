// The invite flow against a real control server through deneb-web (M3
// acceptance: "没装客户端的人在浏览器里打开邀请链接，也能接受，并被引导去下载客户端").
// A fresh browser, logged out: the invite card, the email, the code, joined,
// then the LAN page; for someone with no device yet, "this computer" dashed
// and the download card open. A screenshot at each step into outdir.
// usage: node web/app/join-test.mjs <link> <email> <code> <outdir> [light|dark]
import { mkdirSync } from 'node:fs';
import { open, sleep } from '../probe/cdp.mjs';
const [link, email, code, outdir, theme = 'light'] = process.argv.slice(2);
mkdirSync(outdir, { recursive: true });
const b = await open({ W: 1280, H: 800, scale: 2, dark: theme === 'dark' });
const until = async (js, ms = 8000) => { for (let t = 0; t < ms; t += 100) { if (await b.ev(js)) return true; await sleep(100); } return false; };
let failed = 0;
const say = (ok, w) => (console.log((ok ? 'ok   ' : 'FAIL ') + w), ok || failed++);
const shot = (n) => b.shot(`${outdir}/${n}-${theme}.png`);
await b.S('Page.navigate', { url: link + (link.includes('?') ? '&' : '?') + 'theme=' + theme });
say(await until(`!!document.querySelector('[data-act="join"]')`), 'invite card: ' + (await b.ev(`(document.querySelector('.jc-title')||{}).textContent`)));
await shot('1-invite');
await b.ev(`document.querySelector('[data-act="join"]').click()`);
say(await until(`!!document.querySelector('input[type=email]')`), 'email step');
await b.ev(`document.querySelector('input[type=email]').focus()`);
await b.S('Input.insertText', { text: email });
await sleep(300);
await shot('2-email');
await b.ev(`document.querySelector('.jc-form button').click()`);
say(await until(`document.querySelectorAll('input.cb').length === 6`), 'code step: ' + (await b.ev(`(document.querySelector('.jc-sub')||{}).textContent`)));
await b.ev(`document.querySelector('input.cb').focus()`);
await b.S('Input.insertText', { text: code });
say(await until(`!!document.querySelector('.jc-joined')`), 'joined: ' + (await b.ev(`(document.querySelector('.jc-joined .jc-title')||{}).textContent`)));
await shot('3-joined');
say(await until(`location.pathname.startsWith('/lan/') && !!document.querySelector('.canvas .dev')`), 'on the LAN page: ' + (await b.ev(`(document.querySelector('.head .title')||{}).textContent + ' · ' + (document.querySelector('.head .sub-text')||{}).textContent`)));
const fresh = await b.ev(`!!document.querySelector('.dev[data-id="this-computer"]')`);
if (fresh) say(await until(`!!document.querySelector('.sheet .dl-card')`), 'no device yet: this computer, dashed, and the download card');
else say(true, 'has devices already: they wait in the tray (' + (await b.ev(`document.querySelectorAll('.tray .mini').length`)) + ')');
await sleep(800);
await shot('4-lan');
const errs = await b.ev(`JSON.stringify(performance.getEntriesByType('resource').filter(e => e.responseStatus >= 400).map(e => e.name + ' ' + e.responseStatus))`);
console.log('failed requests:', errs);
await b.close();
process.exit(failed ? 1 : 0);
