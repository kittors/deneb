// 我的设备 on the web, recorded (DESIGN §11): kittors' page against
// deneb-web -mock, with his NAS shared with 小林 first. A card; into 周末联机
// from it, and undo; taken back from 小林, and undo. Loaded only with ?record.
import './rec.js';
const rest = [780, 760];
const op = (name        , args                        ) => `fetch('/api/v1/ops', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ id: 'rec-' + Date.now(), op: '${name}', args: ${JSON.stringify(args)} }) })`;
(window                                      ).RECORDING = {
  title: 'M3-19-我的设备（网页）',
  duration: 13400,
  actions: [
    { at: 100, js: op('share.create', { device: 'd_knas', person: 'p_lin' }) },
    { at: 1000, shot: '我的设备', clean: true },
    { at: 1300, glide: { sel: '.dev[data-id="d_knas"] .tile' }, dur: 700 },
    { at: 2200, down: true },
    { at: 2280, up: true },
    { at: 2500, glide: rest, dur: 600 },
    { at: 3300, shot: '卡片', clean: true },
    { at: 3600, glide: { sel: '.sheet .lan-add[data-l="l_weekend"]' }, dur: 700 },
    { at: 4500, down: true },
    { at: 4580, up: true },
    { at: 4800, glide: rest, dur: 500 },
    { at: 5600, shot: '加进周末联机' },
    { at: 5900, glide: { sel: '.toast button' }, dur: 600 },
    { at: 6700, down: true },
    { at: 6780, up: true },
    { at: 7000, glide: rest, dur: 500 },
    { at: 7900, shot: '撤销了', clean: true },
    { at: 8200, glide: { sel: '.sheet .mchip[data-k="p_lin"] .x' }, dur: 700 },
    { at: 9100, down: true },
    { at: 9180, up: true },
    { at: 9400, glide: rest, dur: 500 },
    { at: 10300, shot: '从小林收回' },
    { at: 10600, glide: { sel: '.toast button' }, dur: 600 },
    { at: 11400, down: true },
    { at: 11480, up: true },
    { at: 11700, glide: rest, dur: 500 },
    { at: 12900, shot: '撤销后又回来了', clean: true },
  ],
};
