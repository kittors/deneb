// The deployment's console (DESIGN §14.15, M7 batch 3, a design): what
// deneb-control's commands show, on the web, for the deployment's
// operators. 概况: what to look at, the server, the numbers; 用户 and 设备:
// find someone or something, its card on the right, what can be done to
// it; 记录: what operators did, and the server's log. The public relays
// stay operate.ts (/ops/relays). The control server's /api/v1/operator/*
// (control/console.go, control/opslog.go; design/plan/m7/ops-console-api.md);
// deneb-web -mock answers the same shapes from a made-up deployment.
import { frame, clear, esc, spaced, G, setOpsDot } from './shell.ts';
import { api, ApiError } from './api.ts';
import { D, after, now } from './clock.ts';
import tokens from './tokens.js';
import { colorIndex } from './model.ts';
import type { Ctx } from './main.ts';
import type { Route } from './router.ts';

type Any = any; // the design's App, untyped
type Part = 'overview' | 'people' | 'devices' | 'log';

interface Who {
  id: string;
  name: string;
  initial: string;
}
interface Person extends Who {
  email: string;
  created: string;
  lastSeen?: string;
  online: boolean;
  devices: number;
  devicesOnline: number;
  operator?: boolean;
  test?: boolean;
}
interface Device {
  id: string;
  name: string;
  kind: string;
  os: string;
  engine: string;
  state: 'online' | 'offline' | 'loggedOut' | 'expired';
  lastSeen?: string;
  expires?: string;
  address: string;
  owner: Who;
}
interface Detail extends Person {
  deviceList: Device[];
  // limit: what holds for them, 0 for none; default: the deployment's, 0
  // for none; own: a limit of their own.
  seats: { used: number; limit: number; own: boolean; default: number };
  browsers: { count: number; newest?: string };
  lans: { count: number; owned: number };
  owned: { id: string; name: string; others: number; first: string }[];
  relays: number;
}
interface Alert {
  key: string;
  level: 'bad' | 'warn';
  title: string;
  text?: string;
  at: string;
  resolved?: string;
  relay?: string;
}
interface Overview {
  alerts: Alert[];
  recent: Alert[];
  // version: the commit the server was built from; memoryLimit 0 when not
  // known; backup absent while there is none.
  server: { started: string; version: string; memory: number; memoryLimit: number; diskFree: number; backup?: string; backups: number; backupAt?: string };
  relays: { count: number; online: number; names: string[]; full: string[]; busy: string[] };
  counts: { people: number; newThisWeek: number; devices: number; online: number; lans: number; lanPeople: number; browsers: number };
}
interface Backup {
  name: string;
  size: number;
  written: string;
}
interface Line {
  seq: number;
  at: string;
  kind: string;
  text: string;
}
interface Rec {
  at: string;
  by: Who;
  text: string;
}

// What the server answers changes the page on the clock's next tick, as
// everything that moves on it does (clock.ts): a recording sees no frame
// out of step.
const tick = () => new Promise<void>((r) => after(0, r));
const get = async <T>(path: string): Promise<T> => {
  const v = await api.operator.get<T>(path);
  await tick();
  return v;
};
const act = async <T = unknown>(a: string, args: Record<string, string>): Promise<T> => {
  try {
    return await api.operator.act<T>(a, args);
  } finally {
    await tick();
  }
};

// ---- Words and small pieces ------------------------------------------------------------

const OS: Record<string, string> = { macos: 'macOS', windows: 'Windows', linux: 'Linux', ios: 'iOS', android: 'Android' };
const os = (o: string) => OS[o] || '其他系统';
const DAY = 86_400_000;
const two = (n: number) => String(n).padStart(2, '0');
const clockOf = (t: string) => {
  const d = new Date(t);
  return `${two(d.getHours())}:${two(d.getMinutes())}`;
};
const dayStart = (t: number) => {
  const d = new Date(t);
  d.setHours(0, 0, 0, 0);
  return d.getTime();
};
// dayOf names the day: 今天, 昨天, 11 月 11 日.
function dayOf(t: string): string {
  const d = dayStart(Date.parse(t));
  const today = dayStart(Date.now());
  if (d === today) return '今天';
  if (d === today - DAY) return '昨天';
  const x = new Date(t);
  return `${x.getMonth() + 1} 月 ${x.getDate()} 日`;
}
// since says how long ago: 刚刚, 12 分钟前, 3 小时前, 昨天 21:10, 11 月 2 日.
function since(t?: string): string {
  if (!t) return '';
  const s = (Date.now() - Date.parse(t)) / 1000;
  if (s < 90) return '刚刚';
  if (s < 3600) return `${Math.round(s / 60)} 分钟前`;
  if (dayOf(t) === '今天') return `${Math.round(s / 3600)} 小时前`;
  if (dayOf(t) === '昨天') return `昨天 ${clockOf(t)}`;
  if (s < 7 * 86400) return `${Math.round(s / 86400)} 天前`;
  return dayOf(t);
}
function stateOf(d: Device): { text: string; tone: string } {
  if (d.state === 'online') return { text: '在线', tone: 'on' };
  if (d.state === 'loggedOut') return { text: '登出了', tone: 'out' };
  if (d.state === 'expired') return { text: '登录过期了', tone: 'out' };
  return { text: spaced(`${since(d.lastSeen)}在线`), tone: '' };
}
const mb = (b: number) => (b >= 1 << 20 ? `${(b / (1 << 20)).toFixed(b >= 10 << 20 ? 0 : 1)} MB` : `${Math.round(b / 1024)} KB`);

function ball(who: Who, me: string, size = 32): string {
  const c = tokens.color.owners[colorIndex(me, who.id)];
  const s = size === 32 ? '' : `;width:${size}px;height:${size}px;font-size:${Math.round(size * 0.4)}px`;
  return `<span class="ball" style="--c:${c}${s}" aria-hidden="true">${esc(who.initial)}</span>`;
}
const KNOWN = new Set(['laptop', 'desktop', 'mini', 'server', 'nas', 'phone', 'console', 'board']);
const tile = (kind: string) => `<span class="cons-tile">${G.device(KNOWN.has(kind) ? kind : 'board', 20)}</span>`;
const tag = (p: { operator?: boolean; test?: boolean }) => (p.operator ? `<span class="cons-tag">管理员</span>` : '') + (p.test ? `<span class="cons-tag grey">测试</span>` : '');

const SEARCH = `<svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" aria-hidden="true"><circle cx="10.5" cy="10.5" r="6"/><path d="m15 15 4.5 4.5"/></svg>`;
const CLOSE = `<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" aria-hidden="true"><path d="M7 7l10 10M17 7 7 17"/></svg>`;
const BACK = `<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m15 6-6 6 6 6"/></svg>`;
const CHECK = `<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m5.5 12.5 4 4 9-9"/></svg>`;

// fadeIn brings in what was just drawn.
const fadeIn = (el: Element, ms = 200) => el.animate([{ opacity: 0 }, { opacity: 1 }], { duration: ms, easing: 'ease-out' });

