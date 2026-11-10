// 共享给我 on the web, recorded (DESIGN §11): 阿杰's page against deneb-web
// -mock. The studio's two devices; a card; 小林 shares her laptop (it pops
// up, the sidebar's row lights up and counts); 不要了 and undo; 小林 takes
// it back (it goes, with a word). Loaded only with ?record.
import './rec.ts';
const rest = [780, 760];
const lin = (on: boolean) => `fetch('/api/v1/_mock/share', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ device: 'd_lin', sharer: 'p_lin', on: ${on} }) })`;
(window as unknown as Record<string, unknown>).RECORDING = {
  title: 'M3-18-共享给我（网页）',
  duration: 14600,
  actions: [
    { at: 1000, shot: '共享给我', clean: true },
    { at: 1300, glide: { sel: '.dev[data-id="d_gpu"] .tile' }, dur: 700 },
    { at: 2200, down: true },
    { at: 2280, up: true },
    { at: 2500, glide: rest, dur: 600 },
    { at: 3300, shot: '卡片', clean: true },
    { at: 3600, js: lin(true) },
    { at: 4400, shot: '新分来的', clean: true },
    { at: 4700, glide: { sel: '.dev[data-id="d_lin"] .tile' }, dur: 800 },
    { at: 5700, down: true },
    { at: 5780, up: true },
    { at: 6100, glide: { sel: '.sheet .out-btn' }, dur: 700 },
    { at: 7000, down: true },
    { at: 7080, up: true },
    { at: 7300, glide: rest, dur: 500 },
    { at: 8100, shot: '不要了' },
    { at: 8300, glide: { sel: '.toast button' }, dur: 600 },
    { at: 9100, down: true },
    { at: 9180, up: true },
    { at: 9400, glide: rest, dur: 500 },
    { at: 10300, shot: '撤销后又回来了', clean: true },
    { at: 10800, js: lin(false) },
    { at: 11700, shot: '小林收回了' },
    { at: 14200, shot: '收回以后', clean: true },
  ],
};
