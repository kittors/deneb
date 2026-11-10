// M4-23 网页上的运营页 (DESIGN §14.12 M4-13), recorded on the real page
// against a development control server (web/probe/devworld), as kittors,
// the deployment's operator. Three public relays, 香港 a bit busy; 法兰克福's
// narrow segment shows its name on hover; 香港's card (now, this month, who
// uses it most); the handle dragged from 香港 to 东京, 45 : 45; 小林 turned
// away, and undo; "+ 加一台公共中转站", its line copied; the line run (on a
// server the dev world plays): 新加坡 comes in as standby; 多用 gives it
// 20%. Loaded only with ?record=ops.
import './rec.ts';
const rest = [760, 800];
// The line run on a server: devworld takes the command the card shows.
const install = { url: 'http://127.0.0.1:47199/install', body: `JSON.stringify({ command: document.querySelector('.relays-card .cmd').textContent, name: '新加坡', addr: '203.0.113.7' })` };
(window as unknown as Record<string, unknown>).RECORDING = {
  title: 'M4-23-网页上的运营页',
  duration: 21400,
  actions: [
    { at: 800, shot: '运营', clean: true },
    { at: 1100, glide: { sel: '.relays-page .wseg.act|法兰克福' }, dur: 900 },
    { at: 2400, shot: '指着窄的那段' },
    { at: 2700, glide: { sel: '.relays-page .rcell|香港|.orb' }, dur: 800 },
    { at: 3700, down: true },
    { at: 3780, up: true },
    { at: 4000, glide: rest, dur: 500 },
    { at: 5000, shot: '香港的卡片', clean: true },
    { at: 5300, glide: { sel: '.relays-page .whandle' }, dur: 800 },
    { at: 6300, down: true },
    { at: 6400, glide: { sel: '.relays-page .wbar', x: 252, y: 19 }, dur: 900 },
    { at: 7500, shot: '香港和东京一样多' },
    { at: 7700, up: true },
    { at: 8000, glide: { sel: '.sheet .people .chip|小林' }, dur: 900 },
    { at: 9100, down: true },
    { at: 9180, up: true },
    { at: 9400, glide: rest, dur: 500 },
    { at: 10300, shot: '不让小林用香港' },
    { at: 10600, glide: { sel: '.toast button' }, dur: 700 },
    { at: 11500, down: true },
    { at: 11580, up: true },
    { at: 11800, glide: rest, dur: 500 },
    { at: 12700, shot: '撤销了', clean: true },
    { at: 13000, glide: { sel: '.relays-page .rcell[data-k="add"]' }, dur: 800 },
    { at: 14000, down: true },
    { at: 14080, up: true },
    { at: 14400, glide: { sel: '.sheet [data-act="copy"]' }, dur: 800 },
    { at: 15400, down: true },
    { at: 15480, up: true },
    { at: 15700, glide: rest, dur: 500 },
    { at: 16600, shot: '一行命令', clean: true },
    { at: 16900, post: install },
    { at: 18200, shot: '新加坡上线了，先当备用', clean: true },
    { at: 18500, glide: { sel: '.sheet [data-act="more"]' }, dur: 700 },
    { at: 19300, down: true },
    { at: 19380, up: true },
    { at: 19600, glide: rest, dur: 500 },
    { at: 20800, shot: '新加坡分到一些', clean: true },
  ],
};
