// The LAN "周末联机" as the sample shows it (DESIGN.md §9), shared by the
// sample page and scene 5: this computer in the middle; Mia's MacBook,
// 阿杰's desktop, my game console, and 阿杰's NAS through the Hong Kong
// relay; a tray of my devices that are not in this LAN; the device card.
//
// Everything on the canvas can be moved (app.js does that). What this file
// adds is what the special places mean: my devices go into the tray to
// leave the LAN (with undo); someone else's device can be moved around but
// not taken out (it comes back, shakes, and says whose it is); tray tiles
// come out onto the canvas to join.
(function () {
  window.Lan = function (app, cfg = {}) {
    const P = App.PEOPLE;
    const $ = (s, r = document) => r.querySelector(s);
    const RELAY = { x: 412, y: 374, name: '香港' };
    const DEVS = {
      mac: { type: 'laptop', owner: 'me', name: '这台电脑', x: 300, y: 278 },
      mia: { type: 'laptop', owner: 'mia', name: 'Mia 的 MacBook', x: 136, y: 128 },
      jdesk: { type: 'desktop', owner: 'jie', name: '阿杰的台式机', x: 468, y: 128 },
      game: { type: 'console', owner: 'me', name: '我的游戏主机', x: 136, y: 430 },
      jnas: { type: 'nas', owner: 'jie', name: '阿杰的 NAS', x: 520, y: 462, relay: true },
    };
    const SPARE = {
      studio: { type: 'mini', owner: 'me', name: '工作室主机' },
      mynas: { type: 'nas', owner: 'me', name: '我的 NAS' },
    };
    // The sample's bends; a link turns the other way by itself when a name
    // grows into its path (app.js, DESIGN.md §6.3).
    const BENDS = { mia: -0.1, jdesk: 0.1, game: 0.1, jnas: 0.06 };
    const SERVICES = {
      laptop: [['screen', '远程桌面', true], ['folder', '共享文件夹', false]],
      desktop: [['screen', '远程桌面', true], ['globe', '网页', false]],
      console: [['globe', '游戏服务器', true]],
      nas: [['folder', '共享文件夹', true], ['screen', '远程桌面', false], ['globe', '网页', true]],
      mini: [['screen', '远程桌面', true], ['terminal', '命令行', true]],
    };
    // What this computer opens to everyone.
    const MINE = cfg.mine || [
      { k: 'screen', icon: 'screen', name: '远程桌面', on: true, port: 3389 },
      { k: 'folder', icon: 'folder', name: '共享文件夹', on: false, port: 445 },
    ];
    const people = cfg.people || ['me', 'mia', 'jie'];
    // Who I refused, device by device (DESIGN.md §6.5): refusing Mia on
    // the game console leaves this computer as it was.
    const refused = {};
    const isRefused = (id, k) => !!(refused[id] && refused[id][k]);
    let termNote = false;
    const whose = (id) => P[DEVS[id].owner].name;

    app.relay('hk', { name: RELAY.name, sub: '中转站', x: RELAY.x, y: RELAY.y });
    const statusOf = (id) => {
      const d = DEVS[id];
      if (id === 'mac') return '你正在用';
      if (d.joining) return '正在连接…';
      if (d.owner !== 'me' && isRefused('mac', d.owner)) return '用不了这台电脑';
      if (id === 'jdesk' && termNote) return '命令行被关了';
      return d.relay ? `经${RELAY.name}中转` : '直接连上';
    };
    const mount = (id, o = {}) => app.device(id, { ...DEVS[id], sub: statusOf(id), ...o });
    function connect(id, state = 'on') {
      const d = DEVS[id];
      return app.link('l-' + id, 'mac', id, { via: d.relay ? 'hk' : null, bend: BENDS[id] == null ? 0.06 : BENDS[id], state, trying: state === 'dash' && d.joining });
    }
    for (const id in DEVS) mount(id);
    for (const id in DEVS) if (id !== 'mac') connect(id);

    function summary() {
      app.setTitle('周末联机', `${people.length} 个人，${Object.keys(DEVS).length} 台设备都连上了`, 'ok');
    }
    summary();

    // ---- Tray: my devices that are not in this LAN (DESIGN.md §6.5) -----------------------
    // What it says follows what the hand holds. At rest: "拖出来，加进局域网"
    // (its devices are dragged out to join). One of mine near: "拖到这里，
    // 移出局域网", a light blue ring. Over it: "松手，移出局域网", blue, with
    // a slot opening at its end; the device already looks as it will in
    // there (tray-sized, no name, its link faint). Someone else's device, or
    // this computer, near: only why it cannot go in; it stops above the tray
    // and nothing else happens. Empty, with nothing in the hand, it sinks
    // away. Its pill, words and slots change size together, smoothly.
    const IDLE = '拖出来，加进局域网';
    const BAND = 104; // the band at the bottom where the tray floats and takes drops
    const H = () => app.canvas.clientHeight;
    const tray = App.h(`<div class="tray" data-k="tray"><span class="tray-label">${IDLE}</span></div>`);
    // The tray and what is in it go into the frame log too (motion-check).
    // (And a device on its way into the tray, out of the canvas's list.)
    app.o.trace = (app.o.trace || []).concat(['.tray', '.tray .mini', '.tray .slot', '.drag-layer .dev']);
    tray.style.left = '330px';
    app.canvas.appendChild(tray);
    const label = tray.querySelector('.tray-label');
    const miniOf = (id) => tray.querySelector(`.mini[data-id="${id}"]`);
    const miniPos = (id) => {
      const r = app.rectOf(miniOf(id));
      return { x: r.x + r.w / 2, y: r.y + r.h / 2 };
    };
    const removable = (n) => n.kind === 'device' && n.owner === 'me' && n.id !== 'mac';
    // Over the tray means on the tray itself, or within 22px of it: only
    // there does it light up and open a slot (DESIGN.md §6.5). The band
    // beside it does not count. Near is about when the name comes within
    // reach of it. (The native client measures the same way.)
    const overTray = (q) => {
      const z = app.rectOf(tray), m = 22;
      return q.x >= z.x - m && q.x <= z.x + z.w + m && q.y >= z.y - m && q.y <= z.y + z.h + m;
    };
    const nearTray = (q) => {
      const z = app.rectOf(tray);
      const dx = Math.max(z.x - q.x, 0, q.x - (z.x + z.w)), dy = Math.max(z.y - q.y, 0, q.y - (z.y + z.h));
      return dx * dx + dy * dy < 170 * 170;
    };
    // Nothing comes to rest in the tray's band, beside the tray either:
    // let go there, a device goes to the nearest free spot above it.
    app.spotRules = () => [{ x: -1e4, y: H() - BAND, w: 2e4, h: 1e4 }];
    const reason = (n) => (n.id === 'mac' ? '正在用的这台电脑不能移出' : `这是${whose(n.id)}的设备，只有${whose(n.id)}能移出`);
    let mode = 'idle', landing = 0, slot = null;
    // New words: the old ones give way at once, the new fade in, and the
    // label's width (so the pill's) follows smoothly.
    // The tray's words and its slot grow and shrink on one spring (App.soft,
    // the native client's), so they move as one piece (§6.5). A tile
    // leaving the tray for good folds away on a curve of about that length.
    const GROW = { duration: 320, easing: 'cubic-bezier(0.3, 0.3, 0.2, 1)' };
    function say(text) {
      const t = App.space(text);
      if (label.textContent === t) return;
      // From the width it shows now (mid-way, if it is still changing) to
      // the new words' own width, on the tray's spring: words that change
      // back and forth make it ease round, never stop dead.
      const cur = App.softNow(label, 'w');
      const w0 = cur != null ? cur : label.offsetWidth;
      label.style.width = '';
      label.textContent = t;
      const w1 = label.offsetWidth;
      label.style.width = w0 + 'px';
      App.soft(label, 'w', w0, w1, (w, rest) => (label.style.width = rest ? '' : w + 'px'));
      label.animate([{ opacity: 0 }, { opacity: 1 }], { duration: 160, easing: 'ease-out' });
    }
    // The slot for the one coming in opens and closes on the same spring;
    // opening again while it closes just turns it round. At 0 wide its
    // margin takes back the tray's gap, so it adds nothing.
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
      // Up while it holds devices, takes one in, or the hand brings one near.
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
      const m = App.h(`<div class="mini" data-id="${id}" title="${d.name}"><div class="tile" style="--c:${P.me.c}">${G.device(d.type, 20)}</div></div>`);
      tray.insertBefore(m, at || null);
      trayDrag(m, id);
      return m;
    }
    for (const id of cfg.tray || ['studio', 'mynas']) addMini(id);

    // Out of the tray onto the canvas: it joins where it is let go (or in
    // the nearest free spot); let go over the tray, it goes back to its
    // place. A click sends it to a free spot right above the tray.
    function trayDrag(m, id) {
      let g = null, start = null;
      app.drag(m, {
        start: (p0, p) => {
          start = miniPos(id);
          // Out of the tray it travels with its name; its place fades.
          g = App.h(`<div class="dev dragging"><div class="tile" style="--c:${P.me.c}">${G.device(SPARE[id].type, 30)}</div><div class="name"><span class="nm">${SPARE[id].name}</span></div></div>`);
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
          // Back to its place: let go over the tray, or Escape.
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

    // Into the LAN from the tray (dragged out, clicked, or undone): it
    // shows where it was (from, at its size there) and glides to its spot
    // (the nearest free one), its link drawn out from both ends as it goes.
    async function bringIn(id, at, o = {}) {
      const d = SPARE[id];
      const m = miniOf(id);
      if (m) collapseMini(m);
      const from = o.from || at;
      DEVS[id] = { ...d, x: from.x, y: from.y };
      const n = mount(id, { keep: false, sub: '正在加进来…' });
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
      const l = connect(id, 'grow');
      summary();
      await app.layer.grow(l, 520, 'both');
      app.setSub(id, statusOf(id));
      if (!o.quiet) app.toast(`已把${d.name}加进局域网`, { dev: d, undo: () => takeOut(id, { quiet: true }) });
    }

    // Into the tray: out of this LAN. The tray opens a slot at its end; the
    // tile flies into it (already tray-sized if the hand brought it over the
    // tray), its link drawn back into this computer, and takes the slot.
    async function takeOut(id, o = {}) {
      const n = app.nodes[id];
      if (!n) return;
      const home = n.home || { x: n.x, y: n.y };
      if (!SPARE[id]) SPARE[id] = { type: DEVS[id].type, owner: 'me', name: DEVS[id].name };
      delete DEVS[id];
      if (app.selected === id) app.closeSheet();
      const l = app.links['l-' + id];
      if (l) app.layer.retract(l, 'a', 380, { glow: true }).then(() => app.unlink('l-' + id));
      app.dropNode(id);
      summary();
      landing++;
      setMode('armed', '松手，移出局域网');
      n.el.classList.remove('dragging');
      const nm = n.el.querySelector('.name');
      if (nm) nm.style.opacity = 0;
      if (n.el.parentNode !== app.dragLayer) app.dragLayer.appendChild(n.el);
      // From the size it had in the hand (shrunk over the tray), or its
      // own when the card's button sent it.
      const s0 = n.dropScale || n.scale || 1;
      n.dropScale = null;
      const a = { x: n.x, y: n.y };
      await App.tween(340, (k) => {
        const to = slotPos();
        n.el.style.left = a.x + (to.x - a.x) * k + 'px';
        n.el.style.top = a.y + (to.y - a.y) * k + 'px';
        n.el.style.transform = `scale(${s0 + (40 / 60 - s0) * k})`;
      });
      // Landed: the slot becomes its tile, in place.
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
      if (!o.quiet) app.toast(`已把${SPARE[id].name}移出局域网`, { dev: SPARE[id], undo: () => bringIn(id, home, { from: miniPos(id), fromScale: 40 / 60, quiet: true }) });
      return mini;
    }

    // While a device is in the hand: the tray says what it means for it.
    // Someone else's, or this computer, cannot reach the tray's band.
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
        const l = app.links['l-' + n.id];
        if (l && l.state === 'on') l.alphaTo = over ? 0.15 : 1;
      } else setMode(near ? 'refuse' : 'idle', near ? reason(n) : IDLE);
    };
    app.onNodeEnd = (n, t, cancel) => {
      if (t && t.kind === 'tray' && !cancel) return;
      const nm = n.el.querySelector('.name');
      if (nm) nm.style.opacity = '';
      const l = app.links['l-' + n.id];
      if (l) l.alphaTo = 1;
      setMode('idle', IDLE);
    };

    // ---- The card ---------------------------------------------------------------------
    const chip = (id, k) => `<button class="chip ${isRefused(id, k) ? 'blocked' : ''}" data-k="${k}">${App.ball(k, isRefused(id, k) ? 'off' : '')}${P[k].name}</button>`;
    // Every device of mine has its own "谁能用".
    const who = (id) => `<div class="label">谁能用</div><div class="people">${people.filter((k) => k !== 'me').map((k) => chip(id, k)).join('')}</div><div class="hint">点一下头像，就不让他用了</div>`;
    function openCard(id) {
      if (!DEVS[id]) return;
      app.selected = id;
      app.select(id);
      const d = DEVS[id];
      const mine = d.owner === 'me';
      const st =
        id === 'mac'
          ? { c: '#3B82F6', icon: G.ui('home', 16), t: '这就是你正在用的电脑', s: '别人能用你开放的服务' }
          : d.relay
            ? { relay: true, icon: G.device('relay', 16), t: `经${RELAY.name}中转`, s: '慢一点，但能正常用' }
            : { c: '#22C55E', icon: G.ui('bolt', 16), t: '直接连上', s: '很快' };
      let body = '';
      if (id === 'mac') {
        body =
          `<div class="label">开放给大家的</div><div class="rows">` +
          MINE.map((s) => `<div class="row">${G.ui(s.icon, 18)}<span>${s.name}</span><button class="sw ${s.on ? 'on' : ''}" data-own data-k="${s.k}" aria-label="${s.name}"></button></div>`).join('') +
          `</div>${who('mac')}` +
          `<button class="more">更多${G.ui('chevron', 14)}</button><div class="detail"><p>地址 100.64.0.10<br />${MINE.map((s) => `${s.name} ${s.port}`).join(' · ')}</p></div>`;
      } else {
        const svc = (SERVICES[d.type] || []).map(([icon, name, on]) =>
          mine
            ? `<div class="row">${G.ui(icon, 18)}<span>${name}</span><button class="sw ${on ? 'on' : ''}" aria-label="${name}"></button></div>`
            : on
              ? `<div class="row">${G.ui(icon, 18)}<span>${name}</span><button class="open-btn">打开</button></div>`
              : `<div class="row na">${G.ui(icon, 18)}<span>${name}</span><em>没开放</em></div>`
        );
        // My own device (not this computer) can be taken out with a click
        // too: the click for the drag into the tray (§6.4).
        const out = mine ? `<button class="out-btn">移出这个局域网</button>` : '';
        body = `<div class="label">${mine ? '开放给大家的' : '你可以用'}</div><div class="rows">${svc.join('')}</div>${mine ? who(id) : ''}<div class="card-foot"><button class="more">更多${G.ui('chevron', 14)}</button>${out}</div><div class="detail"><p>地址 100.64.0.${20 + Object.keys(DEVS).indexOf(id)}</p></div>`;
      }
      const s = app.sheet(app.sheetHead(id, st) + body, id);
      for (const sw of s.querySelectorAll('.sw[data-own]')) sw.onclick = () => setService(sw.dataset.k, !sw.classList.contains('on'), sw);
      for (const ch of s.querySelectorAll('.chip')) ch.onclick = () => setBlocked(id, ch.dataset.k, !isRefused(id, ch.dataset.k), ch);
      const outBtn = s.querySelector('.out-btn');
      if (outBtn) outBtn.onclick = () => takeOut(id);
    }
    app.onDeviceClick = (id) => openCard(id);
    app.onSheetClose = () => (app.selected = null);

    // Closing a service someone is using: their request comes as a red
    // light along their link and breaks up just before this computer.
    function setService(k, on, sw) {
      const s = MINE.find((x) => x.k === k);
      s.on = on;
      sw.classList.toggle('on', on);
      if (k !== 'term') return;
      const l = app.links['l-jdesk'];
      if (!on && l && l.state === 'on') {
        D.after(160, () => app.layer.refuse(l, 'b'));
        D.after(160 + ((l.len - 40) / app.layer.speed) * 1000, () => {
          termNote = true;
          app.setSub('jdesk', statusOf('jdesk'));
        });
      } else if (on) {
        termNote = false;
        app.setSub('jdesk', statusOf('jdesk'));
      }
    }
    // Not letting someone use a device of mine. On this computer their
    // link goes to dots; on another device it is that device's list alone.
    function setBlocked(dev, k, b, chipEl) {
      (refused[dev] ||= {})[k] = b;
      chipEl.classList.toggle('blocked', b);
      $('.ball', chipEl).classList.toggle('off', b);
      if (dev !== 'mac') return;
      for (const id of Object.keys(DEVS).filter((i) => DEVS[i].owner === k)) {
        const l = app.links['l-' + id];
        if (b) {
          if (l) {
            delete app.links['l-' + id];
            app.layer.retract(l, 'b', 420, { glow: true }).then(() => app.layer.remove(l));
          }
          const d = connect(id, 'dash');
          d.alpha = 0;
          d.alphaTo = 1;
        } else {
          if (l) {
            l.alphaTo = 0;
            delete app.links['l-' + id];
            D.after(300, () => app.layer.remove(l));
          }
          app.layer.grow(connect(id, 'grow'), 460, 'both');
        }
        app.setSub(id, statusOf(id));
      }
    }

    // ---- Someone joins ------------------------------------------------------------------
    async function join(btn) {
      if (people.includes('leo')) return;
      if (btn) (btn.disabled = true), (btn.style.opacity = 0.45);
      people.push('leo');
      app.setStack(people);
      const b = $('.stack', app.win).lastElementChild;
      b.animate([{ transform: 'scale(0.3)', opacity: 0 }, { transform: 'scale(1.12)', opacity: 1, offset: 0.6 }, { transform: 'none' }], { duration: 520, easing: 'cubic-bezier(0.3, 1.2, 0.5, 1)' });
      if (app.selected === 'mac') openCard('mac');
      // His device comes in from the right and lands, with a bounce, in
      // the free spot nearest the top right.
      DEVS.leomac = { type: 'laptop', owner: 'leo', name: 'Leo 的 MacBook', x: 1010, y: 120, joining: true };
      const n = mount('leomac', { keep: false });
      const at = app.freeSpot('leomac', { x: 790, y: 120 });
      app.settle(n, at, D.Bouncy);
      await App.wait(520);
      const l = connect('leomac', 'dash');
      app.pill(l, '正在连接…', { at: () => l.at(l.len * 0.7) });
      await App.wait(700);
      await app.layer.grow(l, 460, 'both');
      DEVS.leomac.joining = false;
      app.pill(l, '直接连上 · 很快', { tone: 'ok', hideAfter: 2000 });
      app.setSub('leomac', statusOf('leomac'));
      summary();
    }

    return { openCard, join, DEVS, summary };
  };
})();
