// The frame of every page after login (DESIGN.md §14): the desktop's sidebar
// and header, drawn by the design's App (design/demos/v3/app.js, in
// v3-app.ts), built from the user's view. One App for the visit; each page
// fills its main area and gives it back when it goes.
import './v3-env.ts';
import './v3-glyphs.ts';
import './v3-links.ts';
import './v3-app.ts';
import tokens from './tokens.js';
import { colorIndex, listedLANs } from './model.ts';
import type { UserView, LANView, PersonID } from './model.ts';
import type { Ctx } from './main.ts';

// The design's drawing lives on window: App (the frame, canvas, drag,
// card, toast), G (glyphs), Net (links and lights).
// eslint-disable-next-line @typescript-eslint/no-explicit-any
type Any = any;
export const App: Any = (window as Any).App;
export const G: Any = (window as Any).G;

// Glyphs the web needs beyond the desktop's (as design/demos/web/web.js).
const MORE: Record<string, string> = {
  download: '<path d="M12 4.5v10.5M7.5 10.5 12 15l4.5-4.5"/><path d="M5 19.5h14"/>',
  copy: '<rect x="8.5" y="8.5" width="11" height="11" rx="2.2"/><path d="M15.5 8.5V6.2a1.7 1.7 0 0 0-1.7-1.7H6.2a1.7 1.7 0 0 0-1.7 1.7v7.6a1.7 1.7 0 0 0 1.7 1.7h2.3"/>',
  check: '<path d="m5.5 12.5 4 4 9-9"/>',
  online: '<circle cx="12" cy="12" r="3.2" fill="currentColor" stroke="none"/><path d="M7.2 7.2a6.8 6.8 0 0 0 0 9.6M16.8 7.2a6.8 6.8 0 0 1 0 9.6"/>',
  relayLine: '<path d="M3 12h18M7 8l-4 4 4 4M17 8l4 4-4 4"/>',
  share: '<rect x="3.5" y="6.5" width="10" height="10" rx="2.5"/><path d="M15.5 11.5h5M18 9l2.5 2.5L18 14"/>',
};
if (!G.__web) {
  const ui0 = G.ui;
  G.ui = (k: string, size = 18): string =>
    MORE[k] ? `<svg viewBox="0 0 24 24" width="${size}" height="${size}" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${MORE[k]}</svg>` : ui0(k, size);
  G.__web = true;
}

export const esc = (s: string): string => s.replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c] as string);
export const spaced = (t: string): string => App.space(t);

// A service's name as the desktop shows it (proto.Service.DisplayName):
// its own, or its kind's (web is 网站, …), never blank.
const SERVICE_NAMES: Record<string, string> = { screen: '远程桌面', files: '共享文件夹', web: '网站', shell: '命令行', game: '游戏' };
export const serviceName = (s: { name?: string; kind: string }): string => s.name || SERVICE_NAMES[s.kind] || '服务';
// Its icon in a card, as cmd/deneb's serviceIcon picks it.
export const serviceIcon = (kind: string): string => ({ screen: 'screen', files: 'folder', shell: 'terminal' })[kind] || 'globe';

// "2 小时前在线", from the server's last seen.
export function ago(t?: string): string {
  if (!t) return '不在线';
  const s = (Date.now() - Date.parse(t)) / 1000;
  if (!(s >= 0)) return '不在线';
  if (s < 90) return '刚刚在线';
  if (s < 3600) return `${Math.round(s / 60)} 分钟前在线`;
  if (s < 86400) return `${Math.round(s / 3600)} 小时前在线`;
  if (s < 2 * 86400) return '昨天在线';
  return `${Math.round(s / 86400)} 天前在线`;
}

// Places for n devices in rows of up to five around (cx, cy), as the LAN
// canvas lines up my devices in its middle.
export function rows(n: number, cx: number, cy: number): { x: number; y: number }[] {
  const per = n > 5 ? Math.ceil(n / Math.ceil(n / 5)) : n;
  const lines = Math.ceil(n / Math.max(1, per));
  const out: { x: number; y: number }[] = [];
  for (let i = 0; i < n; i++) {
    const r = Math.floor(i / per), inRow = Math.min(per, n - r * per), c = i - r * per;
    out.push({ x: cx + (c - (inRow - 1) / 2) * 172, y: cy + (r - (lines - 1) / 2) * 176 });
  }
  return out;
}

