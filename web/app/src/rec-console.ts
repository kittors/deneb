// The deployment's console, recorded (DESIGN §14.15, M7-31 to M7-35),
// against deneb-web -mock's made-up deployment, as kittors, its operator.
// console-overview: two things to look at, the server, the numbers; the
// backups and the week's alerts opened. console-people: Leo found, his
// card; his seat limit to 8; out of both browsers. console-delete: his
// account deleted for him. console-devices: a test account's login let
// in; the logged-out ones, 阿杰's old NAS deleted (a second click);
// 小林's laptop made to log in again. console-log: the operators' record
// (what was done before the recording, on the server), the server's log,
// its alerts, and one that comes. Loaded only with ?record=console….
import './rec.ts';

type Action = Record<string, unknown>;
const which = new URLSearchParams(location.search).get('record') || '';
const rest = { sel: 'body', x: 1240, y: 760 };
const click = (at: number, sel: string, dur = 600): Action[] => [
  { at, glide: { sel }, dur },
  { at: at + dur + 200, down: true },
  { at: at + dur + 280, up: true },
];
const typing = (at: number, sel: string, s: string, each = 60): Action[] =>
  [...s].map((_, i) => ({
    at: at + i * each,
    js: `(() => { const e = document.querySelector(${JSON.stringify(sel)}); e.focus(); e.value = ${JSON.stringify(s.slice(0, i + 1))}; e.dispatchEvent(new InputEvent('input', { bubbles: true, inputType: 'insertText', data: ${JSON.stringify(s[i])} })); })()`,
  }));

const scenes: Record<string, { title: string; duration: number; actions: Action[] }> = {
  'console-overview': {
    title: 'M7-31-概况',
    duration: 7600,
    actions: [
      { at: 800, shot: '概况', clean: true },
      ...click(1200, '.cons-row [data-act="backups"]', 800),
      { at: 3000, shot: '备份' },
      ...click(3400, '[data-act="recent"]', 800),
      { at: 5200, shot: '最近报过的' },
      { at: 5500, glide: rest, dur: 800 },
      { at: 7000, shot: '看完了', clean: true },
    ],
  },
  'console-people': {
    title: 'M7-32-找人调位置让他退出',
    duration: 13200,
    actions: [
      { at: 800, shot: '用户', clean: true },
      ...click(1100, '.cons-search input', 700),
      ...typing(2100, '.cons-search input', 'leo', 110),
      { at: 3200, shot: '找到 Leo' },
      ...click(3400, '.cons-item[data-id="p_leo"]', 700),
      { at: 4900, shot: 'Leo 的卡片', clean: true },
      ...click(5200, '.sheet [data-act="seats"]', 700),
      ...click(6300, '.cons-step button:last-child', 500),
      { at: 7300, down: true },
      { at: 7380, up: true },
      { at: 8600, shot: '调成 8' },
      ...click(8900, '.sheet [data-act="signout"]', 600),
      { at: 10000, shot: '要确认' },
      ...click(10200, '.sheet [data-yes]', 500),
      { at: 11300, shot: '都退出了' },
      { at: 11500, glide: rest, dur: 800 },
      { at: 12800, shot: '看完了', clean: true },
    ],
  },
  'console-delete': {
    title: 'M7-33-替人注销账号',
    duration: 11600,
    actions: [
      ...click(500, '.cons-item[data-id="p_leo"]', 800),
      ...click(2300, '.sheet [data-act="delete"]', 700),
      { at: 3900, shot: '替 Leo 注销账号', clean: true },
      ...click(4200, '.cons-confirm input', 700),
      ...typing(5200, '.cons-confirm input', 'leo.chen@example.com', 55),
      { at: 6600, shot: '输好邮箱' },
      ...click(6800, '.cons-del [data-act="go"]', 600),
      { at: 8000, shot: '注销了', clean: true },
      { at: 8300, glide: rest, dur: 800 },
      { at: 11000, shot: '回到用户', clean: true },
    ],
  },
  'console-devices': {
    title: 'M7-34-设备',
    duration: 17600,
    actions: [
      { at: 800, shot: '设备', clean: true },
      ...click(1100, '.cons-wait [data-act="approve"]', 700),
      ...click(2400, '.cons-pick [data-email="test-a@deneb.test"]', 600),
      { at: 3900, shot: '批准了' },
      ...click(4300, '.cons-chip[data-v="loggedOut"]', 700),
      { at: 5800, shot: '登出了的' },
      ...click(6100, '.cons-item[data-id="od_010"]', 700),
      { at: 7400, shot: '旧 NAS 的卡片', clean: true },
      ...click(7700, '.sheet [data-act="delete"]', 700),
      { at: 9000, shot: '再点一下' },
      { at: 9300, down: true },
      { at: 9380, up: true },
      { at: 10300, shot: '删掉了' },
      ...click(10600, '.cons-chip[data-v=""]', 700),
      ...click(11700, '.cons-search input', 600),
      ...typing(12500, '.cons-search input', '小林', 150),
      ...click(13500, '.cons-item[data-id="od_014"]', 700),
      { at: 14800, shot: '小林的笔记本', clean: true },
      ...click(15000, '.sheet [data-act="relogin"]', 600),
      { at: 16300, shot: '要重新登录了' },
      { at: 16500, glide: rest, dur: 700 },
    ],
  },
  'console-log': {
    title: 'M7-35-记录',
    duration: 9800,
    actions: [
      { at: 800, shot: '运营记录', clean: true },
      ...click(1100, '.cons-seg [data-v="log"]', 800),
      { at: 3000, shot: '服务器日志', clean: true },
      ...click(3300, '.cons-logchips [data-v="alert"]', 700),
      { at: 4700, shot: '只看报警' },
      { at: 4900, glide: rest, dur: 700 },
      { at: 5400, js: `fetch('/api/v1/_mock/ops/alert', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' })` },
      { at: 9200, shot: '又来一条', clean: true },
    ],
  },
};

// The page draws what the server answered on the clock's first tick
// (console.ts), so the recording starts with it fading in.
(window as unknown as Record<string, unknown>).RECORDING = scenes[which] || scenes['console-overview'];
