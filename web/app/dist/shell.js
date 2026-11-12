// The frame of every page after login (DESIGN.md §14): the desktop's sidebar
// and header, drawn by the design's App (design/demos/v3/app.js, in
// v3-app.ts), built from the user's view. One App for the visit; each page
// fills its main area and gives it back when it goes.
import { wireAccount } from './account.js';
import './v3-env.js';
import './v3-glyphs.js';
import './v3-links.js';
import './v3-app.js';
import tokens from './tokens.js';
import { colorIndex, listedLANs, sharedOnly } from './model.js';
                                                                             
import { ApiError } from './api.js';
import { D } from './clock.js';
                                     

// The design's drawing lives on window: App (the frame, canvas, drag,
// card, toast), G (glyphs), Net (links and lights).
// eslint-disable-next-line @typescript-eslint/no-explicit-any
               
export const App      = (window       ).App;
export const G      = (window       ).G;

// Glyphs the web needs beyond the desktop's (as design/demos/web/web.js).
const MORE                         = {
  download: '<path d="M12 4.5v10.5M7.5 10.5 12 15l4.5-4.5"/><path d="M5 19.5h14"/>',
  copy: '<rect x="8.5" y="8.5" width="11" height="11" rx="2.2"/><path d="M15.5 8.5V6.2a1.7 1.7 0 0 0-1.7-1.7H6.2a1.7 1.7 0 0 0-1.7 1.7v7.6a1.7 1.7 0 0 0 1.7 1.7h2.3"/>',
  check: '<path d="m5.5 12.5 4 4 9-9"/>',
  online: '<circle cx="12" cy="12" r="3.2" fill="currentColor" stroke="none"/><path d="M7.2 7.2a6.8 6.8 0 0 0 0 9.6M16.8 7.2a6.8 6.8 0 0 1 0 9.6"/>',
  relayLine: '<path d="M3 12h18M7 8l-4 4 4 4M17 8l4 4-4 4"/>',
  share: '<rect x="3.5" y="6.5" width="10" height="10" rx="2.5"/><path d="M15.5 11.5h5M18 9l2.5 2.5L18 14"/>',
  // 运营 (the deployment's page): a pulse, the public relays' load.
  ops: '<path d="M3.5 12.5h4l2.2-5.5 4.3 11 2.3-5.5h4.2"/>',
};
if (!G.__web) {
  const ui0 = G.ui;
  G.ui = (k        , size = 18)         =>
    MORE[k] ? `<svg viewBox="0 0 24 24" width="${size}" height="${size}" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${MORE[k]}</svg>` : ui0(k, size);
  G.__web = true;
}

export const esc = (s        )         => s.replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]          );
export const spaced = (t        )         => App.space(t);

// A service's name as the desktop shows it (proto.Service.DisplayName):
// its own, or its kind's (web is 网站, …), never blank.
const SERVICE_NAMES                         = { screen: '远程桌面', files: '共享文件夹', web: '网站', shell: '命令行', game: '游戏' };
export const serviceName = (s                                 )         => s.name || SERVICE_NAMES[s.kind] || '服务';
// Its icon in a card, as cmd/deneb's serviceIcon picks it.
export const serviceIcon = (kind        )         => ({ screen: 'screen', files: 'folder', shell: 'terminal' })[kind] || 'globe';