// The device glyphs the design has, by the server's kinds.
export const glyphOf = (kind: string): string => ({ laptop: 'laptop', desktop: 'desktop', mini: 'mini', server: 'server', nas: 'nas', console: 'console', phone: 'phone', tablet: 'phone' })[kind] || 'desktop';

// A public device belongs to a LAN, not a person: it takes the studio's
// color (scene 4) under a key of its own.
export const PUBLIC = (lan: string): string => 'lan:' + lan;

// people puts everyone in my view into App.PEOPLE, which the design's
// drawing reads (balls, tiles): blue for me, a color by the desktop's own
// rule for the others (model.colorIndex, as cmd/deneb's ColorFor).
export function people(v: UserView): void {
  const P = App.PEOPLE;
  for (const p of v.people) {
    P[p.id] = { name: p.id === v.me ? '我' : p.displayName || p.name, full: p.displayName || p.name, ch: p.initial || (p.displayName || p.name).slice(0, 1), c: tokens.color.owners[colorIndex(v.me, p.id)] };
  }
  for (const l of v.lans) P[PUBLIC(l.id)] = { name: l.name, full: l.name, ch: l.name.slice(0, 1), c: tokens.color.owners[5] };
  // Whoever shares with me has a ball and a color, listed among the people or not.
  for (const s of v.sharedWithMe || []) if (!P[s.from]) P[s.from] = { name: s.fromLAN || '别人', full: s.fromLAN || '别人', ch: (s.fromLAN || '?').slice(0, 1), c: tokens.color.owners[colorIndex(v.me, s.from)] };
}
export const nameOf = (v: UserView, id: PersonID): string => {
  if (id === v.me) return '我';
  const p = v.people.find((x) => x.id === id);
  return p ? p.displayName || p.name : '';
};

let app: Any = null;
let current: { ctx: Ctx; sel: string } | null = null;

// frame returns the visit's App, its window in root, its sidebar showing
// sel ("lan:<id>", "relays", "share:<lan>"). The page fills app.canvas.
export function frame(root: HTMLElement, ctx: Ctx, sel: string): Any {
  const v = ctx.view();
  people(v);
  if (!app) {
    App.prototype.buildBar = function () {};
    app = new App({ title: '', sub: '', stack: [], side: [], action: '邀请朋友' });
    // The page is the window: no zoom, no centering (App.fit's, for demos).
    app.fit = function () {
      this.zoom = 1;
      this.win.style.zoom = this.win.style.left = this.win.style.top = '';
      if (this.layer) this.layer.resize();
    };
    app.win.classList.add('full');
    app.win.dataset.k = 'win';
    app.fit();
    // The header's button: an invite link for the LAN on screen.
    app.inviteBtn = app.win.querySelector('.head .right .btn');
    // The server out of reach: the header's second line says so, as the
    // desktop's does when its engine is (view.go); back, the page's own
    // words return. What is on screen stays as it was.
    const setTitle0 = app.setTitle.bind(app);
    let last: [string, string, string] | null = null;
    let down = false;
    app.setTitle = (t: string, s: string, tone = 'ok') => {
      last = [t, s, tone];
      if (down) setTitle0(t, '连不上服务器，正在重试…', 'off');
      else setTitle0(t, s, tone);
    };
    ctx.onStreamState((st) => {
      const was = down;
      down = st === 'retrying';
      if (was !== down && last) app.setTitle(...last);
    });
  }
  current = { ctx, sel };
  if (!seenShares) seenShares = new Set((v.sharedWithMe || []).map((s) => s.share));
  // Where the user put things is kept per page (a device of mine has a
  // place in each LAN, and one in 我的设备).
  app.page = sel;
  root.replaceChildren(app.win);
  side(v, sel, ctx);
  const me = v.people.find((p) => p.id === v.me);
  const foot = app.win.querySelector('.side .me');
  foot.querySelector('.ball').outerHTML = App.ball(v.me);
  foot.querySelector('span:not(.ball)').textContent = me ? me.displayName || me.name : '';
  app.layer.resize();
  return app;
}