// swapIn puts new contents in box: the old fade out, the height follows,
// the new fade in (no frame where it jumps).
function swapIn(box: HTMLElement, html: string): Promise<void> {
  // Heights as the layout has them, fractions too: a whole number at the
  // end of the curve would move what is below by a part of a pixel.
  const h0 = box.getBoundingClientRect().height;
  const old = box.firstElementChild ? box.animate([{ opacity: 1 }, { opacity: 0 }], { duration: 110, easing: 'ease-in', fill: 'forwards' }) : null;
  return (old ? old.finished : Promise.resolve()).then(
    () => {
      old?.cancel();
      box.innerHTML = html;
      const h1 = box.getBoundingClientRect().height;
      if (h0 > 0 && Math.abs(h1 - h0) > 1) box.animate([{ height: h0 + 'px' }, { height: h1 + 'px' }], { duration: 220, easing: 'cubic-bezier(0.3, 0.3, 0.2, 1)' });
      box.animate([{ opacity: 0 }, { opacity: 1 }], { duration: 160, easing: 'ease-out' });
    },
    () => {},
  );
}

// ---- The page -----------------------------------------------------------------------------

export function consolePage(root: HTMLElement, ctx: Ctx, route: Route): () => void {
  const part = ((route as { part?: Part }).part || 'overview') as Part;
  if (D.recording && /^console/.test(new URLSearchParams(location.search).get('record') || '')) import('./rec-console.ts');
  const app: Any = frame(root, ctx, 'ops-' + part);
  clear();
  app.setStack([]);
  const el = document.createElement('div');
  el.className = 'cons';
  app.canvas.appendChild(el);
  const me = ctx.view().me;
  const pg: Page = { el, app, ctx, me, gone: false };
  app.onSheetClose = () => after(0, () => el.classList.remove('carded'));
  const onKey = (e: KeyboardEvent) => e.key === 'Escape' && uncard(pg);
  document.addEventListener('keydown', onKey);
  el.addEventListener('click', (e) => {
    const t = e.target as HTMLElement;
    if (t === el || t.classList.contains('cons-col')) uncard(pg);
  });
  const stop = part === 'people' ? peoplePage(pg) : part === 'devices' ? devicesPage(pg) : part === 'log' ? logPage(pg) : overviewPage(pg);
  return () => {
    pg.gone = true;
    stop();
    document.removeEventListener('keydown', onKey);
    el.remove();
  };
}

interface Page {
  el: HTMLElement;
  app: Any;
  ctx: Ctx;
  me: string;
  gone: boolean;
}

// ready marks the page drawn, for the recordings (rec-console.ts).
const ready = (el: HTMLElement) => (el.dataset.ready = '1');

// ---- 概况 ---------------------------------------------------------------------------------------

function overviewPage(pg: Page): () => void {
  const { el, app } = pg;
  app.setTitle('概况', '', 'none');
  let recentOpen = false;
  const draw = (o: Overview) => {
    const c = o.counts;
    const sum = `${c.people} 个人，${c.devices} 台设备，现在 ${c.online} 台在线`;
    app.setTitle('概况', (o.alerts.length ? `有 ${o.alerts.length} 件事要看` : '一切正常') + ' · ' + sum, 'none');
    setOpsDot(o.alerts.length);
    const alertRow = (a: Alert, done = false) =>
      `<div class="cons-alert${done ? ' done' : ''}"><span class="cons-dot ${done ? 'ok' : a.level}"></span><div class="cons-at"><div class="cons-at-title">${esc(spaced(a.title))}</div>${a.text ? `<div class="cons-at-text">${esc(spaced(a.text))}</div>` : ''}</div>` +
      `<div class="cons-at-side"><span class="cons-when">${esc(since(a.at))}</span>${!done && a.relay ? `<button class="cons-link" data-go="/ops/relays">去看${esc(a.relay)}</button>` : ''}${!done && a.key.startsWith('backup') ? `<button class="cons-link" data-act="backups">看备份</button>` : ''}</div></div>`;
    const recent = o.recent.length
      ? `<div class="cons-quiet">最近 7 天${o.alerts.length ? '还' : ''}报过 ${o.recent.length} 次，都已经好了 · <button class="cons-link" data-act="recent">${recentOpen ? '收起' : '看看'}</button></div><div class="cons-recent"${recentOpen ? '' : ' hidden'}>${o.recent.map((a) => alertRow(a, true)).join('')}</div>`
      : '';
    const alerts = o.alerts.length
      ? `<section class="cons-card"><h2 class="cons-h">要看的事</h2>${o.alerts.map((a) => alertRow(a)).join('')}${recent}</section>`
      : `<section class="cons-card"><div class="cons-allok"><span class="cons-okmark">${CHECK}</span>现在没有要看的事</div>${recent}</section>`;
    const s = o.server;
    const up = Math.max(1, Math.round((Date.now() - Date.parse(s.started)) / DAY));
    const stale = !s.backup || Date.now() - Date.parse(s.backup) > 26 * 3600_000;
    const memTone = s.memoryLimit && s.memory / s.memoryLimit > 0.8 ? 'warn' : 'ok';
    const diskTone = s.diskFree < 0.1 ? 'bad' : s.diskFree < 0.2 ? 'warn' : 'ok';
    const r = o.relays;
    const one = r.count === 1 ? r.names[0] : '';
    const relayText = one
      ? r.online ? `${one}在线${r.full.length ? '，满了' : r.busy.length ? '，有点忙' : ''}` : `${one}连不上`
      : r.online < r.count ? `${r.count} 台，${r.count - r.online} 台连不上` : r.full.length ? `${r.count} 台都在线，${r.full.join('、')}满了` : r.busy.length ? `${r.count} 台都在线，${r.busy.join('、')}有点忙` : `${r.count} 台都在线`;
    const relayTone = r.online < r.count ? 'bad' : r.full.length || r.busy.length ? 'warn' : 'ok';
    const row = (tone: string, k: string, v: string, extra = '') => `<div class="cons-row"><span class="cons-dot ${tone}"></span><span class="cons-k">${k}</span><span class="cons-v">${esc(spaced(v))}</span>${extra}</div>`;
    const server =
      `<section class="cons-card"><h2 class="cons-h">服务器</h2>` +
      row('ok', '控制服务器', `在线，已经运行 ${up} 天 · 版本 ${s.version}`) +
      row(memTone, '内存', s.memoryLimit ? `用了 ${mb(s.memory)}，容器给它 ${mb(s.memoryLimit)}` : `用了 ${mb(s.memory)}`) +
      row(diskTone, '磁盘', `数据目录所在的磁盘还剩 ${Math.round(s.diskFree * 100)}%`) +
      (s.backup
        ? row(stale ? 'warn' : 'ok', '备份', `最新的一份是${since(s.backup)}写的 · 一共 ${s.backups} 份，每天一份`, `<button class="cons-link" data-act="backups">看全部</button>`)
        : row('warn', '备份', '还没有备份')) +
      `<div class="cons-backups" hidden></div>` +
      row(relayTone, '公共中转站', relayText, `<button class="cons-link" data-go="/ops/relays">去看</button>`) +
      `</section>`;
    const num = (n: string, unit: string, small: string, go = '') => `<${go ? 'button' : 'div'} class="cons-num"${go ? ` data-go="${go}"` : ''}><span class="cons-n"><b>${n}</b>${unit}</span><small>${esc(small)}</small></${go ? 'button' : 'div'}>`;
    const nums =
      `<section class="cons-card cons-nums">` +
      num(String(c.people), '个人', `本周新来 ${c.newThisWeek} 个`, '/ops/people') +
      num(String(c.devices), '台设备', `现在 ${c.online} 台在线`, '/ops/devices') +
      num(String(c.lans), '个局域网', `平均 ${c.lanPeople} 个人`) +
      num(String(c.browsers), '个人', '在浏览器里登录着') +
      `</section>`;
    el.innerHTML = `<div class="cons-col">${alerts}${server}${nums}</div>`;
    wire();
  };
  const wire = () => {
    for (const b of el.querySelectorAll<HTMLElement>('[data-go]')) b.addEventListener('click', () => pg.ctx.go(b.dataset.go as string));
    el.querySelector('[data-act="recent"]')?.addEventListener('click', (e) => {
      recentOpen = !recentOpen;
      const box = el.querySelector('.cons-recent') as HTMLElement;
      (e.target as HTMLElement).textContent = recentOpen ? '收起' : '看看';
      reveal(box, recentOpen);
    });
    for (const b of el.querySelectorAll('[data-act="backups"]')) b.addEventListener('click', () => void backups());
  };
  let backupsOpen = false;
  const backups = async () => {
    const box = el.querySelector('.cons-backups') as HTMLElement;
    const link = el.querySelector('.cons-row [data-act="backups"]') as HTMLElement;
    backupsOpen = !backupsOpen;
    link.textContent = backupsOpen ? '收起' : '看全部';
    if (backupsOpen && !box.childElementCount) {
      const list = await get<Backup[]>('backups');
      box.innerHTML = list.map((b) => `<div class="cons-bk"><span>${esc(dayOf(b.written))} ${clockOf(b.written)}</span><span class="cons-bk-name">${esc(b.name)}</span><span>${mb(b.size)}</span></div>`).join('');
    }
    reveal(box, backupsOpen);
  };
  get<Overview>('overview').then(
    (o) => {
      if (pg.gone) return;
      draw(o);
      fadeIn(el.firstElementChild as Element, 220);
      ready(el);
    },
    (e) => (el.innerHTML = `<div class="cons-col"><p class="cons-empty">${esc(e instanceof ApiError ? e.message : '连不上服务器')}</p></div>`),
  );
  return () => {};
}

