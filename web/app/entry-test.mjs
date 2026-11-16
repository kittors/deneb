#!/usr/bin/env node
// The website's home and the app's way in (design/plan/m7/site-entry.md), on
// the mock (deneb-web -mock, its code 000000):
//
//  1. Not signed in: / is the website's home; its top bar has 登录, and no
//     打开网页版.
//  2. A login, done, goes into the app (/lan, on to the first LAN), not to
//     the website's home.
//  3. Signed in: / is the home still; its top bar has 打开网页版 in 登录's
//     place, a link to /lan; a click goes into the app.
//  4. In the app, wide enough for its sidebar, the star and "Deneb" over it
//     are a link to /, lit under the pointer; a click opens the home.
//  5. The old links open as before: /lan/<id>, /shared.
//  6. A page that is not there says so, with 回到首页, a link to /.
//
// Stills go to <out>-<step>-<theme>-<W>x<H>.png. A line per check; exit 1
// on a failed one.
//
// usage: node web/app/entry-test.mjs <out prefix> [light|dark] [base] [W] [H]
import { open, sleep } from '../probe/cdp.mjs';

const [out, theme = 'light', base = 'http://127.0.0.1:47622', W = '1440', H = '900'] = process.argv.slice(2);
if (!out) {
  console.error('usage: node web/app/entry-test.mjs <out prefix> [light|dark] [base] [W] [H]');
  process.exit(2);
}
const w = Number(W);
const h = Number(H);
const b = await open({ W: w, H: h, scale: 2, dark: theme === 'dark' });
let failed = 0;
const say = (ok, what) => (console.log((ok ? 'ok   ' : 'FAIL ') + what), ok || failed++);
const until = async (js, ms = 6000) => {
  for (let t = 0; t < ms; t += 100) {
    if (await b.ev(js).catch(() => false)) return true;
    await sleep(100);
  }
  return false;
};
const nav = (path) => b.S('Page.navigate', { url: base + path });
const shot = (step) => b.shot(`${out}-${step}-${theme}-${w}x${h}.png`);
const shown = (sel) => b.ev(`(() => { const e = document.querySelector(${JSON.stringify(sel)}); return !!e && e.getClientRects().length > 0 && getComputedStyle(e).visibility !== 'hidden'; })()`);
const attr = (sel, name) => b.ev(`(document.querySelector(${JSON.stringify(sel)}) || { getAttribute: () => null }).getAttribute(${JSON.stringify(name)})`);
const path = () => b.ev('location.pathname');
const centre = (sel) => b.ev(`(() => { const r = document.querySelector(${JSON.stringify(sel)}).getBoundingClientRect(); return [r.left + r.width / 2, r.top + r.height / 2]; })()`);
const type = async (sel, s) => {
  await b.ev(`document.querySelector(${JSON.stringify(sel)}).focus()`);
  await b.S('Input.insertText', { text: s });
};
const phone = w < 768;
// The app's page: the canvas, or on a phone its list (phone.ts).
const page = phone ? '.m-lan' : '.canvas';
const onHome = `!!document.querySelector('header.top .brand') && location.pathname === '/'`;
const onLAN = `location.pathname.startsWith('/lan/') && !!document.querySelector('${page}')`;

try {
  // ---- 1. Not signed in --------------------------------------------------------------------
  await nav('/download');
  await until(`!!document.querySelector('header.top')`);
  await b.ev(`fetch('/api/v1/logout', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' }).then((r) => r.status)`);
  await nav('/');
  say(await until(onHome), 'not signed in: / is the website’s home');
  say((await shown('header.top .signin')) && !(await shown('header.top .open-app')), 'its top bar has 登录, no 打开网页版');
  await sleep(600);
  await shot('1-home-out');

  // ---- 2. A login goes into the app --------------------------------------------------------
  await nav('/login');
  await until(`!!document.querySelector('input[type=email]')`);
  await type('input[type=email]', 'kittors@deneb.test');
  await b.ev(`document.querySelector('.jc-form button').click()`);
  await until(`document.querySelectorAll('input.cb').length === 6`);
  await type('input.cb', '000000');
  say(await until(onLAN, 8000), 'logged in: into the app, on to the first LAN: ' + (await path()));

  // ---- 3. Signed in, the home --------------------------------------------------------------
  await nav('/');
  say(await until(onHome), 'signed in: / is the website’s home still');
  say((await shown('header.top .open-app')) && !(await shown('header.top .signin')), 'its top bar has 打开网页版 in 登录’s place');
  say((await attr('header.top .open-app', 'href')) === '/lan', '打开网页版 links to /lan: ' + (await attr('header.top .open-app', 'href')));
  await sleep(600);
  await shot('2-home-in');
  const [ox, oy] = await centre('header.top .open-app');
  await b.click(ox, oy);
  say(await until(onLAN, 8000), '打开网页版 goes into the app: ' + (await path()));

  // ---- 4. The sidebar's star and "Deneb" -------------------------------------------------
  if (!phone) {
    say((await attr('.side a.brand', 'href')) === '/', 'the sidebar’s star and "Deneb" link to /');
    say((await attr('.side a.brand', 'aria-label')) === 'Deneb 首页', 'its label: ' + (await attr('.side a.brand', 'aria-label')));
    const rest = await b.ev(`getComputedStyle(document.querySelector('.side a.brand')).backgroundColor`);
    const [bx, by] = await centre('.side a.brand');
    await b.S('Input.dispatchMouseEvent', { type: 'mouseMoved', x: bx, y: by });
    await sleep(400);
    const lit = await b.ev(`getComputedStyle(document.querySelector('.side a.brand')).backgroundColor`);
    say(lit !== rest, `lit under the pointer: ${rest} → ${lit}`);
    await shot('3-brand-hover');
    await b.click(bx, by);
    say(await until(onHome), 'a click opens the website’s home');
  } else {
    // The phone's app has no sidebar: the home is the browser's way.
    say(!(await shown('.side a.brand')), 'a phone: no sidebar, no star there');
    await shot('3-app-phone');
  }

  // ---- 5. The old links --------------------------------------------------------------------
  await nav('/lan/l_weekend');
  say(await until(`location.pathname === '/lan/l_weekend' && !!document.querySelector('${page}')`), '/lan/<id> opens that LAN');
  await nav('/shared');
  say(await until(`location.pathname === '/shared' && !!document.querySelector('${page}')`), '/shared opens 共享给我');
  await nav('/lan');
  say(await until(onLAN), '/lan, the app’s way in, goes on to the first LAN: ' + (await path()));

  // ---- 6. A page that is not there ---------------------------------------------------------
  await nav('/nowhere');
  say(await until(`(document.querySelector('.jc-title') || {}).textContent === '没有这个页面'`), 'a page that is not there says so');
  say((await attr('.jc-foot a.btn', 'href')) === '/', '回到首页 links to /: ' + (await attr('.jc-foot a.btn', 'href')));
  await sleep(400);
  await shot('4-not-found');
} finally {
  await b.close();
}
console.log(failed ? `${failed} failed` : 'all ok');
process.exit(failed ? 1 : 0);