// A page's own: what it put on the canvas and the card go; the frame stays.
export function clear(): void {
  if (!app) return;
  if (app.sheetEl.style.display !== 'none') app.closeSheet();
  if (app.toastEl) app.dropToast(app.toastEl);
  for (const id of Object.keys(app.nodes)) {
    const n = app.dropNode(id);
    if (n) n.el.remove();
  }
  for (const k of Object.keys(app.links)) app.unlink(k);
  app.canvas.replaceChildren(...[...app.canvas.children].filter((c: Element) => c.classList.contains('links')));
  app.onNodeBounds = app.onNodeTarget = app.onNodeDrop = app.onNodeDragging = app.onNodeEnd = app.onDeviceClick = app.onSheetClose = null;
  app.spotRules = null;
  if (app.inviteBtn) app.inviteBtn.style.display = 'none';
}

// The sidebar, as the desktop's (DESIGN.md §7, §14.3): my LANs; 我的设备,
// 共享给我 and the relays; then the console of each LAN I own. What the
// web does not have yet (全部, 新建局域网) is left out rather than shown
// dead.
function side(v: UserView, sel: string, ctx: Ctx): void {
  const P = App.PEOPLE;
  const row = (id: string, label: string, lead: string, tail = '') => `<div class="nav ${id === sel ? 'on' : ''}" data-id="${esc(id)}" role="link" tabindex="0">${lead}<span class="nav-label">${esc(spaced(label))}</span>${tail}</div>`;
  const dots = (l: LANView) => `<span class="dots">${l.members.filter((m) => m.state === 'joined').slice(0, 3).map((m) => App.dot(P[m.person] ? P[m.person].c : tokens.color.owners[1])).join('')}</span>`;
  const lans = listedLANs(v);
  const count = (n: number) => (n > 0 ? `<span class="count">${n}</span>` : '');
  const shared = (v.sharedWithMe || []).filter((s) => !leaving.has(s.share)).length;
  let html = lans.length ? `<div class="group">局域网</div>` + lans.map((l) => row('lan:' + l.id, l.name, dots(l))).join('') + `<div class="gap"></div>` : '';
  html += `<div class="group">设备</div>` + row('mine', '我的设备', G.ui('home', 18), count(v.devices.filter((d) => d.owner === v.me && !d.publicLAN).length));
  html += row('shared', '共享给我', G.ui('inbox', 18), count(shared)) + row('relays', '中转站', G.ui('relayLine', 18));
  for (const l of lans.filter((x) => x.owner === v.me)) html += `<div class="gap"></div><div class="group">${esc(spaced('管理' + l.name))}</div>` + row('share:' + l.id, '设备共享', G.ui('share', 18));
  const list = app.win.querySelector('.side-list');
  list.innerHTML = html;
  for (const nav of list.querySelectorAll('.nav[data-id]')) {
    const go = () => {
      const id = (nav as HTMLElement).dataset.id as string;
      const [kind, arg] = id.includes(':') ? [id.slice(0, id.indexOf(':')), id.slice(id.indexOf(':') + 1)] : [id, ''];
      ctx.go(kind === 'lan' ? '/lan/' + encodeURIComponent(arg) : kind === 'share' ? '/share/' + encodeURIComponent(arg) : '/' + kind);
    };
    nav.addEventListener('click', go);
    nav.addEventListener('keydown', (e: Event) => ((e as KeyboardEvent).key === 'Enter' ? go() : undefined));
  }
}

// Shares I let go of (共享给我's 不要了) whose undo is still up: off the page
// and out of the count already; the server hears of it when the undo goes.
export const leaving = new Set<string>();

// When the view changes, the sidebar follows (a LAN joined, a name changed).
// Something newly shared with me lights its row up a moment, as a LAN that
// comes in does on the desktop (one I let go of and took back does not).
let seenShares: Set<string> | null = null;
export function follow(v: UserView): void {
  if (!app || !current) return;
  people(v);
  side(v, current.sel, current.ctx);
  const ids = (v.sharedWithMe || []).map((s) => s.share);
  const fresh = seenShares ? ids.filter((id) => !seenShares!.has(id)) : [];
  for (const id of ids) seenShares?.add(id);
  if (fresh.length) {
    const r = app.win.querySelector('.nav[data-id="shared"]');
    // (It ends on the row's own background, white when it is chosen.)
    const tint = 'color-mix(in srgb, var(--primary) 14%, transparent)';
    r?.animate([{ background: tint }, { background: tint, offset: 0.3 }], { duration: 1200, easing: 'ease-out' });
  }
}
