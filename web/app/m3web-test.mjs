// M3 on the web, filled in (DESIGN.md §6.7, §6.8, §6.11, §14): against a
// development control server filled by web/probe/devworld (real server
// code, nothing live). Someone waiting to be let in; a device waiting for
// the owner; the owner letting both in; sharing from a card ("+"), by
// dragging a device onto a ball, what a share opens, taking it back and
// undo; a person's card; the LAN's card; the seats running full, the
// dashed "邀请朋友", freeing a seat and undo; a member dragged onto another
// LAN's row. A line per check, screenshots at the steps; exit 1 on a
// failed one.
// usage: node web/app/m3web-test.mjs <outprefix> [light|dark] [web] [world]
import { open, sleep } from '../probe/cdp.mjs';
const [out, theme = 'light', base = 'http://127.0.0.1:47191', world = 'http://127.0.0.1:47199'] = process.argv.slice(2);
const ids = await (await fetch(world + '/ids')).json();
const L = ids.lans, Dv = ids.devices, Pp = ids.people;
const b = await open({ W: 1280, H: 800, scale: 1, dark: theme === 'dark' });
let failed = 0;
const say = (ok, w) => (console.log((ok ? 'ok   ' : 'FAIL ') + w), ok || failed++);
const until = async (js, ms = 5000) => { for (let t = 0; t < ms; t += 100) { if (await b.ev(js)) return true; await sleep(100); } return false; };
const text = (sel) => b.ev(`(document.querySelector(${JSON.stringify(sel)}) || {}).textContent || ''`);
const ctr = (sel) => b.ev(`(() => { const e = document.querySelector(${JSON.stringify(sel)}); if (!e) return null; const r = e.getBoundingClientRect(); return [r.left + r.width / 2, r.top + r.height / 2]; })()`);
const click = async (sel) => { const p = await ctr(sel); if (!p) return say(false, 'no ' + sel); await b.click(p[0], p[1]); };
const hover = async (sel) => { const p = await ctr(sel); if (p) await b.S('Input.dispatchMouseEvent', { type: 'mouseMoved', x: p[0], y: p[1] }); };
const shot = (n) => b.shot(`${out}-${n}-${theme}.png`);
const toast = () => text('.toast');
const view = () => b.ev(`fetch('/api/v1/userview').then(r => r.json())`);
let lastPos = [0, 0];
// drag picks up from, starts moving (so that what spreads for it, spreads),
// then goes to where `to` is by then.
async function drag(from, to, hold = 300) {
  const a = await ctr(from);
  if (!a) return say(false, `drag ${from}`);
  await b.S('Input.dispatchMouseEvent', { type: 'mouseMoved', x: a[0], y: a[1] });
  await b.mouse('mousePressed', a[0], a[1]);
  await sleep(60);
  for (let i = 1; i <= 4; i++) {
    await b.S('Input.dispatchMouseEvent', { type: 'mouseMoved', x: a[0], y: a[1] - 4 * i, button: 'left', buttons: 1 });
    await sleep(16);
  }
  await sleep(500);
  const s = [a[0], a[1] - 16];
  const z = typeof to === 'string' ? await ctr(to) : to;
  if (!z) return say(false, `drag ${from} → ${to}`);
  lastPos = z;
  for (let i = 1; i <= 24; i++) {
    await b.S('Input.dispatchMouseEvent', { type: 'mouseMoved', x: s[0] + ((z[0] - s[0]) * i) / 24, y: s[1] + ((z[1] - s[1]) * i) / 24, button: 'left', buttons: 1 });
    await sleep(16);
  }
  await sleep(hold);
}
const release = () => b.mouse('mouseReleased', lastPos[0], lastPos[1]);
async function as(who) {
  await b.S('Page.navigate', { url: base + '/login' });
  await sleep(500);
  await b.ev(`(async () => { const h = { 'Content-Type': 'application/json' }; await fetch('/api/v1/logout', { method: 'POST' }).catch(() => {}); await fetch('/api/v1/login', { method: 'POST', headers: h, body: JSON.stringify({ email: '${who}@deneb.test', code: '000000' }) }); })()`);
}
const go = async (path) => {
  await b.S('Page.navigate', { url: base + path + '?theme=' + theme });
  await sleep(1500);
};
const tile = (id) => `.dev[data-id="${id}"] .tile`;
const ball = (who) => `.stack .sball[data-k="${Pp[who]}"]`;

