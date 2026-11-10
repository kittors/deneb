// 我的设备 on the web (DESIGN.md §14.11): every device of mine, in a LAN or
// not, in rows in the middle, each saying whether it is online (as a LAN's
// canvas does on the web). Its card says which of my LANs it is in: a
// click on one goes there; on one it is not in (dashed, "+"), adds it,
// with undo (DESIGN.md §6.4's click instead of a drag). It lists whom I
// have shared it with, each taken back with the ×, with undo (the seat is
// let go once the undo is gone, share.release), and says that it is one
// way. With no device yet, "this computer" dashed opens the download card
// and fills in when this computer logs in.
import { frame, clear, follow, App, G, esc, spaced, glyphOf, nameOf, ago, rows } from './shell.ts';
import { D } from './clock.ts';
import { ApiError } from './api.ts';
import { downloadCard } from './download.ts';
import { phoneMine } from './phone.ts';
import { listedLANs } from './model.ts';
import type { Ctx } from './main.ts';
import type { Route } from './router.ts';
import type { UserView, DeviceView, LANView } from './model.ts';

type Dev = { type: string; owner: string; name: string; x: number; y: number; off?: string; pending?: boolean };
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
  const mineList = (): DeviceView[] => v.devices.filter((d) => d.owner === v.me && !d.publicLAN);
  const devOf = (id: string) => v.devices.find((d) => d.id === id);
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
    const at = rows(ids.length, centerX(), midY());
    ids.forEach((id, i) => Object.assign(DEVS[id], at[i]));
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
    app.selected = id;
    app.select(id);
    const st = d.off ? { c: '#a1a1aa', t: d.off, s: '它开机、连上网就会回来' } : { c: '#22C55E', t: '在线', s: '别人能用你开放的服务' };
    const lans = lansFor();
    const chips = lans
      .map((l) =>
        dv.lans.includes(l.id)
          ? `<button class="chip lan-in" data-l="${esc(l.id)}" title="去${esc(l.name)}">${dots(l)}${esc(spaced(l.name))}</button>`
          : `<button class="chip lan-add" data-l="${esc(l.id)}" aria-label="加进${esc(l.name)}">${G.ui('plus', 14)}${esc(spaced(l.name))}</button>`,
      )
      .join('');
    const shares = (dv.shares || []).filter((s) => s.person !== v.me);
    const shareChips = shares.map((s) => `<span class="mchip" data-k="${esc(s.person)}">${App.ball(s.person)}${esc(spaced(nameOf(v, s.person)))}<button class="x" title="收回" aria-label="从${esc(nameOf(v, s.person))}收回">${G.ui('close', 12)}</button></span>`).join('');
    const oneWay = shares.length === 1 ? `只有${nameOf(v, shares[0].person)}能连你，反过来不行` : '只有他们能连你，反过来不行';
    const addrs = (dv.addrs || []).map((a) => a.replace(/\/\d+$/, '')).join('、');
    const sh = app.sheet(
      `<div class="top"><div class="tile-s"><div class="tile" style="--c:${P[v.me].c}">${G.device(d.type, 20)}</div></div>` +
        `<div><b>${esc(spaced(d.name))}</b><span>我的设备</span></div>` +
        `<button class="icon-btn sheet-close" title="关闭" aria-label="关闭">${G.ui('close', 16)}</button></div>` +
        `<div class="status"><span class="badge" style="--c:${st.c}">${G.ui('online', 16)}</span><div>${esc(st.t)}<small>${esc(st.s)}</small></div></div>` +
        (lans.length ? `<div class="label">局域网</div><div class="people lan-chips">${chips}</div>` : '') +
        (shares.length ? `<div class="label">分享给</div><div class="mchips open">${shareChips}</div><div class="hint">${esc(spaced(oneWay))}</div>` : '') +
        `<div class="card-foot"><button class="more">更多${G.ui('chevron', 14)}</button></div><div class="detail"><p>${esc(addrs ? '地址 ' + addrs : '')}</p></div>`,
      id,
    );
    for (const b of sh.querySelectorAll('.lan-in')) (b as HTMLElement).onclick = () => ctx.go('/lan/' + encodeURIComponent((b as HTMLElement).dataset.l as string));
    for (const b of sh.querySelectorAll('.lan-add')) (b as HTMLElement).onclick = () => addTo(id, (b as HTMLElement).dataset.l as string);
    for (const x of sh.querySelectorAll('.mchip .x')) (x as HTMLElement).onclick = () => revoke(id, ((x as HTMLElement).closest('.mchip') as HTMLElement).dataset.k as string);
  }
  app.onDeviceClick = (id: string) => openCard(id);
  app.onSheetClose = () => (app.selected = null);
  const again = (id: string) => app.selected === id && app.sheetEl.style.display !== 'none' && openCard(id);
  const failed = (e: unknown, text: string) => app.toast(e instanceof ApiError ? e.message : text, { parent: cv, x: centerX() });

  // Into a LAN, from the card (lan.addDevice); undo takes it out again.
  async function addTo(id: string, lan: string): Promise<void> {
    const l = v.lans.find((x) => x.id === lan);
    const d = DEVS[id];
    if (!l || !d) return;
    try {
      await ctx.op('lan.addDevice', { lan, device: id });
    } catch (e) {
      return void failed(e, '没能加进去，再试一次');
    }
    app.toast(`已把${d.name}加进${l.name}`, {
      dev: { type: d.type, owner: v.me },
      x: centerX(),
      undo: () => ctx.op('lan.removeDevice', { lan, device: id }).catch((e) => failed(e, '没能撤销，再试一次')),
    });
  }
  // Taken back from someone: at once, with undo (the same share again); the
  // seat goes once the undo is gone.
  function revoke(id: string, person: string): void {
    const dv = devOf(id), d = DEVS[id];
    const s = dv?.shares?.find((x) => x.person === person);
    if (!dv || !d || !s) return;
    let undone = false;
    ctx.op('share.revoke', { share: s.id }).catch((e) => failed(e, '没能收回，再试一次'));
    app.toast(`已从${nameOf(v, person)}收回${d.name}`, {
      dev: { type: d.type, owner: v.me },
      x: centerX(),
      undo: () => {
        undone = true;
        ctx.op('share.create', { device: id, person }).catch((e) => failed(e, '没能撤销，再试一次'));
      },
      gone: () => {
        if (!undone) ctx.op('share.release', { share: s.id }).catch(() => {});
      },
    });
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
  if (D.recording) import('./rec-mine.ts').then(() => ((window as unknown as Record<string, unknown>).__ready = true));
  return () => {
    off();
    clear();
  };
}
