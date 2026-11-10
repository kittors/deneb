// 共享给我 on the web (DESIGN.md §6.11 for the desktop, §14.11 here): the
// devices others share with me. The desktop draws them around this
// computer; a browser is no device, so here they stand in the middle in
// rows, by who gave them, each saying from whom and whether it is online.
// No tray, no invite. The card says what I may use and that it is one way,
// and has 不要了 (share.leave, sent once its undo is gone). Newly shared
// devices pop up in a free spot; taken back, they shrink away with a word.
// Someone with no device yet sees "this computer" dashed below them, which
// opens the download card: none of it can be used without Deneb.
import { frame, clear, follow, leaving, App, G, esc, spaced, glyphOf, nameOf, serviceName, serviceIcon, ago, rows } from './shell.ts';
import { D } from './clock.ts';
import { ApiError } from './api.ts';
import { downloadCard } from './download.ts';
import { phoneShared } from './phone.ts';
import { sharedOnly } from './model.ts';
import type { Ctx } from './main.ts';
import type { Route } from './router.ts';
import type { UserView, SharedDevice } from './model.ts';

type Dev = { type: string; owner: string; name: string; from: string; who: string; share: string; x: number; y: number; off?: string; pending?: boolean };
const SELF = 'this-computer';
const DASH = `<svg class="dash" viewBox="0 0 60 60" width="60" height="60" aria-hidden="true"><rect x="0.75" y="0.75" width="58.5" height="58.5" rx="17.25" fill="none" stroke="currentColor" stroke-width="1.5" stroke-dasharray="4.5 3.5"/></svg>`;

