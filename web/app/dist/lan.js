// A LAN on the web (DESIGN.md §14.4; design/demos/web/weblan.js): the
// desktop's canvas without "this computer". A browser is not in the LAN,
// so nothing here is drawn from it: no device in the middle, no links.
// Each device says whether it is online. My devices sit in the middle and
// the others' around them (an arrangement the user may change, kept on this
// browser). The tray holds my devices that are not in this LAN, as on the
// desktop: dragged out they join (lan.addDevice), dragged in they leave
// (lan.removeDevice), with undo. When I have no device here yet, the middle
// holds "this computer" as a dashed tile that opens the download card, and
// fills in when this computer logs in.
import { frame, clear, follow, App, G, esc, spaced, glyphOf, nameOf, serviceName, serviceIcon, ago } from './shell.js';
import { sharedOnly } from './model.js';
import { D } from './clock.js';
import { ApiError } from './api.js';
import { downloadCard, pickExample } from './download.js';
import { phoneLan } from './phone.js';
                                     
                                         
                                                                    

                                                                                                                
                                   
const SELF = 'this-computer';

export function lanPage(root             , ctx     , route       )             {
  // A studio that shares out its own devices, which I am only a member
  // of: what it gives me is under 共享给我 (DESIGN.md §6.11).
  const l0 = ctx.view().lans.find((l) => route.page === 'lan' && l.id === route.lan);
  if (l0 && sharedOnly(ctx.view(), l0)) {
    queueMicrotask(() => ctx.go('/shared', true));
    return () => {};
  }
  // Narrower than 768: the phone's list (§14.6).
  if (ctx.phone()) return phoneLan(root, ctx, route);
  const lanID = route.page === 'lan' ? route.lan : '';
  const app = frame(root, ctx, 'lan:' + lanID);
  clear();
  let v = ctx.view();
  let lan = v.lans.find((l) => l.id === lanID)                       ;
  if (!lan) {
    app.setTitle('没有这个局域网', '它可能被删了，或者你已经离开', 'none');
    return () => clear();
  }
  const P = App.PEOPLE;
  const cv              = app.canvas;
  const H = () => cv.clientHeight;
  const BAND = 104;
  const DEVS                      = {};
  const SPARE                                                 = {};
  const devOf = (id        ) => v.devices.find((d) => d.id === id)                          ;
  const lanDev = (id        ) => lan .devices.find((d) => d.device === id);
  const statusOf = (id        ) => {
    const d = DEVS[id];
    if (d.pending) return '装上 Deneb 就能加进来';
    return d.off || '在线';
  };

  // ---- Where things go: as the desktop has them around this computer, but
  // with my devices in the middle. A place the user gave stays (App.recall).
  const centerX = () => Math.min(Math.max(cv.clientWidth, 640), 1060) / 2 - 170;
  function layout(ids          )       {
    const cx = Math.max(300, centerX()), cy = Math.max(250, (H() - BAND) / 2);
    const mine = ids.filter((id) => DEVS[id].owner === v.me || DEVS[id].pending);
    const others = ids.filter((id) => !mine.includes(id)).sort((a, b) => DEVS[a].owner.localeCompare(DEVS[b].owner) || DEVS[a].name.localeCompare(DEVS[b].name));
    mine.forEach((id, i) => Object.assign(DEVS[id], { x: cx + (i - (mine.length - 1) / 2) * 172, y: cy }));
    // The corners first, as the desktop's sample has them (Mia top left,
    // 阿杰 top right and bottom right, …), then above, below, the sides.
    const rx = Math.max(240, mine.length * 86 + 160), ry = Math.min(170, cy - 80);
    const ANGLES = [-135, -45, 45, 135, -90, 90, 180, 0];
    others.forEach((id, i) => {
      const ring = Math.floor(i / ANGLES.length);
      const a = ((ANGLES[i % ANGLES.length] + ring * 22.5) * Math.PI) / 180;
      Object.assign(DEVS[id], { x: cx + Math.cos(a) * rx * (1 + ring * 0.45), y: cy + Math.sin(a) * ry * (1 + ring * 0.35) });
    });
  }
  const DASH = `<svg class="dash" viewBox="0 0 60 60" width="60" height="60" aria-hidden="true"><rect x="0.75" y="0.75" width="58.5" height="58.5" rx="17.25" fill="none" stroke="currentColor" stroke-width="1.5" stroke-dasharray="4.5 3.5"/></svg>`;
  function mount(id        , o                          = {}) {
    const d = DEVS[id];
    const n = app.device(id, { ...d, sub: statusOf(id), cls: [d.pending ? 'pending' : '', d.off ? 'off' : ''].join(' '), ...o });
    if (d.pending) n.el.querySelector('.tile').insertAdjacentHTML('afterbegin', DASH);
    n.el.setAttribute('role', 'button');
    n.el.setAttribute('tabindex', '0');
    n.el.setAttribute('aria-label', `${d.name}，${statusOf(id)}`);
    n.el.addEventListener('keydown', (e               ) => e.key === 'Enter' && openCard(id));
    return n;
  }
  function readDevs()           {
    const ids           = [];
    for (const ld of lan .devices) {
      const d = devOf(ld.device);
      if (!d || ld.pending) continue;
      ids.push(d.id);
      DEVS[d.id] = Object.assign(DEVS[d.id] || { x: 0, y: 0 }, { type: glyphOf(d.kind), owner: d.owner, name: d.displayName || d.name, off: d.online ? undefined : ago(d.lastSeen) });
    }
    return ids;
  }
  // "This computer" shows only to someone with no device at all yet (just
  // joined from a link, Deneb not installed): with a device elsewhere, the
  // tray offers it instead.
  const noDevice = ()          => !v.devices.some((d) => d.owner === v.me);
  {
    const ids = readDevs();
    if (noDevice()) {
      DEVS[SELF] = { type: 'laptop', owner: v.me, name: '这台电脑', x: 0, y: 0, pending: true };
      ids.unshift(SELF);
    }
    layout(ids);
    for (const id of ids) mount(id);
  }

  function summary()       {
    const ids = Object.keys(DEVS).filter((id) => !DEVS[id].pending);
    const on = ids.filter((id) => !DEVS[id].off).length;
    const people = lan .members.filter((m) => m.state === 'joined');
    app.setStack(people.map((m) => m.person));
    if (!ids.length) app.setTitle(lan .name, `${people.length} 个人，还没有设备`, 'off');
    else if (on === ids.length) app.setTitle(lan .name, `${people.length} 个人，${ids.length} 台设备都在线`, 'ok');
    else app.setTitle(lan .name, `${people.length} 个人，${ids.length} 台设备，${on} 台在线`, 'off');
  }
  summary();

  // ---- The header's button: an invite link, copied (DESIGN.md §5 邀请朋友) ----
  const invite = app.inviteBtn                      ;
  if (invite) {
    invite.style.display = '';
    invite.onclick = async () => {
      try {
        const r = await ctx.op('lan.invite', { lan: lanID });
        const url = r.result?.invite?.url;
        if (url) await navigator.clipboard.writeText(url).catch(() => {});
        app.toast(url ? '已复制邀请链接，发给朋友就行' : '邀请发出去了', { parent: cv, x: centerX() });
      } catch (e) {
        app.toast(e instanceof ApiError ? e.message : '没能做成，再试一次', { parent: cv, x: centerX() });
      }
    };
  }

  // ---- Tray: my devices that are not in this LAN (as the desktop's, §6.5) --------------------
  const IDLE = '拖出来，加进局域网';
  const tray = App.h(`<div class="tray" data-k="tray-${esc(lanID)}" aria-label="不在这个局域网里的我的设备"><span class="tray-label">${IDLE}</span></div>`)               ;
  app.o.trace = (app.o.trace || []).filter((s        ) => !s.startsWith('.tray')).concat(['.tray', '.tray .mini', '.tray .slot', '.drag-layer .dev']);
  cv.appendChild(tray);
  // It sits under the devices' middle, whatever it holds.
  const stopPlace = D.onFrame(() => {
    const w = tray.offsetWidth;
    tray.style.left = Math.max(16, Math.round(Math.max(300, centerX()) - w / 2)) + 'px';
  });
  const label = tray.querySelector('.tray-label')               ;
  const miniOf = (id        ) => tray.querySelector(`.mini[data-id="${CSS.escape(id)}"]`)                      ;
  const miniPos = (id        )     => {
    const r = app.rectOf(miniOf(id));
    return { x: r.x + r.w / 2, y: r.y + r.h / 2 };
  };
  const removable = (n                                             ) => n.kind === 'device' && n.owner === v.me && n.id !== SELF;
  const overTray = (q    ) => {
    const z = app.rectOf(tray), m = 22;
    return q.x >= z.x - m && q.x <= z.x + z.w + m && q.y >= z.y - m && q.y <= z.y + z.h + m;
  };
  const nearTray = (q    ) => {
    const z = app.rectOf(tray);
    const dx = Math.max(z.x - q.x, 0, q.x - (z.x + z.w)), dy = Math.max(z.y - q.y, 0, q.y - (z.y + z.h));
    return dx * dx + dy * dy < 170 * 170;
  };
  app.spotRules = () => [{ x: -1e4, y: H() - BAND, w: 2e4, h: 1e4 }];
  const reason = (n                ) => (n.id === SELF ? '这台电脑还没加进来' : `这是${nameOf(v, DEVS[n.id].owner)}的设备，只有${nameOf(v, DEVS[n.id].owner)}能移出`);
  let mode = 'idle', landing = 0, slot                     = null, slotEl                     = null;
  const GROW = { duration: 320, easing: 'cubic-bezier(0.3, 0.3, 0.2, 1)' };
  function say(text        )       {
    const t = spaced(text);
    if (label.textContent === t) return;
    const cur = App.softNow(label, 'w');
    const w0 = cur != null ? cur : label.offsetWidth;
    label.style.width = '';
    label.textContent = t;
    const w1 = label.offsetWidth;
    label.style.width = w0 + 'px';
    App.soft(label, 'w', w0, w1, (w        , rest         ) => (label.style.width = rest ? '' : w + 'px'));
    label.animate([{ opacity: 0 }, { opacity: 1 }], { duration: 160, easing: 'ease-out' });
  }
  const drawSlot = (el             , w        ) => {
    w = Math.max(0, w);
    el.style.width = w + 'px';
    el.style.marginLeft = -10 * (1 - Math.min(1, w / 40)) + 'px';
    el.style.opacity = String(Math.min(1, w / 40));
  };
  function openSlot(open         )       {
    if (open === !!slot) return;
    if (open) {
      if (!slotEl) {
        slotEl = App.h(`<div class="slot" data-k="slot"></div>`)               ;
        drawSlot(slotEl, 0);
        tray.appendChild(slotEl);
      }
      const el = (slot = slotEl);
      App.soft(el, 'w', 0, 40, (w        ) => drawSlot(el, w));
    } else {
      const el = slotEl               ;
      slot = null;
      App.soft(el, 'w', 40, 0, (w        ) => drawSlot(el, w), () => {
        if (slotEl === el && !slot) {
          slotEl = null;
          el.remove();
        }
      });
    }
  }
  const slotPos = ()     => {
    const r = app.rectOf(slot || tray);
    return { x: slot ? r.x + r.w / 2 : r.x + r.w - 30, y: r.y + r.h / 2 };
  };
  function setMode(m        , text        )       {
    if (landing > 0) (m = 'armed'), (text = '松手，移出局域网');
    if (m !== mode) {
      tray.classList.toggle('offer', m === 'offer');
      tray.classList.toggle('armed', m === 'armed');
      mode = m;
    }
    say(text);
    openSlot(m === 'armed');
    tray.classList.toggle('away', !tray.querySelector('.mini') && m === 'idle');
  }
  function collapseMini(m             )       {
    m.style.pointerEvents = 'none';
    m.animate([{ width: '40px', marginLeft: '0px', opacity: 1 }, { width: '0px', marginLeft: '-10px', opacity: 0 }], { ...GROW, fill: 'forwards' });
    D.after(GROW.duration + 10, () => {
      m.remove();
      setMode(mode, mode === 'idle' ? IDLE : (label.textContent          ));
    });
  }
  function addMini(id        , at                 )              {
    const d = SPARE[id];
    const m = App.h(`<div class="mini" data-id="${esc(id)}" title="${esc(d.name)}" role="button" tabindex="0" aria-label="把${esc(d.name)}加进局域网"><div class="tile" style="--c:${P[v.me].c}">${G.device(d.type, 20)}</div></div>`)               ;
    tray.insertBefore(m, at || null);
    trayDrag(m, id);
    return m;
  }
  function readSpare()           {
    // My own devices, not the LANs' public ones (those are the console's).
    return v.devices.filter((d) => d.owner === v.me && !d.publicLAN && !lan .devices.some((ld) => ld.device === d.id)).map((d) => {
      SPARE[d.id] = { type: glyphOf(d.kind), name: d.displayName || d.name };
      return d.id;
    });
  }
  for (const id of readSpare()) addMini(id);
  setMode('idle', IDLE);

  function trayDrag(m             , id        )       {
    let g                     = null, start            = null;
    m.addEventListener('keydown', (e) => (e                 ).key === 'Enter' && m.click());
    app.drag(m, {
      start: (_p0    , p    ) => {
        start = miniPos(id);
        g = App.h(`<div class="dev dragging"><div class="tile" style="--c:${P[v.me].c}">${G.device(SPARE[id].type, 30)}</div><div class="name"><span class="nm">${esc(spaced(SPARE[id].name))}</span></div></div>`)               ;
        g.style.left = p.x + 'px';
        g.style.top = p.y + 'px';
        app.dragLayer.appendChild(g);
        m.classList.add('ghost');
      },
      move: (p    ) => {
        const q = app.clampTile({ kind: 'device' }, p);
        g .style.left = q.x + 'px';
        g .style.top = q.y + 'px';
      },
      end: async (p           , cancel         ) => {
        const at = { x: parseFloat(g .style.left), y: parseFloat(g .style.top) };
        if (!cancel && p && at.y < H() - BAND) {
          g .remove();
          m.classList.remove('ghost');
          bringIn(id, at, { from: at, fromScale: 1 });
          return;
        }
        g .classList.remove('dragging');
        (g .querySelector('.name')               ).style.opacity = '0';
        await App.tween(300, (k        ) => {
          g .style.left = at.x + (start .x - at.x) * k + 'px';
          g .style.top = at.y + (start .y - at.y) * k + 'px';
          g .style.transform = `scale(${1 - 0.33 * k})`;
        });
        g .remove();
        m.classList.remove('ghost');
      },
      click: () => {
        const from = miniPos(id);
        bringIn(id, { x: from.x, y: H() - BAND - 110 }, { from, fromScale: 40 / 60 });
      },
    });
  }

  // Into the LAN: it lands where it was let go; the server is told.
  function bringIn(id        , at    , o                                                     = {})       {
    const d = SPARE[id];
    const m = miniOf(id);
    if (m) collapseMini(m);
    const from = o.from || at;
    DEVS[id] = { type: d.type, owner: v.me, name: d.name, x: from.x, y: from.y };
    const n = mount(id, { keep: false });
    const spot = app.freeSpot(id, at);
    app.focusId = id;
    if (o.fromScale && o.fromScale !== 1) {
      n.el.style.transform = `scale(${o.fromScale})`;
      n.el.querySelector('.name').style.opacity = 0;
      n.el.offsetWidth;
      n.el.style.transition = 'transform 0.4s cubic-bezier(0.3, 1.2, 0.5, 1), opacity 0.3s ease-out';
      n.el.style.transform = '';
      n.el.querySelector('.name').style.opacity = '';
    } else n.el.animate([{ transform: 'scale(1.06)' }, { transform: 'none' }], { duration: 420, easing: 'cubic-bezier(0.3, 1.5, 0.5, 1)' });
    app.settle(n, spot, D.Snappy, () => (n.el.style.transition = ''));
    summary();
    ctx.op('lan.addDevice', { lan: lanID, device: id }).catch((e) => {
      app.toast(e instanceof ApiError ? e.message : '没能加进来，再试一次', { parent: cv, x: centerX() });
      takeOut(id, { quiet: true, local: true });
    });
    if (!o.quiet) app.toast(`已把${d.name}加进局域网`, { dev: { type: d.type, owner: v.me }, undo: () => takeOut(id, { quiet: true }) });
  }

  async function takeOut(id        , o                                       = {})                {
    const n = app.nodes[id];
    if (!n) return;
    const home = n.home || { x: n.x, y: n.y };
    if (!SPARE[id]) SPARE[id] = { type: DEVS[id].type, name: DEVS[id].name };
    delete DEVS[id];
    if (app.selected === id) app.closeSheet();
    app.dropNode(id);
    summary();
    if (!o.local) ctx.op('lan.removeDevice', { lan: lanID, device: id }).catch((e) => app.toast(e instanceof ApiError ? e.message : '没能移出，再试一次', { parent: cv, x: centerX() }));
    landing++;
    setMode('armed', '松手，移出局域网');
    n.el.classList.remove('dragging');
    const nm = n.el.querySelector('.name');
    if (nm) nm.style.opacity = 0;
    if (n.el.parentNode !== app.dragLayer) app.dragLayer.appendChild(n.el);
    const s0 = n.dropScale || n.scale || 1;
    n.dropScale = null;
    const a = { x: n.x, y: n.y };
    await App.tween(340, (k        ) => {
      const to = slotPos();
      n.el.style.left = a.x + (to.x - a.x) * k + 'px';
      n.el.style.top = a.y + (to.y - a.y) * k + 'px';
      n.el.style.transform = `scale(${s0 + (40 / 60 - s0) * k})`;
    });
    addMini(id, slot);
    if (slotEl) {
      App.softStop(slotEl, 'w');
      slotEl.remove();
      slotEl = null;
    }
    slot = null;
    n.el.remove();
    landing--;
    setMode(app.held ? mode : 'idle', app.held ? (label.textContent          ) : IDLE);
    if (!o.quiet) app.toast(`已把${SPARE[id].name}移出局域网`, { dev: { type: SPARE[id].type, owner: v.me }, undo: () => bringIn(id, home, { from: miniPos(id), fromScale: 40 / 60, quiet: true }) });
  }

  app.onNodeBounds = (n                                             ) => (n.kind !== 'device' ? null : removable(n) ? { y1: H() - 36 } : { y1: H() - BAND - 70 });
  app.onNodeTarget = (n                                                                   ) => (removable(n) && overTray(n) ? { kind: 'tray', shrink: 40 / 60, keep: true } : null);
  app.onNodeDrop = (n                , t                  ) => {
    if (t.kind !== 'tray') return false;
    takeOut(n.id);
  };
  app.onNodeDragging = (n                                                                                    ) => {
    if (n.kind !== 'device') return;
    const near = nearTray(n), over = removable(n) && overTray(n);
    if (removable(n)) {
      setMode(over ? 'armed' : near ? 'offer' : 'idle', over ? '松手，移出局域网' : near ? '拖到这里，移出局域网' : IDLE);
      const nm = n.el.querySelector('.name')               ;
      if (nm) nm.style.opacity = over ? '0' : '';
    } else setMode(near ? 'refuse' : 'idle', near ? reason(n) : IDLE);
  };
  app.onNodeEnd = (n                     , t                         , cancel         ) => {
    if (t && t.kind === 'tray' && !cancel) return;
    const nm = n.el.querySelector('.name')               ;
    if (nm) nm.style.opacity = '';
    setMode('idle', IDLE);
  };

  // ---- The card ---------------------------------------------------------------------
  // The desktop's card, but it says whether the device is online, not how
  // this computer reaches it; someone else's services cannot be opened from
  // a browser, so it lists them.
  const head = (id        , st                                     ) => {
    const d = DEVS[id];
    return (
      `<div class="top"><div class="tile-s"><div class="tile" style="--c:${P[d.owner].c}">${G.device(d.type, 20)}</div></div>` +
      `<div><b>${esc(spaced(d.name))}</b><span>${d.owner === v.me ? '我的设备' : esc(spaced(nameOf(v, d.owner) + '的设备'))}</span></div>` +
      `<button class="icon-btn sheet-close" title="关闭" aria-label="关闭">${G.ui('close', 16)}</button></div>` +
      `<div class="status"><span class="badge" style="--c:${st.c}">${G.ui('online', 16)}</span><div>${esc(st.t)}<small>${esc(st.s)}</small></div></div>`
    );
  };
  function openCard(id        )       {
    const d = DEVS[id];
    if (!d) return;
    if (d.pending) return openDownload();
    app.selected = id;
    app.select(id);
    const mine = d.owner === v.me;
    const svcs                 = lanDev(id)?.services || [];
    const st = d.off ? { c: '#a1a1aa', t: d.off, s: '它开机、连上网就会回来' } : { c: '#22C55E', t: '在线', s: mine ? '别人能用你开放的服务' : '在装了 Deneb 的电脑上就能用' };
    const rows = svcs.map((s, i) =>
      mine
        ? `<div class="row">${G.ui(serviceIcon(s.kind), 18)}<span>${esc(serviceName(s))}</span><button class="sw ${s.on ? 'on' : ''}" data-own data-i="${i}" role="switch" aria-checked="${s.on}" aria-label="${esc(serviceName(s))}"></button></div>`
        : s.on
          ? `<div class="row">${G.ui(serviceIcon(s.kind), 18)}<span>${esc(serviceName(s))}</span></div>`
          : `<div class="row na">${G.ui(serviceIcon(s.kind), 18)}<span>${esc(serviceName(s))}</span><em>没开放</em></div>`,
    );
    const refused = new Set(devOf(id)?.refused || []);
    const others = lan .members.filter((m) => m.state === 'joined' && m.person !== v.me).map((m) => m.person);
    const who = mine && others.length ? `<div class="label">谁能用</div><div class="people">${others.map((k) => `<button class="chip ${refused.has(k) ? 'blocked' : ''}" data-k="${esc(k)}" aria-pressed="${refused.has(k)}">${App.ball(k, refused.has(k) ? 'off' : '')}${esc(spaced(nameOf(v, k)))}</button>`).join('')}</div><div class="hint">点一下头像，就不让他用了</div>` : '';
    const addrs = (devOf(id)?.addrs || []).map((a) => a.replace(/\/\d+$/, '')).join('、');
    const body =
      (rows.length ? `<div class="label">${mine ? '开放给大家的' : '开放给你的'}</div><div class="rows">${rows.join('')}</div>` : `<p class="say">${mine ? '还没开放任何服务' : '没开放服务给你'}</p>`) +
      who +
      `<div class="card-foot"><button class="more">更多${G.ui('chevron', 14)}</button>${mine ? '<button class="out-btn">移出这个局域网</button>' : ''}</div><div class="detail"><p>${esc(addrs ? '地址 ' + addrs : '')}</p></div>`;
    const s = app.sheet(head(id, st) + body, id);
    for (const sw of s.querySelectorAll('.sw[data-own]')) {
      (sw               ).onclick = () => {
        const svc = svcs[+(sw               ).dataset.i ];
        const on = !sw.classList.contains('on');
        sw.classList.toggle('on', on);
        sw.setAttribute('aria-checked', String(on));
        ctx.op('service.set', { lan: lanID, device: id, service: { kind: svc.kind, name: svc.name, ports: svc.ports }, on }).catch((e) => {
          sw.classList.toggle('on', !on);
          app.toast(e instanceof ApiError ? e.message : '没能改，再试一次', { parent: cv, x: centerX() });
        });
      };
    }
    for (const ch of s.querySelectorAll('.chip')) {
      (ch               ).onclick = () => {
        const k = (ch               ).dataset.k          ;
        const b = !ch.classList.contains('blocked');
        ch.classList.toggle('blocked', b);
        ch.querySelector('.ball')?.classList.toggle('off', b);
        ch.setAttribute('aria-pressed', String(b));
        ctx.op('device.refuse', { device: id, person: k, refuse: b }).catch((e) => {
          ch.classList.toggle('blocked', !b);
          ch.querySelector('.ball')?.classList.toggle('off', !b);
          app.toast(e instanceof ApiError ? e.message : '没能改，再试一次', { parent: cv, x: centerX() });
        });
      };
    }
    const outBtn = s.querySelector('.out-btn')                      ;
    if (outBtn) outBtn.onclick = () => takeOut(id);
  }
  app.onDeviceClick = (id        ) => openCard(id);
  app.onSheetClose = () => (app.selected = null);

  // ---- The download card, for "this computer" (§14.7) ---------------------------------------
  function openDownload()       {
    app.selected = SELF;
    app.select(SELF);
    const s = app.sheet('<div class="dl-slot"></div>', 'download');
    const devs = v.devices.filter((d) => lan .devices.some((ld) => ld.device === d.id));
    s.querySelector('.dl-slot').replaceWith(downloadCard({ lanName: lan .name, example: pickExample(devs, v.me), email: ctx.email, onClose: () => app.closeSheet() }));
  }
  if (DEVS[SELF] && (ctx.justJoined === lanID || D.recording)) D.after(700, openDownload);

  // This computer logged in: the dashed tile fills in, with a bounce (it
  // joined, §5), and takes the device's name; the device's own tile goes.
  // The download card is done with: closed, and its contents gone.
  function doneDownload()       {
    if (app.sheetKey !== 'download') return;
    app.closeSheet();
    D.after(200, () => app.sheetEl.style.display === 'none' && app.sheetEl.replaceChildren());
  }
  function fill(realID        )       {
    const n = app.nodes[SELF];
    if (!n) return;
    const real = app.nodes[realID];
    const d = DEVS[realID];
    doneDownload();
    if (real) {
      // The real device takes the dashed tile's place.
      app.move(realID, n.x, n.y);
      real.el.querySelector('.tile').animate([{ transform: 'none' }, { transform: 'scale(1.08)', offset: 0.4 }, { transform: 'none' }], { duration: 560, easing: 'cubic-bezier(0.3, 1.2, 0.5, 1)' });
    }
    app.dropNode(SELF);
    n.el.animate([{ opacity: 1 }, { opacity: 0 }], { duration: 160, easing: 'ease-out', fill: 'forwards' });
    D.after(170, () => n.el.remove());
    delete DEVS[SELF];
    summary();
    if (d) app.toast(`${d.name}加进来了`, { dev: { type: d.type, owner: v.me } });
  }
  // This computer logged in but is not in this LAN yet: the dashed tile
  // goes, and the tray offers it.
  function dropSelf()       {
    const n = app.dropNode(SELF);
    delete DEVS[SELF];
    doneDownload();
    if (n) {
      n.el.animate([{ opacity: 1, transform: 'none' }, { opacity: 0, transform: 'scale(0.9)' }], { duration: 220, easing: 'ease-in', fill: 'forwards' });
      D.after(230, () => n.el.remove());
    }
  }

  // ---- The server's word ----------------------------------------------------------------
  const off = ctx.onView((nv) => {
    follow(nv);
    v = nv;
    lan = nv.lans.find((l) => l.id === lanID);
    if (!lan) return;
    const ids = readDevs();
    // New devices: they come in from the edge and land; gone ones fade.
    for (const id of ids) {
      if (app.nodes[id]) {
        app.setSub(id, statusOf(id));
        app.nodes[id].el.classList.toggle('off', !!DEVS[id].off);
        continue;
      }
      if (DEVS[id].owner === v.me && DEVS[SELF]) {
        DEVS[id].x = DEVS[SELF].x;
        DEVS[id].y = DEVS[SELF].y;
        mount(id, { keep: false });
        fill(id);
        continue;
      }
      if (!DEVS[id].x) Object.assign(DEVS[id], { x: cv.clientWidth - 120, y: 120 });
      const n = mount(id, { keep: false });
      app.settle(n, app.freeSpot(id, { x: DEVS[id].x, y: DEVS[id].y }), D.Bouncy);
    }
    for (const id of Object.keys(DEVS)) {
      if (id === SELF || ids.includes(id) || !app.nodes[id]) continue;
      if (landing > 0) continue;
      const n = app.dropNode(id);
      delete DEVS[id];
      if (n) {
        n.el.animate([{ opacity: 1, transform: 'none' }, { opacity: 0, transform: 'scale(0.9)' }], { duration: 220, easing: 'ease-in', fill: 'forwards' });
        D.after(230, () => n.el.remove());
      }
    }
    if (DEVS[SELF] && !noDevice()) dropSelf();
    // The tray follows my devices that are not here.
    const spare = readSpare();
    for (const id of spare) if (!miniOf(id) && !app.nodes[id]) addMini(id);
    for (const m of [...tray.querySelectorAll('.mini')]                 ) if (!spare.includes(m.dataset.id          ) && !landing) collapseMini(m);
    summary();
  });
  return () => {
    off();
    stopPlace();
    clear();
    tray.remove();
  };
}
