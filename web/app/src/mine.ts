// 我的设备 on the web (DESIGN.md §14.11): every device of mine, in a LAN or
// not, in rows in the middle, each saying whether it is online (as a LAN's
// canvas does on the web). Dragged onto a LAN's row in the sidebar, it goes
// into that LAN (§6.4); its card says which of my LANs it is in: a click
// on one goes there; on one it is not in (dashed, "+"), adds it, with undo.
// Its "分享给" is the LAN card's (sharecard.ts): whom it goes to, what it
// opens to them, "+", the × that takes it back. With no device yet, "this
// computer" dashed opens the download card and fills in when this computer
// logs in.
import { frame, clear, follow, deletingDevs, App, G, esc, spaced, glyphOf, nameOf, ago, rows, sideRowAt } from './shell.ts';
import { can, shareSection, wireShare, forget } from './sharecard.ts';
import { D } from './clock.ts';
import { ApiError } from './api.ts';
import { downloadCard } from './download.ts';
import { phoneMine } from './phone.ts';
import { listedLANs } from './model.ts';
import type { Ctx } from './main.ts';
import type { Route } from './router.ts';
import type { UserView, DeviceView, LANView } from './model.ts';

type Dev = { type: string; owner: string; name: string; x: number; y: number; off?: string; pending?: boolean; out?: boolean };
const SELF = 'this-computer';
const DASH = `<svg class="dash" viewBox="0 0 60 60" width="60" height="60" aria-hidden="true"><rect x="0.75" y="0.75" width="58.5" height="58.5" rx="17.25" fill="none" stroke="currentColor" stroke-width="1.5" stroke-dasharray="4.5 3.5"/></svg>`;

