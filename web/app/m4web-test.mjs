// M4 on the web (DESIGN.md §14.12): against a development control server
// filled by web/probe/devworld (real server code, nothing live). The
// relays page: the share bar dragged, a card's 只当备用 and 多用 (M4-11); a
// relay dragged onto a LAN's row, the row and the word beside it, undo; one
// already there; a LAN's chip pulled out of its cell; a public one held;
// the card's × and "+ 局域网" (M4-12); someone turned away and back; a new
// server of mine installed. The deployment's page (M4-13): its row, the
// headline, the bar, a narrow segment's name on hover, a card with who uses
// it most, a kick and undo, a new public relay that comes in as standby
// until 多用. Someone who is no operator has neither. A line per check,
// screenshots at the steps; exit 1 on a failed one.
// usage: node web/app/m4web-test.mjs <outprefix> [light|dark] [web] [world]
import { open, sleep } from '../probe/cdp.mjs';
const [out, theme = 'light', base = 'http://127.0.0.1:47191', world = 'http://127.0.0.1:47199'] = process.argv.slice(2);
const ids = await (await fetch(world + '/ids')).json();
const L = ids.lans, R = ids.relays;
const b = await open({ W: 1280, H: 800, scale: 1, dark: theme === 'dark' });
let failed = 0;
const say = (ok, w) => (console.log((ok ? 'ok   ' : 'FAIL ') + w), ok || failed++);
const until = async (js, ms = 5000) => { for (let t = 0; t < ms; t += 100) { if (await b.ev(js)) return true; await sleep(100); } return false; };
const text = (sel) => b.ev(`(document.querySelector(${JSON.stringify(sel)}) || {}).textContent || ''`);
const ctr = (sel) => b.ev(`(() => { const e = document.querySelector(${JSON.stringify(sel)}); if (!e) return null; const r = e.getBoundingClientRect(); return [r.left + r.width / 2, r.top + r.height / 2]; })()`);
const rect = (sel) => b.ev(`(() => { const e = document.querySelector(${JSON.stringify(sel)}); if (!e) return null; const r = e.getBoundingClientRect(); return [r.left, r.top, r.width, r.height]; })()`);
const click = async (sel) => { const p = await ctr(sel); if (!p) return say(false, 'no ' + sel); await b.click(p[0], p[1]); };
const hover = async (sel) => { const p = await ctr(sel); if (p) await b.S('Input.dispatchMouseEvent', { type: 'mouseMoved', x: p[0], y: p[1] }); };
const shot = (n) => b.shot(`${out}-${n}-${theme}.png`);
const toast = () => text('.toast');
const view = () => b.ev(`fetch('/api/v1/userview').then(r => r.json())`);
const mine = async () => Object.fromEntries((await view()).myRelays.map((r) => [r.id, r]));
const pubs = async () => Object.fromEntries((await view()).operator.relays.map((r) => [r.id, r]));
const segs = () => b.ev(`[...document.querySelectorAll('.wseg.act')].filter((e) => e.style.display !== 'none').map((e) => e.getAttribute('aria-label')).join(' | ')`);
const spare = (id) => b.ev(`(() => { const e = document.querySelector('.wseg.spare[data-r="${id}"]'); return !!e && e.style.display !== 'none' && +e.style.opacity > 0.9; })()`);
let lastPos = [0, 0];
// drag picks up from, starts moving, then goes to `to` (a selector or a point).
async function drag(from, to, hold = 300) {
  const a = typeof from === 'string' ? await ctr(from) : from;
  if (!a) return say(false, `drag ${from}`);
  await b.S('Input.dispatchMouseEvent', { type: 'mouseMoved', x: a[0], y: a[1] });
  await b.mouse('mousePressed', a[0], a[1]);
  await sleep(60);
  for (let i = 1; i <= 4; i++) {
    await b.S('Input.dispatchMouseEvent', { type: 'mouseMoved', x: a[0] + i, y: a[1] - 2 * i, button: 'left', buttons: 1 });
    await sleep(16);
  }
  await sleep(250);
  const s = [a[0] + 4, a[1] - 8];
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
const cell = (id) => `.relays-page .rcell[data-k="${id}"]`;
const row = (lan) => `.side .nav[data-id="lan:${lan}"]`;
const install = (command, name, addr) => fetch(world + '/install', { method: 'POST', body: JSON.stringify({ command, name, addr }) }).then((r) => r.status);

// ---- The relays page (M4-11, M4-12) ---------------------------------------------------------
await as('kittors');
await go('/relays');
say(await until(`document.querySelectorAll('.relays-page .rwell.mine .rcell:not(.add-cell)').length === 3`), 'my three relays in their well');
say((await b.ev(`[...document.querySelectorAll('.relays-page .rwell.pub .rname')].map((e) => e.textContent).join(',')`)) === '香港,东京,法兰克福', 'the public ones, in the order of the deployment');
say((await text(`${cell(R.fresh)} small`)).includes('还没进局域网'), 'the new one: not in a LAN yet');
say(/周末联机/.test(await text(`${cell(R.office)} .lchips`)) && /工作室/.test(await text(`${cell(R.office)} .lchips`)), '公司机房: its two LANs as chips');
say((await segs()) === '公司机房 50% | 上海 50%', 'the bar: ' + (await segs()));
say(/50% 的设备先经公司机房，50% 先经上海；都用不了时，自动经公共中转站/.test(await text('.wsay')), 'the line under it: ' + (await text('.wsay')));
await shot('relays');

// The handle dragged to three quarters: sent when let go.
const bar = await rect('.relays-page .wbar');
await drag('.relays-page .whandle', [bar[0] + bar[2] * 0.75, bar[1] + bar[3] / 2]);
say(/75%/.test(await segs()), 'dragging: the shares follow the hand: ' + (await segs()));
await release();
await sleep(900);
let m = await mine();
say(m[R.office].weight === 75 && m[R.sh].weight === 25, `weights sent on letting go: ${m[R.office].weight}/${m[R.sh].weight}`);
say(/75% 的设备先经公司机房，25% 先经上海/.test(await text('.wsay')), 'the line says it');
say(!(await b.ev(`!!document.querySelector('.toast')`)), 'no toast for it (dragging back is the undo)');

// 上海's card: 只当备用, then 多用.
await click(`${cell(R.sh)} .orb`);
say(await until(`!!document.querySelector('.relays-card .seg')`), "上海's card: 分到多少");
say(/大约 25% 的设备先经它/.test(await text('.relays-card')), 'its share in words');
say(/哪些局域网在用/.test(await text('.relays-card')) && /内容全程加密/.test(await text('.relays-card')), 'which LANs use it; the encryption line');
say(/这个月替你转了/.test(await text('.relays-card')) && /谁在用/.test(await text('.relays-card')), 'this month; who uses it');
await shot('card');
await click('.relays-card [data-act="spare"]');
await sleep(900);
m = await mine();
say(m[R.sh].weight === 0, 'only standby: weight 0');
say(await spare(R.sh), 'a dashed 上海 · 备用 at the end of the bar');
say((await segs()) === '公司机房 100%', '公司机房 takes all: ' + (await segs()));
say(/上海只当备用，公司机房用不了时才用/.test(await text('.wsay')), 'the line: standby');
say(await b.ev(`document.querySelector('.relays-card [data-act="spare"]').classList.contains('on') && document.querySelector('.relays-card [data-act="less"]').disabled`), 'the card: 只当备用 pressed, 少用 off');
await shot('standby');
await click('.relays-card [data-act="more"]');
await sleep(900);
m = await mine();
say(m[R.sh].weight === 25 && m[R.office].weight === 75, `多用: back to what it had (${m[R.office].weight}/${m[R.sh].weight})`);
await click('.relays-card .sheet-close');
await sleep(400);

// The new one onto 周末联机's row: the row lights, the word beside it.
await drag(`${cell(R.fresh)} .orb`, row(L.weekend));
say(await b.ev(`document.querySelector('${row(L.weekend)}').classList.contains('target')`), "周末联机's row lights up");
say(/松手，周末联机就用上它/.test(await text('.rpill .rnote')), 'beside it: ' + (await text('.rpill .rnote')));
await shot('over-row');
await release();
await sleep(1000);
say(/周末联机用上了我的服务器/.test(await toast()), 'toast: ' + (await toast()));
m = await mine();
say(m[R.fresh].lans.includes(L.weekend), 'relay.add sent');
say(m[R.office].weight === 50 && m[R.sh].weight === 15 && m[R.fresh].weight === 35, `it takes an equal share (${m[R.office].weight}/${m[R.sh].weight}/${m[R.fresh].weight})`);
say(await until(`!!document.querySelector('${cell(R.fresh)} .lchip')`), 'its chip in the cell');
await shot('joined');
await click('.toast button');
await sleep(1000);
m = await mine();
say(!m[R.fresh].lans.length && m[R.office].weight === 75 && m[R.sh].weight === 25 && m[R.fresh].weight === 1, `undo: out again, the weights as they were (${m[R.office].weight}/${m[R.sh].weight}/${m[R.fresh].weight})`);

// 公司机房 onto 周末联机, which uses it already.
await drag(`${cell(R.office)} .orb`, row(L.weekend));
say(!(await b.ev(`document.querySelector('${row(L.weekend)}').classList.contains('target')`)), 'a LAN that has it: its row stays dark');
say(/周末联机已经在用了/.test(await text('.rpill .rnote')), 'beside it: ' + (await text('.rpill .rnote')));
await release();
await sleep(800);
say(!(await b.ev(`!!document.querySelector('.rpill')`)) && !(await b.ev(`!!document.querySelector('.toast')`)), 'back to its cell, nothing said');

// 工作室's chip pulled out of 公司机房's cell.
const chip = `${cell(R.office)} .lchip[data-l="${L.studio}"]`;
const cr = await rect(cell(R.office));
await drag(chip, [cr[0] + cr[2] / 2, cr[1] + cr[3] + 70]);
say(/松手，工作室就不用它了/.test(await text('.rpill .rnote')), 'out of the cell: ' + (await text('.rpill .rnote')));
await release();
await sleep(1000);
say(/工作室不用公司机房了/.test(await toast()), 'toast: ' + (await toast()));
m = await mine();
say(!m[R.office].lans.includes(L.studio), 'relay.remove sent');
await click('.toast button');
await sleep(1000);
m = await mine();
say(m[R.office].lans.includes(L.studio), 'undo: 工作室 uses it again');
say(await until(`!!document.querySelector('${chip}')`), 'its chip back');

// A public one held: it need not go anywhere.
await drag(`${cell(R['pub-hk'])} .orb`, row(L.weekend));
say(/公共中转站一直都在，不用拖/.test(await text('.rpill .rnote')), 'a public one: ' + (await text('.rpill .rnote')));
await release();
await sleep(800);

// The card's × and "+ 局域网".
await click(`${cell(R.office)} .orb`);
await until(`!!document.querySelector('.relays-card .lan-row')`);
await click(`.relays-card .lan-row .lchip[data-l="${L.studio}"] .x`);
await sleep(1000);
m = await mine();
say(!m[R.office].lans.includes(L.studio) && /工作室不用公司机房了/.test(await toast()), '× in the card: 工作室 no longer uses it');
await click('.relays-card .lan-row .lchip.add');
say(await until(`!!document.querySelector('.relays-card .lan-row .lchip.pick[data-l="${L.studio}"]')`), '"+ 局域网" opens my LANs');
await click(`.relays-card .lan-row .lchip.pick[data-l="${L.studio}"]`);
await sleep(1000);
m = await mine();
say(m[R.office].lans.includes(L.studio) && /工作室用上了公司机房/.test(await toast()), 'picked: 工作室 uses it again');

// Someone turned away, and back.
const who = await b.ev(`(document.querySelector('.relays-card .people .chip') || {}).dataset?.k`);
if (who) {
  const name = await b.ev(`[...document.querySelector('.relays-card .people .chip').childNodes].filter((n) => n.nodeType === 3).map((n) => n.textContent).join('').trim()`);
  await click(`.relays-card .people .chip[data-k="${who}"]`);
  await sleep(900);
  say(new RegExp(`已不让${name}用公司机房`).test(await toast()), 'toast: ' + (await toast()));
  say(await b.ev(`document.querySelector('.relays-card .people .chip[data-k="${who}"]').classList.contains('blocked')`), 'the chip greys where it is');
  await shot('kicked');
  await click(`.relays-card .people .chip[data-k="${who}"]`);
  await sleep(900);
  say(new RegExp(`又让${name}用公司机房了`).test(await toast()), 'again: ' + (await toast()));
} else say(false, 'someone on 公司机房 to turn away');
await click('.relays-card .sheet-close');
await sleep(400);

// A server of mine set up: the line, the waiting cell, then it comes.
await click('.relays-page .rcell[data-k="add"]');
say(await until(`/docker run/.test((document.querySelector('.relays-card .cmd') || {}).textContent || '')`), 'the one line to run');
say(await until(`!!document.querySelector('.relays-page .rcell[data-k="join"]')`), 'the cell waits for it');
const cmd = await text('.relays-card .cmd');
say((await install(cmd, '', '203.0.113.12')) === 200, 'the line run on a server');
say(await until(`[...document.querySelectorAll('.relays-page .rwell.mine .rname')].some((e) => e.textContent === '203.0.113.12')`, 8000), 'it comes in, its address for a name');
say(/我的服务器上线了/.test(await toast()), 'toast: ' + (await toast()));
await shot('installed');

// ---- The deployment's page (M4-13) -------------------------------------------------------
say(await b.ev(`!!document.querySelector('.side .nav[data-id="ops"]')`), "the sidebar's 运营, under 部署");
await go('/ops');
say(await until(`!!document.querySelector('.relays-page .rwell.ops')`), 'the ops page');
say(/个公共中转站都在线 · 现在 \d+ 个人、\d+ 台设备在经它们中转/.test(await text('.head .sub-text')), 'headline: ' + (await text('.head .sub-text')));
say((await segs()) === '香港 60% | 东京 30% | 法兰克福 10%', 'the bar: ' + (await segs()));
say(await b.ev(`document.querySelector('.wseg.act[data-r="${R['pub-fra']}"]').classList.contains('narrow')`), '法兰克福 too narrow for its name');
await hover(`.wseg.act[data-r="${R['pub-fra']}"]`);
await sleep(300);
say((await text('.wtip')) === '法兰克福' && (await b.ev(`document.querySelector('.wtip').classList.contains('on')`)), 'its name over it on hover');
await shot('ops');
await click(`${cell(R['pub-hk'])} .orb`);
say(await until(`/用得最多的/.test((document.querySelector('.relays-card') || {}).textContent || '')`), "香港's card: who uses it most");
say(/有点忙/.test(await text('.relays-card .status')), 'busy: ' + (await text('.relays-card .status')));
say(/这个月转了/.test(await text('.relays-card')) && /带宽/.test(await text('.relays-card')), 'now: bandwidth, this month');
say(await b.ev(`[...document.querySelectorAll('.relays-card .people .chip .gb')].length > 0`), 'each with how much');
await shot('ops-card');
// The bar from 香港 to 东京: 45 : 45.
const ob = await rect('.relays-page .wbar');
await drag('.relays-page .whandle', [ob[0] + ob[2] * 0.45, ob[1] + ob[3] / 2]);
await release();
await sleep(900);
let p = await pubs();
say(p[R['pub-hk']].weight === 45 && p[R['pub-tokyo']].weight === 45, `weights: ${p[R['pub-hk']].weight}/${p[R['pub-tokyo']].weight}/${p[R['pub-fra']].weight}`);
// A kick, and undo.
const top = await b.ev(`(() => { const c = [...document.querySelectorAll('.relays-card .people .chip')].find((x) => !x.disabled); return c && c.dataset.k; })()`);
await click(`.relays-card .people .chip[data-k="${top}"]`);
await sleep(900);
say(/已不让.+用香港/.test(await toast()), 'toast: ' + (await toast()));
await click('.toast button');
await sleep(1200);
const usage = await b.ev(`fetch('/api/v1/operator/relay-usage').then((r) => r.json())`);
say(!(usage.relays.find((r) => r.relay === R['pub-hk']).kicks || []).some((k) => !k.liftedAt), 'undo: no one turned away');
await click('.relays-card .sheet-close');
await sleep(400);
// A new public one: standby until 多用.
await click('.relays-page .rcell[data-k="add"]');
say(await until(`/docker run/.test((document.querySelector('.relays-card .cmd') || {}).textContent || '')`), 'its one line');
say(/刚装好的先当备用/.test(await text('.relays-card')), 'it says it comes in as standby');
say((await install(await text('.relays-card .cmd'), '新加坡', '203.0.113.7')) === 200, 'run');
say(await until(`[...document.querySelectorAll('.relays-page .rname')].some((e) => e.textContent === '新加坡')`, 8000), '新加坡 comes in');
say(/新加坡上线了，先当备用/.test(await toast()), 'toast: ' + (await toast()));
const sg = (await b.ev(`fetch('/api/v1/userview').then((r) => r.json()).then((v) => v.relays.find((r) => r.name === '新加坡').id)`));
await sleep(800);
say(await spare(sg), 'a dashed 新加坡 · 备用 at the end of the bar');
say(await until(`/新加坡/.test((document.querySelector('.relays-card .top') || {}).textContent || '')`), 'the card is the new one');
await click('.relays-card [data-act="more"]');
await sleep(1000);
p = await pubs();
say(p[sg].weight === 20, `多用: it takes 20% (${p[sg].weight})`);
await shot('ops-new');

// ---- Someone who runs no deployment ----------------------------------------------------
await as('mia');
await go('/relays');
say(await until(`!!document.querySelector('.relays-page .rwell.pub')`), "Mia's relays page");
say(!(await b.ev(`!!document.querySelector('.side .nav[data-id="ops"]')`)), 'no 运营 for her');
await go('/ops');
say(await until(`location.pathname === '/relays'`), '/ops takes her to the relays page');

console.log(failed ? `${failed} failed` : 'all ok');
await b.close();
process.exit(failed ? 1 : 0);