// reveal opens or closes box in place: its height, and its padding and
// margins with it (they would come or go at once otherwise), on a curve.
function reveal(box: HTMLElement, open: boolean): void {
  if (open) box.hidden = false;
  const cs = getComputedStyle(box);
  const px = (v: string) => parseFloat(v) || 0;
  // Its height as the box has it: the page's boxes are border-box, so the
  // padding is in the height already.
  const inner = cs.boxSizing === 'border-box' ? 0 : px(cs.paddingTop) + px(cs.paddingBottom);
  const full = {
    height: box.getBoundingClientRect().height - inner + 'px',
    paddingTop: cs.paddingTop,
    paddingBottom: cs.paddingBottom,
    marginTop: cs.marginTop,
    marginBottom: cs.marginBottom,
    opacity: 1,
  };
  const none = { height: '0px', paddingTop: '0px', paddingBottom: '0px', marginTop: '0px', marginBottom: '0px', opacity: 0 };
  const a = box.animate(open ? [none, full] : [full, none], { duration: open ? 240 : 200, easing: 'cubic-bezier(0.3, 0.3, 0.2, 1)' });
  if (!open) a.finished.then(() => (box.hidden = true), () => {});
}

// ---- Lists: 用户, 设备 ------------------------------------------------------------------

interface ListSpec<T> {
  title: string;
  sub: (total: number, r: Any, filtered: boolean) => string;
  search: string;
  chips: [string, string][];
  path: string; // people or devices
  key: string; // what the chip sets: filter or state
  rows: (items: T[]) => string;
  open: (id: string, item: T | undefined) => void; // the row's own, as the list has it
  top?: () => Promise<string>; // above the list (等着批准的登录)
}

function listPage<T>(pg: Page, spec: ListSpec<T>): { reload: () => void; stop: () => void } {
  const { el, app } = pg;
  app.setTitle(spec.title, '', 'none');
  el.innerHTML =
    `<div class="cons-col">` +
    `<div class="cons-tools"><label class="cons-search">${SEARCH}<input type="search" placeholder="${esc(spec.search)}" autocomplete="off" spellcheck="false" aria-label="${esc(spec.search)}" /></label>` +
    `<div class="cons-chips" role="group">${spec.chips.map(([v, t], i) => `<button class="cons-chip${i === 0 ? ' on' : ''}" data-v="${v}">${esc(t)}</button>`).join('')}</div></div>` +
    `<div class="cons-top"></div>` +
    `<section class="cons-card cons-list"><div class="cons-rows"></div></section>` +
    `<div class="cons-more-box"></div></div>`;
  // Unseen until the first rows are in: then it all fades in at once.
  const col = el.firstElementChild as HTMLElement;
  col.style.opacity = '0';
  const input = el.querySelector('input') as HTMLInputElement;
  const rows = el.querySelector('.cons-rows') as HTMLElement;
  const more = el.querySelector('.cons-more-box') as HTMLElement;
  const top = el.querySelector('.cons-top') as HTMLElement;
  let q = '';
  let chip = '';
  let items: T[] = [];
  let total = 0;
  let gen = 0;
  const query = (offset: number) => `${spec.path}?q=${encodeURIComponent(q)}&${spec.key}=${chip}&offset=${offset}&limit=30`;
  const footer = () => {
    const left = total - items.length;
    more.innerHTML = left > 0 ? `<button class="cons-more">再看 ${Math.min(30, left)} 个</button><span class="cons-quiet">还有 ${left} 个</span>` : '';
    more.querySelector('button')?.addEventListener('click', async () => {
      const r = await get<Any>(query(items.length));
      items = items.concat(r[spec.path]);
      const add = document.createElement('div');
      add.innerHTML = spec.rows(r[spec.path]);
      const news = Array.from(add.children);
      rows.append(...news);
      for (const n of news) fadeIn(n);
      footer();
    });
  };
  const load = async (animate: boolean) => {
    const g = ++gen;
    const r = await get<Any>(query(0));
    if (g !== gen || pg.gone) return;
    const topHTML = spec.top && !top.childElementCount ? await spec.top() : null;
    if (g !== gen || pg.gone) return;
    items = r[spec.path];
    total = r.total;
    app.setTitle(spec.title, spec.sub(total, r, !!(q || chip)), 'none');
    const html = items.length ? spec.rows(items) : `<p class="cons-empty">${q ? '没找到' : '这里还没有'}</p>`;
    if (topHTML != null) {
      top.innerHTML = topHTML;
      top.style.display = topHTML ? '' : 'none';
      wireTop();
    }
    if (animate) await swapIn(rows, html);
    else rows.innerHTML = html;
    footer();
    ready(el);
  };
  const wireTop = () => top.dispatchEvent(new Event('drawn'));
  rows.addEventListener('click', (e) => {
    const row = (e.target as HTMLElement).closest('[data-id]') as HTMLElement | null;
    if (row) spec.open(row.dataset.id as string, items.find((x) => (x as { id?: string }).id === row.dataset.id));
  });
  // Filtered as one types, a moment after the last key.
  let wait: { cancel(): void } | null = null;
  input.addEventListener('input', () => {
    wait?.cancel();
    wait = after(220, () => {
      q = input.value.trim();
      void load(true);
    });
  });
  for (const b of el.querySelectorAll<HTMLElement>('.cons-chip'))
    b.addEventListener('click', () => {
      if (b.classList.contains('on')) return;
      el.querySelector('.cons-chip.on')?.classList.remove('on');
      b.classList.add('on');
      chip = b.dataset.v as string;
      void load(true);
    });
  void load(false).then(() => {
    col.style.opacity = '';
    fadeIn(col, 220);
  });
  return { reload: () => void load(true), stop: () => wait?.cancel() };
}

