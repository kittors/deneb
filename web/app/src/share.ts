// Scene 4 on the web (DESIGN.md scene 4, §14.3; design/demos/web/m3-14):
// a LAN's console. Its devices in a grid, its members in rows; a device
// dragged onto a member, or a member onto a device, shares it with them
// (share.create). A click on a row opens what they have; × takes one back
// (share.revoke), with undo (share.create again) until the toast goes, when
// the server is told the seat can go (share.release). The grid and the rows
// keep the order they are given, on this browser.
//
// The interaction is design/demos/04-device-share.html's, on the real view.
import { frame, clear, follow, App, G, esc, spaced, glyphOf, PUBLIC, nameOf, serviceName, serviceIcon } from './shell.ts';
import { D } from './clock.ts';
import { ApiError } from './api.ts';
import type { Ctx } from './main.ts';
import type { Route } from './router.ts';
import type { UserView, DeviceID, PersonID, ShareID, Service } from './model.ts';

// A service's key, as proto.Service.Key: its port ranges sorted (protocol,
// then first port, then last) and joined with commas.
export function serviceKey(s: Service): string {
  const parse = (p: string) => {
    const [proto, range] = p.split('/');
    const [a, b] = (range || '').split('-').map(Number);
    return { p, proto, a, b: b || a };
  };
  const ps = [...new Set(s.ports)].map(parse).sort((x, y) => x.proto.localeCompare(y.proto) || x.a - y.a || x.b - y.b);
  return ps.map((x) => x.p).join(',');
}

type Cell = { id: DeviceID; name: string; kind: string; color: string; catalog: Service[]; open: Service[] };
type Has = { share: ShareID; device: DeviceID; services?: Service[] };
type Mem = { id: PersonID; name: string; has: Has[]; outside?: boolean };
type P2 = { x: number; y: number };

// What the console shows of a view: the LAN's devices I may share (mine
// there, and its public ones when I own it), its members but me, and what
// each of them has from these devices.
function read(v: UserView, lanID: string): { name: string; cells: Cell[]; mems: Mem[] } | null {
  const lan = v.lans.find((l) => l.id === lanID);
  if (!lan || lan.owner !== v.me) return null;
  const P = App.PEOPLE;
  const inLAN = new Set(lan.devices.map((d) => d.device));
  const devs = v.devices.filter((d) => d.publicLAN === lanID || (inLAN.has(d.id) && d.owner === v.me));
  // A device's catalog, and what it opens in this LAN: what a share opens
  // to begin with (the receiver can use what the LAN can).
  const cells = devs.map((d) => ({
    id: d.id,
    name: d.displayName || d.name,
    kind: d.kind,
    color: (P[d.publicLAN ? PUBLIC(lanID) : d.owner] || P[v.me]).c,
    catalog: d.services || [],
    open: (lan.devices.find((x) => x.device === d.id)?.services || []).filter((x) => x.on),
  }));
  const mems: Mem[] = lan.members
    .filter((m) => m.state === 'joined' && m.person !== v.me)
    .map((m) => ({ id: m.person, name: nameOf(v, m.person), has: [] as Has[] }));
  // Anyone else I know (from my other LANs, or a share): a device dragged
  // onto them is theirs alone, "只给他用这一台", without joining this LAN
  // (DESIGN §2; design/plan/m3/control.md §1).
  for (const p of v.people) if (p.id !== v.me && !mems.some((m) => m.id === p.id)) mems.push({ id: p.id, name: nameOf(v, p.id), has: [], outside: true });
  for (const d of devs) for (const s of d.shares || []) {
    const m = mems.find((x) => x.id === s.person);
    if (m) m.has.push({ share: s.id, device: d.id, services: s.services });
  }
  for (const m of mems) m.has.sort((a, b) => a.device.localeCompare(b.device));
  return { name: lan.name, cells, mems };
}

