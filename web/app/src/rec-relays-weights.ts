// M4-21 网页上调权重 (DESIGN §14.12 M4-11), recorded on the real relays page
// against a development control server (web/probe/devworld). kittors's two
// servers in use, 公司机房 and 上海, half and half: the handle between them
// dragged to 75 : 25; 上海's card: 只当备用 (its segment goes, a dashed
// "上海 · 备用" at the bar's end), then 多用 (back to what it had). Loaded
// only with ?record=weights.
import './rec.ts';
const rest = [700, 780];
(window as unknown as Record<string, unknown>).RECORDING = {
  title: 'M4-21-网页上调权重',
  duration: 11800,
  actions: [
    { at: 800, shot: '中转站', clean: true },
    { at: 1100, glide: { sel: '.relays-page .whandle' }, dur: 800 },
    { at: 2100, down: true },
    { at: 2200, glide: { sel: '.relays-page .wbar', x: 276, y: 19 }, dur: 1000 },
    { at: 3400, shot: '往公司机房多分一些' },
    { at: 3600, up: true },
    { at: 3900, glide: { sel: '.relays-page .rcell|上海|.orb' }, dur: 900 },
    { at: 5000, down: true },
    { at: 5080, up: true },
    { at: 5300, glide: rest, dur: 500 },
    { at: 6200, shot: '上海的卡片', clean: true },
    { at: 6500, glide: { sel: '.sheet [data-act="spare"]' }, dur: 700 },
    { at: 7300, down: true },
    { at: 7380, up: true },
    { at: 7600, glide: rest, dur: 500 },
    { at: 8600, shot: '只当备用', clean: true },
    { at: 8900, glide: { sel: '.sheet [data-act="more"]' }, dur: 700 },
    { at: 9700, down: true },
    { at: 9780, up: true },
    { at: 10000, glide: rest, dur: 500 },
    { at: 11200, shot: '又分到一些', clean: true },
  ],
};
