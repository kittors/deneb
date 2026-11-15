// 我的设备's new parts on the web, recorded (design/plan/m7/mydevices.md
// §10): kittors' page against deneb-web -mock. ?record=mydev-services: the
// NAS into 周末联机 and out after asking in place; the MacBook's 共享文件夹
// opened to 周末联机 and closed after asking in place; one set up by hand,
// wrong twice first; opened; taken off after asking in place. mydev-add:
// "添加设备", copy, "首次打开时被系统阻止", the other computer comes, the
// tick, the card goes, the new tile lands; then once more with the card
// closed: a toast. Loaded only with ?record.
import './rec.ts';

type Action = Record<string, unknown>;
const which = new URLSearchParams(location.search).get('record') || '';
const rest = [1180, 760];
const click = (at: number, sel: string, dur = 600): Action[] => [
  { at, glide: { sel }, dur },
  { at: at + dur + 200, down: true },
  { at: at + dur + 280, up: true },
];
// typing puts s in the field one key at a time, as a hand types, after
// emptying it.
const typing = (at: number, sel: string, s: string, each = 70): Action[] =>
  ['', ...s].map((_, i) => ({
    at: at + i * each,
    js: `(() => { const e = document.querySelector(${JSON.stringify(sel)}); e.focus(); e.value = ${JSON.stringify(s.slice(0, i))}; e.dispatchEvent(new Event('input', { bubbles: true })); })()`,
  }));
const chip = (label: string) => `.sheet .schip[aria-label="${label}"]`;
const up = (at: number): Action => ({
  at,
  js: `fetch('/api/v1/_mock/device-up?solo=1', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' })`,
});

const scenes: Record<string, { title: string; duration: number; actions: Action[] }> = {
  'mydev-services': {
    title: 'M7-5-W1-局域网和服务（网页）',
    duration: 29600,
    actions: [
      { at: 900, shot: '我的设备', clean: true },
      // The NAS, in no LAN: into 周末联机, then out after asking in place.
      ...click(1100, '.dev[data-id="d_knas"] .tile', 700),
      { at: 2300, glide: rest, dur: 500 },
      { at: 3100, shot: 'NAS卡片', clean: true },
      ...click(3300, chip('把NAS加入周末联机')),
      { at: 5000, shot: '加入周末联机' },
      ...click(5200, chip('把NAS移出周末联机')),
      { at: 6500, shot: '再点一下移出' },
      ...click(6600, chip('把NAS移出周末联机'), 300),
      { at: 7900, shot: '移出了' },
      // The MacBook's services.
      ...click(8100, '.dev[data-id="d_kmac"] .tile', 700),
      ...click(9500, chip('把共享文件夹开放给周末联机')),
      { at: 11000, shot: '开放给周末联机' },
      ...click(11200, chip('在周末联机关闭共享文件夹')),
      { at: 12500, shot: '再点一下关闭' },
      ...click(12600, chip('在周末联机关闭共享文件夹'), 300),
      { at: 13800, shot: '关闭了' },
      ...click(14000, '.sheet .schip.add'),
      { at: 15300, shot: '添加服务' },
      ...typing(15500, '.sheet .svc-in.name', '我的相册'),
      ...typing(16000, '.sheet .svc-in.port', '70000'),
      ...click(16600, '.sheet .svc-ok'),
      { at: 18200, shot: '端口不对' },
      ...typing(18400, '.sheet .svc-in.port', '3389'),
      ...click(18900, '.sheet .svc-ok'),
      { at: 20500, shot: '端口被占用' },
      ...typing(20700, '.sheet .svc-in.port', '8080'),
      ...click(21200, '.sheet .svc-ok'),
      { at: 22900, shot: '添加了' },
      ...click(23100, chip('把我的相册开放给周末联机')),
      { at: 24600, shot: '相册开放给周末联机' },
      { at: 24800, glide: { sel: '.sheet .svc-row:last-of-type .svc-name' }, dur: 500 },
      ...click(25500, '.sheet .svc-x[aria-label="删除我的相册"]', 500),
      { at: 26800, shot: '再点一下删除' },
      ...click(27000, '.sheet .svc-x[aria-label="删除我的相册"]', 300),
      { at: 28000, glide: rest, dur: 500 },
      { at: 28800, shot: '删除了' },
    ],
  },
  'mydev-add': {
    title: 'M7-5-W2-添加设备（网页）',
    duration: 17400,
    actions: [
      { at: 900, shot: '虚线格', clean: true },
      { at: 1100, glide: { sel: '.dev[data-id="add-one"] .tile' }, dur: 700 },
      { at: 2000, shot: '悬停-添加设备' },
      { at: 2100, down: true },
      { at: 2180, up: true },
      { at: 3200, shot: '三步', clean: true },
      ...click(3400, '.sheet .add-copy'),
      { at: 4400, shot: '已复制' },
      ...click(4700, '.sheet .add-more'),
      { at: 5900, shot: '被阻止时' },
      { at: 6100, glide: rest, dur: 500 },
      up(7200),
      { at: 7900, shot: '已加入' },
      { at: 9600, shot: '新设备落位', clean: true },
      // Again with the card closed: the toast.
      ...click(10200, '.dev[data-id="add-one"] .tile', 700),
      ...click(11800, '.sheet .sheet-close', 600),
      { at: 13000, glide: rest, dur: 500 },
      up(13900),
      { at: 14700, shot: '关着时的提示条' },
    ],
  },
};
(window as unknown as Record<string, unknown>).RECORDING = scenes[which] || scenes['mydev-services'];
