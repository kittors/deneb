// 共享给我 and 我的设备 on the mock (DESIGN.md §14.11): 阿杰's 共享给我 (the
// studio's two devices), the card, 小林 sharing her laptop (it pops up),
// 不要了 with undo, 不要了 for good (share.leave once the toast goes), 小林
// taking hers back (it goes with a word); then kittors' 我的设备, a card,
// adding a device to a LAN from it, and taking a share back. Screenshots at
// each step; a line per check; exit 1 on a failed one.
// usage: node web/app/shared-test.mjs <outprefix> [light|dark] [base]
import { open, sleep } from '../probe/cdp.mjs';
const [out, theme = 'light', base = 'http://127.0.0.1:47622'] = process.argv.slice(2);
const b = await open({ W: 1280, H: 800, scale: 1, dark: theme === 'dark' });
let failed = 0;
const say = (ok, w) => (console.log((ok ? 'ok   ' : 'FAIL ') + w), ok || failed++);
const until = async (js, ms = 4000) => { for (let t = 0; t < ms; t += 100) { if (await b.ev(js)) return true; await sleep(100); } return false; };
const login = async (who) => {
  await b.S('Page.navigate', { url: base + '/login' }); await sleep(600);
  await b.ev(`(async () => { const h = { 'Content-Type': 'application/json' }; await fetch('/api/v1/logout', { method: 'POST' }).catch(() => {}); await fetch('/api/v1/login/email', { method: 'POST', headers: h, body: '{"email":"${who}@deneb.test"}' }); await fetch('/api/v1/login/code', { method: 'POST', headers: h, body: '{"email":"${who}@deneb.test","code":"000000"}' }); })()`);
};
const ctr = (sel) => b.ev(`(() => { const r = document.querySelector(${JSON.stringify(sel)}).getBoundingClientRect(); return [r.left + r.width / 2, r.top + r.height / 2]; })()`);
const click = async (sel) => { const [x, y] = await ctr(sel); await b.click(x, y); };
const text = (sel) => b.ev(`(document.querySelector(${JSON.stringify(sel)}) || {}).textContent || ''`);
const mock = (body) => b.ev(`fetch('/api/v1/_mock/share', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: ${JSON.stringify(JSON.stringify(body))} }).then(r => r.status)`);
const ops = [];
const shot = (n) => b.shot(`${out}-${n}-${theme}.png`);