function personRows(pg: Page): (ps: Person[]) => string {
  return (ps) =>
    ps
      .map(
        (p) =>
          `<div class="cons-item" data-id="${esc(p.id)}" role="button" tabindex="0">${ball(p, pg.me)}<div class="cons-main"><div class="cons-name">${esc(p.name)}${tag(p)}</div><div class="cons-sub">${esc(p.email)}</div></div>` +
          `<div class="cons-meta">${p.devices} 台设备${p.devices ? ` · ${p.devicesOnline} 台在线` : ''}</div><div class="cons-state${p.online ? ' on' : ''}">${p.online ? '在线' : esc(spaced(p.lastSeen ? since(p.lastSeen) + '在线' : '还没有设备'))}</div></div>`,
      )
      .join('');
}

function deviceRows(pg: Page): (ds: Device[]) => string {
  return (ds) =>
    ds
      .map((d) => {
        const st = stateOf(d);
        return (
          `<div class="cons-item" data-id="${esc(d.id)}" role="button" tabindex="0">${tile(d.kind)}<div class="cons-main"><div class="cons-name">${esc(d.name)}</div><div class="cons-sub">${ball(d.owner, pg.me, 16)}${esc(spaced(`${d.owner.name} · ${os(d.os)} · 引擎 ${d.engine}`))}</div></div>` +
          `<div class="cons-state ${st.tone}">${esc(st.text)}</div></div>`
        );
      })
      .join('');
}

function peoplePage(pg: Page): () => void {
  let base = '';
  // The title's second line says how many, and how many came this week.
  get<Overview>('overview').then((o) => (base = `${o.counts.people} 个人 · 本周新来 ${o.counts.newThisWeek} 个`), () => {});
  const list = listPage<Person>(pg, {
    title: '用户',
    sub: (total, _r, filtered) => (filtered || !base ? `找到 ${total} 个人` : base),
    search: '找人：名字或邮箱',
    chips: [
      ['', '全部'],
      ['new', '最近新来的'],
      ['online', '有设备在线'],
      ['operators', '部署管理员'],
      ['test', '测试账号'],
    ],
    path: 'people',
    key: 'filter',
    rows: personRows(pg),
    open: (id) => void openPerson(pg, id, () => list.reload()),
  });
  return list.stop;
}

function devicesPage(pg: Page): () => void {
  let logins: { logins: { id: string; hostname: string; os: string; created: string }[]; accounts: Who[] } | null = null;
  const list = listPage<Device>(pg, {
    title: '设备',
    sub: (total, r, filtered) => (filtered ? `找到 ${total} 台` : `${r.all} 台 · 现在 ${r.online} 台在线`),
    search: '找设备：名字或主人的邮箱',
    chips: [
      ['', '全部'],
      ['online', '在线'],
      ['offline', '不在线'],
      ['loggedOut', '登出了'],
      ['expired', '登录过期了'],
    ],
    path: 'devices',
    key: 'state',
    rows: deviceRows(pg),
    open: (_id, d) => d && openDevice(pg, d, () => list.reload()),
    top: async () => {
      logins = await get('logins');
      if (!logins || !logins.logins.length) return '';
      return (
        `<section class="cons-card cons-wait"><h2 class="cons-h">等着批准的登录<span class="cons-hint">测试账号用的；真邮箱的账号收验证码，不用批准</span></h2>` +
        logins.logins
          .map(
            (l) =>
              `<div class="cons-line" data-login="${esc(l.id)}">${tile('laptop')}<span class="cons-grow">${esc(spaced(`${l.hostname} · ${os(l.os)} · ${since(l.created)}`))}</span><button class="cons-link" data-act="approve">批准…</button></div>` +
              `<div class="cons-pick" hidden>${(logins?.accounts || []).map((a) => `<button class="cons-chip" data-email="${esc(a.name)}">${esc(a.name)}</button>`).join('')}</div>`,
          )
          .join('') +
        `</section>`
      );
    },
  });
  const top = pg.el.querySelector('.cons-top') as HTMLElement;
  top.addEventListener('drawn', () => {
    const b = top.querySelector('[data-act="approve"]') as HTMLElement | null;
    if (!b) return;
    const pick = top.querySelector('.cons-pick') as HTMLElement;
    b.addEventListener('click', () => {
      const open = pick.hidden;
      b.textContent = open ? '收起' : '批准…';
      reveal(pick, open);
    });
    for (const c of pick.querySelectorAll<HTMLElement>('[data-email]'))
      c.addEventListener('click', async () => {
        const login = (top.querySelector('[data-login]') as HTMLElement).dataset.login as string;
        const host = logins?.logins.find((l) => l.id === login)?.hostname || '这台设备';
        try {
          await act('login.approve', { login, email: c.dataset.email as string });
        } catch (e) {
          pg.app.toast(e instanceof ApiError ? e.message : '没能批准，再试一次', {});
          return;
        }
        after(0, () => {
          // The card's place closes up, the column's gap with it (-16), and
          // then it is gone: the list ends where it would have been.
          const h = top.getBoundingClientRect().height;
          top.style.overflow = 'hidden';
          const a = top.animate([{ height: h + 'px', marginBottom: '0px', opacity: 1 }, { height: '0px', marginBottom: '-16px', opacity: 0 }], { duration: 260, easing: 'cubic-bezier(0.3, 0.3, 0.2, 1)', fill: 'forwards' });
          a.finished.then(() => {
            a.cancel();
            top.innerHTML = '';
            top.style.display = 'none';
            top.style.overflow = '';
          }, () => {});
          pg.app.toast(`「${host}」登进了 ${c.dataset.email}`, { x: toastX(pg) });
          list.reload();
        });
      });
  });
  return list.stop;
}

const toastX = (pg: Page) => Math.max(24, pg.app.canvas.clientWidth / 2 - 170);

// ---- The card on the right: a person, a device -------------------------------------------

// The card is the window's own (app.sheet, DESIGN §6.3): it grows out of
// its top right corner, as a device's card does on a LAN's canvas, the
// canvas stays white under it, and the list makes room for it (.carded),
// so that it covers no row. Another card in its place fades in.
function card(pg: Page, key: string, html: string): HTMLElement {
  pg.el.classList.add('carded');
  const s = pg.app.sheet(`<div class="cons-sheet">${html}</div>`, key) as HTMLElement;
  return s.querySelector('.cons-sheet') as HTMLElement;
}
const uncard = (pg: Page) => pg.app.closeSheet();
const closeBtn = `<button class="icon-btn sheet-close" title="关上" aria-label="关上">${CLOSE}</button>`;

