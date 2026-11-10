// The relays page on the mock: open cards, turn someone away and back, the
// install command, a relay coming up.
import { open, sleep } from '../probe/cdp.mjs';
const [out, theme = 'light', base = 'http://127.0.0.1:47624'] = process.argv.slice(2);
const b = await open({ W: 1280, H: 800, scale: 2, dark: theme === 'dark' });
const fails = [];
const check = (ok, w) => { console.log((ok ? 'ok   ' : 'FAIL ') + w); if (!ok) fails.push(w); };
const until = async (js, ms = 6000) => { for (let t = 0; t < ms; t += 100) { if (await b.ev(js)) return true; await sleep(100); } return false; };
const text = (sel) => b.ev(`(document.querySelector(${JSON.stringify(sel)}) || {}).textContent || ''`);
const click = (sel) => b.ev(`document.querySelector(${JSON.stringify(sel)}).click()`);
await b.S('Browser.grantPermissions', { permissions: ['clipboardReadWrite', 'clipboardSanitizedWrite'], origin: base }).catch(() => {});
await b.S('Page.navigate', { url: base + '/login' }); await sleep(600);
await b.ev(`(async () => { const h = { 'Content-Type': 'application/json' }; await fetch('/api/v1/logout', { method: 'POST', headers: h, body: '{}' }); await fetch('/api/v1/login/email', { method: 'POST', headers: h, body: JSON.stringify({ email: 'kittors@deneb.test' }) }); await fetch('/api/v1/login/code', { method: 'POST', headers: h, body: JSON.stringify({ email: 'kittors@deneb.test', code: '000000' }) }); })()`);
await b.S('Page.navigate', { url: base + '/relays?theme=' + theme });
check(await until(`!!document.querySelector('.relays-page .rcell[data-k="r_tokyo"]')`), 'my relay 东京 in its well');
check(await until(`/\\d+ 个人在用/.test((document.querySelector('.rcell[data-k="r_tokyo"] small') || {}).textContent || '')`), 'its second line: ' + (await text('.rcell[data-k="r_tokyo"] small')));
check(!!(await b.ev(`document.querySelector('.rwell.pub .rcell[data-k="r_hk"]')`)), 'the public 香港 in the other well');
check(!!(await b.ev(`document.querySelector('.rcell[data-k="add"]')`)), '用我自己的服务器 cell');
check(await b.ev(`[...document.querySelectorAll('.rcell')].every((c) => c.getAttribute('aria-label') && c.tabIndex === 0)`), 'cells named and focusable');
await sleep(500);
await b.shot(`${out}/r1-page-${theme}.png`);

await click('.rcell[data-k="r_tokyo"]');
check(await until(`!!document.querySelector('.relays-card .chip')`), 'card: who is on it');
const who = await b.ev(`document.querySelector('.relays-card .chip').dataset.k`);
const whoName = await b.ev(`[...document.querySelector('.relays-card .chip').childNodes].filter((n) => n.nodeType === 3).map((n) => n.textContent).join('').trim()`);
const CH = `.relays-card .chip[data-k="${who}"]`;
const card = await text('.relays-card');
check(/在线/.test(card) && /个人 · \d+ 台设备/.test(card) && /MB\/s|KB\/s/.test(card) && /不忙/.test(card) && /周末联机/.test(card) && /GB/.test(card), 'card: now and this month');
check(!(await b.ev(`!!document.querySelector('.relays-card .chip[data-k="p_kittors"]')`)), 'not myself among who is on it');
await sleep(400);
await b.shot(`${out}/r2-card-${theme}.png`);

