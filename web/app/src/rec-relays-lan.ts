// M4-22 网页上把中转站拖进局域网 (DESIGN §14.12 M4-12), recorded on the real
// relays page against a development control server (web/probe/devworld).
// kittors's new server, which has only its address, dragged onto 周末联机's
// row: the row lights, "松手，周末联机就用上它"; let go, it flies in, 周末
// 联机's chip pops into its cell, the share bar takes it in. 公司机房 onto
// 周末联机, which uses it already: grey words, back to its cell. 工作室's
// chip pulled out of 公司机房's cell, and undo. The public 香港 held: it need
// not go anywhere. Loaded only with ?record=lan.
import './rec.ts';
const rest = [700, 780];
(window as unknown as Record<string, unknown>).RECORDING = {
  title: 'M4-22-网页上把中转站拖进局域网',
  duration: 21600,
  actions: [
    { at: 800, shot: '中转站', clean: true },
    { at: 1100, glide: { sel: '.relays-page .rcell|203.0.113.9|.orb' }, dur: 900 },
    { at: 2200, down: true },
    { at: 2300, glide: { sel: '.relays-page .rcell|203.0.113.9|.orb', x: -20, y: -10 }, dur: 300 },
    { at: 2800, glide: { sel: '.side .nav|周末联机' }, dur: 1200, arc: 60 },
    { at: 4300, shot: '拖到周末联机上' },
    { at: 4600, up: true },
    { at: 4900, glide: rest, dur: 700 },
    { at: 6200, shot: '周末联机用上了我的服务器', clean: true },
    { at: 6600, glide: { sel: '.relays-page .rcell|公司机房|.orb' }, dur: 900 },
    { at: 7700, down: true },
    { at: 7800, glide: { sel: '.relays-page .rcell|公司机房|.orb', x: -20, y: -10 }, dur: 300 },
    { at: 8300, glide: { sel: '.side .nav|周末联机' }, dur: 1100, arc: 50 },
    { at: 9700, shot: '周末联机已经在用了' },
    { at: 10000, up: true },
    { at: 10400, glide: { sel: '.relays-page .lchip|工作室' }, dur: 800 },
    { at: 11400, down: true },
    { at: 11500, glide: { sel: '.relays-page .lchip|工作室', x: 40, y: 50 }, dur: 400 },
    { at: 11900, glide: [600, 680], dur: 600 },
    { at: 12700, shot: '拖出格子' },
    { at: 13000, up: true },
    { at: 13400, glide: { sel: '.toast button' }, dur: 700 },
    { at: 14300, down: true },
    { at: 14380, up: true },
    { at: 14600, glide: rest, dur: 600 },
    { at: 15600, shot: '撤销后又回来了', clean: true },
    { at: 15900, glide: { sel: '.relays-page .rcell|香港|.orb' }, dur: 900 },
    { at: 17000, down: true },
    { at: 17100, glide: { sel: '.relays-page .rcell|香港|.orb', x: -20, y: -10 }, dur: 300 },
    { at: 17600, glide: { sel: '.side .nav|周末联机' }, dur: 1200, arc: 40 },
    { at: 19100, shot: '公共中转站一直都在' },
    { at: 19400, up: true },
    { at: 19700, glide: rest, dur: 700 },
    { at: 21000, shot: '最后', clean: true },
  ],
};