// "2 小时前在线", from the server's last seen.
export function ago(t         )         {
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
export function rows(n        , cx        , cy        )                             {
  const per = n > 5 ? Math.ceil(n / Math.ceil(n / 5)) : n;
  const lines = Math.ceil(n / Math.max(1, per));
  const out                             = [];
  for (let i = 0; i < n; i++) {
    const r = Math.floor(i / per), inRow = Math.min(per, n - r * per), c = i - r * per;
    out.push({ x: cx + (c - (inRow - 1) / 2) * 172, y: cy + (r - (lines - 1) / 2) * 176 });
  }
  return out;
}

// The device glyphs the design has, by the server's kinds.
export const glyphOf = (kind        )         => ({ laptop: 'laptop', desktop: 'desktop', mini: 'mini', server: 'server', nas: 'nas', console: 'console', phone: 'phone', tablet: 'phone' })[kind] || 'desktop';

// A public device belongs to a LAN, not a person: it takes the studio's
// color (scene 4) under a key of its own.
export const PUBLIC = (lan        )         => 'lan:' + lan;

// people puts everyone in my view into App.PEOPLE, which the design's
// drawing reads (balls, tiles): blue for me, a color by the desktop's own
// rule for the others (model.colorIndex, as cmd/deneb's ColorFor).
export function people(v          )       {
  const P = App.PEOPLE;
  for (const p of v.people) {
    P[p.id] = { name: p.id === v.me ? '我' : p.displayName || p.name, full: p.displayName || p.name, ch: p.initial || (p.displayName || p.name).slice(0, 1), c: tokens.color.owners[colorIndex(v.me, p.id)] };
  }
  for (const l of v.lans) P[PUBLIC(l.id)] = { name: l.name, full: l.name, ch: l.name.slice(0, 1), c: tokens.color.owners[5] };
  // Whoever invites me by name too (they may be no one I know yet).
  for (const inv of v.invitations || []) if (!P[inv.inviter.id]) P[inv.inviter.id] = { name: inv.inviter.displayName || inv.inviter.name, full: inv.inviter.displayName || inv.inviter.name, ch: inv.inviter.initial || (inv.inviter.displayName || inv.inviter.name).slice(0, 1), c: tokens.color.owners[colorIndex(v.me, inv.inviter.id)] };
  // Whoever shares with me has a ball and a color, listed among the people or not.
  for (const s of v.sharedWithMe || []) if (!P[s.from]) P[s.from] = { name: s.fromLAN || '别人', full: s.fromLAN || '别人', ch: (s.fromLAN || '?').slice(0, 1), c: tokens.color.owners[colorIndex(v.me, s.from)] };
}
export const nameOf = (v          , id          )         => {
  if (id === v.me) return '我';
  const p = v.people.find((x) => x.id === id);
  return p ? p.displayName || p.name : '';
};

let app      = null;
let current                                   = null;

// frame returns the visit's App, its window in root, its sidebar showing
// sel ("lan:<id>", "relays", "share:<lan>"). The page fills app.canvas.
export function frame(root             , ctx     , sel        )      {
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
    let last                                  = null;
    let down = false;
    app.setTitle = (t        , s        , tone = 'ok') => {
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
  // Another page: the canvas and the header's words fade in over the old
  // ones' place (DESIGN §14.10 M3-13 "画布淡入替换"), as the desktop's LANs.
  const switching = !!current && current.sel !== sel;
  current = { ctx, sel };
  if (switching)
    for (const el of [app.canvas, app.win.querySelector('.head')]                 ) el.animate([{ opacity: 0 }, { opacity: 1 }], { duration: 220, easing: 'ease-out' });
  if (!seenShares) seenShares = new Set((v.sharedWithMe || []).map((s) => s.share));
  if (!seenInvites) {
    // An invite by name waiting when the page opens shows its card once.
    seenInvites = new Set();
    D.after(700, () => current && checkInvites(current.ctx.view()));
  }
  // Where the user put things is kept per page (a device of mine has a
  // place in each LAN, and one in 我的设备).
  app.page = sel;
  root.replaceChildren(app.win);
  side(v, sel, ctx);
  const me = v.people.find((p) => p.id === v.me);
  const foot = app.win.querySelector('.side .me');
  foot.querySelector('.ball').outerHTML = App.ball(v.me);
  foot.querySelector('span:not(.ball)').textContent = me ? me.displayName || me.name : '';
  // The account card (M7, DESIGN §6.15).
  wireAccount(app, ctx);
  app.layer.resize();
  return app;
}

// A page's own: what it put on the canvas and the card go; the frame stays.
export function clear()       {
  if (!app) return;
  if (app.sheetEl.style.display !== 'none') app.closeSheet();
  if (app.toastEl) app.dropToast(app.toastEl);
  for (const id of Object.keys(app.nodes)) {
    const n = app.dropNode(id);
    if (n) n.el.remove();
  }
  for (const k of Object.keys(app.links)) app.unlink(k);
  app.canvas.replaceChildren(...[...app.canvas.children].filter((c         ) => c.classList.contains('links')));
  app.onNodeBounds = app.onNodeTarget = app.onNodeDrop = app.onNodeDragging = app.onNodeEnd = app.onDeviceClick = app.onSheetClose = app.onNodeStart = null;
  app.spotRules = null;
  // A page may let a held node up into the header (a LAN's, to the balls).
  delete app.clampTile;
  if (app.inviteBtn) {
    app.inviteBtn.style.display = 'none';
    app.inviteBtn.classList.remove('full');
  }
  const head = app.win.querySelector('.head');
  head.querySelector('.seat-line')?.remove();
  head.querySelector('.stack-hint')?.remove();
  const stack = head.querySelector('.stack');
  stack.className = 'stack';
  stack.replaceChildren();
  head.querySelector('.title').classList.remove('clickable');
  head.querySelector('.title').onclick = null;
}

// The sidebar, as the desktop's (DESIGN.md §7, §14.3): my LANs; 我的设备,
// 共享给我 and the relays; then the console of each LAN I own; last, for
// the deployment's operators, 运营. What the web does not have yet (全部,
// 新建局域网) is left out rather than shown dead. A LAN I wait to be let
// into is a grey row, "等同意" (§6.7); one of mine with someone or something
// waiting for me has a small blue dot.
function side(v          , sel        , ctx     )       {
  const P = App.PEOPLE;
  const row = (id        , label        , lead        , tail = '', cls = '') => `<div class="nav ${id === sel ? 'on' : ''} ${cls}" data-id="${esc(id)}" role="link" tabindex="0">${lead}<span class="nav-label">${esc(spaced(label))}</span>${tail}</div>`;
  const dots = (l         ) => `<span class="dots">${l.members.filter((m) => m.state === 'joined').slice(0, 3).map((m) => App.dot(P[m.person] ? P[m.person].c : tokens.color.owners[1])).join('')}</span>`;
  const lans = v.lans.filter((l) => !sharedOnly(v, l));
  const count = (n        ) => (n > 0 ? `<span class="count">${n}</span>` : '');
  const shared = (v.sharedWithMe || []).filter((s) => !leaving.has(s.share)).length;
  const waits = (l         ) => l.owner === v.me && (l.members.some((m) => m.state === 'pending') || l.devices.some((d) => d.pending));
  const lanRow = (l         ) => (l.pending ? row('lan:' + l.id, l.name, dots(l), `<span class="wait-tag">等同意</span>`, 'wait') : row('lan:' + l.id, l.name, dots(l), waits(l) ? `<span class="ndot" aria-label="有等你同意的"></span>` : ''));
  // Invites made out to me by name, put off: a grey row each, which shows
  // the invite again (§6.7 "收到的邀请").
  const invs = (v.invitations || []).filter((i) => i.state === 'pending');
  const invRow = (i               ) => row('invitation:' + i.invite, `${i.inviter.displayName || i.inviter.name}邀请你 · ${i.lanName}`, `<span class="dots">${App.dot(P[i.inviter.id]?.c || tokens.color.owners[1])}</span>`, '', 'wait');
  let html = lans.length || invs.length ? `<div class="group">局域网</div>` + lans.map(lanRow).join('') + invs.map(invRow).join('') + `<div class="gap"></div>` : '';
  html += `<div class="group">设备</div>` + row('mine', '我的设备', G.ui('home', 18), count(v.devices.filter((d) => d.owner === v.me && !d.publicLAN && !deletingDevs.has(d.id)).length));
  html += row('shared', '共享给我', G.ui('inbox', 18), count(shared)) + row('relays', '中转站', G.ui('relayLine', 18));
  for (const l of listedLANs(v).filter((x) => x.owner === v.me)) html += `<div class="gap"></div><div class="group">${esc(spaced('管理' + l.name))}</div>` + row('share:' + l.id, '设备共享', G.ui('share', 18));
  // The deployment's operators: its public relays, at the foot (§14.12 M4-13).
  if (v.operator) html += `<div class="gap"></div><div class="group">部署</div>` + row('ops', '运营', G.ui('ops', 18));
  const list = app.win.querySelector('.side-list');
  list.innerHTML = html;
  for (const nav of list.querySelectorAll('.nav[data-id]')) {
    const go = () => {
      const id = (nav               ).dataset.id          ;
      const [kind, arg] = id.includes(':') ? [id.slice(0, id.indexOf(':')), id.slice(id.indexOf(':') + 1)] : [id, ''];
      if (kind === 'invitation') return showInvitation(arg, ctx);
      ctx.go(kind === 'lan' ? '/lan/' + encodeURIComponent(arg) : kind === 'share' ? '/share/' + encodeURIComponent(arg) : '/' + kind);
    };
    nav.addEventListener('click', go);
    nav.addEventListener('keydown', (e       ) => ((e                 ).key === 'Enter' ? go() : undefined));
  }
}

// Shares I let go of (共享给我's 不要了) whose undo is still up: off the page
// and out of the count already; the server hears of it when the undo goes.
export const leaving = new Set        ();
// Devices of mine deleted while the undo is up (M7, DESIGN §6.15): off the
// page and out of the count already; the server hears of it when the undo
// goes.
export const deletingDevs = new Set        ();
// People whose seat I am freeing (the seats' card) while the undo is up:
// out of my seats already (DESIGN.md §6.11 位置).
export const freeing = new Set        ();

// showInvitation is an invite made out to me by name, as a card at the top
// right (§6.7, as the desktop's): who and where, and what is in it; "以后再
// 说" puts it off (its row stays), "加入" joins and goes there.
export function showInvitation(id        , ctx     )       {
  const v = ctx.view();
  const inv = (v.invitations || []).find((i) => i.invite === id);
  if (!app || !inv) return;
  const from = inv.inviter.displayName || inv.inviter.name;
  const people = inv.members || [];
  const sub = inv.seatsFull ? `${from}的位置满了，腾出来就能加入` : people.length ? `里面有 ${people.length} 个人，${inv.devices ?? 0} 台设备` : '';
  const s = app.sheet(
    `<div class="top person-top">${App.ball(inv.inviter.id, 'big')}<div><b>${esc(spaced(`${from}邀请你加入「${inv.lanName}」`))}</b>${sub ? `<span>${esc(spaced(sub))}</span>` : ''}</div></div>` +
      `<div class="seat-foot inv-foot"><button class="btn ghost sm" type="button" data-act="later">以后再说</button><button class="btn primary sm" type="button" data-act="join">加入</button></div>`,
    'invitation:' + id,
  );
  (s.querySelector('[data-act="later"]')               ).onclick = () => app.closeSheet();
  const join = s.querySelector('[data-act="join"]')                     ;
  join.onclick = async () => {
    join.disabled = true;
    app.closeSheet();
    try {
      const r = await ctx.op('invite.accept', { invitation: id });
      ctx.go('/lan/' + encodeURIComponent(r.result?.lan || inv.lan));
    } catch (e) {
      app.toast(e instanceof ApiError ? e.message : '没能加入，再试一次', {});
    }
  };
}

// The sidebar's rows something held can be let go on, in canvas
// coordinates (a device of mine, a person, a relay onto a LAN's row).
export function sideRowAt(p                          , kind = 'lan')                                         {
  if (!app) return null;
  for (const el of app.win.querySelectorAll(`.side .nav[data-id^="${kind}:"]`)                           ) {
    const r = app.rectOf(el);
    if (p.x >= r.x - 2 && p.x <= r.x + r.w + 2 && p.y >= r.y - 2 && p.y <= r.y + r.h + 2) return { id: (el.dataset.id          ).slice(kind.length + 1), el };
  }
  return null;
}
export const sideRow = (id        , kind = 'lan')                     => (app ? app.win.querySelector(`.side .nav[data-id="${kind}:${CSS.escape(id)}"]`) : null);

// When the view changes, the sidebar follows (a LAN joined, a name changed).
// Something newly shared with me lights its row up a moment, as a LAN that
// comes in does on the desktop (one I let go of and took back does not).
let seenShares                     = null;
let seenInvites                     = null;
// checkInvites shows the card of an invite by name not seen yet this visit,
// unless a card is open (then its row in the sidebar has it).
function checkInvites(v          )       {
  if (!app || !current || !seenInvites) return;
  const invs = (v.invitations || []).filter((i) => i.state === 'pending').map((i) => i.invite);
  const fresh = invs.find((id) => !seenInvites .has(id));
  for (const id of invs) seenInvites.add(id);
  if (fresh && app.sheetEl.style.display === 'none') showInvitation(fresh, current.ctx);
}
export function follow(v          )       {
  if (!app || !current) return;
  people(v);
  side(v, current.sel, current.ctx);
  checkInvites(v);
  const ids = (v.sharedWithMe || []).map((s) => s.share);
  const fresh = seenShares ? ids.filter((id) => !seenShares .has(id)) : [];
  for (const id of ids) seenShares?.add(id);
  if (fresh.length) {
    const r = app.win.querySelector('.nav[data-id="shared"]');
    // (It ends on the row's own background, white when it is chosen.)
    const tint = 'color-mix(in srgb, var(--primary) 14%, transparent)';
    r?.animate([{ background: tint }, { background: tint, offset: 0.3 }], { duration: 1200, easing: 'ease-out' });
  }
}
