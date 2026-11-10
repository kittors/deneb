// Frame by frame on the relays page: the wells' heights and every cell's
// place, each animation frame, while the add card opens and a new relay
// comes up. No jumps: a well's height moves at most a few pixels a frame;
// a cell keeps its place or appears/leaves (another key).
import { open, sleep } from '../probe/cdp.mjs';
const [base = 'http://127.0.0.1:47624', theme = 'light'] = process.argv.slice(2);
const b = await open({ W: 1280, H: 800, scale: 1, dark: theme === 'dark' });
await b.S('Page.navigate', { url: base + '/login' }); await sleep(600);
await b.ev(`(async () => { const h = { 'Content-Type': 'application/json' }; await fetch('/api/v1/login/email', { method: 'POST', headers: h, body: JSON.stringify({ email: 'kittors@deneb.test' }) }); await fetch('/api/v1/login/code', { method: 'POST', headers: h, body: JSON.stringify({ email: 'kittors@deneb.test', code: '000000' }) }); })()`);
await b.S('Page.navigate', { url: base + '/relays?theme=' + theme });
for (let i = 0; i < 60 && !(await b.ev(`!!document.querySelector('.rcell[data-k="r_tokyo"]')`)); i++) await sleep(100);
await sleep(800);
await b.ev(`(() => { window.__frames = []; const tick = () => { const f = { t: performance.now(), w: {}, c: {} }; for (const w of document.querySelectorAll('.rwell')) f.w[w.dataset.k] = w.getBoundingClientRect().height; for (const c of document.querySelectorAll('.rcell')) { const r = c.getBoundingClientRect(); f.c[c.dataset.k] = [Math.round(r.left), Math.round(r.top), Math.round(r.width), Math.round(r.height), +getComputedStyle(c).opacity]; } window.__frames.push(f); requestAnimationFrame(tick); }; requestAnimationFrame(tick); })()`);
await b.ev(`document.querySelector('.rcell[data-k="add"]').click()`);
await sleep(1200);
await b.ev(`fetch('/api/v1/_mock/relay-up', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' })`);
await sleep(1600);
const F = JSON.parse(await b.ev('JSON.stringify(window.__frames)'));
let problems = [];
let maxStep = 0;
for (let i = 1; i < F.length; i++) {
  for (const k in F[i].w) {
    const d = Math.abs(F[i].w[k] - (F[i - 1].w[k] ?? F[i].w[k]));
    maxStep = Math.max(maxStep, d);
    if (d > 20) problems.push(`well ${k} jumps ${d.toFixed(1)}px at frame ${i}`);
  }
  for (const k in F[i].c) {
    const a = F[i - 1].c[k], c = F[i].c[k];
    if (!a) continue;
    const dx = Math.abs(c[0] - a[0]), dy = Math.abs(c[1] - a[1]);
    if (dx > 12 || dy > 12) problems.push(`cell ${k} jumps (${dx}, ${dy}) at frame ${i}`);
  }
}
const h0 = F[0].w.mine, h1 = F[F.length - 1].w.mine;
console.log(`${F.length} frames; mine well ${h0.toFixed(1)} → ${h1.toFixed(1)}, largest step ${maxStep.toFixed(1)}px`);
console.log(problems.length ? problems.slice(0, 10).join('\n') : 'no jumps');
await b.close();
