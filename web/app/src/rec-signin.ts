// The login page, recorded (DESIGN §14.14), against deneb-web -mock, whose
// every code is 000000. ?record=signin: the email, the code sent, a wrong
// code, "换个邮箱" and back, the right code, the tick, and in: a new
// account's first page fades in. signin-resend: the minute almost over,
// "重发" turns blue; clicked, the line says so and counts again. The
// signin-real ones and signin-link ones are on a real server (below).
// Loaded only with ?record.
import './rec.ts';

type Action = Record<string, unknown>;
const which = new URLSearchParams(location.search).get('record') || '';
const rest = { sel: 'body', x: 1080, y: 690 };
const click = (at: number, sel: string, dur = 600): Action[] => [
  { at, glide: { sel }, dur },
  { at: at + dur + 200, down: true },
  { at: at + dur + 280, up: true },
];
// typing puts s in the email field a key at a time, as a hand types.
const typing = (at: number, sel: string, s: string, each = 55): Action[] =>
  [...s].map((_, i) => ({
    at: at + i * each,
    js: `(() => { const e = document.querySelector(${JSON.stringify(sel)}); e.focus(); e.value = ${JSON.stringify(s.slice(0, i + 1))}; e.dispatchEvent(new InputEvent('input', { bubbles: true, inputType: 'insertText', data: ${JSON.stringify(s[i])} })); })()`,
  }));
// digits types the code into whichever box has the focus, as keys do.
const digits = (at: number, s: string, each = 110): Action[] =>
  [...s].map((ch, i) => ({
    at: at + i * each,
    js: `(() => { const e = document.activeElement; if (!e || !e.classList.contains('cb')) return; e.value = ${JSON.stringify(ch)}; e.dispatchEvent(new InputEvent('input', { bubbles: true, inputType: 'insertText', data: ${JSON.stringify(ch)} })); })()`,
  }));
const live = '.si-step .si-in:not(.si-out)';
// The real server's code (?code=, read from its log before the recording;
// a click on 发验证码 within the minute sends no new one).
const code = new URLSearchParams(location.search).get('code') || '000000';
// paste is ⌘V on the page, the focus anywhere: a paste event with the code.
const paste = (at: number, s: string): Action => ({
  at,
  js: `(() => { const dt = new DataTransfer(); dt.setData('text/plain', ${JSON.stringify(s)}); document.body.dispatchEvent(new ClipboardEvent('paste', { clipboardData: dt, bubbles: true, cancelable: true })); })()`,
});
// Off to the mail and back: the page loses the focus, then has it again.
const away = (at: number): Action => ({ at, js: `document.activeElement && document.activeElement.blur()` });
const back = (at: number): Action => ({ at, js: `window.dispatchEvent(new Event('focus'))` });

const scenes: Record<string, { title: string; duration: number; actions: Action[] }> = {
  signin: {
    title: 'M7-21-网页登录页',
    duration: 15400,
    actions: [
      { at: 700, shot: '登录页', clean: true },
      { at: 900, glide: { sel: live + ' .field' }, dur: 700 },
      ...typing(1800, live + ' input[name=email]', 'tom@example.com'),
      { at: 2900, shot: '输好邮箱' },
      ...click(3100, live + ' .btn.primary', 500),
      { at: 4000, shot: '正在发' },
      { at: 5200, shot: '发到了', clean: true },
      { at: 5400, glide: { sel: live + ' .code-boxes' }, dur: 600 },
      ...digits(6200, '123456'),
      { at: 7400, shot: '验证码不对' },
      ...click(7900, live + ' .back', 700),
      { at: 9300, shot: '换个邮箱' },
      ...click(9500, live + ' .btn.primary', 600),
      { at: 11200, glide: { sel: live + ' .code-boxes' }, dur: 500 },
      ...digits(11800, '000000'),
      { at: 12700, shot: '登录了' },
      { at: 13000, glide: rest, dur: 600 },
      { at: 14700, shot: '进来了', clean: true },
    ],
  },
  'signin-resend': {
    title: 'M7-22-网页上重发验证码',
    duration: 9600,
    actions: [
      // Before the first frame: the code asked for, and the minute run
      // down to its last three seconds (the clock moved a second at a
      // time, as the page counts).
      {
        at: 0,
        js: `(async () => {
          const box = document.querySelector('${live} input[name=email]');
          box.value = 'tom@example.com';
          box.dispatchEvent(new InputEvent('input', { bubbles: true }));
          document.querySelector('${live} form').requestSubmit();
          for (let i = 0; i < 200 && !document.querySelector('${live} .code-boxes'); i++) await window.__advance(50);
          for (let i = 0; i < 57; i++) await window.__advance(1000);
        })()`,
      },
      { at: 600, shot: '快数完了', clean: true },
      { at: 3200, shot: '能重发了', clean: true },
      ...click(3600, live + ' .jc-hint .link', 700),
      { at: 5000, shot: '又发了一封' },
      { at: 5300, glide: rest, dur: 600 },
      { at: 8600, shot: '再数一遍', clean: true },
    ],
  },
};