export function minePage(root: HTMLElement, ctx: Ctx, _route: Route): () => void {
  // Narrower than 768: the phone's list (§14.6).
  if (ctx.phone()) return phoneMine(root, ctx);
  const app = frame(root, ctx, 'mine');
  clear();
  let v = ctx.view();
  const P = App.PEOPLE;
  const cv: HTMLElement = app.canvas;
  const DEVS: Record<string, Dev> = {};
  const mineList = (): DeviceView[] => v.devices.filter((d) => d.owner === v.me && !d.publicLAN && !deletingDevs.has(d.id));
  // Logged out (M7, DESIGN §6.15): grey, "已登出", and their card deletes them.
  const outList = (): DeviceView[] => (v.loggedOutDevices || []).filter((d) => d.owner === v.me && !d.publicLAN && !deletingDevs.has(d.id));
  const devOf = (id: string) => v.devices.find((d) => d.id === id) || (v.loggedOutDevices || []).find((d) => d.id === id);
  // The LANs a device of mine can go in: mine, or ones where members bring
  // their own (not a studio that shares out its own devices).
  const lansFor = (): LANView[] => listedLANs(v).filter((l) => l.mode !== 'share' || l.owner === v.me);
  const subOf = (id: string) => (DEVS[id].pending ? '装上 Deneb 就会出现在这里' : DEVS[id].off || '在线');
  const centerX = () => Math.max(300, Math.min(Math.max(cv.clientWidth, 640), 1060) / 2 - 170);
  const midY = () => Math.max(230, (cv.clientHeight - 104) / 2);

  function read(): string[] {
    const ids: string[] = [];
    for (const d of mineList()) {
      ids.push(d.id);
      DEVS[d.id] = Object.assign(DEVS[d.id] || { x: 0, y: 0 }, { type: glyphOf(d.kind), owner: v.me, name: d.displayName || d.name, off: d.online ? undefined : ago(d.lastSeen) });
    }
    for (const d of outList()) {
      ids.push(d.id);
      DEVS[d.id] = Object.assign(DEVS[d.id] || { x: 0, y: 0 }, { type: glyphOf(d.kind), owner: v.me, name: d.displayName || d.name, off: '已登出', out: true });
    }
    return ids;
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
  {
    const ids = read();
    if (!ids.length) {
      DEVS[SELF] = { type: 'laptop', owner: v.me, name: '这台电脑', x: 0, y: 0, pending: true };
      ids.push(SELF);
    }
    // The logged out ones in a row of their own, under the others (M7).
    const live = ids.filter((id) => !DEVS[id].out), out = ids.filter((id) => DEVS[id].out);
    const gap = out.length && live.length ? 88 : 0;
    const at = [...rows(live.length, centerX(), midY() - gap), ...rows(out.length, centerX(), midY() + gap)];
    [...live, ...out].forEach((id, i) => Object.assign(DEVS[id], at[i]));
    for (const id of ids) mount(id);
  }

  function summary(): void {
    const ds = mineList();
    const on = ds.filter((d) => d.online).length;
    app.setStack([]);
    if (!ds.length) app.setTitle('我的设备', '装好 Deneb 的电脑会出现在这里', 'none');
    else if (on === ds.length) app.setTitle('我的设备', ds.length === 1 ? '1 台设备，在线' : `${ds.length} 台设备都在线`, 'ok');
    else app.setTitle('我的设备', `${ds.length} 台设备，${on} 台在线`, 'off');
  }
  summary();

  // ---- The card ---------------------------------------------------------------------
  const dots = (l: LANView) => `<span class="dots">${l.members.filter((m) => m.state === 'joined').slice(0, 3).map((m) => App.dot(P[m.person] ? P[m.person].c : 'var(--offline-grey)')).join('')}</span>`;
  function openCard(id: string): void {
    const d = DEVS[id];
    if (!d) return;
    if (d.pending) return openDownload();
    const dv = devOf(id);
    if (!dv) return;
    if (app.selected !== id) forget();
    app.selected = id;
    app.select(id);
    const st = d.out ? { c: '#a1a1aa', t: '已登出', s: '在它上面登录，就会回到这里' } : d.off ? { c: '#a1a1aa', t: d.off, s: '它开机、连上网就会回来' } : { c: '#22C55E', t: '在线', s: '别人能用你开放的服务' };
    const lans = d.out ? [] : lansFor();
    const chips = lans
      .map((l) =>
        dv.lans.includes(l.id)
          ? `<button class="chip lan-in" data-l="${esc(l.id)}" title="去${esc(l.name)}">${dots(l)}${esc(spaced(l.name))}</button>`
          : `<button class="chip lan-add" data-l="${esc(l.id)}" aria-label="加进${esc(l.name)}">${G.ui('plus', 14)}${esc(spaced(l.name))}</button>`,
      )
      .join('');
    const addrs = (dv.addrs || []).map((a) => a.replace(/\/\d+$/, '')).join('、');
    const name = can(v, 'rename') ? `<button class="name-edit" type="button" title="改名">${esc(spaced(d.name))}</button>` : `<b>${esc(spaced(d.name))}</b>`;
    const sh = app.sheet(
      `<div class="top"><div class="tile-s"><div class="tile" style="--c:${P[v.me].c}">${G.device(d.type, 20)}</div></div>` +
        `<div>${name}<span>我的设备</span></div>` +
        `<button class="icon-btn sheet-close" title="关闭" aria-label="关闭">${G.ui('close', 16)}</button></div>` +
        `<div class="status"><span class="badge" style="--c:${st.c}">${G.ui('online', 16)}</span><div>${esc(st.t)}<small>${esc(st.s)}</small></div></div>` +
        (lans.length ? `<div class="label">局域网</div><div class="people lan-chips">${chips}</div>` : '') +
        (d.out ? '' : shareSection(v, dv)) +
        `<div class="card-foot"><button class="more">更多${G.ui('chevron', 14)}</button></div><div class="detail"><p>${esc(addrs ? '地址 ' + addrs : '')}</p>` +
        // Deleting it, and how that differs from logging it out (M7).
        `<div class="del-row"><button class="del-btn" type="button">${G.ui('trash', 14)}删除这台设备</button>` +
        `<p>彻底删掉：${d.out ? '' : '马上登出，'}再登录是新设备、新地址。<br>${d.out ? '留着的话，在它上面再登录，地址不变。' : '只是暂时不用，在它上面登出就行，地址不变。'}</p></div></div>`,
      id,
    );
    for (const b of sh.querySelectorAll('.lan-in')) (b as HTMLElement).onclick = () => ctx.go('/lan/' + encodeURIComponent((b as HTMLElement).dataset.l as string));
    for (const b of sh.querySelectorAll('.lan-add')) (b as HTMLElement).onclick = () => addTo(id, (b as HTMLElement).dataset.l as string);
    (sh.querySelector('.del-btn') as HTMLElement).onclick = () => deleteDevice(id);
    if (!d.out) wireShare(app, ctx, () => v, sh, () => openCard(id), { parent: cv, x: centerX() });
    const nb = sh.querySelector('.name-edit') as HTMLElement | null;
    if (nb) nb.onclick = () => rename(nb, d.name, (to) => ctx.op('device.rename', { device: id, name: to }), () => openCard(id));
  }
  // rename turns a name into a field in place (§6.9): Enter or a click away
  // keeps it, Esc goes back.
  function rename(el: HTMLElement, name: string, save: (name: string) => Promise<unknown>, redraw: () => void): void {
    const input = App.h(`<input class="name-input" type="text" maxlength="40" aria-label="名字">`) as HTMLInputElement;
    input.value = name;
    el.replaceWith(input);
    input.focus();
    input.select();
    let done = false;
    const end = (keep: boolean) => {
      if (done) return;
      done = true;
      const to = input.value.trim();
      if (keep && to && to !== name) save(to).catch((e) => failed(e, '没能改名，再试一次'));
      redraw();
    };
    input.addEventListener('keydown', (e) => {
      if (e.key === 'Enter') end(true);
      else if (e.key === 'Escape') {
        e.stopPropagation();
        end(false);
      }
    });
    input.addEventListener('blur', () => end(true));
  }
  app.onDeviceClick = (id: string) => openCard(id);
  app.onSheetClose = () => (app.selected = null);
  const again = (id: string) => app.selected === id && app.sheetEl.style.display !== 'none' && openCard(id);
  const failed = (e: unknown, text: string) => app.toast(e instanceof ApiError ? e.message : text, { parent: cv, x: centerX() });

  // Into a LAN (lan.addDevice), from the card or a drag onto its row; undo
  // takes it out again. A LAN that wants its owner to let new devices in:
  // the toast says it waits (§6.7).
  async function addTo(id: string, lan: string): Promise<void> {
    const l = v.lans.find((x) => x.id === lan);
    const d = DEVS[id];
    if (!l || !d) return;
    try {
      await ctx.op('lan.addDevice', { lan, device: id });
    } catch (e) {
      return void failed(e, '没能加进去，再试一次');
    }
    const waits = l.approveDevices && l.owner !== v.me;
    app.toast(spaced(waits ? `${d.name}等${nameOf(v, l.owner)}同意` : `已把${d.name}加进${l.name}`), {
      dev: { type: d.type, owner: v.me },
      x: centerX(),
      undo: () => ctx.op('lan.removeDevice', { lan, device: id }).catch((e) => failed(e, '没能撤销，再试一次')),
    });
  }

  // ---- Deleting a device (M7, DESIGN §6.15) -------------------------------------------------
  // As 不要了: gone from here at once, with undo; the server hears of it
  // (device.delete) when the undo is gone.
  function vanish(id: string): void {
    const n = app.dropNode(id);
    if (!n) return;
    n.el.animate([{ opacity: 1, transform: n.el.style.transform || 'none' }, { opacity: 0, transform: 'scale(0.6)' }], { duration: 260, easing: 'cubic-bezier(0.4, 0, 1, 1)', fill: 'forwards' });
    D.after(270, () => n.el.remove());
  }
  function pop(n: { el: HTMLElement }): void {
    n.el.animate([{ opacity: 0, transform: 'scale(0.6)' }, { opacity: 1, transform: 'scale(1.06)', offset: 0.6 }, { opacity: 1, transform: 'none' }], { duration: 420, easing: 'cubic-bezier(0.3, 1.2, 0.5, 1)' });
  }
  function deleteDevice(id: string): void {
    const d = DEVS[id];
    if (!d) return;
    app.closeSheet();
    deletingDevs.add(id);
    vanish(id);
    summary();
    follow(v);
    app.toast(spaced(`已删除${d.name}`), {
      dev: { type: d.type, owner: v.me },
      x: centerX(),
      undo: () => {
        deletingDevs.delete(id);
        if (!app.nodes[id]) pop(mount(id, { keep: false }));
        summary();
        follow(v);
      },
      gone: () => {
        if (!deletingDevs.has(id)) return;
        ctx.op('device.delete', { device: id })
          .then(() => deletingDevs.delete(id))
          .catch((e) => {
            deletingDevs.delete(id);
            if (!app.nodes[id]) pop(mount(id, { keep: false }));
            summary();
            follow(v);
            failed(e, '没能删掉，再试一次');
          });
      },
    });
  }

  // ---- Onto a LAN's row in the sidebar (§6.4) ------------------------------------------------
  // A device in the hand may go left, over the sidebar; on a LAN's row the
  // row lights up and the device's second line says, in blue, what letting
  // go does; on one it is in already, it says so, and goes back home.
  app.clampTile = function (n: { id: string }, p: { x: number; y: number }) {
    const q = App.prototype.clampTile.call(this, n, p);
    // Over the sidebar it goes where the hand goes, up to its top rows.
    if (p.x < q.x) {
      q.x = Math.max(-200, p.x);
      q.y = Math.max(-60, Math.min(q.y, p.y));
    }
    return q;
  };
  type Aim = { kind: string; el?: HTMLElement; lan?: string; has?: boolean; shrink?: number };
  app.onNodeTarget = (n: { kind: string; id: string }, p?: { x: number; y: number }): Aim | null => {
    if (n.kind !== 'device' || n.id === SELF || !p || p.x >= 0) return null;
    const r = sideRowAt(p);
    if (!r) return { kind: 'side', shrink: 0.62 };
    const l = v.lans.find((x) => x.id === r.id);
    const has = !!l && (l.devices.some((x) => x.device === n.id) || l.pending || (l.mode === 'share' && l.owner !== v.me));
    return { kind: 'lan', el: has ? undefined : r.el, lan: r.id, has, shrink: 0.62 };
  };
  // Over the sidebar the tile is small (the row shows under it), its name
  // gone, and a word beside it says what letting go does: blue when it
  // goes in, grey when it is there already (§6.4).
  const note = App.h(`<span class="rnote dnote"></span>`) as HTMLElement;
  app.dragLayer.appendChild(note);
  app.onNodeDragging = (n: { id: string; el: HTMLElement; x: number; y: number }, _p: unknown, t: Aim | null) => {
    const l = t && t.lan ? v.lans.find((x) => x.id === t.lan) : null;
    const text = l ? spaced(t!.has ? `${l.name}里已经有了` : `松手，加进${l.name}`) : '';
    if (note.textContent !== text) note.textContent = text;
    note.classList.toggle('go', !!l && !t!.has);
    note.style.opacity = text ? '1' : '0';
    note.style.left = n.x + 24 + 'px';
    note.style.top = n.y + 'px';
    (n.el.querySelector('.name') as HTMLElement).style.opacity = t ? '0' : '';
  };
  app.onNodeDrop = (n: { id: string; home: { x: number; y: number } }, t: Aim) => {
    if (t.kind === 'lan' && !t.has && t.lan) {
      // The row takes it with a soft blue flash, as a LAN that comes in.
      t.el?.animate([{ background: 'color-mix(in srgb, var(--primary) 16%, var(--bg))' }, { background: 'color-mix(in srgb, var(--primary) 16%, var(--bg))', offset: 0.3 }], { duration: 1100, easing: 'ease-out' });
      void addTo(n.id, t.lan);
    }
    app.settle(n, n.home, D.Smooth);
  };
  app.onNodeEnd = (n: { id: string; el: HTMLElement }) => {
    note.style.opacity = '0';
    (n.el.querySelector('.name') as HTMLElement).style.opacity = '';
  };

  // ---- The download card, for "this computer" (§14.7) ---------------------------------------
  function openDownload(): void {
    app.selected = SELF;
    app.select(SELF);
    const s = app.sheet('<div class="dl-slot"></div>', 'download');
    s.querySelector('.dl-slot').replaceWith(downloadCard({ lanName: '', what: '你局域网里的设备', email: ctx.email, onClose: () => app.closeSheet() }));
  }
  if (DEVS[SELF] && D.recording) D.after(700, openDownload);

  // ---- The server's word ----------------------------------------------------------------
  const off = ctx.onView((nv: UserView) => {
    follow(nv);
    v = nv;
    const ids = read();
    for (const id of ids) {
      if (app.nodes[id]) {
        app.setSub(id, subOf(id));
        app.nodes[id].el.classList.toggle('off', !!DEVS[id].off);
        continue;
      }
      // This computer logged in: it takes the dashed tile's place, with a
      // bounce; another device comes in from the edge and lands.
      const self = app.nodes[SELF];
      if (self) {
        Object.assign(DEVS[id], { x: self.x, y: self.y });
        const n = mount(id, { keep: false });
        n.el.querySelector('.tile').animate([{ transform: 'none' }, { transform: 'scale(1.08)', offset: 0.4 }, { transform: 'none' }], { duration: 560, easing: 'cubic-bezier(0.3, 1.2, 0.5, 1)' });
        app.dropNode(SELF);
        self.el.animate([{ opacity: 1 }, { opacity: 0 }], { duration: 160, easing: 'ease-out', fill: 'forwards' });
        D.after(170, () => self.el.remove());
        delete DEVS[SELF];
        if (app.sheetKey === 'download') app.closeSheet();
        continue;
      }
      Object.assign(DEVS[id], { x: cv.clientWidth - 120, y: 120 });
      const n = mount(id, { keep: false });
      app.settle(n, app.freeSpot(id, rows(ids.length, centerX(), midY())[ids.indexOf(id)]), D.Bouncy);
    }
    for (const id of Object.keys(DEVS)) {
      if (id === SELF || ids.includes(id) || !app.nodes[id]) continue;
      const n = app.dropNode(id);
      delete DEVS[id];
      if (n) {
        n.el.animate([{ opacity: 1, transform: 'none' }, { opacity: 0, transform: 'scale(0.9)' }], { duration: 220, easing: 'ease-in', fill: 'forwards' });
        D.after(230, () => n.el.remove());
      }
    }
    summary();
    // An open card follows (a LAN joined, a share taken back).
    if (app.selected && app.selected !== SELF) again(app.selected);
  });
  // M7's recordings (rec-m7.ts) start on this page too.
  const which = new URLSearchParams(location.search).get('record') || '';
  if (D.recording) (which.startsWith('m7') ? import('./rec-m7.ts') : import('./rec-mine.ts')).then(() => ((window as unknown as Record<string, unknown>).__ready = true));
  return () => {
    off();
    note.remove();
    clear();
  };
}
