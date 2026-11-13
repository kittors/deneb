#!/usr/bin/env node
// The link in the code's mail (design/plan/m7/login-link.md §4), against a
// development control server on this computer (-dev -mail log -web-url
// <deneb-web's address>: the codes and the links go into its log, no mail
// goes out) with deneb-web in front of it:
//
//  1. A page asks for a code; the link, opened in another tab of the same
//     browser, says that page goes in too; a click logs in, and the page
//     that waited goes in by itself.
//  2. The same, the tab that waited told nothing (a tab the browser froze):
//     back on it, it asks the server, and goes in.
//  3. Opened in another browser: "在这里登录 Deneb？"; a click logs in.
//  4. Logged in there already, as the link's email: straight in, the link
//     left unused.
//  5. A link used already: the server's words, and back to the login.
//  6. (with an invite link of the server) An invite card waiting for the
//     code: the link opened beside it logs in, and the card joins.
//
// Stills go to the out directory.
//
// usage: node web/app/loginlink-test.mjs http://127.0.0.1:47652 <control log> /tmp/out [light|dark] [invite link]
import { open, sleep } from '../probe/cdp.mjs';
import { mkdirSync, readFileSync } from 'node:fs';

const [base, log, out, theme = 'light', invite = ''] = process.argv.slice(2);
if (!base || !log || !out) {
  console.error('usage: node web/app/loginlink-test.mjs <deneb-web> <control log> <out> [light|dark]');
  process.exit(2);
}
mkdirSync(out, { recursive: true });
const dark = theme === 'dark';
const fails = [];
const check = (ok, what) => {
  console.log((ok ? 'ok   ' : 'FAIL ') + what);
  if (!ok) fails.push(what);
};
async function until(b, js, ms = 6000) {
  for (let t = 0; t < ms; t += 100) {
    if (await b.ev(js).catch(() => false)) return true;
    await sleep(100);
  }
  return false;
}
const text = (b, sel) => b.ev(`(document.querySelector(${JSON.stringify(sel)}) || {}).textContent || ''`);
const type = async (b, sel, s) => {
  await b.ev(`document.querySelector(${JSON.stringify(sel)}).focus()`);
  await b.S('Input.insertText', { text: s });
};
const go = async (b, path) => b.S('Page.navigate', { url: base + path });
const post = (path, body) => fetch(base + path, { method: 'POST', headers: { 'Content-Type': 'application/json', Origin: base }, body: JSON.stringify(body) });
const onFirstPage = `location.pathname === '/' && !!document.querySelector('.welcome')`;

