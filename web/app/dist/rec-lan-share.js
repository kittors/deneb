// M3-21 网页上把设备分给某个人 (DESIGN §6.11, §14.4), recorded on the real
// page against a development control server (web/probe/devworld). kittors
// on 周末联机: the tray's NAS held up to the header, the stack spreads, the
// line says what letting go does; onto Mia, given to her. The MacBook's
// card: "+ 分享给别人", Leo; what it opens to him, a switch closed; taken
// back with the ×, and undo. Mia's card: what he gave her. Loaded only with
// ?record=share.
import './rec.js';
const rest = [760, 820];
(window                                      ).RECORDING = {
  title: 'M3-21-网页上把设备分给某个人',
  duration: 27400,
  actions: [
    { at: 800, shot: '局域网', clean: true },
    { at: 1100, glide: { sel: '.tray .mini|NAS' }, dur: 800 },
    { at: 2100, down: true },
    // Up a little: the drag starts and the stack spreads; then to Mia.
    { at: 2200, glide: { sel: '.tray .mini|NAS', x: 20, y: -40 }, dur: 300 },
    { at: 2900, glide: { sel: '.stack .sball|Mia' }, dur: 1100, arc: 40 },
    { at: 4300, shot: '拖到 Mia 身上' },
    { at: 4600, up: true },
    { at: 4900, glide: rest, dur: 600 },
    { at: 5800, shot: '分享给 Mia 了', clean: true },
    { at: 6200, glide: { sel: '.dev|MacBook|.tile' }, dur: 800 },
    { at: 7200, down: true },
    { at: 7280, up: true },
    { at: 7600, glide: { sel: '.sheet .pchip.plus' }, dur: 800 },
    { at: 8600, down: true },
    { at: 8680, up: true },
    { at: 9300, shot: '分享给别人' },
    { at: 9500, glide: { sel: '.sheet .picker .pchip|Leo' }, dur: 700 },
    { at: 10400, down: true },
    { at: 10480, up: true },
    { at: 10800, glide: rest, dur: 600 },
    { at: 11800, shot: '分享给 Leo 了', clean: true },
    { at: 12200, glide: { sel: '.sheet .pchip|Leo|.pn' }, dur: 700 },
    { at: 13100, down: true },
    { at: 13180, up: true },
    { at: 13700, glide: { sel: '.sheet .share-open .row|共享文件夹|.sw' }, dur: 700 },
    { at: 14600, down: true },
    { at: 14680, up: true },
    { at: 15400, shot: '开放给 Leo 的' },
    { at: 15700, glide: { sel: '.sheet .pchip|Leo' }, dur: 600 },
    { at: 16500, glide: { sel: '.sheet .pchip|Leo|.x' }, dur: 300 },
    { at: 17000, down: true },
    { at: 17080, up: true },
    { at: 17400, glide: rest, dur: 600 },
    { at: 18300, shot: '收回了', clean: true },
    { at: 18600, glide: { sel: '.toast button' }, dur: 700 },
    { at: 19500, down: true },
    { at: 19580, up: true },
    { at: 19900, glide: rest, dur: 500 },
    { at: 20800, shot: '撤销后又回来了', clean: true },
    { at: 21300, glide: { sel: '.stack .sball|Mia' }, dur: 900 },
    { at: 22400, down: true },
    { at: 22480, up: true },
    { at: 22800, glide: rest, dur: 700 },
    { at: 24000, shot: 'Mia 的卡片', clean: true },
    { at: 27000, shot: '最后', clean: true },
  ],
};
