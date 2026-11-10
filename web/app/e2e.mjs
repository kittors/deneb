#!/usr/bin/env node
// End to end on the mock (deneb-web -mock): a newcomer opens an invite link,
// joins with an email and a wrong code, then the right one, lands in the
// LAN with the download card, downloads, and Deneb comes up on this
// computer. Then the same link on a phone. Stills go to the out directory.
//
// usage: node web/app/e2e.mjs http://127.0.0.1:47621 /tmp/out [light|dark]
import { open, sleep } from '../probe/cdp.mjs';
import { mkdirSync } from 'node:fs';

const [base, out, theme = 'light'] = process.argv.slice(2);
mkdirSync(out, { recursive: true });
const fails = [];
const check = (ok, what) => {
  console.log((ok ? 'ok   ' : 'FAIL ') + what);
  if (!ok) fails.push(what);
};

async function until(b, js, ms = 6000) {
  for (let t = 0; t < ms; t += 100) {
    if (await b.ev(js)) return true;
    await sleep(100);
  }
  return false;
}
const text = (b, sel) => b.ev(`(document.querySelector(${JSON.stringify(sel)}) || {}).textContent || ''`);
const type = async (b, sel, s) => {
  await b.ev(`document.querySelector(${JSON.stringify(sel)}).focus()`);
  await b.S('Input.insertText', { text: s });
};
const click = (b, sel) => b.ev(`document.querySelector(${JSON.stringify(sel)}).click()`);

// ---- A computer --------------------------------------------------------------------------
{
  const b = await open({ W: 1280, H: 800, scale: 2, dark: theme === 'dark' });
  const errs = [];
  await b.S('Log.enable').catch(() => {});
  await b.S('Page.navigate', { url: `${base}/i/k3xQ9?theme=${theme}` });
  check(await until(b, `!!document.querySelector('[data-act="join"]')`), 'invite card with 加入');
  check((await text(b, '.jc-title')).includes('kittors 邀请你加入「周末联机」'), 'invite card title');
  await sleep(400);
  await b.shot(`${out}/1-invite-${theme}.png`);

  await click(b, '[data-act="join"]');
  check(await until(b, `!!document.querySelector('input[type=email]')`), 'email step');
  check(await b.ev(`document.activeElement && document.activeElement.type === 'email'`), 'email field has the focus');
  check(await b.ev(`document.querySelector('.jc-form button').disabled`), 'send button waits for an email');
  await type(b, 'input[type=email]', 'leo.chen@gmail.com');
  check(!(await b.ev(`document.querySelector('.jc-form button').disabled`)), 'send button ready');
  await sleep(300);
  await b.shot(`${out}/2-email-${theme}.png`);
  await click(b, '.jc-form button');

  check(await until(b, `document.querySelectorAll('input.cb').length === 6`), 'six code boxes');
  check((await text(b, '.jc-sub')).includes('l***@gmail.com'), 'masked email');
  await type(b, 'input.cb', '123456');
  check(await until(b, `(document.querySelector('.jc-error') || {}).textContent === '不对，再看看邮件'`), 'a wrong code says so');
  check(await b.ev(`[...document.querySelectorAll('input.cb')].every((x) => !x.value)`), 'the boxes empty');
  await sleep(500);
  await b.shot(`${out}/3-wrong-code-${theme}.png`);
  await type(b, 'input.cb', '000000');
  check(await until(b, `!!document.querySelector('.jc-joined')`), 'joined');
  check((await text(b, '.jc-joined .jc-title')).includes('你加入了「周末联机」'), 'joined title');
  await sleep(700);
  await b.shot(`${out}/4-joined-${theme}.png`);

  check(await until(b, `location.pathname === '/lan/l_weekend'`, 4000), 'on to the LAN page');
  check(await until(b, `!!document.querySelector('.dev[data-id="this-computer"]') && !!document.querySelector('.dl-card')`), 'my empty spot and the download card');
  check((await text(b, '.dl-text')).includes('周末联机'), 'download card names the LAN');
  check((await text(b, '.dl-btn')).includes('macOS'), 'download button for this system');
  await sleep(300);
  await b.shot(`${out}/5-lan-${theme}.png`);
  await b.ev(`document.querySelector('a.dl-btn').addEventListener('click', (e) => e.preventDefault())`);
  await click(b, 'a.dl-btn');
  check(await until(b, `!!document.querySelector('.dl-after')`), 'after the download: where to look');
  check((await text(b, '.dl-after')).includes('l***@gmail.com'), 'with the email to log in with');
  await b.shot(`${out}/6-downloading-${theme}.png`);

  // Deneb installed and logged in on this computer: the stream brings it.
  await b.ev(`fetch('/api/v1/_mock/device-up', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' }).then((r) => r.status)`);
  check(await until(b, `!document.querySelector('.dev[data-id="this-computer"]') && !document.querySelector('.dl-card') && !!document.querySelector('.canvas .dev[data-id^="d_new"]')`), 'the stream brings this computer in');
  await b.shot(`${out}/7-installed-${theme}.png`);

  // Logged in already, the same link: no login, "已经在里面".
  await b.S('Page.navigate', { url: `${base}/i/k3xQ9?theme=${theme}` });
  check(await until(b, `(document.querySelector('.jc-title') || {}).textContent === '你已经在「周末联机」里了'`), 'already in');
  // (An old link of a LAN he is in says he is in: that is what matters.)
  await b.S('Page.navigate', { url: `${base}/i/nope?theme=${theme}` });
  check(await until(b, `(document.querySelector('.jc-title') || {}).textContent === '这个邀请链接不存在'`), 'a broken link');
  const ax = await b.S('Accessibility.getFullAXTree');
  check(ax.nodes.some((n) => !n.ignored && n.role?.value === 'heading' && /邀请链接不存在/.test(n.name?.value || '')), 'a screen reader reads the card');
  await b.close();
}