// ---- 阿杰: 共享给我 ----
await login('jie');
await b.S('Page.navigate', { url: base + '/shared?theme=' + theme });
say(await until(`document.querySelectorAll('.canvas .dev').length === 2`), 'two shared devices: ' + (await b.ev(`[...document.querySelectorAll('.canvas .dev')].map(d => d.textContent).join(' | ')`)));
say((await text('.head .title')) === '共享给我', 'title: ' + (await text('.head .title')) + ' · ' + (await text('.head .sub-text')));
say(!(await b.ev(`[...document.querySelectorAll('.side .nav .nav-label')].some(n => n.textContent.includes('工作室'))`)), 'the studio is not among my LANs');
say((await text('.nav[data-id="shared"] .count')) === '2', 'sidebar count: ' + (await text('.nav[data-id="shared"] .count')));
say(!(await b.ev(`(() => { const b = document.querySelector('.head .right .btn'); return b && b.offsetParent !== null; })()`)), 'no invite button');
await sleep(400);
await shot('1-shared');
await click('.dev[data-id="d_gpu"] .tile');
say(await until(`!!document.querySelector('.sheet .out-btn')`), 'card: ' + (await text('.sheet .top span')) + ' · ' + (await text('.sheet .label')) + ' · ' + (await text('.sheet .hint')));
await sleep(400);
await shot('2-card');
// 小林 shares her laptop: it pops up, the sidebar's row lights up and counts.
await b.ev(`document.querySelector('.sheet-close').click()`);
await mock({ device: 'd_lin', sharer: 'p_lin', on: true });
say(await until(`!!document.querySelector('.dev[data-id="d_lin"]')`), 'newly shared pops up: ' + (await text('.dev[data-id="d_lin"]')));
say(await until(`(document.querySelector('.nav[data-id="shared"] .count')||{}).textContent === '3'`), 'count follows: 3');
await sleep(900);
await shot('3-new');
// 不要了, then undo: back as it was, nothing sent.
await b.ev(`(() => { const o = window.fetch; window.__ops = []; window.fetch = (u, i) => { if (String(u).includes('/ops')) window.__ops.push(i && i.body); return o(u, i); }; })()`);
await click('.dev[data-id="d_lin"] .tile');
await until(`!!document.querySelector('.sheet .out-btn')`);
await sleep(300);
await click('.sheet .out-btn');
say(await until(`!document.querySelector('.dev[data-id="d_lin"]') && /已拿掉/.test((document.querySelector('.toast')||{}).textContent||'')`), 'let go: ' + (await text('.toast')));
await sleep(350);
await shot('4-let-go');
await click('.toast button');
say(await until(`!!document.querySelector('.dev[data-id="d_lin"]')`), 'undo: back');
say((await b.ev(`window.__ops.length`)) === 0, 'nothing sent on undo');
// 不要了 for good: share.leave once the toast is gone.
await sleep(600);
await click('.dev[data-id="d_test"] .tile');
await until(`!!document.querySelector('.sheet .out-btn')`);
await sleep(300);
await click('.sheet .out-btn');
await sleep(300);
say(await until(`window.__ops.some(b => b && b.includes('share.leave'))`, 6000), 'share.leave sent when the toast went: ' + (await b.ev(`JSON.stringify(window.__ops)`)));
say(await until(`!document.querySelector('.dev[data-id="d_test"]') && (document.querySelector('.nav[data-id="shared"] .count')||{}).textContent === '2'`), 'gone for good, count 2');
// 小林 takes hers back: it shrinks away, with a word.
await mock({ device: 'd_lin', sharer: 'p_lin', on: false });
say(await until(`/收回了/.test((document.querySelector('.toast')||{}).textContent||'')`), 'taken back: ' + (await text('.toast')));
await sleep(350);
await shot('5-taken-back');
say(await until(`!document.querySelector('.dev[data-id="d_lin"]')`), 'and gone');

// ---- kittors: 我的设备 ----
await login('kittors');
await b.S('Page.navigate', { url: base + '/mine?theme=' + theme });
say(await until(`document.querySelectorAll('.canvas .dev').length === 4`), 'my four devices: ' + (await b.ev(`[...document.querySelectorAll('.canvas .dev')].map(d => d.textContent).join(' | ')`)));
say((await text('.head .title')) === '我的设备', 'title: ' + (await text('.head .title')) + ' · ' + (await text('.head .sub-text')));
await sleep(400);
await shot('6-mine');
await click('.dev[data-id="d_knas"] .tile');
say(await until(`!!document.querySelector('.sheet .lan-chips')`), 'card LANs: ' + (await b.ev(`[...document.querySelectorAll('.sheet .lan-chips .chip')].map(c => (c.classList.contains('lan-add') ? '+' : '') + c.textContent).join(', ')`)));
await sleep(400);
await shot('7-mine-card');
const add = await b.ev(`(() => { const c = document.querySelector('.sheet .lan-add'); return c ? c.dataset.l : ''; })()`);
if (add) {
  await click('.sheet .lan-add');
  say(await until(`/已把.*加进/.test((document.querySelector('.toast')||{}).textContent||'')`), 'added from the card: ' + (await text('.toast')));
  say(await until(`!document.querySelector('.sheet .lan-add[data-l="${add}"]')`), 'the chip is now in');
  await sleep(350);
  await shot('8-added');
  await click('.toast button');
  say(await until(`!!document.querySelector('.sheet .lan-add[data-l="${add}"]')`), 'undo: out again');
}
const errs = await b.ev(`JSON.stringify(performance.getEntriesByType('resource').filter(e => e.responseStatus >= 400).map(e => e.name + ' ' + e.responseStatus))`);
say(errs === '[]', 'failed requests: ' + errs);
await b.close();
process.exit(failed ? 1 : 0);
