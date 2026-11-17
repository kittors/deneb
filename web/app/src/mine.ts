// 我的设备 on the web (DESIGN.md §14.11): every device of mine, in a LAN or
// not, in rows in the middle, each saying whether it is online (as a LAN's
// canvas does on the web). Dragged onto a LAN's row in the sidebar, it goes
// into that LAN (§6.4); its card says which of my LANs it is in: a click
// on one goes there; on one it is not in (dashed, "+"), adds it, with undo.
// Its "分享给" is the LAN card's (sharecard.ts): whom it goes to, what it
// opens to them, "+", the × that takes it back. With no device yet, "this
// computer" dashed opens the download card and fills in when this computer
// logs in.
import { frame, clear, follow, deletingDevs, App, G, esc, spaced, glyphOf, nameOf, ago, rows, sideRowAt, serviceName, serviceIcon } from './shell.ts';
import { can, shareSection, wireShare, forget } from './sharecard.ts';
import { D } from './clock.ts';
import { ApiError } from './api.ts';
import { downloadCard, detectPlatform, DOWNLOADS, downloadURL, maskEmail } from './download.ts';
import { serviceKey } from './share.ts';
import { handService, portTaken } from './handservice.ts';
import { phoneMine } from './phone.ts';
import { listedLANs } from './model.ts';
import type { Ctx } from './main.ts';
import type { Route } from './router.ts';
import type { UserView, DeviceView, LANView, Service } from './model.ts';

