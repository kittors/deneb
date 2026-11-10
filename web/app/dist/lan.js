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
import { Stack, ballsOf } from './stack.js';
import { can, seatsFull, seatLine, fullFor, whyNot, shareTo, shareSection, wireShare, personCard, wireGiven, seatCard, wireSeats, seatState, forget, freed } from './sharecard.js';
import { hold, failed } from './ops.js';
import { D } from './clock.js';
import { ApiError } from './api.js';
import { downloadCard, pickExample } from './download.js';
import { phoneLan } from './phone.js';
                                     
                                         
                                                                    

// A device as the canvas shows it. pending: "this computer", not in yet;
// wait: it waits for the LAN's owner ("等你同意", "等阿杰同意", §6.7);
// note: what its second line says instead of online (§6.8: "主人不让你
// 用", "登录过期了", "需要重新登录"); renew: the amber dot that renews it.
                                                                                                                                                               
                                   
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
  // Not in yet: the owner's ball and a word, until they let me in (§6.7).
  if (lan.pending) return waiting(app, ctx, lanID);
  const P = App.PEOPLE;
  const cv              = app.canvas;
  const H = () => cv.clientHeight;
  const BAND = 104;
  const invite = app.inviteBtn                      ;
  // Where this page's toasts go: over the canvas, under the devices' middle.
  const at = { parent: cv, get x() { return centerX(); } };
  const at0 = () => ({ parent: cv, x: centerX() });
  const DEVS                      = {};
  const SPARE                                                 = {};
  const devOf = (id        ) => v.devices.find((d) => d.id === id)                          ;
  const lanDev = (id        ) => lan .devices.find((d) => d.device === id);
  const statusOf = (id        ) => {
    const d = DEVS[id];
    if (d.pending) return '装上 Deneb 就能加进来';
    return d.wait || d.note || d.off || '在线';
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
    const n = app.device(id, { ...d, sub: statusOf(id), ...o });
    n.el.setAttribute('role', 'button');
    n.el.setAttribute('tabindex', '0');
    n.el.addEventListener('keydown', (e               ) => e.key === 'Enter' && n.el.click());
    dress(id);
    return n;
  }
  // dress puts on a node what its state shows: dashed while not in (this
  // computer, or waiting for the owner), the × of one that waits (the
  // owner turns it away; its owner takes it back), grey while offline or
  // its login has run out, and the amber dot that renews a login (§6.8).
  function dress(id        )       {
    const n = app.nodes[id], d = DEVS[id];
    if (!n || !d) return;
    const el              = n.el;
    const dashed = !!(d.pending || d.wait);
    el.classList.toggle('pending', dashed);
    el.classList.toggle('wait', !!d.wait);
    el.classList.toggle('off', !dashed && !!(d.off || d.note === '登录过期了'));
    const tile = el.querySelector('.tile')               ;
    const dash = tile.querySelector('svg.dash');
    if (dashed && !dash) tile.insertAdjacentHTML('afterbegin', DASH);
    if (!dashed && dash) dash.remove();
    let x = el.querySelector(':scope > .x')                      ;
    if (d.wait && !x) {
      x = App.h(`<button class="x" type="button" aria-label="${lan .owner === v.me ? '拒绝' : '撤回'}">${G.ui('close', 10)}</button>`)               ;
      x.addEventListener('pointerdown', (e) => e.stopPropagation());
      x.addEventListener('click', (e) => {
        e.stopPropagation();
        turnAway(id);
      });
      el.appendChild(x);
    } else if (!d.wait && x) x.remove();
    let r = el.querySelector(':scope > .renew')                      ;
    if (d.renew && !r) {
      r = App.h(`<button class="renew" type="button" aria-label="续上" title="续上"></button>`)               ;
      r.addEventListener('pointerdown', (e) => e.stopPropagation());
      r.addEventListener('click', (e) => {
        e.stopPropagation();
        renew(id);
      });
      el.appendChild(r);
    } else if (!d.renew && r) r.remove();
    app.setSub(id, statusOf(id));
    el.setAttribute('aria-label', `${d.name}，${statusOf(id)}`);
  }
  // Devices waiting for the owner that the owner let in, or turned away,
  // while the toast offers undo (they go to the server as it goes).
  const approving = new Set        (), rejecting = new Set        ();
  // People waiting whom the owner let in or turned away, and the LAN's mode
  // switched, while their toasts offer undo.
  const lettingIn = new Set        (), turningAway = new Set        ();
  let modeHeld                = null;
  function readDevs()           {
    const ids           = [];
    const now = Date.now();
    for (const ld of lan .devices) {
      const d = devOf(ld.device);
      if (!d || (ld.pending && rejecting.has(d.id))) continue;
      // Switched to "成员只用分到的设备": the members' devices are out already.
      if (modeHeld === 'share' && d.owner !== lan .owner) continue;
      if (!ld.pending) approving.delete(d.id);
      ids.push(d.id);
      const waits = ld.pending && !approving.has(d.id);
      const mine = d.owner === v.me;
      const login = d.login;
      const expired = mine && !!login?.expired;
      const renewing = mine && !expired && !!login?.renewFrom && Date.parse(login.renewFrom) <= now;
      DEVS[d.id] = Object.assign(DEVS[d.id] || { x: 0, y: 0 }, {
        type: glyphOf(d.kind),
        owner: d.owner,
        name: d.displayName || d.name,
        off: d.online ? undefined : ago(d.lastSeen),
        wait: waits ? (lan .owner === v.me ? '等你同意' : spaced(`等${nameOf(v, lan .owner)}同意`)) : undefined,
        note: d.refusesMe ? '主人不让你用' : expired ? '登录过期了' : renewing ? '需要重新登录' : undefined,
        renew: expired || renewing,
      });
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

  // ---- The header: the balls, the line under them, "邀请朋友" ------------------------------
  const stack = new Stack(app, () => v, {
    person: (id) => openPerson(id),
    approve: (id) => letIn(id),
    reject: (id) => turnAwayPerson(id),
    unInvite: (b) => {
      ctx.op('invite.revoke', { lan: lanID, person: b.id, invite: b.invite }).catch((e) => failed(app, e, '没能收回，再试一次', at));
      app.toast(spaced(`已收回给${nameOf(v, b.id)}的邀请`), at);
    },
    inviteTo: (id, lan) => inviteWhat(id, lan),
    invite: (id, lan) => inviteBy(id, lan),
  });
  // The line under the balls and "邀请朋友" (§6.11): while one of my devices
  // is in the hand, what letting it go on a ball does; else, my seats
  // nearly full, how many more people can come.
  let aimText = '', aimOk = false;
  function headLine()       {
    stack.say(aimText || seatLine(v), !!aimText && aimOk);
    invite?.classList.toggle('full', seatsFull(v));
  }
  function summary()       {
    const ids = Object.keys(DEVS).filter((id) => !DEVS[id].pending && !DEVS[id].wait);
    const on = ids.filter((id) => !DEVS[id].off).length;
    const people = lan .members.filter((m) => m.state === 'joined');
    const vv = view();
    stack.draw(ballsOf(vv, vv.lans.find((l) => l.id === lanID) ));
    headLine();
    if (!ids.length) app.setTitle(lan .name, `${people.length} 个人，还没有设备`, 'off');
    else if (on === ids.length) app.setTitle(lan .name, `${people.length} 个人，${ids.length} 台设备都在线`, 'ok');
    else app.setTitle(lan .name, `${people.length} 个人，${ids.length} 台设备，${on} 台在线`, 'off');
  }
  summary();

  // ---- The header's button: an invite link, copied (DESIGN.md §5 邀请朋友).
  // My seats full, it is a dashed outline and shows the seats instead.
  async function inviteFriends()                {
    try {
      const r = await ctx.op('lan.invite', { lan: lanID });
      const url = r.result?.invite?.url;
      if (url) await navigator.clipboard.writeText(url).catch(() => {});
      app.toast(url ? '已复制邀请链接，发给朋友就行' : '邀请发出去了', at);
    } catch (e) {
      failed(app, e, '没能做成，再试一次', at);
    }
  }
  if (invite) {
    invite.style.display = '';
    invite.onclick = () => (seatsFull(v) ? openSeats('') : void inviteFriends());
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
        g.style.transition = 'transform 0.22s cubic-bezier(0.3, 1.3, 0.5, 1)';
        app.dragLayer.appendChild(g);
        m.classList.add('ghost');
        holdShare(id);
      },
      move: (p    ) => {
        const q = app.clampTile({ kind: 'device' }, p);
        // One of mine may go up into the header, to the balls (§6.11).
        if (sharing === id) q.y = Math.max(-46, Math.min(q.y, p.y));
        g .style.left = q.x + 'px';
        g .style.top = q.y + 'px';
        if (sharing === id) {
          const a = aimAt(p);
          g .style.transform = a.up ? `scale(${a.who ? 0.38 : 0.6})` : '';
          (g .querySelector('.name')               ).style.opacity = a.up ? '0' : '';
        }
      },
      end: async (p           , cancel         ) => {
        const at = { x: parseFloat(g .style.left), y: parseFloat(g .style.top) };
        const a = sharing === id ? endShare() : null;
        if (a && a.up && !cancel && p) {
          // Let go up in the header: given to whoever it is over, when it
          // can be; it goes back to the tray either way.
          if (a.ok) shareTo(app, ctx, v, devOf(id) , a.who, { ...at0(), ball: stack.ball(a.who) });
          g .style.transition = '';
          const s0 = a.who ? 0.38 : 0.6;
          await App.tween(320, (k        ) => {
            g .style.left = at.x + (start .x - at.x) * k + 'px';
            g .style.top = at.y + (start .y - at.y) * k + 'px';
            g .style.transform = `scale(${s0 + (40 / 60 - s0) * k})`;
          });
          g .remove();
          m.classList.remove('ghost');
          return;
        }
        if (!cancel && p && at.y < H() - BAND) {
          g .remove();
          m.classList.remove('ghost');
          bringIn(id, at, { from: at, fromScale: 1 });
          return;
        }
        g .classList.remove('dragging');
        g .style.transition = '';
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
  // One of my devices in the hand may go up into the header, to the balls,
  // to be given to someone (§6.11): smaller there, its name gone.
  app.onNodeStart = (n                              ) => n.kind === 'device' && holdShare(n.id);
  app.clampTile = function (n                , p    ) {
    const q = App.prototype.clampTile.call(this, n, p);
    if (sharing && n.id === sharing) q.y = Math.max(-46, Math.min(q.y, p.y));
    return q;
  };
  app.onNodeTarget = (n                                                                   , p     ) => {
    if (sharing === n.id && p) {
      const a = aimAt(p);
      if (a.up) return { kind: 'header', shrink: a.who ? 0.38 : 0.6 };
    }
    return removable(n) && overTray(n) ? { kind: 'tray', shrink: 40 / 60, keep: true } : null;
  };
  app.onNodeDrop = (n                          , t                  ) => {
    if (t.kind === 'header') {
      // Given to whoever it is over, when it can be; it goes back home.
      const a = lastAim;
      if (a && a.ok) shareTo(app, ctx, v, devOf(n.id) , a.who, { ...at0(), ball: stack.ball(a.who) });
      app.settle(n, n.home, D.Smooth);
      return;
    }
    if (t.kind !== 'tray') return false;
    takeOut(n.id);
  };
  app.onNodeDragging = (n                                                                                    , _p    , t                         ) => {
    if (n.kind !== 'device') return;
    const near = nearTray(n), over = removable(n) && overTray(n);
    const up = !!t && t.kind === 'header';
    if (removable(n)) {
      setMode(over ? 'armed' : near ? 'offer' : 'idle', over ? '松手，移出局域网' : near ? '拖到这里，移出局域网' : IDLE);
      const nm = n.el.querySelector('.name')               ;
      if (nm) nm.style.opacity = over || up ? '0' : '';
    } else setMode(near ? 'refuse' : 'idle', near ? reason(n) : IDLE);
  };
  app.onNodeEnd = (n                                 , t                         , cancel         ) => {
    if (sharing === n.id) endShare();
    if (t && t.kind === 'tray' && !cancel) return;
    const nm = n.el.querySelector('.name')               ;
    if (nm) nm.style.opacity = '';
    setMode('idle', IDLE);
  };

  // ---- Sharing: one of my devices onto a ball (§6.11) -----------------------------------
  // While one is in the hand, the stack spreads; over a ball, the line says
  // whether letting go gives it to them, or why not.
  let sharing = '';
  let lastAim                                                   = null;
  const shareable = (id        ) => {
    const d = devOf(id);
    return !!d && id !== SELF && can(v, 'share') && d.owner === v.me && !DEVS[id]?.wait;
  };
  const targets = () => new Set(lan .members.filter((m) => m.state === 'joined' && m.person !== v.me).map((m) => m.person));
  function holdShare(id        )       {
    if (!shareable(id) || !targets().size) return;
    sharing = id;
    lastAim = null;
    stack.spread(true, targets());
    aimText = '放到谁身上，就只给谁用这一台';
    aimOk = false;
    headLine();
  }
  function aimAt(p    )                                            {
    const up = p.y < 0;
    const b = stack.ballAt(p, targets());
    const why = b ? whyNot(v, devOf(sharing) , b.id) : '';
    const a = { up, who: b ? b.id : '', ok: !!b && !why };
    stack.aim(a.who, a.ok);
    aimText = !b ? '放到谁身上，就只给谁用这一台' : a.ok ? spaced(`松手，只给${nameOf(v, b.id)}用这一台`) : why;
    aimOk = a.ok;
    headLine();
    return (lastAim = a);
  }
  function endShare()                                                   {
    sharing = '';
    stack.spread(false);
    aimText = '';
    aimOk = false;
    headLine();
    return lastAim;
  }

  // ---- Waiting for the owner (§6.7) ------------------------------------------------------
  // The owner lets a device in (a click on it) or turns it away (its ×);
  // its own owner takes it back (the ×). The page shows it at once; the
  // server hears as the toast goes, so undo needs no server.
  function letInDevice(id        )       {
    const d = DEVS[id];
    if (!d) return;
    approving.add(id);
    d.wait = undefined;
    dress(id);
    const n = app.nodes[id];
    n?.el.querySelector('.tile').animate([{ transform: 'scale(0.9)' }, { transform: 'scale(1.06)', offset: 0.5 }, { transform: 'none' }], { duration: 560, easing: 'cubic-bezier(0.3, 1.2, 0.5, 1)' });
    summary();
    const back = () => {
      approving.delete(id);
      sync(v);
    };
    hold(app, ctx, [['device.approve', { lan: lanID, device: id }]], spaced(`已同意${d.name}加进来`), { ...at0(), dev: { type: d.type, owner: d.owner }, undo: back, failed: back });
  }
  function turnAway(id        )       {
    const d = DEVS[id];
    if (!d) return;
    const mine = d.owner === v.me;
    rejecting.add(id);
    if (app.selected === id) app.closeSheet();
    const n = app.dropNode(id);
    delete DEVS[id];
    n?.el.animate([{ opacity: 1, transform: 'none' }, { opacity: 0, transform: 'scale(0.6)' }], { duration: 240, easing: 'cubic-bezier(0.4, 0, 1, 1)', fill: 'forwards' });
    D.after(250, () => n?.el.remove());
    summary();
    const back = () => {
      rejecting.delete(id);
      sync(v);
    };
    const op                   = mine ? ['lan.removeDevice', { lan: lanID, device: id }] : ['device.reject', { lan: lanID, device: id }];
    hold(app, ctx, [op         ], spaced((mine ? '已撤回' : '已拒绝') + d.name), { ...at0(), dev: { type: d.type, owner: d.owner }, undo: back, failed: back });
  }
  // Someone waiting to come in: a click lets them in, unless my seats are
  // full and they hold none: then the seats' card, to free one first.
  function letIn(who        )       {
    if (fullFor(v, who)) return openSeats(who);
    lettingIn.add(who);
    stack.draw(ballsOf(view(), lan ));
    const back = () => {
      lettingIn.delete(who);
      sync(v);
    };
    hold(app, ctx, [['member.approve', { lan: lanID, person: who }]], spaced(`已同意${nameOf(v, who)}加入`), { ...at0(), undo: back, failed: back });
  }
  function turnAwayPerson(who        )       {
    turningAway.add(who);
    stack.draw(ballsOf(view(), lan ));
    const back = () => {
      turningAway.delete(who);
      sync(v);
    };
    hold(app, ctx, [['member.reject', { lan: lanID, person: who }]], spaced(`已拒绝${nameOf(v, who)}`), { ...at0(), undo: back, failed: back });
  }
  // view is v with what waits for the toast already done.
  function view()           {
    if (!lettingIn.size && !turningAway.size) return v;
    const lans = v.lans.map((l) =>
      l.id !== lanID ? l : { ...l, members: l.members.filter((m) => !turningAway.has(m.person)).map((m) => (lettingIn.has(m.person) && m.state === 'pending' ? { ...m, state: 'joined'          } : m)) },
    );
    return { ...v, lans };
  }

  // ---- A member onto another LAN's row: invited there (§6.7) --------------------------------
  function inviteWhat(who        , to        )                               {
    const l = v.lans.find((x) => x.id === to);
    if (!l || l.pending) return { why: '这个局域网还没进去', say: '' };
    const name = nameOf(v, who);
    if (l.members.some((m) => m.person === who)) return { why: '已经在了', say: '' };
    if (l.owner === v.me && fullFor(v, who)) return { why: `${seatsOf()} 个位置都用上了`, say: '' };
    return { why: '', say: spaced(`松手，邀请${name}加入${l.name}`) };
  }
  const seatsOf = () => v.seats?.limit ?? 0;
  function inviteBy(who        , to        )       {
    const l = v.lans.find((x) => x.id === to);
    if (!l) return;
    ctx.op('lan.invite', { lan: to, person: who }).catch((e) => failed(app, e, '没能邀请，再试一次', at0()));
    app.toast(spaced(`已邀请${nameOf(v, who)}加入${l.name}`), at0());
  }

  // ---- Renewing a login (§6.8): the amber dot, or the card's button ---------------------------
  function renew(id        )       {
    const d = DEVS[id];
    if (!d) return;
    d.renew = false;
    d.note = undefined;
    dress(id);
    ctx.op('device.renew', { device: id }).catch((e) => failed(app, e, '没能续上，再试一次', at0()));
    app.toast('已续上', { ...at0(), dev: { type: d.type, owner: d.owner } });
    if (app.selected === id) openCard(id);
  }

  // ---- The other cards: a person's, the seats', the LAN's ----------------------------------
  // Each is drawn again from every new view while it is open (its key in
  // app.sheetKey), so what it shows follows the server.
  function openPerson(who        )       {
    forget();
    app.selected = 'person:' + who;
    for (const k in app.nodes) app.nodes[k].el.classList.remove('sel');
    drawPerson(who);
  }
  function drawPerson(who        )       {
    const role = lan .owner === who ? `${lan .name}的拥有者` : `${lan .name}的成员`;
    const s = app.sheet(personCard(v, who, role), 'person:' + who);
    wireShare(app, ctx, () => v, s, () => drawPerson(who), at0());
    wireGiven(ctx, s);
  }
  function openSeats(forWho        )       {
    seatState.freeing = false;
    seatState.forWho = forWho;
    app.selected = 'seats';
    drawSeats();
  }
  function drawSeats()       {
    const s = app.sheet(seatCard(v, true), 'seats');
    wireSeats(app, ctx, () => v, s, () => {
      drawSeats();
      headLine();
    }, () => void inviteFriends(), at0());
  }
  // The LAN's card (a click on its name, §6.7): its owner and members; the
  // owner's two switches of who waits, and "成员只用分到的设备" (§6.11).
  function openLAN()       {
    app.selected = 'lan';
    for (const k in app.nodes) app.nodes[k].el.classList.remove('sel');
    drawLAN();
  }
  function drawLAN()       {
    const l = lan ;
    const mine = l.owner === v.me;
    const joined = l.members.filter((m) => m.state === 'joined').map((m) => m.person);
    const mode = modeHeld || l.mode || 'lan';
    let html =
      `<div class="top lan-top"><div class="lan-mark"><span class="dots">${joined.slice(0, 3).map((k) => App.ball(k)).join('')}</span></div>` +
      `<div>${mine && can(v, 'rename') ? `<button class="name-edit" type="button" title="改名">${esc(spaced(l.name))}</button>` : `<b>${esc(spaced(l.name))}</b>`}<span>${esc(spaced('拥有者 · ' + nameOf(v, l.owner)))}</span></div>` +
      `<button class="icon-btn sheet-close" title="关闭" aria-label="关闭">${G.ui('close', 16)}</button></div>` +
      `<div class="label">成员</div><div class="pchips">${joined.map((k) => `<span class="pchip still">${App.ball(k)}<span class="pn">${esc(spaced(nameOf(v, k)))}</span></span>`).join('')}</div>`;
    if (mine && can(v, 'approve'))
      html +=
        `<div class="label">新来的</div><div class="rows">` +
        `<div class="row"><span>新设备要我同意</span><button class="sw ${l.approveDevices ? 'on' : ''}" data-own data-ap="devices" role="switch" aria-checked="${!!l.approveDevices}" aria-label="新设备要我同意"></button></div>` +
        `<div class="row"><span>新成员要我同意</span><button class="sw ${l.approveMembers ? 'on' : ''}" data-own data-ap="members" role="switch" aria-checked="${!!l.approveMembers}" aria-label="新成员要我同意"></button></div>` +
        `</div><div class="hint">打开后，新来的要你点一下才进来</div>`;
    if (mine && can(v, 'shareMode'))
      html +=
        `<div class="label">设备共享</div><div class="rows"><div class="row"><span>成员只用分到的设备</span><button class="sw ${mode === 'share' ? 'on' : ''}" data-own data-mode role="switch" aria-checked="${mode === 'share'}" aria-label="成员只用分到的设备"></button></div></div>` +
        `<div class="hint">${mode === 'share' ? '关掉后，成员要自己把设备拖进来' : '打开后，成员的设备移出去，你把设备拖到成员身上分给他'}</div>`;
    const s = app.sheet(html, 'lan');
    for (const sw of s.querySelectorAll('.sw[data-ap]')                           )
      sw.onclick = () => {
        const on = !sw.classList.contains('on');
        sw.classList.toggle('on', on);
        sw.setAttribute('aria-checked', String(on));
        ctx.op(sw.dataset.ap === 'devices' ? 'lan.setApproveDevices' : 'lan.setApproveMembers', { lan: lanID, on }).catch((e) => {
          sw.classList.toggle('on', !on);
          failed(app, e, '没能改，再试一次', at0());
        });
      };
    const msw = s.querySelector('.sw[data-mode]')                      ;
    if (msw)
      msw.onclick = () => {
        const to = (modeHeld || l.mode || 'lan') === 'share' ? 'lan' : 'share';
        modeHeld = to;
        sync(v);
        const back = () => {
          modeHeld = null;
          sync(v);
        };
        hold(app, ctx, [['lan.setMode', { lan: lanID, mode: to }]], `「${l.name}」` + (to === 'share' ? '改成成员只用分到的设备' : '改回大家的设备互相看得见'), { ...at0(), undo: back, failed: back });
      };
    const nameBtn = s.querySelector('.name-edit')                      ;
    if (nameBtn) nameBtn.onclick = () => rename(nameBtn, l.name, (name) => ctx.op('lan.rename', { lan: lanID, name }));
  }
  // rename turns a name into a field in place (§6.9): Enter or a click away
  // keeps it, Esc goes back.
  function rename(el             , name        , save                                    )       {
    const input = App.h(`<input class="name-input" type="text" maxlength="40" aria-label="名字">`)                    ;
    input.value = name;
    el.replaceWith(input);
    input.focus();
    input.select();
    let done = false;
    const end = (keep         ) => {
      if (done) return;
      done = true;
      const to = input.value.trim();
      if (keep && to && to !== name) save(to).catch((e) => failed(app, e, '没能改名，再试一次', at0()));
      redrawCard();
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
  // redrawCard draws the open card again from the latest view.
  function redrawCard()       {
    const k                = app.sheetKey;
    if (!k || app.sheetEl.style.display === 'none' || app.sheetEl.__closing) return;
    if (k === 'lan') drawLAN();
    else if (k === 'seats') drawSeats();
    else if (k.startsWith('person:')) {
      const who = k.slice(7);
      if (lan .members.some((m) => m.person === who && m.state === 'joined')) drawPerson(who);
      else app.closeSheet();
    } else if (DEVS[k] && !DEVS[k].pending) openCard(k);
  }
  const title = app.win.querySelector('.head .title')               ;
  title.classList.add('clickable');
  title.onclick = () => (app.sheetKey === 'lan' ? app.closeSheet() : openLAN());

  // ---- The card ---------------------------------------------------------------------
  // The desktop's card, but it says whether the device is online, not how
  // this computer reaches it; someone else's services cannot be opened from
  // a browser, so it lists them.
                                                                  
  const head = (id        , st        ) => {
    const d = DEVS[id];
    const mine = d.owner === v.me;
    const name = mine && can(v, 'rename') ? `<button class="name-edit" type="button" title="改名">${esc(spaced(d.name))}</button>` : `<b>${esc(spaced(d.name))}</b>`;
    return (
      `<div class="top"><div class="tile-s"><div class="tile" style="--c:${P[d.owner].c}">${G.device(d.type, 20)}</div></div>` +
      `<div>${name}<span>${mine ? '我的设备' : esc(spaced(nameOf(v, d.owner) + '的设备'))}</span></div>` +
      `<button class="icon-btn sheet-close" title="关闭" aria-label="关闭">${G.ui('close', 16)}</button></div>` +
      `<div class="status"><span class="badge" style="--c:${st.c}">${G.ui('online', 16)}</span><div>${esc(st.t)}<small>${esc(st.s)}</small></div>${st.act ? `<button class="btn ghost sm st-act" type="button">${esc(spaced(st.act))}</button>` : ''}</div>`
    );
  };
  // statusOfCard is the card's status block (§6.8): one sentence of where
  // it stands, and what can be done about it, as a button.
  function statusOfCard(id        )         {
    const d = DEVS[id], dv = devOf(id);
    const mine = d.owner === v.me;
    const owner = nameOf(v, d.owner);
    const grey = '#a1a1aa';
    if (d.wait) return mine ? { c: grey, t: d.wait, s: spaced(`${nameOf(v, lan .owner)}同意以后，它才连得进来`) } : { c: grey, t: '等你同意', s: '点一下设备块就是同意' };
    if (d.note === '登录过期了') return mine ? { c: grey, t: '登录过期了', s: '续上就能连', act: '续上' } : { c: grey, t: '登录过期了', s: spaced(`要等${owner}在那台设备上续上`), act: `提醒${owner}` };
    if (d.note === '主人不让你用') return { c: grey, t: '主人不让你用', s: spaced(`要用的话，问问${owner}`), act: `问问${owner}` };
    if (d.off) return { c: grey, t: d.off, s: '它开机、连上网就会回来' };
    if (d.note === '需要重新登录' && dv?.login?.expiresAt) {
      const days = Math.max(1, Math.ceil((Date.parse(dv.login.expiresAt) - Date.now()) / 86400e3));
      return { c: '#F59E0B', t: '需要重新登录', s: `还有 ${days} 天到期`, act: '续上' };
    }
    return { c: '#22C55E', t: '在线', s: mine ? '别人能用你开放的服务' : '在装了 Deneb 的电脑上就能用' };
  }
  // A ready sentence to send, copied (§6.8 提醒阿杰、问问阿杰).
  async function tell(text        , who        )                {
    await navigator.clipboard.writeText(text).catch(() => {});
    app.toast(spaced(`已复制，发给${who}就行`), at0());
  }
  function openCard(id        )       {
    const d = DEVS[id];
    if (!d) return;
    if (d.pending) return openDownload();
    // The owner's click on one that waits lets it in (§6.7).
    if (d.wait && lan .owner === v.me) return letInDevice(id);
    if (app.selected !== id) forget();
    app.selected = id;
    app.select(id);
    const mine = d.owner === v.me;
    const svcs                 = lanDev(id)?.services || [];
    const st = statusOfCard(id);
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
    const dv = devOf(id);
    const share = mine && !d.wait && dv ? shareSection(v, dv, [...targets()]) : '';
    const body =
      (rows.length ? `<div class="label">${mine ? '开放给大家的' : '开放给你的'}</div><div class="rows">${rows.join('')}</div>` : `<p class="say">${mine ? '还没开放任何服务' : '没开放服务给你'}</p>`) +
      who +
      share +
      `<div class="card-foot"><button class="more">更多${G.ui('chevron', 14)}</button>${mine ? `<button class="out-btn">${d.wait ? '不加了' : '移出这个局域网'}</button>` : ''}</div><div class="detail"><p>${esc(addrs ? '地址 ' + addrs : '')}</p></div>`;
    const s = app.sheet(head(id, st) + body, id);
    if (share) wireShare(app, ctx, () => v, s, () => openCard(id), at0());
    const act = s.querySelector('.st-act')                      ;
    if (act)
      act.onclick = () => {
        const owner = nameOf(v, d.owner);
        if (st.act === '续上') renew(id);
        else if (st.t === '登录过期了') void tell(spaced(`你的${d.name}在 Deneb 里登录过期了，打开 Deneb 点一下续上就好`), owner);
        else void tell(spaced(`能让我用一下你的${d.name}吗？在 Deneb 里点一下就行`), owner);
      };
    const nameBtn = s.querySelector('.name-edit')                      ;
    if (nameBtn) nameBtn.onclick = () => rename(nameBtn, d.name, (name) => ctx.op('device.rename', { device: id, name }));
    for (const sw of s.querySelectorAll('.sw[data-own][data-i]')) {
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
    if (outBtn) outBtn.onclick = () => (d.wait ? turnAway(id) : takeOut(id));
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
  function sync(nv          )       {
    follow(nv);
    v = nv;
    lan = nv.lans.find((l) => l.id === lanID);
    if (!lan) return void ctx.go('/', true);
    // Waiting again (or still): the page of waiting.
    if (lan.pending) return void ctx.go('/lan/' + encodeURIComponent(lanID), true);
    freed(nv);
    if (modeHeld && lan.mode === modeHeld) modeHeld = null;
    for (const who of lettingIn) if (lan.members.some((m) => m.person === who && m.state === 'joined')) lettingIn.delete(who);
    for (const who of turningAway) if (!lan.members.some((m) => m.person === who)) turningAway.delete(who);
    const waited = new Set(Object.keys(DEVS).filter((id) => DEVS[id].wait));
    const ids = readDevs();
    // New devices: they come in from the edge and land; gone ones fade.
    for (const id of ids) {
      if (app.nodes[id]) {
        // Let in by the owner (from elsewhere too): it fills, with a spring.
        if (waited.has(id) && !DEVS[id].wait && !approving.has(id)) app.nodes[id].el.querySelector('.tile').animate([{ transform: 'scale(0.9)' }, { transform: 'scale(1.06)', offset: 0.5 }, { transform: 'none' }], { duration: 560, easing: 'cubic-bezier(0.3, 1.2, 0.5, 1)' });
        dress(id);
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
      // One of mine the owner turned away: it says so (§6.7), never goes
      // without a word.
      if (waited.has(id) && DEVS[id].owner === v.me && !rejecting.has(id)) app.toast(spaced(`${nameOf(v, lan.owner)}没有同意${DEVS[id].name}加入「${lan.name}」`), at0());
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
    redrawCard();
  }
  const off = ctx.onView(sync);
  // ?record=<scene>: the scene's script (DESIGN §11), recorded on this page.
  if (D.recording) {
    const recs                                         = {
      wait: () => import('./rec-lan-wait.js'),
      share: () => import('./rec-lan-share.js'),
      seats: () => import('./rec-lan-seats.js'),
      drag: () => import('./rec-lan-drag.js'),
    };
    const which = new URLSearchParams(location.search).get('record') || '';
    if (recs[which]) recs[which]().then(() => ((window                                      ).__ready = true));
  }
  return () => {
    off();
    stopPlace();
    clear();
    tray.remove();
  };
}

// waiting is a LAN I asked to join and wait to be let into (§6.7): its row
// in the sidebar is grey, "等同意"; its page, the owner's ball and a word.
// Let in, the LAN's own page takes its place.
function waiting(app                          , ctx     , lanID        )             {
  let v = ctx.view();
  const lan = v.lans.find((l) => l.id === lanID)           ;
  const owner = nameOf(v, lan.owner);
  app.setTitle(lan.name, spaced(`等${owner}同意`), 'off');
  const el = App.h(`<div class="waiting" data-k="waiting">${App.ball(lan.owner, 'big')}<p>${esc(spaced(`等${owner}同意你加入「${lan.name}」`))}</p></div>`)               ;
  app.canvas.appendChild(el);
  const off = ctx.onView((nv) => {
    follow(nv);
    v = nv;
    const l = nv.lans.find((x) => x.id === lanID);
    if (!l) ctx.go('/', true);
    else if (!l.pending) ctx.go('/lan/' + encodeURIComponent(lanID), true);
  });
  return () => {
    off();
    el.remove();
    clear();
  };
}