const order0 = await b.ev(`[...document.querySelectorAll('.relays-card .chip')].map((c) => c.dataset.k).join(',')`);
await click(CH);
check(await until(`document.querySelector('${CH}').classList.contains('blocked')`), `kick: ${whoName} greyed`);
check((await b.ev(`[...document.querySelectorAll('.relays-card .chip')].map((c) => c.dataset.k).join(',')`)) === order0, 'the balls keep their order: ' + order0);
check(await until(`/已不让${whoName}用东京中转站/.test((document.querySelector('.toast') || {}).textContent || '')`), 'toast with undo: ' + (await text('.toast')));
await sleep(500);
await b.shot(`${out}/r3-kicked-${theme}.png`);
await click('.toast button');
check(await until(`!document.querySelector('${CH}').classList.contains('blocked')`), `undo: ${whoName} back`);
await click(CH);
check(await until(`document.querySelector('${CH}').classList.contains('blocked')`), 'kick again');
await sleep(300);
await click(CH);
check(await until(`!document.querySelector('${CH}').classList.contains('blocked')`), 'a second click lets him back');
check(await until(`!document.querySelector('.toast') || !/已不让/.test(document.querySelector('.toast').textContent) || getComputedStyle(document.querySelector('.toast')).opacity < 0.5`, 1500), 'letting him back takes the toast away');

await click('.rcell[data-k="r_hk"]');
check(await until(`/自动兜底，不用管/.test((document.querySelector('.relays-card') || {}).textContent || '')`), 'public card: look only');
check(/每条连接 2 MB\/s/.test(await text('.relays-card')), 'public card: rate limit');
check(!(await b.ev(`!!document.querySelector('.relays-card .chip')`)), 'public card: no one to turn away');
await sleep(400);
await b.shot(`${out}/r4-public-${theme}.png`);

const h0 = await b.ev(`document.querySelector('.rwell.mine').getBoundingClientRect().height`);
await click('.rcell[data-k="add"]');
check(await until(`/docker run/.test((document.querySelector('.relays-card .cmd') || {}).textContent || '')`), 'the one line: ' + (await text('.relays-card .cmd')).slice(0, 60));
check(await until(`!!document.querySelector('.rcell[data-k="join"] .orb.wait')`), 'waiting cell breathes');
await click('.relays-card [data-act="copy"]');
check(await until(`/已复制|已选中/.test((document.querySelector('.relays-card [data-act="copy"]') || {}).textContent || '')`), 'copy: ' + (await text('.relays-card [data-act="copy"]')));
await sleep(500);
await b.shot(`${out}/r5-command-${theme}.png`);

const n0 = await b.ev(`document.querySelectorAll('.rwell.mine .rcell').length`);
await b.ev(`fetch('/api/v1/_mock/relay-up', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' }).then((r) => r.status)`);
check(await until(`document.querySelectorAll('.rwell.mine .rcell').length === ${n0 + 1} && !!document.querySelector('.rcell[data-k="add"]')`), 'the new relay is in, the add cell back');
check(await until(`/新加坡/.test((document.querySelector('.relays-card') || {}).textContent || '')`), 'the card is the new relay');
check(await until(`/新加坡中转站上线了/.test((document.querySelector('.toast') || {}).textContent || '')`), 'toast: 上线了');
check(/刚上线/.test(await text('.rcell:nth-child(2) small')) || /刚上线/.test(await b.ev(`[...document.querySelectorAll('.rcell small')].map((x) => x.textContent).join(',')`)), 'its cell says 刚上线');
await sleep(900);
const h1 = await b.ev(`document.querySelector('.rwell.mine').getBoundingClientRect().height`);
check(h1 > h0, `the well grew ${h0} → ${h1}`);
await b.shot(`${out}/r6-new-relay-${theme}.png`);

// Keyboard: Tab to a cell, Enter opens its card.
await b.ev(`document.querySelector('.rcell[data-k="r_hk"]').focus()`);
await b.key('Enter', 'Enter', 13);
check(await until(`/公共中转站/.test((document.querySelector('.relays-card') || {}).textContent || '')`), 'Enter on a cell opens it');
const errs = b.events.filter((e) => e.method === 'Runtime.exceptionThrown').map((e) => e.params.exceptionDetails?.exception?.description?.slice(0, 200));
check(!errs.length, 'no errors ' + JSON.stringify(errs));
console.log(fails.length ? `${fails.length} failed` : 'all passed');
await b.close();
