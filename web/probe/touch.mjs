// Phone check: layout at a phone's size, and whether a finger drag scrolls.
import { open, sleep } from './cdp.mjs';
import { readFileSync } from 'node:fs';
const [url, out, fx, fy, tx, ty] = [process.argv[2], process.argv[3], ...process.argv.slice(4).map(Number)];
const b = await open({ W: 390, H: 844, scale: 3, dark: false });
await b.S('Emulation.setDeviceMetricsOverride', { width: 390, height: 844, deviceScaleFactor: 3, mobile: true });
await b.S('Emulation.setTouchEmulationEnabled', { enabled: true, maxTouchPoints: 5 });
await b.S('Page.navigate', { url });
for (let i = 0; i < 300 && !(await b.ev('!!(window.__t && window.__t.firstFrame)')); i++) await sleep(50);
await sleep(1200);
await b.shot(`${out}-0.png`);
const tp = (x, y) => [{ x, y, id: 1, radiusX: 4, radiusY: 4, force: 1 }];
await b.S('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: tp(fx, fy) });
for (let i = 1; i <= 12; i++) { await b.S('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: tp(fx + (tx - fx) * i / 12, fy + (ty - fy) * i / 12) }); await sleep(16); }
await b.S('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
await sleep(800);
await b.shot(`${out}-1-touch.png`);
await b.S('Input.dispatchMouseEvent', { type: 'mouseWheel', x: fx, y: fy, deltaX: 0, deltaY: 400 });
await sleep(800);
await b.shot(`${out}-2-wheel.png`);
const same = (a, c) => readFileSync(a).equals(readFileSync(c));
console.log('touch drag changed the screen:', !same(`${out}-0.png`, `${out}-1-touch.png`), '| wheel changed it:', !same(`${out}-1-touch.png`, `${out}-2-wheel.png`));
const ta = await b.ev(`(() => { const c = document.querySelector('canvas'); return c ? getComputedStyle(c).touchAction : 'no canvas' })()`);
console.log('canvas touch-action:', ta);
await b.close();