// On the real server (deneb-control -dev through deneb-web, a network's
// latency on every request): the whole login, as a newcomer does it.
scenes['signin-real'] = {
  title: 'M7-01-网页登录（连本机服务器）',
  duration: 13600,
  actions: [
    { at: 700, shot: '登录页', clean: true },
    { at: 900, glide: { sel: live + ' .field' }, dur: 700 },
    ...typing(1800, live + ' input[name=email]', 'tom@example.com'),
    { at: 2900, shot: '输好邮箱' },
    ...click(3100, live + ' .btn.primary', 500),
    { at: 5000, shot: '发到了', clean: true },
    // Off to the mail for the code: the pointer goes up, the page loses the
    // focus; back, the caret is in the first box again.
    { at: 5300, glide: { sel: 'body', x: 1200, y: 30 }, dur: 600 },
    away(6000),
    { at: 6900, glide: { sel: live + ' .code-boxes' }, dur: 600 },
    back(7600),
    { at: 8000, shot: '回来了' },
    paste(8400, code),
    { at: 8850, shot: '登录了' },
    { at: 9600, glide: rest, dur: 600 },
    { at: 12800, shot: '进来了', clean: true },
  ],
};
scenes['signin-real-expired'] = {
  title: 'M7-02-验证码过期和重发（连本机服务器）',
  duration: 12400,
  actions: [
    // Before the first frame: the code asked for, and ten minutes gone by on
    // the page's clock, a second at a time as the page counts.
    {
      at: 0,
      js: `(async () => {
        const box = document.querySelector('${live} input[name=email]');
        box.value = 'tom@example.com';
        box.dispatchEvent(new InputEvent('input', { bubbles: true }));
        document.querySelector('${live} form').requestSubmit();
        for (let i = 0; i < 400 && !document.querySelector('${live} .code-boxes'); i++) await window.__advance(50);
        for (let i = 0; i < 600; i++) await window.__advance(1000);
      })()`,
    },
    { at: 600, shot: '过期了', clean: true },
    { at: 800, glide: { sel: live + ' .code-boxes' }, dur: 600 },
    ...digits(1600, '111111'),
    { at: 3200, shot: '输了过期的码' },
    ...click(3600, live + ' .jc-hint .link', 700),
    { at: 5000, shot: '又发了一封' },
    { at: 5400, glide: { sel: live + ' .code-boxes' }, dur: 600 },
    paste(6400, code),
    { at: 6850, shot: '登录了' },
    { at: 7700, glide: rest, dur: 600 },
    { at: 11600, shot: '进来了', clean: true },
  ],
};

// The link in the mail (login-link.md), on the real server: its token is
// ?link= here (read from the server's log before the recording), or after
// the # of the link page itself.
const link = new URLSearchParams(location.search).get('link') || '';
// In the window beside (record-web's tab, not recorded): the button, once
// its page shows it, clicked.
const clickThere = (sel: string) =>
  `(async () => { for (let i = 0; i < 200; i++) { const b = document.querySelector(${JSON.stringify(sel)}); if (b) { b.click(); return true; } await new Promise((r) => setTimeout(r, 50)); } return false; })()`;

// The page that waits for the code goes in by itself: the link opened in
// another window of this browser, 登录 there.
scenes['signin-link-waited'] = {
  title: 'M7-09-原来的页面自己进去（连本机服务器）',
  duration: 11400,
  actions: [
    { at: 700, shot: '登录页', clean: true },
    { at: 900, glide: { sel: live + ' .field' }, dur: 700 },
    ...typing(1800, live + ' input[name=email]', 'tom@example.com'),
    ...click(3100, live + ' .btn.primary', 500),
    { at: 5000, shot: '发到了', clean: true },
    // Off to the mail: the pointer goes up, the page loses the focus; the
    // link opens in another window, which asks first, and 登录 there.
    { at: 5300, glide: { sel: 'body', x: 1200, y: 30 }, dur: 600 },
    away(6000),
    { at: 6300, tab: { open: '/login/link#' + link } },
    { at: 7300, tab: { js: clickThere('.ll-go') } },
    { at: 7900, shot: '自己进去了' },
    { at: 8700, glide: rest, dur: 700 },
    { at: 10800, shot: '进来了', clean: true },
  ],
};
// The link's page, in the browser whose login page waits (record-web
// --side): it says that page goes in too; 登录, the tick, and in.
const onLink = (title: string, used = false): { title: string; duration: number; actions: Action[] } => ({
  title,
  duration: used ? 6200 : 7400,
  actions: used
    ? [
        { at: 900, shot: '用不了了', clean: true },
        ...click(1200, live + ' .ll-foot .btn', 800),
        { at: 3000, glide: rest, dur: 700 },
        { at: 5600, shot: '回到登录页', clean: true },
      ]
    : [
        { at: 900, shot: '问一句', clean: true },
        ...click(1200, live + ' .ll-go', 800),
        { at: 2700, shot: '登录了' },
        { at: 3100, glide: rest, dur: 700 },
        { at: 6800, shot: '进来了', clean: true },
      ],
});
scenes['signin-link-here'] = onLink('M7-09-点链接登录（同一个浏览器）');
scenes['signin-link-away'] = onLink('M7-09-在别的浏览器里点开链接');
scenes['signin-link-used'] = onLink('M7-09-链接用不了了', true);

(window as unknown as Record<string, unknown>).RECORDING = scenes[which] || scenes.signin;
