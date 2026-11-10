// Scene 4 on the mock: drag a device onto a member, open the row, take it
// back, undo; screenshots at each step and console errors.
import { open, sleep } from '../probe/cdp.mjs';
const [out, theme = 'light'] = process.argv.slice(2);
const b = await open({ W: 1280, H: 800, scale: 1, dark: theme === 'dark' });
const base = process.argv[4] || 'http://127.0.0.1:47620';
await b.S('Page.navigate', { url: base + '/login' }); await sleep(700);
await b.ev(`(async () => { const h = { 'Content-Type': 'application/json' }; await fetch('/api/v1/login/email', { method: 'POST', headers: h, body: '{"email":"kittors@deneb.test"}' }); await fetch('/api/v1/login/code', { method: 'POST', headers: h, body: '{"email":"kittors@deneb.test","code":"000000"}' }); })()`);
await b.S('Page.navigate', { url: base + '/share/l_studio?theme=' + theme }); await sleep(2000);
const ctr = (sel) => b.ev(`(() => { const r = document.querySelector(${JSON.stringify(sel)}).getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2 }; })()`);
const drag = async (a, z) => {
  await b.S('Input.dispatchMouseEvent', { type: 'mouseMoved', x: a.x, y: a.y });
  await b.mouse('mousePressed', a.x, a.y); await sleep(80);
  for (let i = 1; i <= 24; i++) { await b.S('Input.dispatchMouseEvent', { type: 'mouseMoved', x: a.x + (z.x - a.x) * i / 24, y: a.y + (z.y - a.y) * i / 24, button: 'left', buttons: 1 }); await sleep(16); }
  await sleep(200); await b.mouse('mouseReleased', z.x, z.y); await sleep(900);
};
const count = (k) => b.ev(`document.querySelector('.mrow[data-k="${k}"] .num').textContent`);
console.log('Leo before:', await count('p_leo'));
await drag(await ctr('.cell[data-d="d_gpu"] .tile'), await ctr('.mrow[data-k="p_leo"] .ball'));
console.log('Leo after share:', await count('p_leo'));
await b.shot(out + '-1-shared.png');
// The same again: "已经有了"
await drag(await ctr('.cell[data-d="d_gpu"] .tile'), await ctr('.mrow[data-k="p_leo"] .ball'));
console.log('note:', await b.ev(`(document.querySelector('.mrow[data-k="p_leo"] .note') || {}).textContent || 'none'`));
await b.shot(out + '-2-already.png');
await b.click(...Object.values(await ctr('.mrow[data-k="p_leo"] .mtop span'))); await sleep(700);
await b.shot(out + '-3-open.png');
await b.click(...Object.values(await ctr('.mrow[data-k="p_leo"] .mchip .x'))); await sleep(700);
console.log('Leo after revoke:', await count('p_leo'), '| toast:', await b.ev(`(document.querySelector('.toast') || {}).textContent || 'none'`));
await b.shot(out + '-4-revoked.png');
await b.click(...Object.values(await ctr('.toast button'))); await sleep(900);
console.log('Leo after undo:', await count('p_leo'));
// The server's view, straight from the API.
console.log('server shares of d_gpu:', await b.ev(`fetch('/api/v1/userview').then(r => r.json()).then(v => JSON.stringify((v.devices.find(d => d.id === 'd_gpu') || {}).shares || []))`));
const errs = b.events.filter((e) => e.method === 'Runtime.exceptionThrown').map((e) => e.params.exceptionDetails?.exception?.description?.slice(0, 200));
console.log('errors:', errs.length ? errs : 'none');
await b.close();