// The link the server last wrote into its log for email (its mail, not
// sent): the line after the code's. Not the one in skip.
async function linkFor(email, skip = '') {
  for (let t = 0; t < 5000; t += 100) {
    const lines = readFileSync(log, 'utf8').split('\n');
    for (let i = lines.length - 1; i > 0; i--) {
      const m = lines[i].match(/its login link is \S+\/login\/link#([A-Za-z0-9_-]+)/);
      if (m && lines[i - 1].includes(`the login code for ${email} is`) && m[1] !== skip) return m[1];
    }
    await sleep(100);
  }
  throw new Error('no login link in the log for ' + email);
}

// A page of b at the code's step for email.
async function waitForCode(b, email) {
  await go(b, '/login');
  await until(b, `!!document.querySelector('input[type=email]')`);
  await type(b, 'input[type=email]', email);
  await b.ev(`document.querySelector('.jc-form button').click()`);
  return until(b, `document.querySelectorAll('input.cb').length === 6`);
}

const run = Date.now().toString(36);
const here = `here-${run}@example.net`;
const frozen = `frozen-${run}@example.net`;
const away = `away-${run}@example.net`;
const browsers = [];
try {
  // ---- 1. The same browser ----------------------------------------------------------------
  const a = await open({ dark });
  browsers.push(a);
  check(await waitForCode(a, here), 'a page waits for the code');
  const t1 = await linkFor(here);
  const b = await a.tab();
  await go(b, '/login/link#' + t1);
  check(await until(b, `!!document.querySelector('.ll-go')`), 'the link opens its page, which asks first');
  check((await text(b, '.si-title')) === '登录 Deneb', 'in the browser that waits: "登录 Deneb"');
  const sub1 = await text(b, '.si-sub');
  check(sub1.includes(here) && sub1.includes('原来的页面也会自己进去'), 'whom it logs in as, and that the other page goes in too');
  check(await b.ev(`location.pathname === '/login/link' && location.hash === ''`), 'the token is off the address bar');
  await sleep(400); // its fade in
  await b.shot(`${out}/1-link-here.png`);
  await b.ev(`document.querySelector('.ll-go').click()`);
  check(await until(b, `(document.querySelector('.ll-go') || {}).textContent === '登录了'`, 4000), 'a click logs in: the button ticks');
  check(await until(b, onFirstPage), 'the link page goes on to the first page');
  // Back on the tab that waited (in the background, its clock stood still):
  // it says how it got in, then goes on.
  await a.S('Page.bringToFront');
  check(await until(a, `/用邮件里的链接登录了/.test(document.querySelector('.jc-error.ok')?.textContent || '')`, 4000), 'back on the page that waited: it says it is in by the link');
  await a.shot(`${out}/1-waited-in.png`);
  check(await until(a, onFirstPage), 'and goes on to the first page');

  // ---- 2. The page that waited, told nothing -----------------------------------------------
  // A new browser (logged out), its page waiting; the link used by a request
  // from another tab of it, with no word on the channel: the session is
  // the browser's, the page does not know.
  const f = await open({ dark });
  browsers.push(f);
  check(await waitForCode(f, frozen), 'a page waits for the code (2)');
  const t2 = await linkFor(frozen);
  const g = await f.tab();
  await go(g, '/login');
  await until(g, `document.readyState === 'complete'`);
  const r2 = await g.ev(`fetch('/api/v1/login/link', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ token: ${JSON.stringify(t2)} }) }).then((r) => r.status)`);
  check(r2 === 200, 'logged in by the link from another tab, quietly');
  await sleep(500);
  check(await f.ev(`document.querySelectorAll('input.cb').length === 6 && !document.querySelector('.jc-error.ok')`), 'the page that waited does not know yet');
  await f.S('Page.bringToFront');
  check(await until(f, `/登录了/.test(document.querySelector('.jc-error.ok')?.textContent || '')`, 4000), 'back on it, it asks the server, and is in');
  check(await until(f, onFirstPage), 'on to the first page');

  // ---- 3. Another browser ------------------------------------------------------------------
  check((await post('/api/v1/login/email', { email: away })).status === 200, 'a code asked for, from elsewhere');
  const t3 = await linkFor(away);
  const c = await open({ dark });
  browsers.push(c);
  await go(c, '/login/link#' + t3);
  check(await until(c, `!!document.querySelector('.ll-go')`), 'another browser: the link page asks');
  check((await text(c, '.si-title')) === '在这里登录 Deneb？', 'it asks "在这里登录 Deneb？"');
  check((await text(c, '.si-sub')).includes(away), 'with the email');
  check((await text(c, '.ll-go')) === '在这里登录', 'the button says "在这里登录"');
  await sleep(400);
  await c.shot(`${out}/3-link-away.png`);
  await c.ev(`document.querySelector('.ll-go').click()`);
  check(await until(c, onFirstPage), 'a click logs in, on to the first page');

  // ---- 4. Logged in there already ----------------------------------------------------------
  check((await post('/api/v1/login/email', { email: away })).status === 200, 'a new code asked for (the last one used)');
  const t4 = await linkFor(away, t3);
  await go(c, '/login/link#' + t4);
  check(await until(c, onFirstPage), 'logged in already as that email: straight in');
  check((await post('/api/v1/login/link/info', { token: t4 })).status === 200, 'the link left unused');

  // ---- 5. A link used already --------------------------------------------------------------
  await c.S('Network.clearBrowserCookies');
  await go(c, '/login/link#' + t3);
  check(await until(c, `/用不了了/.test(document.querySelector('.si-title')?.textContent || '')`), 'a used link: says it does not work');
  check((await text(c, '.si-sub')).includes('回到登录页再发一封'), 'and what to do, in the server\'s words');
  await sleep(400);
  await c.shot(`${out}/5-link-used.png`);
  await c.ev(`document.querySelector('.ll-foot .btn').click()`);
  check(await until(c, `location.pathname === '/login' && !!document.querySelector('input[type=email]')`), '"回到登录页" goes to the login');

  // ---- 6. An invite card ---------------------------------------------------------------------
  if (invite) {
    const who = `invite-${run}@example.net`;
    const d = await open({ dark });
    browsers.push(d);
    await d.S('Page.navigate', { url: invite });
    check(await until(d, `!!document.querySelector('[data-act="join"]')`), 'an invite: its card');
    await d.ev(`document.querySelector('[data-act="join"]').click()`);
    check(await until(d, `!!document.querySelector('input[type=email]')`), 'its email');
    await type(d, 'input[type=email]', who);
    await d.ev(`document.querySelector('.jc-form button').click()`);
    check(await until(d, `document.querySelectorAll('input.cb').length === 6`), 'its code');
    const t6 = await linkFor(who);
    const e = await d.tab();
    await go(e, '/login/link#' + t6);
    check(await until(e, `!!document.querySelector('.ll-go')`), 'the link page, beside the card');
    check((await text(e, '.si-title')) === '登录 Deneb', 'the card waits in this browser: "登录 Deneb"');
    await e.ev(`document.querySelector('.ll-go').click()`);
    check(await until(e, onFirstPage), 'the link page goes in');
    await d.S('Page.bringToFront');
    check(await until(d, `!!document.querySelector('.jc-joined')`, 8000), 'back on the card: in by the link, and joined');
    await d.shot(`${out}/6-invite-joined.png`);
  }
} catch (err) {
  check(false, 'ran through: ' + err.message);
} finally {
  for (const x of browsers) await x.close();
}
console.log(fails.length ? `${fails.length} FAILED` : 'all ok');
process.exit(fails.length ? 1 : 0);
