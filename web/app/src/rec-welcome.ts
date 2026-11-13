// A new account's first page, recorded (DESIGN §14.14), against deneb-web
// -mock logged in as someone new. ?record=welcome: the page; the other
// systems' downloads; the download, and what to do next; a computer of
// mine signs in and the first card says so; 加入朋友的 opens into a field,
// the link pasted, 打开: the invite. welcome-new: 新建局域网 makes one named
// after me, and its canvas comes. welcome-name: the sidebar's 新建局域网
// turns into a field for the name; Enter makes it. Loaded only with ?record.
import './rec.ts';

type Action = Record<string, unknown>;
const which = new URLSearchParams(location.search).get('record') || '';
const rest = { sel: 'body', x: 1080, y: 690 };
const click = (at: number, sel: string, dur = 600): Action[] => [
  { at, glide: { sel }, dur },
  { at: at + dur + 200, down: true },
  { at: at + dur + 280, up: true },
];
const typing = (at: number, sel: string, s: string, each = 55): Action[] =>
  [...s].map((_, i) => ({
    at: at + i * each,
    js: `(() => { const e = document.querySelector(${JSON.stringify(sel)}); e.focus(); e.value = ${JSON.stringify(s.slice(0, i + 1))}; e.dispatchEvent(new InputEvent('input', { bubbles: true, inputType: 'insertText', data: ${JSON.stringify(s[i])} })); })()`,
  }));
// The downloads go nowhere in a recording: the click is the page's alone.
const noDownloads: Action = { at: 0, js: `document.addEventListener('click', (e) => { if (e.target.closest && e.target.closest('a.wc-dl')) e.preventDefault(); }, true)` };

const scenes: Record<string, { title: string; duration: number; actions: Action[] }> = {
  welcome: {
    title: 'M7-23-新账号的第一页',
    duration: 14400,
    actions: [
      noDownloads,
      { at: 800, shot: '第一页', clean: true },
      ...click(1100, '.wc-more', 700),
      { at: 2600, shot: '三个系统' },
      ...click(2900, '.wc-dl.primary', 600),
      { at: 4300, shot: '下载以后' },
      { at: 4500, glide: rest, dur: 600 },
      // A computer of mine signs in: the first card says so.
      { at: 5400, js: `fetch('/api/v1/_mock/device-up', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' })` },
      { at: 6800, shot: '装好了', clean: true },
      ...click(7100, '.wc-way[data-way="join"]', 700),
      ...typing(8400, '.wc-join input', 'https://deneb.example.com/i/k3xQ9', 28),
      { at: 9700, shot: '粘贴邀请链接' },
      ...click(9900, '.wc-join .btn', 600),
      { at: 12000, glide: rest, dur: 600 },
      { at: 13600, shot: '邀请卡', clean: true },
    ],
  },
  'welcome-new': {
    title: 'M7-24-第一页上新建局域网',
    duration: 6400,
    actions: [
      { at: 600, glide: { sel: '.wc-way[data-way="new"]' }, dur: 700 },
      { at: 1500, shot: '指着新建局域网' },
      { at: 1700, down: true },
      { at: 1780, up: true },
      { at: 2300, glide: rest, dur: 600 },
      { at: 5600, shot: '建好了', clean: true },
    ],
  },
  'welcome-name': {
    title: 'M7-25-侧边栏新建局域网',
    duration: 7000,
    actions: [
      ...click(600, '.side .new-lan', 700),
      { at: 2000, shot: '起名' },
      ...typing(2300, '.nl-input', '周末联机', 120),
      { at: 3300, shot: '起好了' },
      { at: 3600, key: 'Enter' },
      { at: 4000, glide: rest, dur: 600 },
      { at: 6200, shot: '建好了', clean: true },
    ],
  },
};

(window as unknown as Record<string, unknown>).RECORDING = scenes[which] || scenes.welcome;