export function sharedPage(root: HTMLElement, ctx: Ctx, _route: Route): () => void {
  // Narrower than 768: the phone's list (§14.6).
  if (ctx.phone()) return phoneShared(root, ctx);
  const app = frame(root, ctx, 'shared');
  clear();
  let v = ctx.view();
  const P = App.PEOPLE;
  const cv: HTMLElement = app.canvas;
  const DEVS: Record<string, Dev> = {};
  // Shares let go (不要了) are in shell's leaving: off the page and out of
  // the sidebar's count at once, told to the server once the undo is gone;
  // they stay there so the server's views until then keep them off.
  const list = (): SharedDevice[] => (v.sharedWithMe || []).filter((s) => !leaving.has(s.share));
  const shareOf = (id: string) => (v.sharedWithMe || []).find((s) => s.device.id === id);
  // "工作室 · 在线" for a studio's device, "Mia · 在线" for a person's.
  const fromOf = (s: SharedDevice) => s.fromLAN || nameOf(v, s.from) || '别人';
  const whoOf = (s: SharedDevice) => nameOf(v, s.from) || s.fromLAN || '对方';
  const nameOfDev = (s: SharedDevice) => s.device.displayName || s.device.name;
  const subOf = (id: string) => (DEVS[id].pending ? '装上 Deneb 就能用' : `${DEVS[id].from} · ${DEVS[id].off || '在线'}`);
  const centerX = () => Math.max(300, Math.min(Math.max(cv.clientWidth, 640), 1060) / 2 - 170);
  const midY = () => Math.max(230, (cv.clientHeight - 104) / 2);
  const noDevice = (): boolean => !v.devices.some((d) => d.owner === v.me);

  function read(): string[] {
    const ids: string[] = [];
    const sorted = list().sort((a, b) => fromOf(a).localeCompare(fromOf(b)));
    for (const s of sorted) {
      const d = s.device;
      ids.push(d.id);
      DEVS[d.id] = Object.assign(DEVS[d.id] || { x: 0, y: 0 }, { type: glyphOf(d.kind), owner: s.from, name: nameOfDev(s), from: fromOf(s), who: whoOf(s), share: s.share, off: d.online ? undefined : ago(d.lastSeen) });
    }
    return ids;
  }
  // The places in rows, the dashed "this computer" under them.
  function spots(n: number): { x: number; y: number }[] {
    return rows(n, centerX(), midY() - (DEVS[SELF] && n ? 88 : 0));
  }
  function layout(ids: string[]): void {
    const real = ids.filter((id) => id !== SELF);
    const at = spots(real.length);
    real.forEach((id, i) => Object.assign(DEVS[id], at[i]));
    if (DEVS[SELF]) Object.assign(DEVS[SELF], { x: centerX(), y: real.length ? at[at.length - 1].y + 190 : midY() });
  }
  function mount(id: string, o: Record<string, unknown> = {}) {
    const d = DEVS[id];
    const n = app.device(id, { ...d, sub: subOf(id), cls: [d.pending ? 'pending' : '', d.off ? 'off' : ''].join(' '), ...o });
    if (d.pending) n.el.querySelector('.tile').insertAdjacentHTML('afterbegin', DASH);
    n.el.setAttribute('role', 'button');
    n.el.setAttribute('tabindex', '0');
    n.el.setAttribute('aria-label', `${d.name}，${subOf(id)}`);
    n.el.addEventListener('keydown', (e: KeyboardEvent) => e.key === 'Enter' && openCard(id));
    return n;
  }
  // In: it pops up where it stands (Bouncy). Out: it shrinks and fades.
  function pop(n: { el: HTMLElement }): void {
    const sp = new D.Spring(0.6, D.Bouncy);
    sp.to(1);
    n.el.style.transform = 'scale(0.6)';
    n.el.animate([{ opacity: 0 }, { opacity: 1 }], { duration: 180, easing: 'ease-out' });
    const stop = D.onFrame((now: number) => {
      const { x, v } = sp.sample(now);
      if (!n.el.isConnected || (Math.abs(x - 1) < 0.001 && Math.abs(v) < 0.05)) {
        stop();
        n.el.style.transform = '';
      } else n.el.style.transform = `scale(${x.toFixed(4)})`;
    });
  }
  function vanish(id: string): void {
    const n = app.dropNode(id);
    if (!n) return;
    n.el.animate([{ opacity: 1, transform: n.el.style.transform || 'none' }, { opacity: 0, transform: 'scale(0.6)' }], { duration: 260, easing: 'cubic-bezier(0.4, 0, 1, 1)', fill: 'forwards' });
    D.after(270, () => n.el.remove());
  }
  {
    const ids = read();
    if (noDevice()) {
      DEVS[SELF] = { type: 'laptop', owner: v.me, name: '这台电脑', from: '', who: '', share: '', x: 0, y: 0, pending: true };
      ids.push(SELF);
    }
    layout(ids);
    for (const id of ids) mount(id);
  }

  function summary(): void {
    const shown = list();
    const on = shown.filter((s) => s.device.online).length;
    app.setStack([...new Set(shown.map((s) => s.from))]);
    if (!shown.length) {
      // A studio's member who has nothing yet: what will come, and from whom.
      const l = v.lans.find((x) => sharedOnly(v, x) && !x.pending);
      app.setTitle('共享给我', l ? `已加入${l.name}，${nameOf(v, l.owner)}分给你的设备会出现在这里` : '别人分给你的设备会出现在这里', 'none');
    } else if (on === shown.length) app.setTitle('共享给我', shown.length === 1 ? '1 台设备，在线' : `${shown.length} 台设备都在线`, 'ok');
    else app.setTitle('共享给我', `${shown.length} 台设备，${on} 台在线`, 'off');
  }
  summary();

  // ---- The card ---------------------------------------------------------------------
  // The desktop's (§6.11): whose it is, whether it is online (not how this
  // computer reaches it: a browser does not), what I may use, one way, and
  // 不要了 where a LAN's card has "移出这个局域网".
  function openCard(id: string): void {
    const d = DEVS[id];
    if (!d) return;
    if (d.pending) return openDownload();
    const s = shareOf(id);
    if (!s) return;
    app.selected = id;
    app.select(id);
    const st = d.off ? { c: '#a1a1aa', t: d.off, s: '它开机、连上网就会回来' } : { c: '#22C55E', t: '在线', s: '在装了 Deneb 的电脑上就能用' };
    const rowsHTML = s.services.map((x) => `<div class="row">${G.ui(serviceIcon(x.kind), 18)}<span>${esc(serviceName(x))}</span></div>`);
    const addrs = (s.device.addrs || []).map((a) => a.replace(/\/\d+$/, '')).join('、');
    const sh = app.sheet(
      `<div class="top"><div class="tile-s"><div class="tile" style="--c:${P[d.owner].c}">${G.device(d.type, 20)}</div></div>` +
        `<div><b>${esc(spaced(d.name))}</b><span>${esc(spaced(s.fromLAN ? s.fromLAN + '的设备' : d.who + '分享给你的'))}</span></div>` +
        `<button class="icon-btn sheet-close" title="关闭" aria-label="关闭">${G.ui('close', 16)}</button></div>` +
        `<div class="status"><span class="badge" style="--c:${st.c}">${G.ui('online', 16)}</span><div>${esc(st.t)}<small>${esc(st.s)}</small></div></div>` +
        (rowsHTML.length ? `<div class="label">你可以用</div><div class="rows">${rowsHTML.join('')}</div>` : `<p class="say">还没开放服务给你</p>`) +
        `<div class="hint one-way">只有你能连它，反过来不行</div>` +
        `<div class="card-foot"><button class="more">更多${G.ui('chevron', 14)}</button><button class="out-btn">不要了</button></div><div class="detail"><p>${esc(addrs ? '地址 ' + addrs : '')}</p></div>`,
      id,
    );
    (sh.querySelector('.out-btn') as HTMLElement).onclick = () => leave(id);
  }
  app.onDeviceClick = (id: string) => openCard(id);
  app.onSheetClose = () => (app.selected = null);

  // 不要了: gone from here at once, with undo; the server hears of it when
  // the undo is gone (the toast timed out, another took its place, the
  // page went), and the one who shared it is not told (control §1).
  function leave(id: string): void {
    const d = DEVS[id], s = shareOf(id);
    if (!d || !s) return;
    app.closeSheet();
    leaving.add(s.share);
    vanish(id);
    summary();
    follow(v);
    app.toast(`已拿掉${d.name}`, {
      dev: { type: d.type, owner: d.owner },
      x: centerX(),
      undo: () => {
        leaving.delete(s.share);
        if (!app.nodes[id] && shareOf(id)) pop(mount(id, { keep: false }));
        summary();
        follow(v);
      },
      gone: () => {
        if (!leaving.has(s.share)) return;
        ctx.op('share.leave', { share: s.share }).catch((e) => {
          leaving.delete(s.share);
          sync(ctx.view());
          app.toast(e instanceof ApiError ? e.message : '没能拿掉，再试一次', { parent: cv, x: centerX() });
        });
      },
    });
  }

  // ---- The download card, for "this computer" (§14.7) ---------------------------------------
  function openDownload(): void {
    app.selected = SELF;
    app.select(SELF);
    const s = app.sheet('<div class="dl-slot"></div>', 'download');
    const first = list()[0];
    s.querySelector('.dl-slot').replaceWith(
      downloadCard({ lanName: '', what: '分给你的设备', after: '分给你的设备就能用了', example: first ? spaced(nameOfDev(first)) : undefined, email: ctx.email, onClose: () => app.closeSheet() }),
    );
  }
  if (DEVS[SELF] && (ctx.justJoined || D.recording)) D.after(700, openDownload);
  // This computer logged in: the dashed tile and the download card go.
  function dropSelf(): void {
    const n = app.dropNode(SELF);
    delete DEVS[SELF];
    if (app.sheetKey === 'download') {
      app.closeSheet();
      D.after(200, () => app.sheetEl.style.display === 'none' && app.sheetEl.replaceChildren());
    }
    if (n) {
      n.el.animate([{ opacity: 1 }, { opacity: 0 }], { duration: 200, easing: 'ease-out', fill: 'forwards' });
      D.after(210, () => n.el.remove());
    }
  }

  // ---- The server's word ----------------------------------------------------------------
  function sync(nv: UserView): void {
    follow(nv);
    v = nv;
    const ids = read();
    for (const id of ids) {
      if (app.nodes[id]) {
        app.setSub(id, subOf(id));
        app.nodes[id].el.classList.toggle('off', !!DEVS[id].off);
        continue;
      }
      // Newly shared: it pops up in a free spot, the next in the row (the
      // others stay where they are).
      const on = ids.filter((x) => app.nodes[x]).map((x) => app.nodes[x]);
      const last = on.reduce((a: { x: number; y: number } | null, n: { x: number; y: number }) => (!a || n.y > a.y + 40 || (Math.abs(n.y - a.y) <= 40 && n.x > a.x) ? n : a), null);
      let want = last ? { x: last.x + 172, y: last.y } : spots(1)[0];
      // Past the room the card leaves: a new row, under the first.
      if (last && want.x > cv.clientWidth - 340) want = { x: Math.min(...on.map((n: { x: number }) => n.x)), y: last.y + 176 };
      Object.assign(DEVS[id], app.freeSpot(id, want));
      pop(mount(id, { keep: false }));
    }
    for (const id of Object.keys(DEVS)) {
      if (id === SELF || ids.includes(id)) continue;
      const d = DEVS[id];
      if (leaving.has(d.share)) continue;
      // Taken back: it shrinks away, and says so (no undo: not mine).
      if (app.nodes[id]) {
        if (app.selected === id) app.closeSheet();
        vanish(id);
        app.toast(`${d.who}收回了${d.name}`, { dev: { type: d.type, owner: d.owner }, x: centerX() });
      }
      delete DEVS[id];
    }
    if (DEVS[SELF] && !noDevice()) dropSelf();
    summary();
  }
  const off = ctx.onView(sync);
  if (D.recording) import('./rec-shared.ts').then(() => ((window as unknown as Record<string, unknown>).__ready = true));
  return () => {
    off();
    clear();
  };
}
