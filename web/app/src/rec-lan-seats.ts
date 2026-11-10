// M3-22 网页上的位置 (DESIGN §6.11 位置; pricing §1.4, §1.5), recorded on
// the real page against a development control server (web/probe/devworld).
// kittors has 5 of his 6 places taken: "还能加 1 人" under "邀请朋友". He lets
// 阿明 in: once the toast goes, his places are full and "邀请朋友" turns a
// dashed outline. A click shows the places; "腾一个位置", Leo's ×: "还能
// 加 1 人" again, and undo puts it back. Loaded only with ?record=seats.
import './rec.ts';
const rest = [760, 820];
(window as unknown as Record<string, unknown>).RECORDING = {
  title: 'M3-22-网页上的位置',
  duration: 22800,
  actions: [
    { at: 800, shot: '还能加 1 人', clean: true },
    { at: 1100, glide: { sel: '.stack .sball|阿明' }, dur: 900 },
    { at: 2200, down: true },
    { at: 2280, up: true },
    { at: 2500, glide: rest, dur: 700 },
    // The toast goes at about 6.3 s; the server lets him in; the places fill.
    { at: 8200, shot: '位置满了', clean: true },
    { at: 8500, glide: { sel: '.head .btn.primary' }, dur: 800 },
    { at: 9500, down: true },
    { at: 9580, up: true },
    { at: 9900, glide: rest, dur: 600 },
    { at: 10900, shot: '位置卡片', clean: true },
    { at: 11200, glide: { sel: '.sheet [data-act="free"]' }, dur: 800 },
    { at: 12200, down: true },
    { at: 12280, up: true },
    { at: 12800, shot: '腾一个位置' },
    { at: 13100, glide: { sel: '.sheet .seat-ball|Leo|.x' }, dur: 700 },
    { at: 14000, down: true },
    { at: 14080, up: true },
    { at: 14400, glide: rest, dur: 600 },
    { at: 15600, shot: '腾出一个了', clean: true },
    { at: 15900, glide: { sel: '.toast button' }, dur: 800 },
    { at: 16900, down: true },
    { at: 16980, up: true },
    { at: 17300, glide: rest, dur: 600 },
    { at: 18600, shot: '撤销了', clean: true },
    { at: 18900, glide: { sel: '.sheet .sheet-close' }, dur: 700 },
    { at: 19800, down: true },
    { at: 19880, up: true },
    { at: 20200, glide: rest, dur: 600 },
    { at: 22400, shot: '收起', clean: true },
  ],
};
