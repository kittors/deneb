// M3-23 网页上把人和设备拖到侧边栏 (DESIGN §6.4, §6.7), recorded on the
// real page against a development control server (web/probe/devworld).
// kittors on 周末联机 drags Mia's ball onto 桌游夜's row: the row lights up,
// letting go invites her (a dashed ball on 桌游夜, "等他接受"); 阿杰 onto
// 工作室: "已经在了", back to the stack. Then 我的设备: the NAS onto 周末联
// 机's row goes into that LAN; the MacBook, there already, goes back home.
// Loaded only with ?record=drag.
import './rec.ts';
const rest = [760, 820];
(window as unknown as Record<string, unknown>).RECORDING = {
  title: 'M3-23-网页上把人和设备拖到侧边栏',
  duration: 24200,
  actions: [
    { at: 800, shot: '周末联机', clean: true },
    { at: 1100, glide: { sel: '.stack .sball|Mia' }, dur: 900 },
    { at: 2200, down: true },
    { at: 2300, glide: { sel: '.stack .sball|Mia', x: -20, y: 40 }, dur: 300 },
    { at: 2800, glide: { sel: '.side .nav|桌游夜' }, dur: 1200, arc: 60 },
    { at: 4300, shot: '拖到桌游夜上' },
    { at: 4600, up: true },
    { at: 4900, glide: rest, dur: 700 },
    { at: 5900, shot: '邀请了 Mia', clean: true },
    { at: 6200, glide: { sel: '.stack .sball|阿杰' }, dur: 900 },
    { at: 7300, down: true },
    { at: 7400, glide: { sel: '.stack .sball|阿杰', x: -20, y: 40 }, dur: 300 },
    { at: 7900, glide: { sel: '.side .nav|工作室' }, dur: 1100, arc: 60 },
    { at: 9300, shot: '阿杰已经在了' },
    { at: 9600, up: true },
    { at: 9900, glide: rest, dur: 700 },
    { at: 10900, glide: { sel: '.side .nav|桌游夜' }, dur: 900 },
    { at: 12000, down: true },
    { at: 12080, up: true },
    { at: 12400, glide: rest, dur: 600 },
    { at: 13600, shot: '桌游夜里等她接受', clean: true },
    { at: 13900, glide: { sel: '.side .nav|我的设备' }, dur: 900 },
    { at: 15000, down: true },
    { at: 15080, up: true },
    { at: 15400, glide: { sel: '.dev|NAS|.tile' }, dur: 800 },
    { at: 16500, down: true },
    { at: 16600, glide: { sel: '.dev|NAS|.tile', x: -40, y: -20 }, dur: 300 },
    { at: 17100, glide: { sel: '.side .nav|周末联机' }, dur: 1200, arc: 40 },
    { at: 18600, shot: '拖到周末联机上' },
    { at: 18900, up: true },
    { at: 19200, glide: rest, dur: 700 },
    { at: 20400, shot: '加进周末联机了', clean: true },
    { at: 20700, glide: { sel: '.dev|MacBook|.tile' }, dur: 800 },
    { at: 21700, down: true },
    { at: 21800, glide: { sel: '.dev|MacBook|.tile', x: -40, y: -20 }, dur: 300 },
    { at: 22200, glide: { sel: '.side .nav|周末联机' }, dur: 900, arc: 30 },
    { at: 23300, shot: '已经有了' },
    { at: 23600, up: true },
  ],
};
