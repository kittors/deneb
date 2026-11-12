// M7 on the web, recorded (DESIGN §6.15, §14.13): kittors' 我的设备 against
// deneb-web -mock. ?record=m7-account: the account card, the privacy notes,
// 退出登录 and the login page's link; m7-device: deleting a device, undo,
// and deleting one logged out; m7-delete: 注销账号, the LANs with others in
// them handed over or deleted, the email again, deleted, the login page
// saying so. Loaded only with ?record.
import './rec.js';

                                      
const which = new URLSearchParams(location.search).get('record') || '';
const rest = [1180, 760];
const click = (at        , sel        , dur = 600)           => [
  { at, glide: { sel }, dur },
  { at: at + dur + 200, down: true },
  { at: at + dur + 280, up: true },
];
// typing puts s in the field one key at a time, as a hand types.
const typing = (at        , sel        , s        , each = 55)           =>
  [...s].map((_, i) => ({
    at: at + i * each,
    js: `(() => { const e = document.querySelector(${JSON.stringify(sel)}); e.value = ${JSON.stringify(s.slice(0, i + 1))}; e.dispatchEvent(new Event('input')); })()`,
  }));
const scroll = (at        , top        )         => ({ at, js: `document.querySelector('.acct-del').scrollTo({ top: ${top}, behavior: 'smooth' })` });

const scenes                                                                         = {
  'm7-account': {
    title: 'M7-11-网页上的账号卡片',
    duration: 9400,
    actions: [
      ...click(900, '.side .me', 800),
      { at: 2800, shot: '账号卡片' },
      { at: 3000, glide: { sel: '.acct-card [data-a="privacy"]' }, dur: 600 },
      { at: 3900, shot: '指着隐私说明' },
      ...click(4200, '.acct-card [data-a="logout"]'),
      { at: 6300, shot: '退出登录以后', clean: true },
      { at: 6600, glide: { sel: '.join-privacy a' }, dur: 700 },
      { at: 7800, shot: '登录页的隐私说明' },
    ],
  },
  'm7-device': {
    title: 'M7-12-网页上删除设备',
    duration: 19600,
    actions: [
      { at: 700, shot: '我的设备', clean: true },
      ...click(900, '.dev[data-id="d_kgame"] .tile', 700),
      ...click(2700, '.sheet .more'),
      { at: 4300, shot: '更多里的删除' },
      ...click(4500, '.sheet .del-btn'),
      { at: 6200, shot: '删掉了' },
      ...click(6400, '.toast button'),
      { at: 8100, shot: '撤销', clean: true },
      ...click(8400, '.dev[data-id="d_kold"] .tile', 700),
      ...click(10200, '.sheet .more', 500),
      { at: 11700, shot: '已登出的设备' },
      ...click(11900, '.sheet .del-btn'),
      { at: 13100, glide: rest, dur: 500 },
      { at: 18900, shot: '提示条走了', clean: true },
    ],
  },
  'm7-delete': {
    title: 'M7-13-网页上注销账号',
    duration: 19400,
    actions: [
      ...click(900, '.side .me', 800),
      ...click(2700, '.acct-card [data-a="delete"]'),
      { at: 3800, shot: '正在看会删掉什么' },
      { at: 5000, shot: '会删掉的' },
      scroll(5200, 260),
      ...click(6200, '.acct-choice.delete'),
      { at: 8100, shot: '整个删掉' },
      ...click(8300, '.acct-choice.transfer'),
      { at: 10200, shot: '转给最早加入的人' },
      scroll(10400, 2000),
      ...click(11200, '.acct-email'),
      ...typing(12300, '.acct-email', 'kittors@deneb.test'),
      { at: 13800, shot: '邮箱对上了' },
      ...click(14000, '.acct-go'),
      { at: 15300, shot: '正在注销' },
      { at: 15500, glide: rest, dur: 500 },
      { at: 17800, shot: '注销了', clean: true },
    ],
  },
};

(window                                      ).RECORDING = scenes[which] || scenes['m7-account'];