// line is a row of a card: what it is, and what can be done.
const sec = (title: string, inner: string, action = '') => `<div class="cons-sec"><div class="cons-st"><span>${title}</span>${action}</div>${inner}</div>`;

async function openPerson(pg: Page, id: string, changed: () => void): Promise<void> {
  let p: Detail;
  try {
    p = await get<Detail>('people/' + encodeURIComponent(id));
  } catch (e) {
    pg.app.toast(e instanceof ApiError ? e.message : '连不上服务器', {});
    return;
  }
  if (pg.gone) return;
  const he = p.name;
  const key = 'person:' + p.id;
  const draw = (): string => {
    const seats = `用了 ${p.seats.used} 个，${p.seats.limit ? '上限 ' + p.seats.limit : '不限'}${p.seats.own ? '（单独调过）' : '（部署的默认）'}`;
    const br = p.browsers.count ? `在 ${p.browsers.count} 个浏览器里登录着，最近一次是${p.browsers.newest ? since(p.browsers.newest) : ''}` : '没有在浏览器里登录';
    return (
      `<div class="top">${ball(p, pg.me, 40)}<div><b>${esc(p.name)}${tag(p)}</b><span>${esc(p.email)}</span></div>${closeBtn}</div>` +
      `<div class="cons-dsub">${esc(spaced(`${dayOf(p.created)}开通 · ${p.online ? '在线' : p.lastSeen ? since(p.lastSeen) + '在线' : '还没有设备'}`))} · <button class="cons-link" data-act="copy">复制邮箱</button></div>` +
      sec(
        `设备（${p.deviceList.length}）`,
        p.deviceList.length
          ? p.deviceList
              .map((x) => {
                const st = stateOf(x);
                return `<div class="cons-dev" data-dev="${esc(x.id)}" role="button" tabindex="0">${tile(x.kind)}<span class="cons-grow">${esc(x.name)}</span><span class="cons-state ${st.tone}">${esc(spaced(os(x.os) + ' · ' + st.text))}</span></div>`;
              })
              .join('')
          : `<div class="cons-line quiet">还没有设备</div>`,
      ) +
      sec('位置', `<div class="cons-line" data-line="seats"><span class="cons-grow">${esc(spaced(seats))}</span></div>`, `<button class="cons-link" data-act="seats">调整</button>`) +
      sec('浏览器', `<div class="cons-line" data-line="browsers"><span class="cons-grow">${esc(spaced(br))}</span></div>`, p.browsers.count ? `<button class="cons-link" data-act="signout">${esc(spaced(`让${he}都退出`))}</button>` : '') +
      sec('局域网', `<div class="cons-line"><span class="cons-grow">${p.lans.count ? `在 ${p.lans.count} 个局域网里${p.lans.owned ? `，自己建了 ${p.lans.owned} 个` : ''}` : '不在局域网里'}</span></div>`) +
      sec('部署管理员', `<div class="cons-line" data-line="operator"><span class="cons-grow">${p.operator ? '是' : '不是'}</span></div>`, p.id === pg.me ? '' : `<button class="cons-link" data-act="operator">${esc(spaced(p.operator ? `不让${he}管了` : '设成管理员'))}</button>`) +
      (p.id === pg.me || p.test ? '' : `<div class="cons-danger"><button class="cons-link danger" data-act="delete">${esc(spaced(`替${he}注销账号…`))}</button></div>`)
    );
  };
  let page = card(pg, key, draw());
  // Drawn again in place (算了): the card keeps its key, its height follows.
  const refresh = () => {
    page = card(pg, key, draw());
    wire(page);
  };
  const wire = (page: HTMLElement) => {
    page.querySelector('[data-act="copy"]')?.addEventListener('click', (e) => {
      void navigator.clipboard?.writeText(p.email).catch(() => {});
      const b = e.target as HTMLElement;
      b.textContent = '复制了';
      after(1400, () => (b.textContent = '复制邮箱'));
    });
    for (const x of page.querySelectorAll<HTMLElement>('[data-dev]'))
      x.addEventListener('click', () => {
        const d = p.deviceList.find((y) => y.id === x.dataset.dev);
        if (d) openDevice(pg, d, changed, p);
      });
    page.querySelector('[data-act="seats"]')?.addEventListener('click', () => seatsLine(page));
    page.querySelector('[data-act="signout"]')?.addEventListener('click', () =>
      confirmLine(page, 'browsers', `${p.browsers.count} 个浏览器都会退出`, '都退出', refresh, async () => {
        p = await act<Detail>('sessions.revoke', { person: p.id });
        return '已经都退出了';
      }),
    );
    page.querySelector('[data-act="operator"]')?.addEventListener('click', () =>
      confirmLine(
        page,
        'operator',
        p.operator ? `不让${he}管这个部署？${he}就看不到这些页面了` : `把${he}设成部署管理员？${he}就能看到这些页面、做这些事`,
        p.operator ? '不让管' : '设成',
        refresh,
        async () => {
          p = await act<Detail>('operator.set', { person: p.id, on: String(!p.operator) });
          changed();
          return p.operator ? '是' : '不是';
        },
      ),
    );
    // The card goes, the list goes, the page comes: all on one tick of the
    // clock, so that they start together.
    page.querySelector('[data-act="delete"]')?.addEventListener('click', () =>
      after(0, () => {
        uncard(pg);
        deleteAccount(pg, p, changed);
      }),
    );
  };
  wire(page);
  // 调整: the line becomes − n +, and 恢复默认; each click holds, the server
  // hears a moment after the last, and the toast offers undo.
  const seatsLine = (page: HTMLElement) => {
    const line = page.querySelector('[data-line="seats"]') as HTMLElement;
    // From no limit, the steps start at what is used.
    let n = p.seats.limit || Math.max(p.seats.used, 1);
    // Undo gives back what held: their own limit (-1 for their own "none"),
    // or the deployment's (0).
    const was = p.seats.own ? p.seats.limit || -1 : 0;
    page.querySelector('[data-act="seats"]')?.remove();
    line.innerHTML = `<span class="cons-grow">上限</span><span class="cons-step"><button aria-label="少一个">−</button><b>${n}</b><button aria-label="多一个">+</button></span><button class="cons-link" data-act="default">恢复默认</button>`;
    fadeIn(line, 160);
    const b = line.querySelector('b') as HTMLElement;
    let wait: { cancel(): void } | null = null;
    let restoring = false;
    const set = (v: number, restore = false) => {
      restoring = restore;
      n = Math.max(1, Math.min(10000, v));
      b.textContent = String(n);
      b.animate([{ transform: 'scale(1.18)' }, { transform: 'none' }], { duration: 220, easing: 'ease-out' });
      wait?.cancel();
      wait = after(700, async () => {
        const to = restoring || n === p.seats.default ? 0 : n;
        try {
          p = await act<Detail>('seats.set', { person: p.id, limit: String(to) });
        } catch (e) {
          pg.app.toast(e instanceof ApiError ? e.message : '没能调成，再试一次', {});
          return;
        }
        pg.app.toast(to ? `${he} 的位置上限调成了 ${n}` : `${he} 的位置上限恢复成了部署的默认（${p.seats.default ? p.seats.default + ' 个' : '不限'}）`, {
          x: toastX(pg),
          undo: async () => {
            p = await act<Detail>('seats.set', { person: p.id, limit: String(was) });
            n = p.seats.limit || Math.max(p.seats.used, 1);
            b.textContent = String(n);
          },
        });
      });
    };
    const [minus, plus] = line.querySelectorAll('.cons-step button');
    minus.addEventListener('click', () => set(n - 1));
    plus.addEventListener('click', () => set(n + 1));
    // 恢复默认: the deployment's number, or none; sent as 0 either way.
    line.querySelector('[data-act="default"]')?.addEventListener('click', () => set(p.seats.default || Math.max(p.seats.used, 1), true));
  };
}