type Dev = { type: string; owner: string; name: string; x: number; y: number; off?: string; pending?: boolean; out?: boolean };
const SELF = 'this-computer';
// "添加设备": the dashed tile after my devices (M7, design/plan/m7/mydevices.md §7).
const ADD = 'add-one';
const SITE = 'deneb.app';
const LINUX = 'sudo apt install ./deneb.deb ./denebd.deb';
// In the card's width the command shows on two lines, the first ending in
// "\": one command still, as a shell reads it (and as 复制 copies it).
const LINUX_SHOWN = 'sudo apt install \\\n  ./deneb.deb ./denebd.deb';
const CHECK = `<svg viewBox="0 0 24 24" width="12" height="12" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m5.5 12.5 4 4 9-9"/></svg>`;
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
  const subOf = (id: string) => (id === ADD ? '我的设备' : DEVS[id].pending ? '装上 Deneb 就会出现在这里' : DEVS[id].off || '在线');
  const centerX = () => Math.max(300, Math.min(Math.max(cv.clientWidth, 640), 1060) / 2 - 170);
  // Where a row of n tiles centers: at centerX, unless its first tile would
  // go under the sidebar ("添加设备" makes a row of five).
  const rowX = (n: number) => Math.max(centerX(), 64 + ((Math.min(n, n > 5 ? Math.ceil(n / Math.ceil(n / 5)) : n) - 1) / 2) * 172);
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
    if (id === ADD) {
      n.el.classList.add('add-one');
      n.el.querySelector('.tile').innerHTML = DASH + G.ui('plus', 26);
    } else if (d.pending) n.el.querySelector('.tile').insertAdjacentHTML('afterbegin', DASH);
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
    if (!DEVS[SELF]) {
      // After the live ones, "添加设备" (M7).
      DEVS[ADD] = { type: 'laptop', owner: v.me, name: '添加设备', x: 0, y: 0, pending: true };
      live.push(ADD);
      ids.push(ADD);
    }
    const gap = out.length && live.length ? 88 : 0;
    const at = [...rows(live.length, rowX(live.length), midY() - gap), ...rows(out.length, rowX(live.length), midY() + gap)];
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
  function openCard(id: string): void {
    const d = DEVS[id];
    if (!d) return;
    if (id === ADD) return openAdd();
    if (d.pending) return openDownload();
    const dv = devOf(id);
    if (!dv) return;
    if (app.selected !== id) forget();
    app.selected = id;
    app.select(id);
    const st = d.out ? { c: '#a1a1aa', t: '已登出', s: '在它上面登录，就会回到这里' } : d.off ? { c: '#a1a1aa', t: d.off, s: '它开机、连上网就会回来' } : { c: '#22C55E', t: '在线', s: '别人能用你开放的服务' };
    const lans = d.out ? [] : lansFor();
    // The LANs it is in: toggle chips, as a service's below (design/
    // components.md 多选胶囊).
    const stateIn = (l: LANView) => {
      const at = l.devices.find((x) => x.device === id);
      return !at ? 'off' : at.pending ? 'wait' : 'on';
    };
    const chips = lans
      .map((l) => {
        const st = stateIn(l);
        const tip = st === 'on' ? `移出${l.name}` : st === 'wait' ? `等待${nameOf(v, l.owner)}同意，点按撤回` : `加入${l.name}`;
        const label = st === 'on' ? `把${d.name}移出${l.name}` : st === 'wait' ? `撤回${d.name}加入${l.name}的请求` : `把${d.name}加入${l.name}`;
        return chipHTML(l.id, l.name, st, tip, label);
      })
      .join('');
    const addrs = (dv.addrs || []).map((a) => a.replace(/\/\d+$/, '')).join('、');
    const name = can(v, 'rename') ? `<button class="name-edit" type="button" title="改名">${esc(spaced(d.name))}</button>` : `<b>${esc(spaced(d.name))}</b>`;
    const sh = app.sheet(
      `<div class="top"><div class="tile-s"><div class="tile" style="--c:${P[v.me].c}">${G.device(d.type, 20)}</div></div>` +
        `<div>${name}<span>我的设备</span></div>` +
        `<button class="icon-btn sheet-close" title="关闭" aria-label="关闭">${G.ui('close', 16)}</button></div>` +
        `<div class="status"><span class="badge" style="--c:${st.c}">${G.ui('online', 16)}</span><div>${esc(st.t)}<small>${esc(st.s)}</small></div></div>` +
        (lans.length ? `<div class="label">局域网</div><div class="people lan-chips">${chips}</div>` : '') +
        (d.out ? '' : servicesHTML(dv)) +
        (d.out ? '' : shareSection(v, dv)) +
        `<div class="card-foot"><button class="more">更多${G.ui('chevron', 14)}</button></div><div class="detail"><p>${esc(addrs ? '地址 ' + addrs : '')}</p>` +
        // Deleting it, and how that differs from logging it out (M7).
        `<div class="del-row"><button class="del-btn" type="button">${G.ui('trash', 14)}删除这台设备</button>` +
        `<p>彻底删掉：${d.out ? '' : '马上登出，'}再登录是新设备、新地址。<br>${d.out ? '留着的话，在它上面再登录，地址不变。' : '只是暂时不用，在它上面登出就行，地址不变。'}</p></div></div>`,
      id,
    );
    for (const b of sh.querySelectorAll('.lan-chips .schip') as NodeListOf<HTMLElement>) {
      const l = lans.find((x) => x.id === b.dataset.l);
      if (!l) continue;
      b.onclick = () => {
        const st = stateIn(l);
        if (st === 'off') return void addTo(id, l.id);
        if (st === 'wait') return void removeFrom(id, l.id, true);
        arm(b, '移出', () => void removeFrom(id, l.id));
      };
    }
    (sh.querySelector('.del-btn') as HTMLElement).onclick = () => deleteDevice(id);
    if (!d.out) wireServices(sh, dv);
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
  app.onSheetClose = () => {
    app.selected = null;
    addOpen = false;
  };
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
      return void failed(e, '未能加入，请重试');
    }
    const waits = l.approveDevices && l.owner !== v.me;
    app.toast(spaced(waits ? `${d.name}正在等待${nameOf(v, l.owner)}同意` : `已将${d.name}加入${l.name}`), {
      dev: { type: d.type, owner: v.me },
      x: centerX(),
      undo: () => ctx.op('lan.removeDevice', { lan, device: id }).catch((e) => failed(e, '未能撤销，请重试')),
    });
  }
  // removeFrom takes it out of the LAN (asked in place first), or takes its
  // asking back; undo puts it in again.
  async function removeFrom(id: string, lan: string, asked = false): Promise<void> {
    const l = v.lans.find((x) => x.id === lan);
    const d = DEVS[id];
    if (!l || !d) return;
    try {
      await ctx.op('lan.removeDevice', { lan, device: id });
    } catch (e) {
      return void failed(e, '未能移出，请重试');
    }
    app.toast(spaced(asked ? `已撤回${d.name}加入${l.name}的请求` : `已将${d.name}移出${l.name}`), {
      dev: { type: d.type, owner: v.me },
      x: centerX(),
      undo: () => ctx.op('lan.addDevice', { lan, device: id }).catch((e) => failed(e, '未能撤销，请重试')),
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
    if (n.kind !== 'device' || n.id === SELF || n.id === ADD || !p || p.x >= 0) return null;
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

  // ---- Services (M7, design/plan/m7/mydevices.md §6) -------------------------------------
  // A device's services: what it has set up and what a LAN lists of it; per
  // LAN it is in, a chip: blue and ticked where it is open there. A click
  // opens it; closing asks first. "+ 加一个服务" sets one up by hand.
  type SvcRow = { svc: Service; key: string; on: Record<string, boolean> };
  const inLANs = (dv: DeviceView): LANView[] => lansFor().filter((l) => l.devices.some((x) => x.device === dv.id && !x.pending));
  function servicesOf(dv: DeviceView, ins: LANView[]): SvcRow[] {
    const rows: SvcRow[] = [];
    const add = (s: Service) => {
      const key = serviceKey(s);
      if (rows.some((r) => r.key === key)) return;
      const on: Record<string, boolean> = {};
      for (const l of ins) on[l.id] = !!l.devices.find((x) => x.device === dv.id)?.services.some((x) => serviceKey(x) === key && x.on);
      rows.push({ svc: { kind: s.kind, name: s.name, ports: s.ports }, key, on });
    };
    for (const s of dv.services || []) add(s);
    for (const l of ins) for (const s of l.devices.find((x) => x.device === dv.id)?.services || []) add(s);
    return rows;
  }
  const portOf = (s: Service) => (s.ports[0] || '').split('/')[1] || '';
  const svcLabel = (rows: SvcRow[], r: SvcRow) => {
    const n = serviceName(r.svc);
    return rows.some((o) => o.key !== r.key && serviceName(o.svc) === n) ? `${n} · ${portOf(r.svc)}` : n;
  };
  // The device whose "添加服务" is open, what is typed in it (kept as the
  // card redraws for the server's word), and lines that go after a while.
  let editing: string | null = null;
  let draft = { name: '', port: '', err: '', busy: false };
  let shellTold = false;
  const hints: Record<string, { text: string; until: number }> = {};
  // What shows for the first time fades in, and what goes fades out
  // (DESIGN §5). The card is drawn anew for every change, so a part on its
  // way in or out is marked, and drawn again mid-way where it was.
  const moving = new Map<string, { t: number; out: boolean }>();
  const MOVE = 220;
  const move = (k: string, out = false) => moving.set(k, { t: D.now(), out });
  const motion = (k: string, cls: string): string => {
    const m = moving.get(k);
    const e = m ? D.now() - m.t : MOVE;
    if (!m || e >= MOVE) {
      if (m && !m.out) moving.delete(k);
      return `class="${cls}${m?.out ? ' svc-gone' : ''}"`;
    }
    return `class="${cls} ${m.out ? 'svc-leave' : 'svc-enter'}" style="--d:-${Math.round(e)}ms"`;
  };
  // leave fades the part k out, then forgets what it showed.
  const leave = (k: string, dev: string, forget: () => void) => {
    move(k, true);
    again(dev);
    D.after(MOVE + 10, () => {
      moving.delete(k);
      forget();
      again(dev);
    });
  };
  function hint(dev: string, text: string): void {
    const until = D.now() + 5000;
    hints[dev] = { text, until };
    move('hint:' + dev);
    again(dev);
    D.after(5000, () => hints[dev]?.until === until && leave('hint:' + dev, dev, () => delete hints[dev]));
  }
  function servicesHTML(dv: DeviceView): string {
    const ins = inLANs(dv);
    if (!ins.length) return `<div class="label">服务</div><p class="say svc-say">加入局域网后可开放服务</p>`;
    const rows = servicesOf(dv, ins);
    const h = hints[dv.id] ? `<p ${motion('hint:' + dv.id, 'svc-hint')}>${esc(hints[dv.id].text)}</p>` : '';
    const list = rows
      .map((r) => {
        const name = svcLabel(rows, r);
        const chips = ins
          .map((l) => {
            const on = !!r.on[l.id];
            return chipHTML(l.id, l.name, on ? 'on' : 'off', on ? `在${l.name}关闭` : `开放给${l.name}`, on ? `在${l.name}关闭${name}` : `把${name}开放给${l.name}`);
          })
          .join('');
        return (
          `<div ${motion('row:' + r.key, 'svc-row')} data-k="${esc(r.key)}"><span class="svc-name">${G.ui(serviceIcon(r.svc.kind), 16)}<span>${esc(spaced(name))}</span></span>` +
          `<span class="svc-chips">${chips}</span><button class="svc-x" title="删除${esc(name)}" aria-label="删除${esc(name)}">${G.ui('close', 12)}</button></div>`
        );
      })
      .join('');
    const edit =
      editing === dv.id
        ? `<div ${motion('edit', 'svc-edit')}><input class="svc-in name" type="text" maxlength="40" placeholder="名称" aria-label="名称" value="${esc(draft.name)}">` +
          `<input class="svc-in port${draft.err.includes('端口') ? ' bad' : ''}" type="text" inputmode="numeric" maxlength="5" placeholder="端口" aria-label="端口" value="${esc(draft.port)}">` +
          `<span class="svc-acts"><button class="link-btn svc-cancel">取消</button><button class="btn primary sm svc-ok"${draft.port && !draft.busy ? '' : ' disabled'}>${draft.busy ? '添加中…' : '添加'}</button></span></div>` +
          `<p ${motion('err', 'svc-err')} role="alert">${esc(draft.err)}</p>`
        : `<button ${motion('add', 'schip add')}>${G.ui('plus', 12)}添加服务</button>`;
    return `<div class="label">服务</div><div class="svc-list">${rows.length ? '' : '<p class="say svc-say">暂无服务</p>'}${list}${h}${edit}</div>`;
  }
  // chipHTML is a toggle chip (design/components.md 多选胶囊): ticked when
  // chosen, dashed when not, dashed in blue while asked and waiting. Its tip
  // is a tooltip; its label says what a click does.
  function chipHTML(k: string, name: string, st: 'on' | 'off' | 'wait', tip: string, label: string): string {
    const extra = st === 'wait' ? '<span class="schip-x">等待同意</span>' : '';
    return (
      `<button class="schip${st === 'on' ? ' on' : st === 'wait' ? ' wait' : ''}" data-l="${esc(k)}" aria-pressed="${st === 'on'}" title="${esc(tip)}" aria-label="${esc(label)}">` +
      `${st === 'on' ? CHECK : ''}<span class="schip-t">${esc(spaced(name))}</span>${extra}</button>`
    );
  }
  // arm is the inline confirm (design/components.md 原地确认): the first
  // click turns the control into "再点一下，<verb>" in red, its width
  // following; the second does it. Moving off it, or 4 s, takes it back.
  function arm(b: HTMLElement, verb: string, act: () => void): void {
    if (b.classList.contains('asking')) {
      b.classList.remove('asking');
      return act();
    }
    const was = b.innerHTML;
    const w0 = b.offsetWidth;
    b.classList.add('asking');
    b.innerHTML = `<span class="schip-t">再点一下，${esc(verb)}</span>`;
    const w1 = b.offsetWidth;
    const ease = 'cubic-bezier(0.3, 0.3, 0.2, 1)';
    if (Math.abs(w1 - w0) > 1) b.animate([{ width: w0 + 'px' }, { width: w1 + 'px' }], { duration: MOVE, easing: ease });
    b.firstElementChild?.animate([{ opacity: 0 }, { opacity: 1 }], { duration: 160, easing: 'ease-out' });
    // The question goes when the pointer is off it, or after 4 s. Not on
    // mouseleave: its words replaced under the pointer, the browser may
    // say it left while it is still on it.
    const off = (e: PointerEvent) => {
      const r = b.getBoundingClientRect();
      if (e.clientX < r.left || e.clientX > r.right || e.clientY < r.top || e.clientY > r.bottom) back();
    };
    const back = () => {
      document.removeEventListener('pointermove', off);
      if (!b.isConnected || !b.classList.contains('asking')) return;
      const v0 = b.offsetWidth;
      b.classList.remove('asking');
      b.innerHTML = was;
      const v1 = b.offsetWidth;
      if (Math.abs(v1 - v0) > 1) b.animate([{ width: v0 + 'px' }, { width: v1 + 'px' }], { duration: MOVE, easing: ease });
      for (const c of b.children) c.animate([{ opacity: 0 }, { opacity: 1 }], { duration: 160, easing: 'ease-out' });
    };
    document.addEventListener('pointermove', off);
    D.after(4000, back);
  }
  function wireServices(sh: HTMLElement, dv: DeviceView): void {
    const ins = inLANs(dv);
    const rows = servicesOf(dv, ins);
    const find = (row: HTMLElement) => rows.find((r) => r.key === row.dataset.k);
    for (const b of sh.querySelectorAll<HTMLElement>('.svc-row .schip')) {
      b.onclick = () => {
        const row = b.closest('.svc-row') as HTMLElement;
        const r = find(row);
        const l = ins.find((x) => x.id === b.dataset.l);
        if (!r || !l) return;
        if (r.on[l.id]) return arm(b, '关闭', () => void setOn(dv, r, l, false));
        void setOn(dv, r, l, true);
        if (r.svc.kind === 'shell' && !shellTold) {
          shellTold = true;
          hint(dv.id, '开放命令行后，对方可以经这台电脑访问其他地方，请只开放给信任的人。');
        }
      };
    }
    for (const x of sh.querySelectorAll<HTMLElement>('.svc-x')) {
      x.onclick = () => {
        const row = x.closest('.svc-row') as HTMLElement;
        const r = find(row);
        if (!r) return;
        const name = svcLabel(rows, r);
        arm(x, '删除', () => {
          // It folds away, then the card is drawn without it.
          const h = row.offsetHeight;
          row
            .animate([{ height: h + 'px', opacity: 1 }, { height: '0px', opacity: 0, marginBottom: '-12px' }], { duration: MOVE, easing: 'cubic-bezier(0.3, 0.3, 0.2, 1)', fill: 'forwards' })
            .finished.then(
              () => void removeService(dv, r, name),
              () => void removeService(dv, r, name),
            );
        });
      };
    }
    const add = sh.querySelector<HTMLElement>('.schip.add');
    if (add)
      add.onclick = () => {
        // The chip fades as the fields come in its place.
        add.onclick = null;
        void add.animate([{ opacity: 1 }, { opacity: 0 }], { duration: 90, easing: 'ease-out', fill: 'forwards' }).finished.then(() => {
          editing = dv.id;
          draft = { name: '', port: '', err: '', busy: false };
          move('edit');
          openCard(dv.id);
          (app.sheetEl.querySelector('.svc-in.name') as HTMLInputElement | null)?.focus();
        });
      };
    const ed = sh.querySelector<HTMLElement>('.svc-edit');
    if (!ed) return;
    const nameIn = ed.querySelector('.svc-in.name') as HTMLInputElement;
    const portIn = ed.querySelector('.svc-in.port') as HTMLInputElement;
    const ok = ed.querySelector('.svc-ok') as HTMLButtonElement;
    nameIn.oninput = () => (draft.name = nameIn.value);
    portIn.oninput = () => {
      portIn.value = portIn.value.replace(/\D/g, '').slice(0, 5);
      draft.port = portIn.value;
      ok.disabled = !draft.port || draft.busy;
    };
    const cancel = () => closeEditor(dv.id);
    for (const i of [nameIn, portIn])
      i.onkeydown = (e: KeyboardEvent) => {
        if (e.key === 'Enter') void submit(dv, ins);
        else if (e.key === 'Escape') {
          e.stopPropagation();
          cancel();
        }
      };
    ok.onclick = () => void submit(dv, ins);
    (ed.querySelector('.svc-cancel') as HTMLElement).onclick = cancel;
  }
  async function setOn(dv: DeviceView, r: SvcRow, l: LANView, on: boolean): Promise<void> {
    try {
      await ctx.op('service.set', { lan: l.id, device: dv.id, service: r.svc, on });
    } catch (e) {
      failed(e, '操作未完成，请重试');
      again(dv.id);
    }
  }
  async function removeService(dv: DeviceView, r: SvcRow, name: string): Promise<void> {
    try {
      await ctx.op('service.remove', { device: dv.id, service: r.svc });
    } catch (e) {
      failed(e, '未能删除，请重试');
      return void again(dv.id);
    }
    app.toast(`已删除「${name}」`, { x: centerX() });
  }
  // closeEditor folds the fields away (drawn again mid-way where they
  // were, as the card redraws), then "+ 添加服务" comes back in their place.
  function closeEditor(dev: string): void {
    leave('edit', dev, () => {
      editing = null;
      draft = { name: '', port: '', err: '', busy: false };
      move('add');
    });
  }
  // submit sets the service up: in the device's first LAN, closed, so that
  // it is open to no one until a chip opens it.
  async function submit(dv: DeviceView, ins: LANView[]): Promise<void> {
    if (draft.busy || !ins.length) return;
    const port = Number(draft.port);
    const bad = (err: string) => {
      if (err !== draft.err) move('err');
      draft.err = err;
      openCard(dv.id);
      (app.sheetEl.querySelector('.svc-in.port') as HTMLInputElement | null)?.focus();
    };
    if (!Number.isInteger(port) || port < 1 || port > 65535) return bad('端口应为 1–65535 之间的数字');
    const taken = portTaken(dv.services || [], port);
    if (taken) return bad(`端口 ${port} 已被「${serviceName(taken)}」占用`);
    const svc = handService(draft.name, port);
    draft = { ...draft, err: '', busy: true };
    openCard(dv.id);
    try {
      await ctx.op('service.set', { lan: ins[0].id, device: dv.id, service: svc, on: false });
    } catch (e) {
      draft.busy = false;
      return bad(e instanceof ApiError ? e.message : '未能添加：无法连接服务器，请重试');
    }
    move('row:' + serviceKey(svc));
    closeEditor(dv.id);
    app.toast(`已添加「${svc.name}」`, {
      x: centerX(),
      undo: () => ctx.op('service.remove', { device: dv.id, service: svc }).catch((e) => failed(e, '未能撤销，请重试')),
    });
  }

  // ---- "添加设备" (M7, design/plan/m7/mydevices.md §7) ---------------------------------
  // Three steps; it waits for that computer, and says so when it comes.
  let addOpen = false;
  function openAdd(): void {
    app.selected = ADD;
    app.select(ADD);
    addOpen = true;
    const plat = detectPlatform();
    const here = plat && plat !== 'linux' ? `<p class="add-here">这台电脑：<a class="link-btn accent" href="${downloadURL(plat)}" download>下载 ${DOWNLOADS[plat].label}</a></p>` : '';
    const s = app.sheet(
      `<div class="top"><div class="tile-s"><div class="tile add-tile">${G.ui('plus', 18)}</div></div><div><b>添加设备</b><span>我的设备</span></div>` +
        `<button class="icon-btn sheet-close" title="关闭" aria-label="关闭">${G.ui('close', 16)}</button></div>` +
        `<ol class="add-steps">` +
        `<li><span class="n">1</span><div><b>在另一台电脑上打开</b><div class="add-line"><span class="add-url">${SITE}</span><button class="link-btn accent add-copy">复制网址</button></div>${here}</div></li>` +
        `<li><span class="n">2</span><div><b>下载并安装 Deneb</b><button class="add-more" aria-expanded="false">首次打开时被系统阻止${G.ui('chevron', 12)}</button>` +
        `<div class="add-help" hidden><p><b>Mac</b><span>打开「系统设置」>「隐私与安全」，在「安全性」中点按「仍要打开」，再输入登录密码。</span></p>` +
        `<p><b>Windows</b><span>出现「Windows 已保护你的电脑」时，点「更多信息」，再点「仍要运行」。</span></p>` +
        `<p><b>Linux</b><span>需同时安装 deneb.deb 和 denebd.deb（后台服务）。在下载目录中运行：</span></p>` +
        `<div class="add-cmd"><code>${esc(LINUX_SHOWN)}</code><button class="link-btn accent add-copy-cmd">复制</button></div></div></div></li>` +
        `<li class="add-3"><span class="n">3</span><div><b class="add-who">${ctx.email ? `使用 ${esc(maskEmail(ctx.email))} 登录` : '使用同一邮箱登录'}</b></div></li></ol>` +
        `<p class="add-wait"><span class="spin small"></span>等待设备上线…</p>`,
      'add-one',
    );
    const copy = (b: HTMLElement, text: string) => {
      const was = b.textContent || '';
      navigator.clipboard
        ?.writeText(text)
        .then(() => {
          b.textContent = '已复制';
          D.after(2000, () => (b.textContent = was));
        })
        .catch(() => b.replaceWith(Object.assign(document.createElement('code'), { textContent: text })));
    };
    (s.querySelector('.add-copy') as HTMLElement).onclick = (e) => copy(e.currentTarget as HTMLElement, 'https://' + SITE);
    (s.querySelector('.add-copy-cmd') as HTMLElement).onclick = (e) => copy(e.currentTarget as HTMLElement, LINUX);
    const more = s.querySelector('.add-more') as HTMLElement;
    // The help opens and closes in height, as the other wells here do:
    // step 3 moves down with it, not at once.
    const ease = 'cubic-bezier(0.3, 0.3, 0.2, 1)';
    more.onclick = () => {
      const help = s.querySelector('.add-help') as HTMLElement;
      const open = help.hidden;
      more.setAttribute('aria-expanded', String(open));
      help.getAnimations().forEach((a) => a.cancel());
      help.hidden = false;
      const h = help.offsetHeight;
      const shut = { height: '0px', paddingTop: '0px', paddingBottom: '0px', marginTop: '0px', opacity: 0 };
      const full = { height: h + 'px', paddingTop: '12px', paddingBottom: '12px', marginTop: '8px', opacity: 1 };
      help.style.overflow = 'hidden';
      const a = help.animate(open ? [shut, full] : [full, shut], { duration: 300, easing: ease });
      a.onfinish = () => {
        help.style.overflow = '';
        if (!open) help.hidden = true;
      };
    };
  }
  // arrived is a device of mine that came: the card ticks its third step
  // and goes; closed, a toast says so.
  // then: what follows the card's going (the new one landing).
  function arrived(id: string, then?: () => void): void {
    const d = DEVS[id];
    if (!d) return;
    if (addOpen && app.sheetKey === 'add-one') {
      const s = app.sheetEl;
      const step = s.querySelector('.add-3') as HTMLElement | null;
      if (step) {
        step.classList.add('done');
        (step.querySelector('.n') as HTMLElement).innerHTML = CHECK;
        const who = step.querySelector('.add-who') as HTMLElement;
        who.textContent = spaced(`${d.name}已加入`);
        who.animate([{ opacity: 0, transform: 'translateY(3px)' }, { opacity: 1, transform: 'none' }], { duration: 220, easing: 'ease-out' });
      }
      // "等它上线…" fades where it is: the card keeps its height.
      s.querySelector('.add-wait')?.animate([{ opacity: 1 }, { opacity: 0 }], { duration: 160, easing: 'ease-out', fill: 'forwards' });
      D.after(1200, () => {
        if (addOpen && app.sheetKey === 'add-one') app.closeSheet();
        if (then) D.after(200, then);
      });
    } else {
      then?.();
      app.toast(spaced(`${d.name}已加入`), { dev: { type: d.type, owner: v.me }, x: centerX() });
    }
  }

  // ---- The download card, for "this computer" (§14.7) ---------------------------------------
  function openDownload(): void {
    app.selected = SELF;
    app.select(SELF);
    const s = app.sheet('<div class="dl-slot"></div>', 'download');
    s.querySelector('.dl-slot').replaceWith(downloadCard({ lanName: '', what: '你局域网里的设备', email: ctx.email, onClose: () => app.closeSheet() }));
  }
  if (DEVS[SELF] && D.recording) D.after(700, openDownload);

  // ---- The server's word ----------------------------------------------------------------
  const landing = new Set<string>(); // come, waiting for the card to go
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
      // The card open, it ticks its third step first and goes; then the
      // new one lands, in sight, nothing flying under the card (§6.3).
      if (landing.has(id)) continue;
      landing.add(id);
      const land = () => {
        landing.delete(id);
        if (!DEVS[id] || app.nodes[id]) return;
        Object.assign(DEVS[id], { x: cv.clientWidth - 120, y: 120 });
        const n = mount(id, { keep: false });
        const add = app.nodes[ADD];
        if (add) {
          // It lands where "添加设备" was; "添加设备" moves on to the next
          // place, one clear of where it lands, so that the two never
          // overlap.
          const at = { x: add.x, y: add.y };
          const live = [...ids.filter((x) => !DEVS[x]?.out), ADD];
          app.settle(add, app.freeSpot(ADD, rows(live.length, rowX(live.length), midY())[live.length - 1], { more: app.foot(id, at, 22) }), D.Smooth);
          app.settle(n, at, D.Bouncy);
        } else app.settle(n, app.freeSpot(id, rows(ids.length, centerX(), midY())[ids.indexOf(id)]), D.Bouncy);
      };
      arrived(id, land);
    }
    for (const id of Object.keys(DEVS)) {
      if (id === SELF || id === ADD || ids.includes(id) || !app.nodes[id]) continue;
      const n = app.dropNode(id);
      delete DEVS[id];
      if (n) {
        n.el.animate([{ opacity: 1, transform: 'none' }, { opacity: 0, transform: 'scale(0.9)' }], { duration: 220, easing: 'ease-in', fill: 'forwards' });
        D.after(230, () => n.el.remove());
      }
    }
    summary();
    // An open card follows (a LAN joined, a share taken back).
    if (app.selected && app.selected !== SELF && app.selected !== ADD) again(app.selected);
  });
  // M7's recordings (rec-m7.ts) start on this page too.
  const which = new URLSearchParams(location.search).get('record') || '';
  if (D.recording)
    (which.startsWith('m7') ? import('./rec-m7.ts') : which.startsWith('mydev') ? import('./rec-mydev.ts') : import('./rec-mine.ts')).then(
      () => ((window as unknown as Record<string, unknown>).__ready = true),
    );
  return () => {
    off();
    note.remove();
    clear();
  };
}