// ---- A phone ----------------------------------------------------------------------------
{
  const b = await open({ W: 390, H: 844, scale: 3, dark: theme === 'dark' });
  await b.S('Emulation.setDeviceMetricsOverride', { width: 390, height: 844, deviceScaleFactor: 3, mobile: true });
  await b.S('Emulation.setTouchEmulationEnabled', { enabled: true, maxTouchPoints: 5 });
  await b.S('Emulation.setUserAgentOverride', { userAgent: 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1' });
  await b.S('Page.navigate', { url: `${base}/i/old42?theme=${theme}` });
  check(await until(b, `(document.querySelector('.jc-title') || {}).textContent === '这个邀请过期了'`), 'an old link, logged out');
  await b.S('Page.navigate', { url: `${base}/i/k3xQ9?theme=${theme}` });
  check(await until(b, `!!document.querySelector('[data-act="join"]')`), 'phone: invite card');
  await sleep(300);
  await b.shot(`${out}/8-phone-invite-${theme}.png`);
  await click(b, '[data-act="join"]');
  await until(b, `!!document.querySelector('input[type=email]')`);
  await type(b, 'input[type=email]', 'mia2@example.com');
  await click(b, '.jc-form button');
  await until(b, `document.querySelectorAll('input.cb').length === 6`);
  const boxes = await b.ev(`(() => { const r = [...document.querySelectorAll('input.cb')].map((x) => x.getBoundingClientRect()); return { left: r[0].left, right: r[5].right, w: innerWidth } })()`);
  check(boxes.left >= 16 && boxes.right <= boxes.w - 16, 'phone: the six boxes fit');
  await sleep(300);
  await b.shot(`${out}/9-phone-code-${theme}.png`);
  await type(b, 'input.cb', '000000');
  check(await until(b, `location.pathname === '/lan/l_weekend' && !!document.querySelector('.m-dock')`, 6000), 'phone: the LAN with the dock');
  await sleep(300);
  await b.shot(`${out}/10-phone-lan-${theme}.png`);
  await b.close();
}

console.log(fails.length ? `${fails.length} failed` : 'all passed');
process.exit(fails.length ? 1 : 0);