// confirmLine turns a card's line into the question and its two buttons;
// yes does it, and the line says how it ended.
function confirmLine(page: HTMLElement, which: string, ask: string, yes: string, cancel: () => void, run: () => Promise<string>, danger = false): void {
  const line = page.querySelector(`[data-line="${which}"]`) as HTMLElement;
  const head = line.closest('.cons-sec')?.querySelector('.cons-st .cons-link') as HTMLElement | null;
  head?.animate([{ opacity: 1 }, { opacity: 0 }], { duration: 120, fill: 'forwards' }).finished.then(() => head.remove(), () => {});
  void swapIn(line, `<div class="cons-ask"><span class="cons-grow">${esc(spaced(ask))}</span><button class="cons-btn${danger ? ' danger' : ''}" data-yes>${esc(yes)}</button><button class="cons-link quiet" data-no>算了</button></div>`).then(() => wireAsk());
  const wireAsk = () => {
    (line.querySelector('[data-no]') as HTMLElement).addEventListener('click', cancel);
    const y = line.querySelector('[data-yes]') as HTMLButtonElement;
    y.addEventListener('click', async () => {
      y.disabled = true;
      let said: string;
      try {
        said = await run();
      } catch (e) {
        said = e instanceof ApiError ? e.message : '没能做成，再试一次';
      }
      void swapIn(line, `<span class="cons-grow done">${esc(spaced(said))}</span>`);
    });
  };
}

// openDevice shows a device's card, as the list or a person's card has it:
// at once, so the card before it never stays up to be clicked by mistake.
function openDevice(pg: Page, dev: Device, changed: () => void, from?: Detail): void {
  const st = stateOf(dev);
  const html =
    (from ? `<button class="cons-back" data-act="back">${BACK}${esc(from.name)}</button>` : '') +
    `<div class="top"><div class="tile-s"><div class="tile" style="--c:${tokens.color.owners[colorIndex(pg.me, dev.owner.id)]}">${G.device(KNOWN.has(dev.kind) ? dev.kind : 'board', 20)}</div></div>` +
    `<div><b>${esc(dev.name)}</b><span class="cons-owner" role="button" tabindex="0">${esc(spaced(dev.owner.name + ' 的设备'))}</span></div>${closeBtn}</div>` +
    sec(
      '这台设备',
      `<div class="cons-kv"><span>状态</span><b class="cons-state ${st.tone}" data-line="state">${esc(st.text)}</b></div>` +
        `<div class="cons-kv"><span>系统</span><b>${esc(os(dev.os))} · 引擎 ${esc(dev.engine)}</b></div>` +
        `<div class="cons-kv"><span>Deneb 地址</span><b class="mono">${esc(dev.address)}</b></div>` +
        (dev.expires ? `<div class="cons-kv"><span>登录</span><b data-line="expires">${esc(fmtDate(dev.expires))}到期</b></div>` : ''),
    ) +
    sec(
      '能做的',
      (dev.state === 'online' || dev.state === 'offline'
        ? `<div class="cons-do" data-line="relogin"><button class="cons-btn ghost" data-act="relogin">让它重新登录</button><span class="cons-why">马上退出，主人要在这台电脑上再登录一次，地址不变</span></div>`
        : '') +
        `<div class="cons-do" data-line="delete"><button class="cons-btn ghost danger" data-act="delete">删除这台设备</button><span class="cons-why">它在各个局域网里的位置、分享、服务一起删掉${dev.state === 'online' ? '，它马上登出' : ''}<br />删了就找不回来</span></div>`,
    );
  const page = card(pg, 'device:' + dev.id, html);
  page.querySelector('[data-act="back"]')?.addEventListener('click', () => from && void openPerson(pg, from.id, changed));
  page.querySelector('.cons-owner')?.addEventListener('click', () => void openPerson(pg, dev.owner.id, changed));
  page.querySelector('[data-act="relogin"]')?.addEventListener('click', async (e) => {
    const b = e.currentTarget as HTMLButtonElement;
    b.disabled = true;
    try {
      await act('device.relogin', { device: dev.id });
    } catch (err) {
      b.disabled = false;
      pg.app.toast(err instanceof ApiError ? err.message : '没能做成，再试一次', {});
      return;
    }
    after(0, () => {
      const line = page.querySelector('[data-line="relogin"]') as HTMLElement;
      void swapIn(line, `<span class="cons-why done">${esc(spaced(`已经让它重新登录了，等${dev.owner.name}在这台电脑上再登录`))}</span>`);
      const s = page.querySelector('[data-line="state"]') as HTMLElement;
      s.textContent = '登录过期了';
      s.className = 'cons-state out';
      const ex = page.querySelector('[data-line="expires"]') as HTMLElement | null;
      if (ex) ex.textContent = '要重新登录';
      for (const x of [s, ex]) x?.animate([{ opacity: 0 }, { opacity: 1 }], { duration: 180, easing: 'ease-out' });
      pg.app.toast(`${dev.owner.name}的「${dev.name}」要重新登录了`, { x: toastX(pg) });
      changed();
    });
  });
  // 删除: the button turns solid red, "再点一下，删掉", for 3 seconds.
  const del = page.querySelector('[data-act="delete"]') as HTMLButtonElement;
  let armed: { cancel(): void } | null = null;
  del.addEventListener('click', async () => {
    if (!armed) {
      del.classList.remove('ghost');
      del.textContent = '再点一下，删掉';
      armed = after(3000, () => {
        armed = null;
        del.classList.add('ghost');
        del.textContent = '删除这台设备';
      });
      return;
    }
    armed.cancel();
    del.disabled = true;
    try {
      await act('device.delete', { device: dev.id });
    } catch (err) {
      del.disabled = false;
      pg.app.toast(err instanceof ApiError ? err.message : '没能删掉，再试一次', {});
      return;
    }
    after(0, () => {
      uncard(pg);
      const row = pg.el.querySelector(`.cons-rows [data-id="${CSS.escape(dev.id)}"]`) as HTMLElement | null;
      if (row) {
        const h = row.offsetHeight;
        row.animate([{ height: h + 'px', opacity: 1 }, { height: '0px', opacity: 0, paddingTop: '0px', paddingBottom: '0px' }], { duration: 260, easing: 'cubic-bezier(0.3, 0.3, 0.2, 1)', fill: 'forwards' }).finished.then(() => row.remove(), () => {});
      }
      pg.app.toast(`删掉了${dev.owner.name}的「${dev.name}」`, { x: toastX(pg) });
    });
  });
}