// ---- 阿明: waiting to be let into 周末联机 ----
await as('ming');
await go('/lan/' + L.weekend);
say(await until(`!!document.querySelector('.waiting')`), 'ming waits: ' + (await text('.waiting')) + ' · ' + (await text('.head .sub-text')));
say((await text(`.nav[data-id="lan:${L.weekend}"]`)).includes('等同意'), 'his row says 等同意');
await shot('01-waiting');

// ---- 阿杰: his 树莓派 waits for kittors ----
await as('jie');
await go('/lan/' + L.weekend);
say(await until(`/等 ?kittors ?同意/.test((document.querySelector('.dev[data-id="${Dv['jie.pi']}"] .name small') || {}).textContent || '')`), '阿杰 sees his 树莓派 wait: ' + (await text(`.dev[data-id="${Dv['jie.pi']}"] .name small`)));
await click(tile(Dv['jie.pi']));
say(await until(`/同意以后，它才连得进来/.test((document.querySelector('.sheet .status') || {}).textContent || '')`), 'its card: ' + (await text('.sheet .status')));
await shot('02-his-waits');

// ---- kittors ----
await as('kittors');
await go('/lan/' + L.weekend);
say(await until(`!!document.querySelector('${ball('ming')}[data-how="waits"]')`), '阿明 waits: a dashed ball');
say((await text('.stack-hint')) === '还能加 1 人', 'seat line: ' + (await text('.stack-hint')));
say(await b.ev(`!!document.querySelector('.nav[data-id="lan:${L.weekend}"] .ndot')`), 'the row has a blue dot');
say((await text(`.dev[data-id="${Dv['jie.pi']}"] .name small`)) === '等你同意', '树莓派: 等你同意');
await shot('03-owner');
// Let the device in: it fills; the server hears as the toast goes.
await click(tile(Dv['jie.pi']));
say(await until(`!document.querySelector('.dev[data-id="${Dv['jie.pi']}"]').classList.contains('pending')`), 'let in: it fills · ' + (await toast()));
await shot('04-let-in');
say(await until(`fetch('/api/v1/userview').then(r => r.json()).then(v => v.lans.find(l => l.id === '${L.weekend}').devices.some(d => d.device === '${Dv['jie.pi']}' && !d.pending))`, 9000), 'the server let it in once the toast went');
// The LAN's card.
await click('.head .title');
say(await until(`/拥有者/.test((document.querySelector('.sheet .lan-top') || {}).textContent || '')`), 'LAN card: ' + (await b.ev(`[...document.querySelectorAll('.sheet .label')].map(e => e.textContent).join(' / ')`)));
await shot('05-lan-card');
await click('.sheet .sheet-close');
await sleep(300);
// Share the MacBook from its card: "+ 分享给别人", then Leo.
await click(tile(Dv['kittors.mac']));
say(await until(`!!document.querySelector('.sheet .pchip.plus.alone')`), 'MacBook card: + 分享给别人');
await click('.sheet .pchip.plus');
say(await until(`!!document.querySelector('.sheet .picker .pchip[data-pick="${Pp.leo}"]')`), 'picker: ' + (await b.ev(`[...document.querySelectorAll('.sheet .picker .pchip')].map(e => e.textContent).join(', ')`)));
await shot('06-picker');
await click(`.sheet .picker .pchip[data-pick="${Pp.leo}"]`);
say(/已把 ?MacBook ?分享给 ?Leo/.test(await toast()), 'toast: ' + (await toast()));
say(await until(`!!document.querySelector('.sheet .pchip[data-who="${Pp.leo}"]')`), 'the chip is in: ' + (await text('.sheet .hint')));
// What it opens to Leo: the open chip, a switch each.
await click(`.sheet .pchip[data-who="${Pp.leo}"] .pn`);
say(await until(`!!document.querySelector('.sheet .share-open .sw')`), 'open: ' + (await text('.sheet .share-open .label')));
await shot('07-opens-to-leo');
const on0 = await b.ev(`document.querySelectorAll('.sheet .share-open .sw.on').length`);
await click('.sheet .share-open .sw');
say(await until(`fetch('/api/v1/userview').then(r => r.json()).then(v => (v.devices.find(d => d.id === '${Dv['kittors.mac']}').shares || []).find(s => s.person === '${Pp.leo}')?.services.length === ${on0} - 1)`), 'a switch closes one for Leo');
// Take it back, then undo.
await hover(`.sheet .pchip[data-who="${Pp.leo}"]`);
await sleep(200);
await click(`.sheet .pchip[data-who="${Pp.leo}"] .x`);
say(await until(`/已从 ?Leo ?收回 ?MacBook/.test(document.querySelector('.toast')?.textContent || '')`), 'taken back: ' + (await toast()));
say(await until(`!document.querySelector('.sheet .pchip[data-who="${Pp.leo}"]')`), 'the chip goes');
await click('.toast button');
say(await until(`!!document.querySelector('.sheet .pchip[data-who="${Pp.leo}"]')`, 6000), 'undo: it comes back');
await click('.sheet .sheet-close');
await sleep(400);
// The tray's NAS onto Mia's ball: given to her. While held, the stack
// spreads and the line says what letting go does.
await drag(`.tray .mini[data-id="${Dv['kittors.nas']}"]`, ball('mia'));
say(await b.ev(`document.querySelector('.stack').classList.contains('spread')`), 'held: the stack spreads');
say(/松手，只给 ?Mia ?用这一台/.test(await text('.stack-hint')), 'over Mia: ' + (await text('.stack-hint')));
await shot('08-onto-mia');
await release();
say(await until(`/已把 ?NAS ?分享给 ?Mia/.test(document.querySelector('.toast')?.textContent || '')`), 'toast: ' + (await toast()));
say(await until(`!document.querySelector('.stack').classList.contains('spread')`), 'the stack closes up');
// The MacBook is in the LAN: Mia uses it already.
await drag(tile(Dv['kittors.mac']), ball('mia'));
say(/Mia ?已经能用了/.test(await text('.stack-hint')), 'MacBook over Mia: ' + (await text('.stack-hint')));
await shot('09-already');
await release();
await sleep(900);
// Mia's card.
await click(ball('mia'));
say(await until(`/你分享给 ?Mia ?的/.test((document.querySelector('.sheet') || {}).textContent || '')`), 'Mia\'s card: ' + (await b.ev(`(document.querySelector('.sheet .person-top') || {}).textContent`)) + ' · ' + (await b.ev(`[...document.querySelectorAll('.sheet .pchip')].map(e => e.textContent).join(', ')`)));
await shot('10-mia-card');
await click('.sheet .sheet-close');
await sleep(300);
// Let 阿明 in: my seats are then full.
await click(ball('ming'));
say(/已同意 ?阿明 ?加入/.test(await toast()), 'let 阿明 in: ' + (await toast()));
say(await until(`document.querySelector('.head .btn.primary').classList.contains('full')`, 9000), 'seats full: 邀请朋友 is dashed');
await shot('11-full');
await click('.head .btn.primary');
say(await until(`/6 个位置都用上了/.test((document.querySelector('.sheet .seat-title') || {}).textContent || '')`), 'seat card: ' + (await text('.sheet .seat-title')));
await click('.sheet [data-act="free"]');
say(await until(`!!document.querySelector('.sheet .seat-ball .x')`), 'freeing: × on the balls · ' + (await text('.sheet .say')));
await shot('12-freeing');
await click(`.sheet .seat-ball[data-k="${Pp.leo}"] .x`);
say(await until(`/还能加 1 人/.test((document.querySelector('.sheet .seat-title') || {}).textContent || '')`), 'Leo out: ' + (await text('.sheet .seat-title')) + ' · ' + (await toast()));
await shot('13-freed');
await click('.toast button');
say(await until(`/6 个位置都用上了/.test((document.querySelector('.sheet .seat-title') || {}).textContent || '')`), 'undo: full again');
await click('.sheet .sheet-close');
await sleep(400);
// 阿明 onto 工作室's row: invited there.
await drag(ball('ming'), `.nav[data-id="lan:${L.studio}"]`);
say(await b.ev(`document.querySelector('.nav[data-id="lan:${L.studio}"]').classList.contains('target')`), 'the row lights up: ' + (await text('.mpill .rnote')));
await shot('14-invite-ming');
await release();
say(await until(`/已邀请 ?阿明 ?加入 ?工作室/.test(document.querySelector('.toast')?.textContent || '')`), 'toast: ' + (await toast()));
// 阿明 sees the invite by name: its card, then he joins.
await as('ming');
await go('/lan/' + L.weekend);
say(await until(`/kittors ?邀请你加入「工作室」/.test((document.querySelector('.sheet') || {}).textContent || '')`, 4000), 'ming gets the invite card: ' + (await text('.sheet .top')));
say((await text(`.nav[data-id^="invitation:"]`)).includes('邀请你'), 'and a row: ' + (await text(`.nav[data-id^="invitation:"]`)));
await shot('14b-invitation');
await click('.sheet [data-act="join"]');
say(await until(`location.pathname.includes('${L.studio}') || location.pathname === '/shared'`, 6000), 'joined: ' + (await b.ev('location.pathname')));
await as('kittors');
await go('/lan/' + L.weekend);
// Mia is in 工作室 already.
await drag(ball('mia'), `.nav[data-id="lan:${L.studio}"]`);
say((await text('.mpill .rnote')) === '已经在了', 'Mia over 工作室: ' + (await text('.mpill .rnote')));
await release();
await sleep(700);
// 我的设备: a device onto a LAN's row in the sidebar.
await go('/mine');
await drag(tile(Dv['kittors.nas']), `.nav[data-id="lan:${L.weekend}"]`);
say(/松手，加进 ?周末联机/.test(await text('.drag-layer .dnote')), 'NAS over 周末联机: ' + (await text('.drag-layer .dnote')));
say(await b.ev(`document.querySelector('.nav[data-id="lan:${L.weekend}"]').classList.contains('target')`), 'the row lights up');
await shot('15-mine-onto-row');
await release();
say(await until(`/已把 ?NAS ?加进 ?周末联机/.test(document.querySelector('.toast')?.textContent || '')`), 'toast: ' + (await toast()));
await sleep(800);
await drag(tile(Dv['kittors.mac']), `.nav[data-id="lan:${L.weekend}"]`);
say(/周末联机 ?里已经有了/.test(await text('.drag-layer .dnote')), 'MacBook over 周末联机: ' + (await text('.drag-layer .dnote')));
await release();
await sleep(900);
say((await text(`.dev[data-id="${Dv['kittors.mac']}"] .name small`)) === '在线', 'it goes back, its line back');
// 阿杰: who shares with him, their card.
await as('jie');
await go('/shared');
await click(`.stack .sball[data-k="${Pp.kittors}"]`);
say(await until(`/分享了 2 台设备给你/.test((document.querySelector('.sheet .person-top') || {}).textContent || '')`), 'kittors\'s card on 共享给我: ' + (await b.ev(`(document.querySelector('.sheet .person-top') || {}).textContent`)));
await shot('16-sharer-card');
await click('.sheet .pchip.given');
say(await until(`/你可以用/.test((document.querySelector('.sheet') || {}).textContent || '') && /工作室的设备/.test((document.querySelector('.sheet .top') || {}).textContent || '')`), 'a chip opens that device\'s card: ' + (await text('.sheet .top')));
await shot('17-given-card');
const errs = b.events.filter((e) => e.method === 'Runtime.exceptionThrown').map((e) => e.params.exceptionDetails?.exception?.description?.slice(0, 200));
say(!errs.length, 'page errors: ' + (errs.length ? errs.join(' | ') : 'none'));
await b.close();
process.exit(failed ? 1 : 0);