export function sharePage(root: HTMLElement, ctx: Ctx, route: Route): () => void {
  const v0 = ctx.view();
  const lanID = (route.page === 'share' && route.lan) || (v0.lans.find((l) => l.owner === v0.me && !l.pending) || { id: '' }).id;
  const app = frame(root, ctx, 'share:' + lanID);
  clear();
  app.setStack([]);
  let model = read(v0, lanID);
  if (!model) {
    app.setTitle('设备共享', '只有局域网的拥有者能把它的设备分给别人', 'none');
    return () => clear();
  }
  app.setTitle('设备共享', '把设备拖到成员身上，就分享给他了', 'none');
  const cv: HTMLElement = app.canvas;
  const layer: HTMLElement = app.dragLayer;
  const $ = (s: string, r: ParentNode = cv) => r.querySelector(s) as HTMLElement;
  const tile = (c: Cell, size = 30) => `<div class="tile" style="--c:${c.color}">${G.device(glyphOf(c.kind), size)}</div>`;
  const cellOf = (id: string) => model!.cells.find((c) => c.id === id) as Cell;
  const mem = (k: string) => model!.mems.find((m) => m.id === k) as Mem;
  const rowOf = (k: string) => $(`.mrow[data-k="${CSS.escape(k)}"]`);

  // ---- Build -----------------------------------------------------------------------------
  const wells = App.h(
    `<div class="share-page">` +
      `<section class="well-card devs" data-k="devs"><div class="card-label"><span>${esc(spaced(model.name + '的设备'))}</span><span class="n-devs"></span></div><div class="grid" role="list"></div></section>` +
      `<section class="well-card members" data-k="members"><div class="card-label"><span>成员</span><span class="n-mems"></span></div><div class="mrows" role="list"></div>` +
      `<div class="card-label out-label"><span>${esc(spaced('不在' + model.name + '的人'))}</span><span class="n-out"></span></div><div class="mrows mrows-out" role="list"></div></section>` +
      `</div>`,
  );
  cv.appendChild(wells);
  const grid = $('.grid'), rows = $('.mrows'), rowsOut = $('.mrows-out');
  const drawCell = (c: Cell) => App.h(`<div class="cell" data-d="${esc(c.id)}" role="listitem" tabindex="0" aria-label="${esc(c.name)}"><div class="dev-s">${tile(c)}</div><span>${esc(spaced(c.name))}</span></div>`);
  const drawRow = (m: Mem) =>
    App.h(
      `<div class="mrow" data-k="${esc(m.id)}" role="listitem"><div class="mtop" role="button" tabindex="0" aria-label="${esc(m.name)}，${m.has.length} 台">${App.ball(m.id)}<span>${esc(spaced(m.name))}</span><span class="cnt"><span class="num"><i>${m.has.length}</i></span>台</span>${G.ui('chevron', 14)}</div><div class="mchips"></div></div>`,
    );
  function build(): void {
    grid.replaceChildren(...model!.cells.map(drawCell));
    const inside = model!.mems.filter((m) => !m.outside), outside = model!.mems.filter((m) => m.outside);
    rows.replaceChildren(...inside.map(drawRow));
    rowsOut.replaceChildren(...outside.map(drawRow));
    $('.n-devs').textContent = `${model!.cells.length} 台`;
    $('.n-mems').textContent = `${inside.length} 人`;
    $('.n-out').textContent = `${outside.length} 人`;
    $('.out-label').style.display = rowsOut.style.display = outside.length ? '' : 'none';
    loadOrder('devices', grid, 'd');
    loadOrder('members', rows, 'k');
    loadOrder('others', rowsOut, 'k');
    for (const c of model!.cells) wireCell(c);
    for (const m of model!.mems) wireRow(m);
    if (!model!.cells.length) grid.innerHTML = `<p class="empty-note">这个局域网里还没有你能分享的设备</p>`;
    if (!inside.length) rows.innerHTML = `<p class="empty-note">还没有别的成员，先邀请朋友进来</p>`;
  }

  // ---- Positions (the canvas is the reference; the page is not zoomed) ----------------------
  const rectOf = (el: Element) => {
    const r = el.getBoundingClientRect(), s = cv.getBoundingClientRect();
    return { x: r.left - s.left, y: r.top - s.top, w: r.width, h: r.height };
  };
  const center = (el: Element): P2 => {
    const r = rectOf(el);
    return { x: r.x + r.w / 2, y: r.y + r.h / 2 };
  };
  const hit = (el: Element, p: P2, pad = 0) => {
    const r = rectOf(el);
    return p.x >= r.x - pad && p.x <= r.x + r.w + pad && p.y >= r.y - pad && p.y <= r.y + r.h + pad;
  };
  const rowAt = (p: P2) => model!.mems.map((m) => m.id).find((k) => rowOf(k) && hit(rowOf(k), p, 4));
  const cellAt = (p: P2) => ([...grid.children] as HTMLElement[]).find((c) => c.dataset.d && hit(c, p, 2));
  const at = (el: HTMLElement): P2 => {
    const b = rectOf(el.offsetParent as Element);
    return { x: b.x + el.offsetLeft + el.offsetWidth / 2, y: b.y + el.offsetTop + el.offsetHeight / 2 };
  };
  const gridIndex = (p: P2) => {
    let bi = 0, bd = Infinity;
    ([...grid.children] as HTMLElement[]).forEach((c, i) => {
      const q = at(c), d = Math.hypot(q.x - p.x, q.y - p.y);
      if (d < bd) (bd = d), (bi = i);
    });
    return bi;
  };
  const rowIndex = (p: P2, row: HTMLElement) => ([...(row.parentElement as HTMLElement).children] as HTMLElement[]).filter((r) => r !== row && at(r).y < p.y).length;
  const clampC = (p: P2): P2 => ({ x: D.clamp(p.x, 30, cv.clientWidth - 30), y: D.clamp(p.y, 30, cv.clientHeight - 30) });
  const lightRow = (k?: string) => {
    for (const m of model!.mems) {
      rowOf(m.id)?.classList.toggle('target', m.id === k && !full(k));
      rowOf(m.id)?.classList.toggle('full', m.id === k && full(k));
    }
  };
  const lightCell = (c?: Element) => {
    for (const x of grid.children) x.classList.toggle('target', x === c);
  };

  // ---- Order, remembered on this browser ----------------------------------------------------
  const orderKey = (name: string) => `deneb.order.${lanID}.${name}`;
  const saveOrder = (name: string, box: HTMLElement, attr: string) => {
    if (D.recording) return;
    try {
      localStorage.setItem(orderKey(name), JSON.stringify(([...box.children] as HTMLElement[]).map((el) => el.dataset[attr])));
    } catch (_) {}
  };
  function loadOrder(name: string, box: HTMLElement, attr: string): void {
    if (D.recording) return;
    try {
      for (const id of JSON.parse(localStorage.getItem(orderKey(name)) || '[]')) {
        const el = box.querySelector(`[data-${attr}="${CSS.escape(id)}"]`);
        if (el) box.appendChild(el);
      }
    } catch (_) {}
  }
  function reorder(box: HTMLElement, el: HTMLElement, index: number): void {
    const items = [...box.children] as HTMLElement[];
    if (items.indexOf(el) === index) return;
    const before = new Map(items.map((x) => [x, x.getBoundingClientRect()]));
    const others = items.filter((x) => x !== el);
    box.insertBefore(el, others[index] || null);
    for (const x of box.children as unknown as HTMLElement[]) {
      const a = before.get(x) as DOMRect, b = x.getBoundingClientRect();
      const dx = a.left - b.left, dy = a.top - b.top;
      if (Math.abs(dx) + Math.abs(dy) > 0.5) x.animate([{ transform: `translate(${dx}px, ${dy}px)` }, { transform: 'none' }], { duration: 340, easing: 'cubic-bezier(0.25, 0.4, 0.2, 1)' });
    }
  }
  const dwell = () => {
    let want: number | null = null, timer: { cancel(): void } | null = null;
    return (box: HTMLElement, el: HTMLElement, i: number | null) => {
      if (i === want) return;
      want = i;
      if (timer) timer.cancel();
      timer = i == null ? null : D.after(150, () => reorder(box, el, i));
    };
  };

  // ---- Counts roll, rows open ---------------------------------------------------------------
  function roll(k: string): void {
    const row = rowOf(k);
    if (!row) return;
    const box = $('.num', row);
    const n = mem(k).has.length;
    const old = box.lastElementChild as HTMLElement;
    $('.mtop', row).setAttribute('aria-label', `${mem(k).name}，${n} 台`);
    if (old.textContent === String(n)) return;
    const up = n > +(old.textContent || 0);
    const nu = App.h(`<i>${n}</i>`);
    box.appendChild(nu);
    old.style.position = 'absolute';
    old.style.right = '0';
    old.animate([{ transform: 'none', opacity: 1 }, { transform: `translateY(${up ? -14 : 14}px)`, opacity: 0 }], { duration: 260, easing: 'ease-in', fill: 'forwards' });
    nu.animate([{ transform: `translateY(${up ? 14 : -14}px)`, opacity: 0 }, { transform: 'none', opacity: 1 }], { duration: 300, easing: 'cubic-bezier(0.3, 1.2, 0.5, 1)' });
    D.after(270, () => old.remove());
  }
  const chip = (h: Has) => {
    const c = cellOf(h.device);
    return c ? `<span class="mchip" data-d="${esc(h.device)}"><span class="tile-xs">${tile(c, 14)}</span>${esc(spaced(c.name))}<button class="x" title="收回" aria-label="收回${esc(c.name)}">${G.ui('close', 12)}</button></span>` : '';
  };
  function fillChips(k: string): void {
    const box = $('.mchips', rowOf(k));
    const m = mem(k);
    box.innerHTML = m.has.length ? m.has.map(chip).join('') : `<span class="empty-note">还没分给他设备</span>`;
    for (const x of box.querySelectorAll('.x'))
      (x as HTMLElement).onclick = (e) => {
        e.stopPropagation();
        revoke(k, ((x as HTMLElement).closest('.mchip') as HTMLElement).dataset.d as string);
      };
    for (const c of box.querySelectorAll('.mchip'))
      (c as HTMLElement).onclick = (e) => {
        e.stopPropagation();
        shareCard(k, (c as HTMLElement).dataset.d as string);
      };
  }
  const fitChips = (k: string) => {
    const row = rowOf(k), box = $('.mchips', row);
    if (!row.classList.contains('open')) return void (box.style.height = '0px');
    const was = box.style.height;
    box.style.transition = 'none';
    box.style.height = 'auto';
    const h = box.scrollHeight + 10;
    box.style.height = was;
    box.offsetHeight;
    box.style.transition = '';
    box.style.height = h + 'px';
  };
  function toggle(k: string): void {
    const row = rowOf(k);
    const open = !row.classList.contains('open');
    if (open) fillChips(k);
    row.classList.toggle('open', open);
    $('.mtop', row).setAttribute('aria-expanded', String(open));
    fitChips(k);
  }
  // Every seat taken, and this person not on mine yet: a device cannot go
  // to them (定价文档 §1.5: it stops, the row says why; no shake, no box).
  const full = (k: string): boolean => {
    const st = ctx.view().seats;
    return !!st && st.enforced && (st.left ?? st.limit - st.used) <= 0 && !(st.people || []).includes(k);
  };
  function nudge(k: string, say = '已经有了', shake = true): void {
    const row = rowOf(k), top = $('.mtop', row);
    if (shake) row.animate([{ transform: 'none' }, { transform: 'translateX(-6px)' }, { transform: 'translateX(6px)' }, { transform: 'translateX(-4px)' }, { transform: 'translateX(2px)' }, { transform: 'none' }], { duration: 400, easing: 'ease-out' });
    top.querySelector('.note')?.remove();
    const note = App.h(`<span class="note">${esc(say)}</span>`);
    $('.cnt', top).before(note);
    note.animate([{ opacity: 0 }, { opacity: 1 }], { duration: 180, easing: 'ease-out' });
    D.after(1800, () => note.isConnected && note.remove());
  }

  // ---- Sharing and taking back ---------------------------------------------------------------
  // Both show at once and go to the server; what the server says wins (a
  // refusal puts it back, with its words).
  function refused(k: string, e: unknown, undo: () => void): void {
    undo();
    const err = e instanceof ApiError ? e : null;
    if (err && err.code === 'conflict') return nudge(k, err.message || '已经有了');
    if (err && err.code === 'seats_full') return nudge(k, '位置满了', false);
    app.toast(err ? err.message : '没能做成，再试一次', { parent: cv, x: 340 });
  }
  function share(k: string, id: DeviceID): void {
    const m = mem(k);
    if (m.has.some((h) => h.device === id)) return nudge(k);
    if (full(k)) return nudge(k, '位置满了', false);
    const h: Has = { share: '', device: id };
    m.has.unshift(h);
    roll(k);
    const ball = $('.ball', rowOf(k));
    ball.animate([{ transform: 'none' }, { transform: 'scale(1.2)', offset: 0.4 }, { transform: 'none' }], { duration: 460, easing: 'cubic-bezier(0.3, 1.3, 0.5, 1)' });
    if (rowOf(k).classList.contains('open')) {
      fillChips(k);
      fitChips(k);
      $(`.mchip[data-d="${CSS.escape(id)}"]`, rowOf(k))?.animate([{ transform: 'scale(0.6)', opacity: 0 }, { transform: 'none', opacity: 1 }], { duration: 300, easing: 'cubic-bezier(0.3, 1.3, 0.5, 1)' });
    }
    const open = cellOf(id).open.map(serviceKey);
    ctx.op('share.create', { device: id, person: k, services: open.length ? open : undefined }).then(
      (r) => (h.share = (r.result && r.result.share) || h.share),
      (e) =>
        refused(k, e, () => {
          // (The view may have been drawn anew meanwhile: the member as it is now.)
          const mm = mem(k);
          const j = mm ? mm.has.findIndex((x) => x.device === id) : -1;
          if (j >= 0) mm.has.splice(j, 1);
          roll(k);
          if (rowOf(k)?.classList.contains('open')) (fillChips(k), fitChips(k));
        }),
    );
  }
  function revoke(k: string, id: DeviceID): void {
    const m = mem(k);
    const i = m.has.findIndex((x) => x.device === id);
    if (i < 0) return;
    const h = m.has[i];
    m.has.splice(i, 1);
    roll(k);
    const c = $(`.mchip[data-d="${CSS.escape(id)}"]`, rowOf(k));
    if (c) {
      c.animate([{ transform: 'none', opacity: 1 }, { transform: 'scale(0.6)', opacity: 0 }], { duration: 220, easing: 'ease-in', fill: 'forwards' });
      D.after(230, () => (fillChips(k), fitChips(k)));
    }
    const back = () => {
      const mm = mem(k);
      if (!mm) return;
      if (!mm.has.some((x) => x.device === id)) mm.has.splice(Math.min(i, mm.has.length), 0, h);
      roll(k);
      if (rowOf(k)?.classList.contains('open')) (fillChips(k), fitChips(k));
    };
    let undone = false;
    ctx.op('share.revoke', { share: h.share }).catch((e) => refused(k, e, back));
    app.toast(`已收回${cellOf(id).name}`, {
      parent: cv,
      x: 340,
      undo: () => {
        undone = true;
        back();
        // Undo is the same share made again: the server puts it back as it
        // was, services and all (design/plan/m3/control.md, decisions).
        ctx.op('share.create', { device: id, person: k }).catch((e) => refused(k, e, () => revoke(k, id)));
      },
      // The undo is gone: the seat it held can go now (share.release).
      gone: () => {
        if (!undone && h.share) ctx.op('share.release', { share: h.share }).catch(() => {});
      },
    });
  }

  // ---- A share's card: what it opens to them (service.set with the share) -------------------
  // Their name and the device's, the one-way rule in a sentence (DESIGN
  // §12 item 12: "只有他能连你，反过来不行"), and a switch per service.
  function shareCard(k: string, id: DeviceID): void {
    const c = cellOf(id), m = mem(k);
    const h = m && m.has.find((x) => x.device === id);
    if (!c || !h) return;
    const opened = new Set((h.services || []).map(serviceKey));
    const rows = c.catalog.map(
      (s, i) => `<div class="row">${G.ui(serviceIcon(s.kind), 18)}<span>${esc(serviceName(s))}</span><button class="sw ${opened.has(serviceKey(s)) ? 'on' : ''}" data-own data-i="${i}" role="switch" aria-checked="${opened.has(serviceKey(s))}" aria-label="${esc(serviceName(s))}"></button></div>`,
    );
    const html =
      `<div class="top"><div class="tile-s">${tile(c, 20)}</div><div><b>${esc(spaced(c.name))}</b><span>${esc(spaced('分享给' + m.name))}</span></div><button class="icon-btn sheet-close" title="关闭" aria-label="关闭">${G.ui('close', 16)}</button></div>` +
      `<div class="status"><span class="badge" style="--c:#2f6bff">${G.ui('share', 16)}</span><div>${esc(spaced('只有' + m.name + '能连它'))}<small>${esc(spaced('反过来不行，' + m.name + '的设备它连不到'))}</small></div></div>` +
      (rows.length ? `<div class="label">${esc(spaced('开放给' + m.name + '的'))}</div><div class="rows">${rows.join('')}</div>` : `<p class="say">这台设备还没有服务可以开放</p>`) +
      `<div class="card-foot"><span></span><button class="out-btn">收回</button></div>`;
    const s = app.sheet(html, 'share:' + k + ':' + id);
    for (const sw of s.querySelectorAll('.sw[data-own]')) {
      (sw as HTMLElement).onclick = () => {
        const svc = c.catalog[+((sw as HTMLElement).dataset.i as string)];
        const on = !sw.classList.contains('on');
        sw.classList.toggle('on', on);
        sw.setAttribute('aria-checked', String(on));
        ctx.op('service.set', { device: id, share: h.share, service: { kind: svc.kind, name: svc.name, ports: svc.ports }, on }).catch((e) => {
          sw.classList.toggle('on', !on);
          app.toast(e instanceof ApiError ? e.message : '没能改，再试一次', { parent: cv, x: 340 });
        });
      };
    }
    (s.querySelector('.out-btn') as HTMLElement).onclick = () => {
      app.closeSheet();
      revoke(k, id);
    };
  }

  // ---- A device: onto a person (share it), or onto another cell ------------------------------
  function wireCell(d: Cell): void {
    const cell = $(`.cell[data-d="${CSS.escape(d.id)}"]`);
    const settle = dwell();
    let g: HTMLElement | null = null, from = 0, grab: { x: number; y: number; t: number } | null = null;
    const place = (p: P2, s?: number) => {
      g!.style.left = p.x + 'px';
      g!.style.top = p.y + 'px';
      if (s != null) g!.style.transform = `scale(${s})`;
    };
    const home = () => center($('.dev-s', cell));
    const flyHome = async () => {
      const el = g!, a = { x: parseFloat(el.style.left), y: parseFloat(el.style.top) }, b = home();
      const s0 = parseFloat((el.style.transform.match(/scale\(([\d.]+)\)/) || [0, '1'])[1] as string);
      el.style.transition = '';
      await App.tween(320, (t: number) => place({ x: a.x + (b.x - a.x) * t, y: a.y + (b.y - a.y) * t }, s0 + (1 - s0) * t), D.Ease.out);
      el.remove();
      cell.classList.remove('ghost');
    };
    app.drag(cell, {
      ref: cv,
      start: (p0: P2) => {
        from = [...grid.children].indexOf(cell);
        const c = home();
        grab = { x: p0.x - c.x, y: p0.y - c.y, t: D.now() };
        g = App.h(`<div class="dev dragging">${tile(d)}<div class="name"><span class="nm">${esc(spaced(d.name))}</span></div></div>`) as HTMLElement;
        g.style.transition = 'transform 0.22s cubic-bezier(0.3, 1.3, 0.5, 1)';
        layer.appendChild(g);
        place(home(), 1);
        cell.classList.add('ghost');
      },
      move: (p: P2) => {
        const q = clampC(p);
        const overGrid = hit($('.devs'), q);
        const k = D.Ease.out(Math.max(0, 1 - (D.now() - grab!.t) / 220));
        place({ x: q.x - grab!.x * k, y: q.y - grab!.y * k }, overGrid ? 1 : 0.6);
        lightRow(rowAt(q));
        settle(grid, cell, overGrid ? gridIndex(q) : null);
      },
      end: async (p: P2 | null, cancel: boolean) => {
        lightRow();
        settle(grid, cell, null);
        const q = p && clampC(p);
        const k = !cancel && q ? rowAt(q) : undefined;
        if (cancel || !q || !hit($('.devs'), q)) reorder(grid, cell, from);
        else {
          reorder(grid, cell, gridIndex(q));
          saveOrder('devices', grid, 'd');
        }
        if (k && full(k)) {
          nudge(k, '位置满了', false);
          await flyHome();
          return;
        }
        if (k && !mem(k).has.some((h) => h.device === d.id)) {
          const el = g!, a = { x: parseFloat(el.style.left), y: parseFloat(el.style.top) }, b = center($('.ball', rowOf(k)));
          await App.tween(300, (t: number) => {
            place({ x: a.x + (b.x - a.x) * t, y: a.y + (b.y - a.y) * t }, 0.6 - 0.45 * t);
            el.style.opacity = String(1 - 0.8 * t);
          }, D.Ease.in);
          el.remove();
          cell.classList.remove('ghost');
          cell.animate([{ opacity: 0, transform: 'scale(0.9)' }, { opacity: 1, transform: 'none' }], { duration: 260, easing: 'cubic-bezier(0.3, 1.3, 0.5, 1)' });
          share(k, d.id);
          return;
        }
        if (k) nudge(k);
        await flyHome();
      },
    });
  }

  // ---- A member: up or down the list, or onto a device (share it) ----------------------------
  function wireRow(m: Mem): void {
    const row = rowOf(m.id);
    const rows = row.parentElement as HTMLElement;
    const list = m.outside ? 'others' : 'members';
    const settle = dwell();
    let g: HTMLElement | null = null, from = 0;
    $('.mtop', row).addEventListener('keydown', (e) => (e as KeyboardEvent).key === 'Enter' && toggle(m.id));
    app.drag(row, {
      ref: cv,
      // A chip and its × are their own (the share's card, taking it back).
      ignore: 'button, .mchip',
      click: () => toggle(m.id),
      start: (_p0: P2, p: P2) => {
        from = [...rows.children].indexOf(row);
        g = App.h(`<div class="mpill">${App.ball(m.id)}<span>${esc(spaced(m.name))}</span></div>`) as HTMLElement;
        g.style.left = p.x + 'px';
        g.style.top = p.y + 'px';
        layer.appendChild(g);
        g.animate([{ transform: 'translate(-50%, -50%) scale(0.9)', opacity: 0.6 }, { transform: 'translate(-50%, -50%) rotate(-2deg)', opacity: 1 }], { duration: 200, easing: 'cubic-bezier(0.3, 1.3, 0.5, 1)' });
        row.classList.add('ghost');
      },
      move: (p: P2) => {
        const q = clampC(p);
        g!.style.left = q.x + 'px';
        g!.style.top = q.y + 'px';
        lightCell(cellAt(q));
        settle(rows, row, hit($('.members'), q) ? rowIndex(q, row) : null);
      },
      end: async (p: P2 | null, cancel: boolean) => {
        lightCell();
        settle(rows, row, null);
        const q = p && clampC(p);
        const c = !cancel && q ? cellAt(q) : undefined;
        if (cancel || !q || !hit($('.members'), q)) reorder(rows, row, from);
        else {
          reorder(rows, row, rowIndex(q, row));
          saveOrder(list, rows, 'k');
        }
        const el = g!;
        if (c) {
          const id = c.dataset.d as string;
          el.animate([{ opacity: 1 }, { opacity: 0, transform: 'translate(-50%, -50%) scale(0.6)' }], { duration: 220, easing: 'ease-in', fill: 'forwards' });
          D.after(230, () => el.remove());
          row.classList.remove('ghost');
          if (mem(m.id).has.some((h) => h.device === id)) return nudge(m.id);
          $('.tile', c).animate([{ transform: 'none' }, { transform: 'translateY(-6px) scale(1.04)', offset: 0.4 }, { transform: 'none' }], { duration: 420, easing: 'cubic-bezier(0.3, 1.3, 0.5, 1)' });
          share(m.id, id);
          return;
        }
        const a = { x: parseFloat(el.style.left), y: parseFloat(el.style.top) }, b = center($('.ball', row));
        await App.tween(280, (t: number) => {
          el.style.left = a.x + (b.x - a.x) * t + 'px';
          el.style.top = a.y + (b.y - a.y) * t + 'px';
          el.style.opacity = String(1 - t);
        }, D.Ease.out);
        el.remove();
        row.classList.remove('ghost');
      },
    });
  }

  build();
  // Recording (DESIGN §11): the scene's script, then ready.
  if (D.recording) import('./rec-share.ts').then(() => ((window as unknown as Record<string, unknown>).__ready = true));

  // ---- The server's word ---------------------------------------------------------------------
  // A new view: the devices or members changed, the console is drawn anew;
  // otherwise each count rolls to what the server has, and open rows follow.
  const same = (a: string[], b: string[]) => a.length === b.length && a.every((x, i) => x === b[i]);
  const off = ctx.onView((v) => {
    follow(v);
    const next = read(v, lanID);
    if (!next) return;
    const open = model!.mems.filter((m) => rowOf(m.id)?.classList.contains('open')).map((m) => m.id);
    const reshape = !same(model!.cells.map((c) => c.id).sort(), next.cells.map((c) => c.id).sort()) || !same(model!.mems.map((m) => m.id).sort(), next.mems.map((m) => m.id).sort());
    model = next;
    if (reshape) {
      build();
      for (const k of open) if (rowOf(k)) toggle(k);
      return;
    }
    for (const m of model.mems) {
      roll(m.id);
      if (open.includes(m.id)) (fillChips(m.id), fitChips(m.id));
    }
  });
  return () => {
    off();
    clear();
  };
}