function fmtDate(t: string): string {
  const d = new Date(t);
  return `${d.getFullYear()} 年 ${d.getMonth() + 1} 月 ${d.getDate()} 日`;
}

// ---- 替人注销账号 -------------------------------------------------------------------------

function deleteAccount(pg: Page, p: Detail, changed: () => void): void {
  const { el, app } = pg;
  const he = p.name;
  const choice: Record<string, string> = {};
  for (const l of p.owned) choice[l.id] = 'transfer';
  const online = p.deviceList.filter((x) => x.state === 'online').length;
  const items = [
    `${p.deviceList.length} 台设备${online ? `（在线的 ${online} 台马上登出）` : ''}`,
    `${he}在 ${p.lans.count} 个局域网里的位置`,
    '给出和收到的分享、发出和收到的邀请',
    ...(p.relays ? [`${he}自己的中转站 ${p.relays} 台（装在${he}机器上的，要${he}自己停掉）`] : []),
    '浏览器里的登录',
  ];
  const out = el.firstElementChild as HTMLElement | null;
  const leave = out ? out.animate([{ opacity: 1 }, { opacity: 0 }], { duration: 160, easing: 'ease-in', fill: 'forwards' }).finished : Promise.resolve();
  void leave.then(() => build(), () => build());
  const build = () => {
    app.setTitle(spaced(`替${he}注销账号`), '只在本人要求时这样做', 'none');
    el.innerHTML =
      `<div class="cons-col"><section class="cons-card cons-del">` +
      `<p class="cons-del-warn">只在本人要求时这样做。这件事会记进运营记录。</p>` +
      `<h2 class="cons-h">会一起删掉</h2><ul class="cons-del-list">${items.map((t) => `<li>${esc(spaced(t))}</li>`).join('')}</ul>` +
      p.owned
        .map(
          (l) =>
            `<div class="cons-del-lan"><span>${esc(spaced(`「${l.name}」里还有 ${l.others} 个人：`))}</span><span class="cons-chips" data-lan="${esc(l.id)}"><button class="cons-chip on" data-v="transfer">${esc(spaced(`转给最早加入的${l.first}`))}</button><button class="cons-chip" data-v="delete">整个删掉</button></span></div>`,
        )
        .join('') +
      `<p class="cons-quiet">删掉的在备份里还会留到那份备份被换掉，最长两个月左右。</p>` +
      `<label class="cons-confirm"><span>${esc(spaced(`输${he}的邮箱确认`))}</span><input type="email" autocomplete="off" spellcheck="false" autocapitalize="none" placeholder="${esc(p.email)}" /></label>` +
      `<div class="cons-del-foot"><button class="cons-btn ghost" data-act="cancel">算了</button><button class="cons-btn danger" data-act="go" disabled>注销</button></div>` +
      `</section></div>`;
    fadeIn(el.firstElementChild as Element, 220);
    for (const g of el.querySelectorAll<HTMLElement>('[data-lan]'))
      for (const b of g.querySelectorAll<HTMLElement>('button'))
        b.addEventListener('click', () => {
          g.querySelector('.on')?.classList.remove('on');
          b.classList.add('on');
          choice[g.dataset.lan as string] = b.dataset.v as string;
        });
    const input = el.querySelector('.cons-confirm input') as HTMLInputElement;
    const go = el.querySelector('[data-act="go"]') as HTMLButtonElement;
    input.addEventListener('input', () => (go.disabled = input.value.trim().toLowerCase() !== p.email.toLowerCase()));
    // Out, then the list again, drawn afresh (it fades in).
    const back = () => {
      const card = el.firstElementChild as HTMLElement;
      card.animate([{ opacity: 1 }, { opacity: 0 }], { duration: 160, easing: 'ease-in', fill: 'forwards' }).finished.then(() => pg.ctx.go('/ops/people', true), () => {});
    };
    (el.querySelector('[data-act="cancel"]') as HTMLElement).addEventListener('click', back);
    go.addEventListener('click', async () => {
      go.disabled = true;
      const args: Record<string, string> = { person: p.id, confirm: input.value.trim() };
      for (const [k, v] of Object.entries(choice)) args['lan:' + k] = v;
      try {
        await act('account.delete', args);
      } catch (e) {
        go.disabled = false;
        app.toast(e instanceof ApiError ? e.message : '没能注销，再试一次', {});
        return;
      }
      const card = el.querySelector('.cons-del') as HTMLElement;
      void swapIn(card, `<div class="cons-gone"><span class="cons-okmark big">${CHECK}</span><b>${esc(spaced(`${he}的账号注销了`))}</b><span class="cons-quiet">记进了运营记录</span></div>`);
      after(1400, () => {
        changed();
        back();
      });
    });
  };
}

// ---- 记录 -------------------------------------------------------------------------------------

function logPage(pg: Page): () => void {
  const { el, app } = pg;
  app.setTitle('记录', '部署管理员做过的事，和服务器的日志', 'none');
  el.innerHTML =
    `<div class="cons-col"><div class="cons-seg" role="tablist"><button class="on" data-v="records">运营记录</button><button data-v="log">服务器日志</button></div>` +
    `<div class="cons-pane"></div></div>`;
  const pane = el.querySelector('.cons-pane') as HTMLElement;
  const col = el.firstElementChild as HTMLElement;
  col.style.opacity = '0';
  let stopLog: (() => void) | null = null;
  const show = async (which: string, animate: boolean) => {
    stopLog?.();
    stopLog = null;
    const recs = which === 'records' ? await get<Rec[]>('records?limit=' + REC_PAGE) : [];
    const html = which === 'records' ? recordsHTML(pg, recs) : logHTML();
    if (pg.gone) return;
    if (animate) await swapIn(pane, html);
    else pane.innerHTML = html;
    if (which === 'log') stopLog = tail(pane);
    else wireRecords(pg, pane, recs);
    ready(el);
  };
  for (const b of el.querySelectorAll<HTMLElement>('.cons-seg button'))
    b.addEventListener('click', () => {
      if (b.classList.contains('on')) return;
      el.querySelector('.cons-seg .on')?.classList.remove('on');
      b.classList.add('on');
      void show(b.dataset.v as string, true);
    });
  void show('records', false).then(() => {
    col.style.opacity = '';
    fadeIn(col, 220);
  });
  return () => stopLog?.();
}

// The operators' record comes REC_PAGE at a time, newest first; it is kept
// a year (DESIGN §14.15).
const REC_PAGE = 200;
const SERVER = `<span class="cons-srv" title="服务器上的命令" aria-hidden="true"><svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round"><path d="m6 8 4 4-4 4M12 16h6"/></svg></span>`;

// recRows are records as lines under their days; day is the day shown last
// above them (records further back go on under it).
function recRows(pg: Page, recs: Rec[], day: string): { html: string; day: string } {
  let html = '';
  for (const r of recs) {
    const d = dayOf(r.at);
    if (d !== day) {
      html += `<div class="cons-day">${esc(d)}</div>`;
      day = d;
    }
    // Done on the server with deneb-control: no one's ball, the command's mark.
    const who = r.by.id ? ball(r.by, pg.me, 22) : SERVER;
    html += `<div class="cons-rec"><span class="cons-time">${clockOf(r.at)}</span>${who}<span class="cons-grow">${esc(spaced(r.by.name + ' ' + r.text))}</span></div>`;
  }
  return { html, day };
}

