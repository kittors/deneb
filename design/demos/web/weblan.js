// The LAN on the web (DESIGN.md §14.4): the desktop's canvas without "this
// computer". A browser is not in the LAN, so nothing here is drawn from
// it: no device in the middle as the desktop has, no links, no relay.
// Each device says whether it is online. My devices sit in the middle and
// the others' around them, where the desktop puts them. The tray holds my
// devices that are not in this LAN, and works as the desktop's (§6.5).
// When I have no device here yet, the middle holds "this computer" as a
// dashed tile, drawn as a device waiting for approval (§6.7); a click on
// it opens the download card (§14.7).
//
// Taken from design/demos/v3/lan.js, with the links, the relay and this
// computer left out.
(function () {
  window.WebLan = function (app, cfg = {}) {
    const P = App.PEOPLE;
    const $ = (s, r = document) => r.querySelector(s);
    const ME = cfg.me || 'me';
    const LAN = cfg.lan || '周末联机';
    const DEVS = cfg.devs;
    const SPARE = cfg.spare || {};
    const SERVICES = {
      laptop: [['screen', '远程桌面', true], ['folder', '共享文件夹', false]],
      desktop: [['screen', '远程桌面', true], ['globe', '网页', false]],
      console: [['globe', '游戏服务器', true]],
      nas: [['folder', '共享文件夹', true], ['screen', '远程桌面', false], ['globe', '网页', true]],
      mini: [['screen', '远程桌面', true], ['terminal', '命令行', true]],
    };
    const people = cfg.people || ['me', 'mia', 'jie'];
    const refused = {};
    const isRefused = (id, k) => !!(refused[id] && refused[id][k]);
    const nameOf = (k) => (k === ME ? '我' : P[k].full || P[k].name);
    const whose = (id) => nameOf(DEVS[id].owner);
    const statusOf = (id) => {
      const d = DEVS[id];
      if (d.pending) return '装上 Deneb 就能加进来';
      if (d.joining) return '正在加进来…';
      return d.off || '在线';
    };
    const DASH = `<svg class="dash" viewBox="0 0 60 60" width="60" height="60" aria-hidden="true"><rect x="0.75" y="0.75" width="58.5" height="58.5" rx="17.25" fill="none" stroke="currentColor" stroke-width="1.5" stroke-dasharray="4.5 3.5"/></svg>`;
    const mount = (id, o = {}) => {
      const d = DEVS[id];
      const n = app.device(id, { ...d, sub: statusOf(id), cls: [d.pending ? 'pending' : '', d.off ? 'off' : ''].join(' '), ...o });
      if (d.pending) n.el.querySelector('.tile').insertAdjacentHTML('afterbegin', DASH);
      return n;
    };
    for (const id in DEVS) mount(id);

    function summary() {
      const ids = Object.keys(DEVS).filter((id) => !DEVS[id].pending);
      const on = ids.filter((id) => !DEVS[id].off).length;
      if (on === ids.length) app.setTitle(LAN, `${people.length} 个人，${ids.length} 台设备都在线`, 'ok');
      else app.setTitle(LAN, `${people.length} 个人，${ids.length} 台设备，${on} 台在线`, 'off');
    }
    summary();

    // ---- Tray: my devices that are not in this LAN (as the desktop's, §6.5) ----------------
    const IDLE = '拖出来，加进局域网';
    const BAND = 104;
    const H = () => app.canvas.clientHeight;
    const tray = App.h(`<div class="tray" data-k="tray-${LAN}"><span class="tray-label">${IDLE}</span></div>`);
    app.o.trace = (app.o.trace || []).concat(['.tray', '.tray .mini', '.tray .slot', '.drag-layer .dev']);
    tray.style.left = cfg.trayLeft || '330px';
    app.canvas.appendChild(tray);
    const label = tray.querySelector('.tray-label');
    const miniOf = (id) => tray.querySelector(`.mini[data-id="${id}"]`);
    const miniPos = (id) => {
      const r = app.rectOf(miniOf(id));
      return { x: r.x + r.w / 2, y: r.y + r.h / 2 };
    };
    const removable = (n) => n.kind === 'device' && n.owner === ME && !(DEVS[n.id] && DEVS[n.id].pending);
    const overTray = (q) => {
      const z = app.rectOf(tray), m = 22;
      return q.x >= z.x - m && q.x <= z.x + z.w + m && q.y >= z.y - m && q.y <= z.y + z.h + m;
    };
    const nearTray = (q) => {
      const z = app.rectOf(tray);
      const dx = Math.max(z.x - q.x, 0, q.x - (z.x + z.w)), dy = Math.max(z.y - q.y, 0, q.y - (z.y + z.h));
      return dx * dx + dy * dy < 170 * 170;
    };
    app.spotRules = () => [{ x: -1e4, y: H() - BAND, w: 2e4, h: 1e4 }];
    const reason = (n) => (DEVS[n.id] && DEVS[n.id].pending ? '这台电脑还没加进来' : `这是${whose(n.id)}的设备，只有${whose(n.id)}能移出`);
    let mode = 'idle', landing = 0, slot = null;
    const GROW = { duration: 320, easing: 'cubic-bezier(0.3, 0.3, 0.2, 1)' };
    function say(text) {
      const t = App.space(text);
      if (label.textContent === t) return;
      const cur = App.softNow(label, 'w');
      const w0 = cur != null ? cur : label.offsetWidth;
      label.style.width = '';
      label.textContent = t;
      const w1 = label.offsetWidth;
      label.style.width = w0 + 'px';
      App.soft(label, 'w', w0, w1, (w, rest) => (label.style.width = rest ? '' : w + 'px'));
      label.animate([{ opacity: 0 }, { opacity: 1 }], { duration: 160, easing: 'ease-out' });
    }
    let slotEl = null;
    const drawSlot = (el, w) => {
      w = Math.max(0, w);
      el.style.width = w + 'px';
      el.style.marginLeft = -10 * (1 - Math.min(1, w / 40)) + 'px';
      el.style.opacity = Math.min(1, w / 40);
    };
    function openSlot(open) {
      if (open === !!slot) return;
      if (open) {
        if (!slotEl) {
          slotEl = App.h(`<div class="slot" data-k="slot"></div>`);
          drawSlot(slotEl, 0);
          tray.appendChild(slotEl);
        }
        const el = (slot = slotEl);
        App.soft(el, 'w', 0, 40, (w) => drawSlot(el, w));
      } else {
        const el = slotEl;
        slot = null;
        App.soft(el, 'w', 40, 0, (w) => drawSlot(el, w), () => {
          if (slotEl === el && !slot) {
            slotEl = null;
            el.remove();
          }
        });
      }
    }
    const slotPos = () => {
      const r = app.rectOf(slot || tray);
      return { x: slot ? r.x + r.w / 2 : r.x + r.w - 30, y: r.y + r.h / 2 };
    };
    function setMode(m, text) {
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
    function collapseMini(m) {
      m.style.pointerEvents = 'none';
      m.animate([{ width: '40px', marginLeft: '0px', opacity: 1 }, { width: '0px', marginLeft: '-10px', opacity: 0 }], { ...GROW, fill: 'forwards' });
      D.after(GROW.duration + 10, () => {
        m.remove();
        setMode(mode, mode === 'idle' ? IDLE : label.textContent);
      });
    }
    function addMini(id, at) {
      const d = SPARE[id];
      const m = App.h(`<div class="mini" data-id="${id}" title="${d.name}"><div class="tile" style="--c:${P[ME].c}">${G.device(d.type, 20)}</div></div>`);
      tray.insertBefore(m, at || null);
      trayDrag(m, id);
      return m;
    }
    for (const id of cfg.tray || []) addMini(id);
    setMode('idle', IDLE);

    function trayDrag(m, id) {
      let g = null, start = null;
      app.drag(m, {
        start: (p0, p) => {
          start = miniPos(id);
          g = App.h(`<div class="dev dragging"><div class="tile" style="--c:${P[ME].c}">${G.device(SPARE[id].type, 30)}</div><div class="name"><span class="nm">${SPARE[id].name}</span></div></div>`);
          g.style.left = p.x + 'px';
          g.style.top = p.y + 'px';
          app.dragLayer.appendChild(g);
          m.classList.add('ghost');
        },
        move: (p) => {
          const q = app.clampTile({ kind: 'device' }, p);
          g.style.left = q.x + 'px';
          g.style.top = q.y + 'px';
        },
        end: async (p, cancel) => {
          const at = { x: parseFloat(g.style.left), y: parseFloat(g.style.top) };
          if (!cancel && p && at.y < H() - BAND) {
            g.remove();
            m.classList.remove('ghost');
            bringIn(id, at, { from: at, fromScale: 1 });
            return;
          }
          g.classList.remove('dragging');
          g.querySelector('.name').style.opacity = 0;
          await App.tween(300, (k) => {
            g.style.left = at.x + (start.x - at.x) * k + 'px';
            g.style.top = at.y + (start.y - at.y) * k + 'px';
            g.style.transform = `scale(${1 - 0.33 * k})`;
          });
          g.remove();
          m.classList.remove('ghost');
        },
        click: () => {
          const from = miniPos(id);
          bringIn(id, { x: from.x, y: H() - BAND - 110 }, { from, fromScale: 40 / 60 });
        },
      });
    }

    // Into the LAN: it lands where it was let go (the nearest free spot).
    // There is no link to draw out on the web; it says it is online.
    async function bringIn(id, at, o = {}) {
      const d = SPARE[id];
      const m = miniOf(id);
      if (m) collapseMini(m);
      const from = o.from || at;
      DEVS[id] = { ...d, x: from.x, y: from.y };
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
      if (!o.quiet) app.toast(`已把${d.name}加进局域网`, { dev: { ...d, owner: ME }, undo: () => takeOut(id, { quiet: true }) });
    }

    async function takeOut(id, o = {}) {
      const n = app.nodes[id];
      if (!n) return;
      const home = n.home || { x: n.x, y: n.y };
      if (!SPARE[id]) SPARE[id] = { type: DEVS[id].type, owner: ME, name: DEVS[id].name };
      delete DEVS[id];
      if (app.selected === id) app.closeSheet();
      app.dropNode(id);
      summary();
      landing++;
      setMode('armed', '松手，移出局域网');
      n.el.classList.remove('dragging');
      const nm = n.el.querySelector('.name');
      if (nm) nm.style.opacity = 0;
      if (n.el.parentNode !== app.dragLayer) app.dragLayer.appendChild(n.el);
      const s0 = n.dropScale || n.scale || 1;
      n.dropScale = null;
      const a = { x: n.x, y: n.y };
      await App.tween(340, (k) => {
        const to = slotPos();
        n.el.style.left = a.x + (to.x - a.x) * k + 'px';
        n.el.style.top = a.y + (to.y - a.y) * k + 'px';
        n.el.style.transform = `scale(${s0 + (40 / 60 - s0) * k})`;
      });
      const mini = addMini(id, slot);
      if (slotEl) {
        App.softStop(slotEl, 'w');
        slotEl.remove();
        slotEl = null;
      }
      slot = null;
      n.el.remove();
      landing--;
      setMode(app.held ? mode : 'idle', app.held ? label.textContent : IDLE);
      if (!o.quiet) app.toast(`已把${SPARE[id].name}移出局域网`, { dev: { ...SPARE[id], owner: ME }, undo: () => bringIn(id, home, { from: miniPos(id), fromScale: 40 / 60, quiet: true }) });
      return mini;
    }

    app.onNodeBounds = (n) => (n.kind !== 'device' ? null : removable(n) ? { y1: H() - 36 } : { y1: H() - BAND - 70 });
    app.onNodeTarget = (n) => (removable(n) && overTray(n) ? { kind: 'tray', shrink: 40 / 60, keep: true } : null);
    app.onNodeDrop = (n, t) => {
      if (t.kind !== 'tray') return false;
      takeOut(n.id);
    };
    app.onNodeDragging = (n) => {
      if (n.kind !== 'device') return;
      const near = nearTray(n), over = removable(n) && overTray(n);
      if (removable(n)) {
        setMode(over ? 'armed' : near ? 'offer' : 'idle', over ? '松手，移出局域网' : near ? '拖到这里，移出局域网' : IDLE);
        const nm = n.el.querySelector('.name');
        if (nm) nm.style.opacity = over ? 0 : '';
      } else setMode(near ? 'refuse' : 'idle', near ? reason(n) : IDLE);
    };
    app.onNodeEnd = (n, t, cancel) => {
      if (t && t.kind === 'tray' && !cancel) return;
      const nm = n.el.querySelector('.name');
      if (nm) nm.style.opacity = '';
      setMode('idle', IDLE);
    };

    // ---- The card ---------------------------------------------------------------------
    // The desktop's card, but it says whether the device is online, not how
    // this computer reaches it; and someone else's services cannot be opened
    // from a browser, so it lists them.
    const head = (id, st) => {
      const n = app.nodes[id];
      return (
        `<div class="top"><div class="tile-s"><div class="tile" style="--c:${P[n.owner].c}">${G.device(n.type, 20)}</div></div>` +
        `<div><b>${App.space(n.name)}</b><span>${n.owner === ME ? '我的设备' : App.space(nameOf(n.owner) + '的设备')}</span></div>` +
        `<button class="icon-btn sheet-close" title="关闭">${G.ui('close', 16)}</button></div>` +
        `<div class="status"><span class="badge" style="--c:${st.c}">${st.icon}</span><div>${st.t}<small>${st.s}</small></div></div>`
      );
    };
    const chip = (id, k) => `<button class="chip ${isRefused(id, k) ? 'blocked' : ''}" data-k="${k}">${App.ball(k, isRefused(id, k) ? 'off' : '')}${App.space(nameOf(k))}</button>`;
    const who = (id) => `<div class="label">谁能用</div><div class="people">${people.filter((k) => k !== ME).map((k) => chip(id, k)).join('')}</div><div class="hint">点一下头像，就不让他用了</div>`;
    function openCard(id) {
      const d = DEVS[id];
      if (!d) return;
      if (d.pending) return openDownload();
      app.selected = id;
      app.select(id);
      const mine = d.owner === ME;
      const st = d.off
        ? { c: '#a1a1aa', icon: G.ui('online', 16), t: d.off, s: '它开机、连上网就会回来' }
        : { c: '#22C55E', icon: G.ui('online', 16), t: '在线', s: mine ? '别人能用你开放的服务' : '在装了 Deneb 的电脑上就能用' };
      const svc = (SERVICES[d.type] || []).map(([icon, name, on]) =>
        mine
          ? `<div class="row">${G.ui(icon, 18)}<span>${name}</span><button class="sw ${on ? 'on' : ''}" aria-label="${name}"></button></div>`
          : on
            ? `<div class="row">${G.ui(icon, 18)}<span>${name}</span></div>`
            : `<div class="row na">${G.ui(icon, 18)}<span>${name}</span><em>没开放</em></div>`
      );
      const out = mine ? `<button class="out-btn">移出这个局域网</button>` : '';
      const body = `<div class="label">${mine ? '开放给大家的' : '开放给你的'}</div><div class="rows">${svc.join('')}</div>${mine ? who(id) : ''}<div class="card-foot"><button class="more">更多${G.ui('chevron', 14)}</button>${out}</div><div class="detail"><p>地址 100.64.0.${20 + Object.keys(DEVS).indexOf(id)}</p></div>`;
      const s = app.sheet(head(id, st) + body, id);
      for (const ch of s.querySelectorAll('.chip')) ch.onclick = () => setBlocked(id, ch.dataset.k, !isRefused(id, ch.dataset.k), ch);
      const outBtn = s.querySelector('.out-btn');
      if (outBtn) outBtn.onclick = () => takeOut(id);
    }
    app.onDeviceClick = (id) => openCard(id);
    app.onSheetClose = () => (app.selected = null);
    function setBlocked(dev, k, b, chipEl) {
      (refused[dev] ||= {})[k] = b;
      chipEl.classList.toggle('blocked', b);
      $('.ball', chipEl).classList.toggle('off', b);
    }

    // ---- The download card (§14.7) ------------------------------------------------------
    let downloaded = false;
    function openDownload() {
      const pid = Object.keys(DEVS).find((i) => DEVS[i].pending);
      app.selected = pid;
      if (pid) app.select(pid);
      const sample = Object.values(DEVS).find((d) => d.owner !== ME && d.type === 'nas') || Object.values(DEVS).find((d) => d.owner !== ME);
      const html =
        `<div class="top"><div class="dl-mark">${G.star(20, '#fff')}</div><div><b>下载 Deneb</b><span>装在这台电脑上</span></div><button class="icon-btn sheet-close" title="关闭">${G.ui('close', 16)}</button></div>` +
        `<p class="dl-text">${App.space(`装上以后，这台电脑就能用${LAN}里的设备，比如${sample ? sample.name : ''}。`)}</p>` +
        `<button class="btn primary dl-btn" data-act="dl">${G.ui('download', 16)}下载 macOS 版</button>` +
        `<button class="dl-more">也有 Windows 版、Linux 版</button>` +
        (downloaded ? `<div class="dl-after">装好打开以后，用同一个邮箱登录，这台电脑会自己出现在这里。</div>` : '');
      const s = app.sheet(html, 'download');
      $('[data-act="dl"]', s).onclick = () => {
        if (!downloaded) {
          downloaded = true;
          openDownload();
        }
        app.toast(cfg.downloadToast || '开始下载了', { parent: cfg.toastParent });
      };
    }

    // Logged in on this computer: the dashed tile fills in, with a bounce
    // (it joined, §5), and takes its name.
    function fillPending(name, type = 'laptop') {
      const id = Object.keys(DEVS).find((i) => DEVS[i].pending);
      if (!id) return;
      const d = DEVS[id];
      d.pending = false;
      d.name = name;
      d.type = type;
      const n = app.nodes[id];
      n.name = name;
      n.type = type;
      if (app.sheetKey === 'download') app.closeSheet();
      const tile = n.el.querySelector('.tile');
      const dash = tile.querySelector('.dash');
      n.el.classList.remove('pending');
      if (dash) dash.remove();
      tile.innerHTML = G.device(type, 30);
      tile.animate([{ transform: 'none' }, { transform: 'scale(1.08)', offset: 0.4 }, { transform: 'none' }], { duration: 560, easing: 'cubic-bezier(0.3, 1.2, 0.5, 1)' });
      tile.animate([{ opacity: 0.4 }, { opacity: 1 }], { duration: 200, easing: 'ease-out' });
      n.el.querySelector('.nm').textContent = App.space(name);
      app.setSub(id, statusOf(id));
      summary();
      app.toast(`${name}加进来了`, { dev: { type, owner: ME } });
    }

    // Another LAN takes the canvas (the page builds the next one).
    function destroy() {
      if (app.sheetEl.style.display !== 'none') app.closeSheet();
      // A toast is about the canvas it was on: it goes with it.
      if (app.toastEl) app.dropToast(app.toastEl);
      for (const id of Object.keys(DEVS)) {
        const n = app.dropNode(id);
        if (n) n.el.remove();
      }
      tray.remove();
    }

    return { openCard, openDownload, fillPending, summary, DEVS, takeOut, bringIn, destroy };
  };
})();