function recordsHTML(pg: Page, recs: Rec[]): string {
  const rows = recs.length ? recRows(pg, recs, '').html : `<p class="cons-empty">还没有运营记录</p>`;
  return (
    `<section class="cons-card cons-recs">${rows}</section>` +
    (recs.length >= REC_PAGE ? `<div class="cons-more-box"><button class="cons-more cons-recs-more">再往前看</button></div>` : '') +
    `<p class="cons-quiet center">运营记录留 1 年</p>`
  );
}

// wireRecords lets 再往前看 bring the next REC_PAGE, before the oldest shown.
function wireRecords(pg: Page, pane: HTMLElement, recs: Rec[]): void {
  const more = pane.querySelector('.cons-recs-more') as HTMLButtonElement | null;
  if (!more) return;
  let oldest = recs[recs.length - 1].at;
  let day = dayOf(oldest);
  more.addEventListener('click', async () => {
    more.disabled = true;
    const older = await get<Rec[]>(`records?limit=${REC_PAGE}&before=${encodeURIComponent(oldest)}`).catch(() => [] as Rec[]);
    more.disabled = false;
    if (pg.gone) return;
    const r = recRows(pg, older, day);
    day = r.day;
    const add = document.createElement('div');
    add.innerHTML = r.html;
    const news = Array.from(add.children);
    (pane.querySelector('.cons-recs') as HTMLElement).append(...news);
    for (const n of news) fadeIn(n);
    if (older.length) oldest = older[older.length - 1].at;
    if (older.length < REC_PAGE) (more.parentElement as HTMLElement).remove();
  });
}

const KINDS: [string, string][] = [
  ['', '全部'],
  ['login', '登录'],
  ['device', '设备'],
  ['alert', '报警'],
  ['error', '出错'],
];

function logHTML(): string {
  return (
    `<div class="cons-chips cons-logchips">${KINDS.map(([v, t], i) => `<button class="cons-chip${i === 0 ? ' on' : ''}" data-v="${v}">${t}</button>`).join('')}</div>` +
    `<section class="cons-card cons-logcard"><button class="cons-link cons-earlier" hidden>再往前看</button><p class="cons-quiet cons-cap" hidden>最多往前翻到 2000 行；更早的，到服务器上用 deneb-control logs 看</p><div class="cons-log" role="log"></div></section>` +
    `<p class="cons-quiet center">今天和昨天的日志，有新的就接着往下长；日志是英文的，原样显示</p>`
  );
}

// glide scrolls box to its bottom on the page's clock (a recording's too).
function glide(box: HTMLElement): void {
  const from = box.scrollTop;
  const to = box.scrollHeight - box.clientHeight;
  const t0 = now();
  const off = D.onFrame(() => {
    const k = Math.min(1, (now() - t0) / 320);
    box.scrollTop = from + (to - from) * (1 - Math.pow(1 - k, 3));
    if (k >= 1) off();
  });
}

// tail shows the log's last lines and those that come, every 2 seconds;
// the chips filter it.
function tail(pane: HTMLElement): () => void {
  const box = pane.querySelector('.cons-log') as HTMLElement;
  const earlier = pane.querySelector('.cons-earlier') as HTMLElement;
  const cap = pane.querySelector('.cons-cap') as HTMLElement;
  let kind = '';
  let shown = 0; // lines on the page, for the 2000 the server stops at
  let last = 0;
  let first = 0;
  let gen = 0;
  let stopped = false;
  const lineHTML = (l: Line) => `<div class="cons-ln k-${esc(l.kind)}"><span class="t">${clockOf(l.at)}:${two(new Date(l.at).getSeconds())}</span><span class="x">${esc(l.text)}</span></div>`;
  const atBottom = () => box.scrollTop + box.clientHeight >= box.scrollHeight - 8;
  const load = async () => {
    const g = ++gen;
    const r = await get<{ lines: Line[]; more: boolean; last: number; note?: string }>(`log?kind=${kind}`);
    if (g !== gen || stopped) return;
    // A server whose log is not in files says so, and there is nothing.
    box.innerHTML = r.note ? `<p class="cons-empty">${esc(r.note)}</p>` : r.lines.length ? r.lines.map(lineHTML).join('') : `<p class="cons-empty">今天和昨天没有这一类</p>`;
    first = r.lines[0]?.seq || 0;
    last = r.last;
    shown = r.lines.length;
    earlier.hidden = !r.more;
    cap.hidden = true;
    box.scrollTop = box.scrollHeight;
  };
  const poll = () =>
    after(2000, async () => {
      if (stopped) return;
      const g = gen;
      const r = await get<{ lines: Line[]; last: number }>(`log?kind=${kind}&after=${last}`).catch(() => null);
      if (stopped) return;
      if (r && g === gen) {
        last = r.last;
        if (r.lines.length) {
          box.querySelector('.cons-empty')?.remove();
          shown += r.lines.length;
          const stick = atBottom();
          const add = document.createElement('div');
          add.innerHTML = r.lines.map(lineHTML).join('');
          const news = Array.from(add.children) as HTMLElement[];
          box.append(...news);
          for (const n of news) n.animate([{ opacity: 0, background: 'var(--track)' }, { opacity: 1, background: 'var(--track)', offset: 0.3 }, { opacity: 1, background: 'transparent' }], { duration: 1600, easing: 'ease-out' });
          if (stick) glide(box);
        }
      }
      poll();
    });
  void load().then(() => poll());
  for (const b of pane.querySelectorAll<HTMLElement>('.cons-logchips .cons-chip'))
    b.addEventListener('click', () => {
      if (b.classList.contains('on')) return;
      pane.querySelector('.cons-logchips .on')?.classList.remove('on');
      b.classList.add('on');
      kind = b.dataset.v as string;
      box.animate([{ opacity: 1 }, { opacity: 0 }], { duration: 110, fill: 'forwards' }).finished.then(async () => {
        await load();
        box.getAnimations().forEach((a) => a.cancel());
        box.animate([{ opacity: 0 }, { opacity: 1 }], { duration: 160, easing: 'ease-out' });
      }, () => {});
    });
  // 再往前看: the 200 before the first shown, as long as the server says
  // there are more (it stops at the newest 2000 of the kind). Lines written
  // since the last poll move those 2000 on, so where it stops may be a few
  // short of 2000 here: within a page of it, that is where it stopped.
  earlier.addEventListener('click', async () => {
    const r = await get<{ lines: Line[]; more: boolean }>(`log?kind=${kind}&before=${first}`);
    if (stopped) return;
    const h0 = box.scrollHeight;
    box.insertAdjacentHTML('afterbegin', r.lines.map(lineHTML).join(''));
    if (r.lines.length) first = r.lines[0].seq;
    shown += r.lines.length;
    box.scrollTop += box.scrollHeight - h0;
    earlier.hidden = !r.more;
    cap.hidden = r.more || shown <= 2000 - 200;
  });
  return () => {
    stopped = true;
  };
}
