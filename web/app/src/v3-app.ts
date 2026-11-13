// @ts-nocheck
// design/demos/v3/app.js as it is: the desktop's look, drawn as the design's demos draw it.
// The app frame every scene shares (DESIGN.md §7, §9): window, sidebar,
// header, canvas with device tiles and links, the device card, the tray,
// toasts, and the small demo bar under the window.
//
// Every node on the canvas can be dragged (DESIGN.md §6.2, §6.3): it is
// lifted above everything and its links follow like soft ropes; nothing
// else moves while it is held. On release it stays where it was let go (in
// the nearest free spot, inside the window), remembered for next time. A scene
// only says what the special places are (a tray, an area, a person) and
// what happens there. Links bend around nodes they would pass under, and
// status chips slide along their link to a clear spot.
(function () {
  const $ = (s, r = document) => r.querySelector(s);
  const h = (html) => {
    const t = document.createElement('template');
    t.innerHTML = html.trim();
    return t.content.firstElementChild;
  };
  // esc: words going into markup stay words. Names here are anyone's (a
  // device's, a person's, a LAN's, a relay's): one with < or & in it shows
  // as typed, never as an element.
  const esc = (s) => String(s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]);

  const PEOPLE = {
    me: { name: '我', full: 'kittors', ch: 'K', c: '#3B82F6' },
    mia: { name: 'Mia', ch: 'M', c: '#EC4899' },
    jie: { name: '阿杰', ch: '杰', c: '#F59E0B' },
    leo: { name: 'Leo', ch: 'L', c: '#8B5CF6' },
    lin: { name: '小林', ch: '林', c: '#22C55E' },
    team: { name: '工作室', ch: '工', c: '#06B6D4' },
  };
  const SCENES = [
    { file: 'sample.html', title: '主界面' },
    { file: '01-direct-relay.html', title: '先直接连，连不上就经中转' },
    { file: '02-drag-relay.html', title: '拖一个中转站进来' },
    { file: '03-multi-network.html', title: '同时在几个局域网' },
    { file: '04-device-share.html', title: '管理员把设备分给某个人' },
    { file: '05-lan-members.html', title: '局域网里各管各的设备' },
  ];
  const ball = (k, cls = '') => {
    const p = PEOPLE[k];
    return `<span class="ball ${cls}" style="--c:${p.c}">${esc(p.ch)}</span>`;
  };
  const dot = (c) => `<span class="ball" style="--c:${c}"></span>`;

  // Rectangles {x, y, w, h} in canvas coordinates.
  const R = (x, y, w, hh) => ({ x, y, w, h: hh });
  const grow = (r, p) => R(r.x - p, r.y - p, r.w + 2 * p, r.h + 2 * p);
  const ov = (a, b) => a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h;
  const inside = (p, r) => p.x >= r.x && p.x <= r.x + r.w && p.y >= r.y && p.y <= r.y + r.h;

  class App {
    constructor(o) {
      this.o = o;
      this.nodes = {};
      this.links = {};
      this.zoom = 1;
      this.page = (location.pathname.split('/').pop() || 'index').replace(/\.html$/, '');
      const root = document.getElementById('app') || document.body;
      if (o.frame !== false) this.buildWindow(root);
      this.buildBar(root);
      this.fit();
      addEventListener('resize', () => this.fit());
      if (this.canvas) {
        // Tidying runs before the links are drawn in each frame, so a link
        // never shows for a frame under a node it is about to bend around.
        D.onFrame(() => this.tidy());
        this.layer = new Net.Layer(this.canvas, { zoom: () => this.zoom });
      }
      if (D.recording) D.after(0, () => this.trace());
      addEventListener('keydown', (e) => e.key === 'Escape' && this.onEscape && this.onEscape());
    }

    // ---- Frame -----------------------------------------------------------------
    buildWindow(root) {
      const o = this.o;
      const side = (o.side || [])
        .map((it) => {
          if (it.group) return `<div class="group">${it.group}</div>`;
          if (it.gap) return `<div class="gap"></div>`;
          const lead = it.dots ? `<span class="dots">${it.dots.map((d) => dot(PEOPLE[d] ? PEOPLE[d].c : d)).join('')}</span>` : it.icon ? G.ui(it.icon, 18) : '';
          const tail = it.count != null ? `<span class="count">${it.count}</span>` : it.sw != null ? `<button class="sw sm ${it.sw ? 'on' : ''}" aria-label="${it.label}"></button>` : '';
          return `<div class="nav ${it.on ? 'on' : ''}" data-id="${it.id || ''}">${lead}<span class="nav-label">${it.label}</span>${tail}</div>`;
        })
        .join('');
      const win = h(`
        <div class="win window">
          <aside class="side">
            <div class="lights"><i></i><i></i><i></i></div>
            <div class="brand">${G.star(18)}<span>Deneb</span></div>
            <div class="side-list">${side}</div>
            <div class="me">${ball('me')}<span>kittors</span><button class="icon-btn" title="设置">${G.ui('gear', 18)}</button></div>
          </aside>
          <main class="main">
            <header class="head">
              <div>
                <div class="title"></div>
                <div class="sub"><span class="dot"></span><span class="sub-text"></span></div>
              </div>
              <div class="right"><div class="stack"></div></div>
            </header>
            <section class="canvas"></section>
            <div class="sheet" style="display:none"></div>
          </main>
          <div class="drag-layer"></div>
        </div>`);
      root.appendChild(win);
      this.win = win;
      this.canvas = $('.canvas', win);
      this.sheetEl = $('.sheet', win);
      // The card steps aside while anything is on the move (DESIGN.md
      // §6.3): it fades out as a drag starts or something flies, and back
      // in once all has been still a moment. The canvas never moves for it.
      this.handling = 0;
      D.onFrame((now) => {
        const s = this.sheetEl;
        if (s.style.display === 'none' || s.__closing) return s.classList.remove('away');
        if (this.busy()) {
          this.calmSince = null;
          s.classList.add('away');
        } else if (s.classList.contains('away')) {
          if (this.calmSince == null) this.calmSince = now;
          else if (now - this.calmSince > 240) {
            s.classList.remove('away');
            this.awayEnd = now + 260;
          }
        }
      });
      // A click on the canvas itself (not on a node) closes the card.
      this.canvas.addEventListener('click', (e) => {
        const t = e.target;
        if (t.__dragged) return (t.__dragged = false);
        // The click that ends a drag (a tile pulled out of the tray, say,
        // and let go over the canvas) is no click: the card stays.
        if (this.dragClick) return;
        if ((t === this.canvas || t.classList.contains('links') || t.classList.contains('area')) && this.sheetEl.style.display !== 'none') this.closeSheet();
      });
      this.dragLayer = $('.drag-layer', win);
      this.setTitle(o.title || '', o.sub || '', o.subDot);
      this.setStack(o.stack || []);
      if (o.action) {
        const b = h(`<button class="btn primary">${G.ui('plus', 16)}${o.action}</button>`);
        $('.head .right', win).appendChild(b);
      }
    }
    setTitle(title, sub, tone = 'ok') {
      if (!this.win) return;
      $('.title', this.win).textContent = title;
      const st = $('.sub-text', this.win);
      if (st.textContent && st.textContent !== sub) {
        st.animate([{ opacity: 0.2 }, { opacity: 1 }], { duration: 260, easing: 'ease-out' });
      }
      st.textContent = sub;
      const d = $('.sub .dot', this.win);
      d.dataset.tone = tone;
    }
    setStack(keys) {
      if (!this.win) return;
      $('.stack', this.win).innerHTML = keys.map((k) => ball(k)).join('');
    }
    nav(id) {
      return this.win && $(`.nav[data-id="${id}"]`, this.win);
    }

    // The window is laid out at its real size and zoomed to fit; zoom
    // keeps text and lines sharp where a transform would blur them.
    fit() {
      const W = this.o.width || 1180, H = this.o.height || 740;
      this.zoom = Math.min((innerWidth - 64) / W, (innerHeight - 112) / H, 1.3);
      for (const el of [this.win, this.o.stageEl].filter(Boolean)) {
        el.style.zoom = this.zoom;
        el.style.left = Math.max(0, (innerWidth / this.zoom - W) / 2) + 'px';
        el.style.top = Math.max(0, (innerHeight / this.zoom - H - 56 / this.zoom) / 2) + 'px';
      }
      if (this.layer) this.layer.resize();
    }

    // Small demo controls under the window: scenes, simulated events,
    // replay, theme. Not part of the product.
    buildBar(root) {
      const i = SCENES.findIndex((s) => location.pathname.endsWith('/' + s.file));
      const prev = SCENES[(i - 1 + SCENES.length) % SCENES.length], next = SCENES[(i + 1) % SCENES.length];
      const bar = h(`<div class="demo-bar"></div>`);
      const qs = () => `?theme=${D.theme.get()}`;
      bar.appendChild(h(`<a class="icon-btn" title="${prev.title}">${G.ui('chevronLeft', 16)}</a>`)).onclick = () => (location.href = prev.file + qs());
      bar.appendChild(h(`<span class="bar-title">${i >= 0 ? (i ? i + ' · ' : '') + SCENES[i].title : ''}</span>`));
      bar.appendChild(h(`<a class="icon-btn" title="${next.title}">${G.ui('chevron', 16)}</a>`)).onclick = () => (location.href = next.file + qs());
      for (const c of this.o.controls || []) {
        bar.appendChild(h(`<span class="bar-sep"></span>`));
        if (c.seg) {
          const seg = h(`<span class="seg">${c.seg.map((s, k) => `<button class="${k === (c.value || 0) ? 'on' : ''}" data-k="${k}">${s}</button>`).join('')}</span>`);
          seg.onclick = (e) => {
            const b = e.target.closest('button');
            if (!b || b.classList.contains('on')) return;
            for (const x of seg.children) x.classList.toggle('on', x === b);
            c.onChange(+b.dataset.k);
          };
          bar.appendChild(seg);
          c.el = seg;
        } else {
          const b = h(`<button class="bar-btn">${c.label}</button>`);
          b.onclick = () => c.onClick(b);
          bar.appendChild(b);
          c.el = b;
        }
      }
      bar.appendChild(h(`<span class="bar-sep"></span>`));
      // Replay starts the story over, and puts every node back where it
      // began (a drag is remembered until then).
      bar.appendChild(h(`<button class="icon-btn" title="重播（摆放也还原）">${G.ui('replay', 16)}</button>`)).onclick = () => {
        this.forget();
        location.href = location.pathname.split('/').pop() + qs();
      };
      const tb = bar.appendChild(h(`<button class="icon-btn" title="深浅色"></button>`));
      const paint = () => (tb.innerHTML = G.ui(D.theme.get() === 'dark' ? 'sun' : 'moon', 16));
      tb.onclick = () => {
        D.theme.toggle();
        paint();
      };
      paint();
      root.appendChild(bar);
      this.bar = bar;
    }

    // ---- Remembered arrangement ------------------------------------------------------
    // Where the user put things stays on this machine. Recordings never read
    // or write it, so every recording starts from the designed picture.
    key(id) {
      return `deneb.pos.${this.page}.${id}`;
    }
    recall(id) {
      if (D.recording) return null;
      try {
        const v = JSON.parse(localStorage.getItem(this.key(id)) || 'null');
        return v && isFinite(v.x) && isFinite(v.y) ? v : null;
      } catch (_) {
        return null;
      }
    }
    remember(id, p) {
      if (D.recording) return;
      try {
        localStorage.setItem(this.key(id), JSON.stringify({ x: Math.round(p.x), y: Math.round(p.y) }));
      } catch (_) {}
    }
    forget() {
      try {
        const pre = `deneb.pos.${this.page}.`;
        for (const k of Object.keys(localStorage)) if (k.startsWith(pre)) localStorage.removeItem(k);
      } catch (_) {}
    }

    // ---- Canvas: devices, relays, links -----------------------------------------
    color(id) {
      const n = this.nodes[id];
      return n.kind === 'relay' ? '#A1A1AA' : PEOPLE[n.owner].c;
    }
    device(id, o) {
      const el = h(`<div class="dev ${o.cls || ''}" data-id="${id}"><div class="tile" style="--c:${PEOPLE[o.owner].c}">${G.device(o.type, 30)}</div><div class="name"><span class="nm">${esc(o.name)}</span><small>${esc(o.sub || '')}</small></div></div>`);
      return this.addNode(id, 'device', el, o);
    }
    relay(id, o) {
      const el = h(`<div class="relay" data-id="${id}"><div class="orb">${G.device('relay', 18)}</div><div class="name ${o.labelSide || ''}">${esc(o.name)}<small>${esc(o.sub || '')}</small></div></div>`);
      return this.addNode(id, 'relay', el, Object.assign({ owner: null }, o));
    }
    addNode(id, kind, el, o) {
      const saved = o.keep === false ? null : this.recall(id);
      if (saved) Object.assign(o, saved);
      el.style.left = o.x + 'px';
      el.style.top = o.y + 'px';
      (o.parent || this.canvas).appendChild(el);
      const n = Object.assign({ id, kind, el }, o);
      this.nodes[id] = n;
      if (o.onClick !== false)
        el.addEventListener('click', (e) => {
          // The click that ends a drag is not a click on the node.
          if (el.__dragged) return (el.__dragged = false);
          if (o.onClick) o.onClick(e);
          else if (kind === 'device' && this.onDeviceClick) this.onDeviceClick(id);
          else if (kind === 'relay' && this.onRelayClick) this.onRelayClick(id);
        });
      if (o.drag !== false) this.dragNode(id);
      this.dirty = true;
      return n;
    }
    // A node leaves the canvas (the scene animates it away first).
    dropNode(id) {
      const n = this.nodes[id];
      if (!n) return;
      delete this.nodes[id];
      this.dirty = true;
      return n;
    }
    setSub(id, text) {
      const n = this.nodes[id];
      if (!n) return;
      const sm = $('.name small', n.el);
      if (sm.textContent === text) return;
      sm.textContent = text;
      sm.animate([{ opacity: 0, transform: 'translateY(2px)' }, { opacity: 1, transform: 'none' }], { duration: 260, easing: 'ease-out' });
      this.dirty = true;
    }
    move(id, x, y) {
      const n = this.nodes[id];
      if (!n) return;
      if (Math.abs(n.x - x) + Math.abs(n.y - y) > 0.01) this.lastMove = D.now();
      n.x = x;
      n.y = y;
      n.el.style.left = x + 'px';
      n.el.style.top = y + 'px';
      for (const k in this.links) {
        const l = this.links[k];
        if (l.from === id || l.to === id || l.viaId === id) this.relink(l);
      }
      this.dirty = true;
    }
    pt(id) {
      const n = this.nodes[id];
      return { x: n.x, y: n.y };
    }
    link(id, a, b, o = {}) {
      const bend = o.bend == null ? 0.08 : o.bend;
      const l = this.layer.add({
        a: this.pt(a),
        b: this.pt(b),
        via: o.via ? this.pt(o.via) : null,
        bend,
        ca: this.color(a),
        cb: this.color(b),
        state: o.state || 'on',
        trying: o.trying,
        busy: o.busy,
        onRelay: o.via
          ? (c) => {
              // The relay may already be leaving while a light passes it.
              const n = this.nodes[o.via];
              if (!n) return;
              n.el.style.setProperty('--c', `rgb(${c.join(',')})`);
              n.el.classList.add('pass');
              D.after(160, () => n.el.classList.remove('pass'));
            }
          : null,
      });
      Object.assign(l, { id, from: a, to: b, viaId: o.via || null, bend0: bend });
      this.links[id] = l;
      this.dirty = true;
      return l;
    }
    relink(l) {
      if (!this.nodes[l.from] || !this.nodes[l.to]) return;
      l.set({ a: this.pt(l.from), b: this.pt(l.to), via: l.viaId && this.nodes[l.viaId] ? this.pt(l.viaId) : l.o.via });
    }
    unlink(id) {
      const l = this.links[id];
      if (!l) return;
      this.layer.remove(l);
      if (l.pill) l.pill.remove();
      delete this.links[id];
      this.dirty = true;
    }
    // Someone else's device: a shake (it can be moved, not taken away).
    wiggle(el) {
      el.__wiggle = D.now() + 420;
      const t = el.querySelector('.tile') || el.querySelector('.orb') || el;
      t.animate(
        [{ transform: 'none' }, { transform: 'translateX(-5px)' }, { transform: 'translateX(5px)' }, { transform: 'translateX(-3px)' }, { transform: 'translateX(2px)' }, { transform: 'none' }],
        { duration: 380, easing: 'ease-out' }
      );
    }

    // ---- Footprints, free spots ----------------------------------------------------
    // The space a node takes: its tile (or ball) and its name, in canvas
    // coordinates, read from layout sizes (never from transforms, so a
    // lifted or shaking node measures the same).
    footRel(id) {
      const n = this.nodes[id];
      if (!n) return [];
      const half = n.kind === 'relay' ? 20 : 30;
      const out = [R(-half, -half, 2 * half, 2 * half)];
      const nm = n.el.querySelector(':scope > .name');
      const w = nm ? nm.offsetWidth : 0, hh = nm ? nm.offsetHeight : 0;
      if (w && hh) {
        if (n.kind === 'relay') {
          if (nm.classList.contains('below')) out.push(R(-w / 2, 28, w, hh));
          else if (nm.classList.contains('right')) out.push(R(32, -17, w, hh));
          else out.push(R(-32 - w, -17, w, hh));
        } else if (n.el.classList.contains('up')) out.push(R(-w / 2, -40 - hh, w, hh));
        else out.push(R(-w / 2, 40, w, hh));
      }
      return out;
    }
    foot(id, at, pad = 0, rel) {
      const n = this.nodes[id];
      const p = at || n;
      return (rel || this.footRel(id)).map((r) => grow(R(p.x + r.x, p.y + r.y, r.w, r.h), pad));
    }
    // An element's box in canvas coordinates (the canvas may be shifted).
    rectOf(el) {
      const r = el.getBoundingClientRect(), c = this.canvas.getBoundingClientRect();
      return R((r.left - c.left) / this.zoom, (r.top - c.top) / this.zoom, r.width / this.zoom, r.height / this.zoom);
    }
    // Floating things a node may not be put under: the tray, an open card,
    // an invitation, and whatever the scene adds.
    blockers() {
      const out = [];
      const add = (el) => el && el.isConnected && el.style.display !== 'none' && el.offsetWidth && out.push(grow(this.rectOf(el), 6));
      for (const el of this.canvas.querySelectorAll('.tray')) add(el);
      if (this.sheetEl && this.sheetEl.style.display !== 'none') add(this.sheetEl);
      if (this.win) for (const el of this.win.querySelectorAll('.invite')) add(el);
      if (this.o.blockers) out.push(...this.o.blockers());
      return out;
    }
    // The part of the canvas in view.
    bounds() {
      return { x0: 12, y0: 12, x1: this.canvas.clientWidth - 12, y1: this.canvas.clientHeight - 12 };
    }
    // While dragging, the tile stays inside the window.
    clampTile(n, p) {
      const B = this.bounds(), r = n.kind === 'relay' ? 20 : 30;
      return { x: D.clamp(p.x, B.x0 + r, B.x1 - r), y: D.clamp(p.y, B.y0 + r, B.y1 - r) };
    }
    // The nearest place to p where the node fits: inside the view, not on
    // another node or its name (8px apart), not under a floating card.
    freeSpot(id, p, o = {}) {
      const B = this.bounds();
      const rel = this.footRel(id);
      // spotRules: places the scene keeps this node out of (in scene 3, the
      // other LANs' areas).
      const obs = [...(o.more || []), ...this.blockers(), ...(this.spotRules ? this.spotRules(id) : [])];
      for (const k in this.nodes) if (k !== id && !(o.except || []).includes(k)) obs.push(...this.foot(k, null, 22));
      // Other links' paths: a spot no link runs through is better, so a
      // node is not dropped into the middle of a fan of links.
      const pts = [];
      for (const k in this.links) {
        const l = this.links[k];
        if (l.from === id || l.to === id || l.viaId === id || l.state === 'retract') continue;
        for (let s = 0; s < l.len; s += 8) pts.push(l.at(s));
      }
      const fits = (q, clearOfLinks) => {
        for (const r of this.foot(id, q, 0, rel)) {
          if (r.x < B.x0 || r.y < B.y0 || r.x + r.w > B.x1 || r.y + r.h > B.y1) return false;
          for (const b of obs) if (ov(r, b)) return false;
          if (clearOfLinks) {
            const g = grow(r, 6);
            for (const t of pts) if (inside(t, g)) return false;
          }
        }
        return true;
      };
      // Equal choices: the side it came from (o.toward), then round both
      // ways from there, so a node dropped on another bounces back the way
      // it came rather than to an arbitrary side.
      const a0 = o.toward ? Math.atan2(o.toward.y - p.y, o.toward.x - p.x) : Math.PI / 2;
      const search = (clearOfLinks, far) => {
        if (fits(p, clearOfLinks)) return { x: p.x, y: p.y };
        for (let r = 6; r < far; r += 6) {
          const n = Math.max(8, Math.round((2 * Math.PI * r) / 9));
          for (let j = 0; j < n; j++) {
            const i = j % 2 ? (j + 1) / 2 : -j / 2;
            const a = a0 + (i / n) * Math.PI * 2;
            const q = { x: p.x + Math.cos(a) * r, y: p.y + Math.sin(a) * r };
            if (fits(q, clearOfLinks)) return q;
          }
        }
        return null;
      };
      return search(true, 200) || search(false, 700) || { x: p.x, y: p.y };
    }

    // ---- Dragging nodes ---------------------------------------------------------------
    // The scene may set: onNodeTarget(n, p) -> null | {el, accept, hint,
    // shrink, kind}; onNodeDrop(n, t, p) (return false to fall back to a
    // plain move); onNodeMoved(n, at).
    // Nothing is left behind where it was: the whole node (tile, name and
    // status) goes with the hand, and a refused drop springs back home
    // (DESIGN.md §6.1). Only things taken out of a container (the tray)
    // leave their slot faded.
    dragNode(id) {
      const n = this.nodes[id];
      this.drag(n.el, {
        start: (p0) => {
          // The hand takes over from any spring still carrying it.
          if (n.st && n.st.active) n.st.stop();
          n.home = { x: n.x, y: n.y };
          // The point that was grabbed stays under the hand: no jump to
          // put the tile's middle there.
          n.grab = { x: p0.x - n.x, y: p0.y - n.y };
          n.vel = { x: 0, y: 0, t: D.now() };
          this.held = id;
          this.lift(n);
          if (this.onNodeStart) this.onNodeStart(n);
        },
        move: (p) => {
          let q = this.clampTile(n, { x: p.x - n.grab.x, y: p.y - n.grab.y });
          // A scene may stop a node short of a place it cannot go (someone
          // else's device above the tray, inside its own LAN's area).
          const b = this.onNodeBounds && this.onNodeBounds(n);
          if (b) q = { x: D.clamp(q.x, b.x0 ?? -1e9, b.x1 ?? 1e9), y: D.clamp(q.y, b.y0 ?? -1e9, b.y1 ?? 1e9) };
          // The hand's speed (smoothed), so a node let go while moving
          // carries on into its landing instead of stopping dead.
          const now = D.now(), dt = Math.max(1, now - n.vel.t);
          n.vel = { x: n.vel.x * 0.6 + ((q.x - n.x) / dt) * 1000 * 0.4, y: n.vel.y * 0.6 + ((q.y - n.y) / dt) * 1000 * 0.4, t: now };
          this.move(id, q.x, q.y);
          const t = this.onNodeTarget ? this.onNodeTarget(n, p) : null;
          this.light(t && t.accept !== false ? t : null);
          // Over a target that takes it smaller (the tray), it shrinks to
          // that size on a spring, and grows back as it leaves (§6.5).
          this.scaleTo(n, t && t.shrink ? (t.shrink === true ? 0.62 : t.shrink) : 1);
          // Nothing else moves while a node is in the hand: making way
          // happens once it is let go (DESIGN.md §6.3).
          if (this.onNodeDragging) this.onNodeDragging(n, p, t);
        },
        end: (p, cancel) => {
          this.lastMove = D.now();
          this.light(null);
          // A hand that stopped some time ago has no speed left to give.
          if (D.now() - n.vel.t > 80) n.vel = { x: 0, y: 0, t: D.now() };
          this.held = null;
          const t = !cancel && p && this.onNodeTarget ? this.onNodeTarget(n, p) : null;
          // Let go over a target that keeps it small (the tray), it stays
          // that size for the target's own landing (dropScale); anywhere
          // else it grows back to its size as it is put down.
          if (t && t.keep) {
            App.softStop(n.el, 'scale');
            n.dropScale = n.scale || 1;
          } else this.scaleTo(n, 1);
          if (this.onNodeEnd) this.onNodeEnd(n, t, cancel);
          if (!t || !p || cancel) {
            if (cancel || !p) return this.settle(n, n.home, D.Smooth);
            return this.place(n);
          }
          if (t.accept === false) return this.refuse(n, t.hint);
          if (this.onNodeDrop && this.onNodeDrop(n, t, p) !== false) return;
          this.place(n);
        },
      });
    }
    // Let go on open canvas: it stays, in the nearest free spot.
    place(n) {
      const spot = this.freeSpot(n.id, { x: n.x, y: n.y }, { toward: n.home });
      this.focusId = n.id;
      // posKey: a scene may file the place under another name (scene 3
      // remembers a device's place per LAN).
      this.settle(n, spot, D.Snappy, () => this.remember(this.posKey ? this.posKey(n.id) : n.id, spot));
      if (this.onNodeMoved) this.onNodeMoved(n, spot);
    }
    // Not allowed (taking someone else's device out): back home, a shake,
    // and the reason in a toast.
    refuse(n, hint) {
      // returning: on its way home it does not count where it passes (an
      // area in scene 3 would otherwise stretch to follow it).
      n.returning = true;
      this.settle(n, n.home, D.Smooth, () => {
        n.returning = false;
        this.wiggle(n.el);
        if (hint) this.toast(hint);
      });
    }
    // Above everything while held: the lifted node moves to a layer over
    // the card, the tray and the toasts.
    // The node's own size (1 = as it rests), on the soft spring.
    scaleTo(n, s) {
      const cur = App.softNow(n.el, 'scale');
      if (cur == null && (n.scale || 1) === s) return;
      App.soft(n.el, 'scale', cur != null ? cur : n.scale || 1, s, (v, rest) => {
        n.scale = v;
        n.el.style.transform = rest && v === 1 ? '' : `scale(${v.toFixed(4)})`;
      });
    }
    lift(n) {
      if (this.dragLayer && n.el.parentNode !== this.dragLayer) this.dragLayer.appendChild(n.el);
      n.el.classList.add('dragging');
    }
    land(n) {
      n.el.classList.remove('dragging');
      if (this.dragLayer && n.el.parentNode === this.dragLayer) this.canvas.appendChild(n.el);
      const nm = n.el.querySelector(':scope > .name');
      if (nm) nm.style.opacity = '';
    }
    // Spring a node to a place (its links follow), then put it down. A
    // node already on its way is retargeted with the speed it has, never
    // stopped and restarted; one let go by the hand starts with the hand's
    // speed. It is put down when it is truly at rest, so nothing snaps.
    settle(n, to, opts = D.Snappy, done) {
      n.el.classList.remove('dragging');
      const st = n.st;
      if (st && st.active) {
        st.sp.to(to.x, to.y, opts);
        st.to = to;
        st.done = done;
        st.t0 = D.now();
        st.opts = opts;
        return;
      }
      const v = n.vel || { x: 0, y: 0 };
      n.vel = null;
      const sp = new D.Spring2(n.x, n.y, opts);
      sp.jump(n.x, n.y, v.x, v.y);
      sp.to(to.x, to.y, opts);
      const s2 = (n.st = { sp, to, done, opts, t0: D.now(), active: true });
      const off = D.onFrame((now) => {
        if (!this.nodes[n.id]) return s2.stop();
        const x = s2.sp.x.sample(now), y = s2.sp.y.sample(now);
        this.move(n.id, x.x, y.x);
        const rest = Math.hypot(x.x - s2.to.x, y.x - s2.to.y) < 0.2 && Math.hypot(x.v, y.v) < 4;
        if (rest || now - s2.t0 > (s2.opts.duration || 500) * 3) {
          s2.stop();
          this.move(n.id, s2.to.x, s2.to.y);
          this.land(n);
          if (s2.done) s2.done();
        }
      });
      s2.stop = () => {
        off();
        s2.active = false;
      };
    }
    light(t) {
      const el = t && t.el;
      if (this.lit && this.lit !== el) this.lit.classList.remove('target');
      if (el) el.classList.add('target');
      this.lit = el || null;
    }

    // ---- Tidying: links around nodes, chips in clear spots ---------------------------------
    // Re-routing waits for stillness: nothing in the hand, no node moved
    // for a moment, no link still swinging. Then a blocked link bends
    // around once (the rope eases it over) and chips pick their spot once;
    // nothing changes its mind mid-motion, so nothing flickers or jitters.
    quiet(now = D.now()) {
      if (this.held || now - (this.lastMove || -1e9) < 160) return false;
      for (const id in this.nodes) if (this.nodes[id].st && this.nodes[id].st.active) return false;
      return !this.layer.links.some((l) => l.loose());
    }
    tidy() {
      const calm = this.quiet();
      if (this.dirty && calm) {
        this.dirty = false;
        this.feet = {};
        for (const id in this.nodes) this.feet[id] = this.foot(id, null, 4);
        this.bendAround();
      }
      for (const k in this.links) if (this.links[k].pill) this.placePill(this.links[k], calm);
    }
    // A link must not pass under another node or under a name (its own
    // ends' names included); if it does, it tries the other side or a
    // deeper curve, and the rope eases it over. It goes back to its own
    // shape once that is clear again.
    bendAround() {
      for (const k in this.links) {
        const l = this.links[k];
        if (l.state === 'retract' || !this.nodes[l.from] || !this.nodes[l.to]) continue;
        const a = this.pt(l.from), b = this.pt(l.to);
        const v = l.viaId && this.nodes[l.viaId] ? this.pt(l.viaId) : null;
        const pref = l.bendFn ? l.bendFn(a, b) : l.bend0;
        const obs = [];
        for (const id in this.feet) {
          const f = this.feet[id];
          if (id === l.from || id === l.to || id === l.viaId) {
            // Its own ends' names count only where the line would cross
            // the text, not where it passes the corner.
            if (f[1] && f[1].w > 20) obs.push(grow(f[1], -8));
          } else obs.push(...f);
        }
        const hits = (bd) => {
          let c = 0;
          for (const [p, q] of v ? [[a, v], [v, b]] : [[a, b]]) {
            const m = Net.ctl(p, q, bd);
            for (let t = 0.06; t < 0.95; t += 0.068) {
              const s = Net.qAt(p, m, q, t);
              for (const r of obs) if (inside(s, r)) c++;
            }
          }
          return c;
        };
        const cur = l.o.bend;
        let best = cur, bs = hits(cur);
        if (bs > 0) {
          const s0 = Math.abs(pref) < 0.02 ? 0.12 : Math.abs(pref);
          const sg = pref < 0 ? -1 : 1;
          for (const c of [pref, -pref, sg * s0 * 1.8, -sg * s0 * 1.8, 0, sg * 0.3, -sg * 0.3, sg * 0.42, -sg * 0.42, sg * 0.56, -sg * 0.56]) {
            const sc = hits(c);
            if (sc < bs) (bs = sc), (best = c);
          }
        } else if (Math.abs(cur - pref) > 1e-4 && hits(pref) === 0) best = pref;
        if (best !== cur) l.set({ bend: best });
      }
    }

    // A small status chip on a link. Text changes crossfade. It sits at the
    // link's middle (or where the scene asks), and slides along the link to
    // the nearest spot that covers no name, node or other link.
    pill(l, text, o = {}) {
      if (!l.pill) {
        l.pill = h(`<div class="pill"><span></span></div>`);
        this.canvas.appendChild(l.pill);
        l.pill.animate([{ opacity: 0, transform: 'translate(-50%, -50%) scale(0.92)' }, { opacity: 1, transform: 'translate(-50%, -50%)' }], { duration: 240, easing: 'cubic-bezier(0.3, 1.3, 0.5, 1)' });
        l.pillT = null;
      }
      const sp = $('span', l.pill);
      if (sp.textContent !== text) {
        // New words: the chip grows or shrinks to them smoothly instead of
        // snapping to the new width.
        // A change still running is cancelled before the new width is
        // measured: measured mid-way, the chip would end on the old words'
        // width and snap.
        const w0 = l.pill.offsetWidth;
        if (l.pill.__w) l.pill.__w.cancel();
        sp.textContent = text;
        const w1 = l.pill.offsetWidth;
        l.pill.__w = w0 && Math.abs(w1 - w0) > 1 ? l.pill.animate([{ width: w0 + 'px' }, { width: w1 + 'px' }], { duration: 320, easing: 'cubic-bezier(0.3, 0.3, 0.2, 1)' }) : null;
        sp.animate([{ opacity: 0 }, { opacity: 1 }], { duration: 220, easing: 'ease-out' });
      }
      l.pill.dataset.tone = o.tone || '';
      l.pill.onclick = o.onClick || null;
      l.pill.style.cursor = o.onClick ? 'pointer' : '';
      if (o.at) l.pillAt = o.at;
      this.placePill(l, true);
      if (l.pillHandle) l.pillHandle.cancel();
      if (o.hideAfter) l.pillHandle = D.after(o.hideAfter, () => this.hidePill(l));
      return l.pill;
    }
    // A chip sits on its line: at the scene's own point, or at a share t
    // of the line's length, so it moves exactly with the line (no lag, no
    // drift). Only when everything is still does it look for a better
    // spot (no name, node or other line under it), and then it glides
    // there along the line.
    placePill(l, choose) {
      const el = l.pill;
      if (!el) return;
      if (!l.pillT) l.pillT = new D.Spring(0.5, { duration: 420, bounce: 0 });
      if (l.pillMode == null) l.pillMode = l.pillAt ? 'at' : 't';
      if (choose) {
        const w = el.offsetWidth || 90, hh = 26;
        const feet = [];
        for (const id in this.nodes) feet.push(...this.foot(id, null, 3));
        const pts = [];
        for (const k in this.links) {
          const m = this.links[k];
          if (m === l || m.state === 'retract') continue;
          for (let s = 0; s < m.len; s += 10) pts.push(m.at(s));
        }
        const score = (c) => {
          const r = R(c.x - w / 2 - 3, c.y - hh / 2 - 2, w + 6, hh + 4);
          let sc = 0;
          for (const f of feet) if (ov(r, f)) sc += 10;
          for (const q of pts) if (inside(q, r)) sc += 1;
          return sc;
        };
        const cands = [];
        if (l.pillAt) cands.push({ at: true, p: l.pillAt() });
        for (const t of [0.5, 0.42, 0.58, 0.34, 0.66, 0.26, 0.74]) cands.push({ t, p: l.at(l.len * t) });
        const cur = l.pillMode === 'at' ? cands[0] : cands.find((c) => c.t === l.pillT.target) || { t: l.pillT.target, p: l.at(l.len * l.pillT.target) };
        // Stay while the current spot is clear; move only for a clear one.
        if (score(cur.p) >= 1) {
          let best = null, bs = Infinity;
          cands.forEach((c, i) => {
            const sc = score(c.p) + i * 0.01;
            if (sc < bs) (bs = sc), (best = c);
          });
          if (best && best !== cur && bs < score(cur.p)) {
            if (best.at) l.pillMode = 'at';
            else {
              // From the scene's point onto the line: start where it is.
              if (l.pillMode === 'at') l.pillT.jump(this.nearT(l, cur.p));
              l.pillMode = 't';
              l.pillT.to(best.t);
            }
          }
        }
      }
      const p = l.pillMode === 'at' && l.pillAt ? l.pillAt() : l.at(l.len * l.pillT.value);
      el.style.left = p.x + 'px';
      el.style.top = p.y + 'px';
    }
    // The share of a line's length nearest a point.
    nearT(l, p) {
      let bt = 0.5, bd = Infinity;
      for (let t = 0.05; t <= 0.95; t += 0.01) {
        const q = l.at(l.len * t), d = Math.hypot(q.x - p.x, q.y - p.y);
        if (d < bd) (bd = d), (bt = t);
      }
      return bt;
    }
    hidePill(l) {
      const el = l.pill;
      if (!el) return;
      l.pill = null;
      l.pillAt = null;
      l.pillMode = null;
      l.pillT = null;
      // Removal goes by the demo clock, not the animation's finish event,
      // which a recording's virtual clock never fires.
      el.animate([{ opacity: 1 }, { opacity: 0 }], { duration: 200, easing: 'ease-out', fill: 'forwards' });
      D.after(200, () => el.remove());
    }

    // ---- Device card -------------------------------------------------------------
    // key: what the card is about. The entrance plays only when the card
    // opens; refreshing it (a status changed) swaps the words in place,
    // and switching to another device fades the content briefly. An open
    // "更多" stays open.
    sheet(html, key) {
      const s = this.sheetEl;
      if (s.__closing) {
        // Reopened while closing: it stays, as it is.
        s.__closing = false;
        for (const a of s.getAnimations()) a.cancel();
      }
      const wasOpen = s.style.display !== 'none';
      const moreOpen = wasOpen && !!$('.more.open', s);
      const sameKey = wasOpen && key != null && key === this.sheetKey;
      const h0 = wasOpen ? s.offsetHeight : 0;
      // (A height change still running stops here, so the new height is
      // the new words' own, not that change's mid-way value.)
      if (s.__h) s.__h.cancel(), (s.__h = null);
      this.sheetKey = key;
      s.innerHTML = html;
      s.style.display = '';
      // Opening, it grows out of its top right corner (0.96 → 1, about
      // 200ms). Another device: the new words show at once and fade in
      // (two sets fading across each other read as a muddle).
      if (!wasOpen) s.animate([{ opacity: 0, transform: 'translateY(-8px) scale(0.96)' }, { opacity: 1, transform: 'none' }], { duration: 200, easing: 'cubic-bezier(0.2, 0.8, 0.2, 1)' });
      else if (!sameKey && key != null) for (const c of s.children) c.animate([{ opacity: 0 }, { opacity: 1 }], { duration: 150, easing: 'ease-out' });
      const close = $('.sheet-close', s);
      if (close) close.onclick = () => this.closeSheet();
      for (const sw of s.querySelectorAll('.sw:not([data-own])')) sw.addEventListener('click', () => sw.classList.toggle('on'));
      const more = $('.more', s), det = $('.detail', s);
      if (more && moreOpen && sameKey) {
        more.classList.add('open');
        det.style.transition = 'none';
        det.style.height = det.scrollHeight + 'px';
        det.offsetHeight;
        det.style.transition = '';
      }
      if (more)
        more.onclick = () => {
          const open = !more.classList.contains('open');
          more.classList.toggle('open', open);
          det.style.height = open ? det.scrollHeight + 'px' : '0px';
        };
      // Its height follows the new words smoothly.
      if (wasOpen) {
        const h1 = s.offsetHeight;
        if (Math.abs(h1 - h0) > 1) s.__h = s.animate([{ height: h0 + 'px' }, { height: h1 + 'px' }], { duration: 200, easing: 'cubic-bezier(0.3, 0.3, 0.2, 1)' });
      }
      this.dirty = true;
      return s;
    }
    // Closing, it shrinks back into its corner and fades.
    closeSheet() {
      const s = this.sheetEl;
      if (s.style.display === 'none' || s.__closing) return;
      s.__closing = true;
      // From the opacity it shows now: stepped aside (faded out), it just
      // goes, with no flash back in first.
      const o0 = +getComputedStyle(s).opacity;
      s.animate([{ opacity: o0, transform: 'none' }, { opacity: 0, transform: 'translateY(-6px) scale(0.97)' }], { duration: o0 < 0.05 ? 1 : 160, easing: 'ease-in', fill: 'forwards' });
      D.after(170, () => {
        if (!s.__closing) return;
        s.__closing = false;
        s.style.display = 'none';
        for (const a of s.getAnimations()) a.cancel();
      });
      this.sheetKey = null;
      for (const k in this.nodes) this.nodes[k].el.classList.remove('sel');
      if (this.onSheetClose) this.onSheetClose();
      this.dirty = true;
    }
    // The card's header and status block, shared by every scene.
    sheetHead(id, st) {
      const n = this.nodes[id];
      const owner = PEOPLE[n.owner];
      const mine = n.owner === 'me';
      return (
        `<div class="top"><div class="tile-s"><div class="tile" style="--c:${owner.c}">${G.device(n.type, 20)}</div></div>` +
        `<div><b>${esc(n.name)}</b><span>${mine ? '我的设备' : esc(owner.name + '的设备')}</span></div>` +
        `<button class="icon-btn sheet-close" title="关闭">${G.ui('close', 16)}</button></div>` +
        (st ? `<div class="status"><span class="badge ${st.relay ? 'relay-badge' : ''}" style="--c:${st.c || '#a1a1aa'}">${st.icon}</span><div>${esc(st.t)}<small>${esc(st.s)}</small></div></div>` : '')
      );
    }
    select(id) {
      for (const k in this.nodes) this.nodes[k].el.classList.toggle('sel', k === id);
    }
    // Something is on the move: in the hand (a node, a tray tile, an
    // area), or still flying or springing to its place.
    busy() {
      if (this.held || this.handling > 0 || App.flying > 0) return true;
      for (const id in this.nodes) if (this.nodes[id].st && this.nodes[id].st.active) return true;
      return false;
    }

    // ---- Toast -------------------------------------------------------------------
    // A toast says what just happened, with undo (DESIGN.md §6.5): a light
    // chip like the tray and the card, the device's small tile on the left
    // (o.dev), a blue "撤销" on the right. It rises in and sinks out. A new
    // one takes over in the same chip: the chip stays, its width follows,
    // the words fade in. The pointer on it holds it.
    toast(text, o = {}) {
      // The words are words (a name in them shows as typed: esc above).
      text = esc(App.space(String(text)));
      const icon = o.dev ? `<span class="toast-tile"><span class="tile" style="--c:${PEOPLE[o.dev.owner].c}">${G.device(o.dev.type, 15)}</span></span>` : '';
      const body = h(`<div class="toast-body">${icon}<span>${text}</span>${o.undo ? '<button>撤销</button>' : ''}</div>`);
      const parent = o.parent || this.canvas;
      let el = this.toastEl;
      if (el && el.isConnected && !el.__leaving && el.parentNode === parent) {
        // (web) The toast it takes over from: its undo is gone.
        if (el.__gone) {
          const g = el.__gone;
          el.__gone = null;
          g();
        }
        const w0 = el.offsetWidth;
        if (el.__w) el.__w.cancel();
        el.replaceChildren(body);
        el.classList.toggle('has-undo', !!o.undo);
        const w1 = el.offsetWidth;
        el.__w = Math.abs(w1 - w0) > 1 ? el.animate([{ width: w0 + 'px' }, { width: w1 + 'px' }], { duration: 320, easing: 'cubic-bezier(0.3, 0.3, 0.2, 1)' }) : null;
        body.animate([{ opacity: 0 }, { opacity: 1 }], { duration: 150, easing: 'ease-out' });
      } else {
        if (el && el.isConnected) this.dropToast(el);
        el = h(`<div class="toast"></div>`);
        // Each toast its own name in the frame log (an old one may still be
        // fading out when the next one comes).
        el.dataset.k = 't' + (this.toastSeq = (this.toastSeq || 0) + 1);
        el.appendChild(body);
        el.classList.toggle('has-undo', !!o.undo);
        el.style.left = (o.x || 330) + 'px';
        // Above the tray while it shows; at the bottom edge when it does not.
        el.style.bottom = (o.bottom != null ? o.bottom : this.trayUp() ? 90 : 22) + 'px';
        parent.appendChild(el);
        // It must not cover what was just put down: then it shows at the top.
        if (!o.parent && this.focusId && this.nodes[this.focusId]) {
          const r = this.rectOf(el);
          if (this.foot(this.focusId, null, 6).some((f) => ov(f, r))) {
            el.style.bottom = 'auto';
            el.style.top = '16px';
          }
        }
        el.animate([{ opacity: 0, transform: 'translate(-50%, 14px) scale(0.96)' }, { opacity: 1, transform: 'translate(-50%, 0) scale(1)' }], { duration: 300, easing: 'cubic-bezier(0.3, 1.2, 0.5, 1)' });
        el.addEventListener('mouseenter', () => {
          el.__hover = true;
          if (el.__timer) el.__timer.cancel();
        });
        el.addEventListener('mouseleave', () => {
          el.__hover = false;
          el.__timer = D.after(1500, () => this.dropToast(el));
        });
        this.toastEl = el;
      }
      // (web) o.gone: called once its undo can no longer be pressed (it
      // timed out, another toast took its place, the page went), and not
      // when undo was pressed. The web tells the server then (share.release).
      el.__gone = o.gone || null;
      const btn = $('button', body);
      if (btn)
        btn.onclick = () => {
          el.__gone = null;
          this.dropToast(el);
          o.undo();
        };
      if (el.__timer) el.__timer.cancel();
      if (!el.__hover) el.__timer = D.after(o.duration || 4000, () => this.dropToast(el));
      return el;
    }
    dropToast(el) {
      if (!el || !el.isConnected || el.__leaving) return;
      if (el.__gone) {
        const g = el.__gone;
        el.__gone = null;
        g();
      }
      el.__leaving = true;
      if (el.__timer) el.__timer.cancel();
      el.animate([{ opacity: 1, transform: 'translate(-50%, 0) scale(1)' }, { opacity: 0, transform: 'translate(-50%, 8px) scale(0.98)' }], { duration: 200, easing: 'ease-in', fill: 'forwards' });
      D.after(210, () => el.remove());
      if (this.toastEl === el) this.toastEl = null;
    }
    // Whether the tray is up (a toast then sits above it).
    trayUp() {
      const t = this.canvas && this.canvas.querySelector('.tray');
      return !!t && !t.classList.contains('away');
    }

    // ---- Dragging ------------------------------------------------------------------
    // Pointer position in canvas coordinates (the canvas is zoomed).
    local(e, ref = this.canvas) {
      const r = ref.getBoundingClientRect();
      return { x: (e.clientX - r.left) / this.zoom, y: (e.clientY - r.top) / this.zoom };
    }
    // Press, then move past 4px to drag; Escape cancels. Handlers get
    // canvas coordinates.
    drag(el, o) {
      el.addEventListener('pointerdown', (e) => {
        if (e.button !== 0) return;
        // ignore: parts that are buttons of their own (a × on a chip).
        if (o.ignore && e.target.closest(o.ignore)) return;
        e.preventDefault();
        e.stopPropagation();
        const ref = o.ref || this.canvas;
        const p0 = this.local(e, ref);
        let dragging = false, done = false;
        el.__dragged = false;
        if (o.press) o.press(p0);
        const move = (ev) => {
          const p = this.local(ev, ref);
          if (!dragging && Math.hypot(p.x - p0.x, p.y - p0.y) > 4) {
            dragging = true;
            this.handling++;
            if (o.start) o.start(p0, p);
          }
          if (dragging && o.move) o.move(p);
        };
        const finish = (ev, cancel) => {
          if (done) return;
          done = true;
          removeEventListener('pointermove', move);
          removeEventListener('pointerup', up);
          this.onEscape = null;
          if (!dragging) return o.click && o.click(p0);
          this.handling--;
          el.__dragged = true;
          this.dragClick = true;
          setTimeout(() => (this.dragClick = false), 0);
          if (o.end) o.end(ev ? this.local(ev, ref) : null, cancel);
        };
        const up = (ev) => finish(ev, false);
        addEventListener('pointermove', move);
        addEventListener('pointerup', up);
        this.onEscape = () => finish(null, true);
      });
    }
  }

  // Recording only: what is on screen, frame by frame, for
  // design/tools/motion-check.mjs to find jumps, jitter and flicker. Each
  // entry is the visible box (after every transform) and opacity.
  App.prototype.trace = function () {
    const T = (window.__trace = []);
    const ref = this.canvas || this.o.stageEl;
    const r1 = (v) => Math.round(v * 10) / 10;
    // A hidden element shows as transparent, whatever its opacity says.
    const op = (el) => {
      const cs = getComputedStyle(el);
      return cs.visibility === 'hidden' ? 0 : r1(+cs.opacity);
    };
    D.onSample((now) => {
      const c = ref.getBoundingClientRect(), z = this.zoom;
      const box = (el) => {
        const r = el.getBoundingClientRect();
        return [r1((r.left - c.left + r.width / 2) / z), r1((r.top - c.top + r.height / 2) / z), r1(r.width / z), r1(r.height / z)];
      };
      const f = { t: Math.round(now), h: this.held || 0, e: {}, l: {} };
      // (web) Each page's boxes under its own names: a device on two LANs'
      // pages is two boxes, one going as the other comes, not one jumping.
      const pg = this.page ? this.page + '/' : '';
      for (const id in this.nodes) {
        const n = this.nodes[id];
        const tile = n.el.querySelector('.tile, .orb') || n.el;
        const nm = n.el.querySelector(':scope > .name');
        f.e['node:' + pg + id] = [...box(tile), op(n.el), (n.el.__wiggle || 0) > now ? 1 : 0, this.held === id ? 1 : 0];
        if (nm) f.e['name:' + pg + id] = [...box(nm), op(nm), 0, this.held === id ? 1 : 0];
      }
      for (const k in this.links) {
        const l = this.links[k];
        f.l[k] = [Math.round(l.o.bend * 1000) / 1000, l.state];
        if (l.pill) f.e['pill:' + k] = [...box(l.pill), op(l.pill), 0, 0];
      }
      for (const sel of ['.area', '.toast', ...(this.o.trace || [])])
        for (const el of document.querySelectorAll(sel)) {
          const key = el.dataset.id || el.dataset.d || el.dataset.k;
          if (key && el.offsetParent) f.e[sel + ':' + pg + key] = [...box(el), op(el), 0, el.classList.contains('lifted') || el.classList.contains('ghost') || el.classList.contains('dragging') ? 1 : 0];
        }
      if (this.sheetEl) f.sheet = this.sheetEl.style.display !== 'none' ? op(this.sheetEl) : -1;
      // Stepped aside for something on the move, or coming back: a fade
      // that is meant, not a blink.
      const away = this.sheetEl && (this.sheetEl.classList.contains('away') || now < (this.awayEnd || 0)) ? 1 : 0;
      if (away) f.sheetAway = 1;
      // The card's box too: its height must follow new words smoothly.
      if (f.sheet != null && f.sheet >= 0) f.e['sheet:card'] = [...box(this.sheetEl), f.sheet, 0, away];
      f.toasts = [...document.querySelectorAll('.toast')].map(op);
      T.push(f);
    });
  };

  // A curve over a duration on the demo clock (records at its true speed).
  // A number on the native client's "soft" spring (360ms, no bounce),
  // drawn each frame by apply(value, atRest): the tray's words and its
  // slot grow and shrink on it, together (DESIGN.md §6.5). A spring keeps
  // its speed when the target changes mid-way, so words that change back
  // and forth make the tray ease round, never stop dead or jump (§6.6).
  // from is used only when nothing is moving it yet; done() runs once it
  // rests at the target.
  App.soft = (el, key, from, target, apply, done) => {
    const k = '__soft_' + key;
    let st = el[k];
    if (!st) st = el[k] = { sp: new D.Spring(from, { duration: 360, bounce: 0 }), off: null };
    // (web) At rest within a hundredth of the way it has to go, at most 0.2:
    // a width in pixels rests as before; a scale (0.62 → 1) no longer
    // snaps to its end at 0.8.
    const span = Math.abs(target - (st.off ? st.sp.sample(D.now()).x : from)) || 1;
    st.tol = Math.min(0.2, span * 0.01);
    st.vtol = Math.min(4, span * 0.2);
    st.sp.to(target);
    st.apply = apply;
    st.done = done;
    if (!st.off)
      st.off = D.onFrame((now) => {
        const { x, v } = st.sp.sample(now);
        if (Math.abs(x - st.sp.target) < st.tol && Math.abs(v) < st.vtol) {
          st.off();
          delete el[k];
          st.apply(st.sp.target, true);
          if (st.done) st.done();
        } else st.apply(x, false);
      });
    return st.sp;
  };
  // Where App.soft has it now (or null when nothing moves it).
  App.softNow = (el, key) => (el['__soft_' + key] ? el['__soft_' + key].sp.value : null);
  App.softStop = (el, key) => {
    const st = el['__soft_' + key];
    if (st && st.off) st.off();
    delete el['__soft_' + key];
  };
  // (Something on a tween is in flight: the card stays aside meanwhile.)
  App.flying = 0;
  App.tween = (dur, fn, ease = D.Ease.inOut) =>
    new Promise((res) => {
      const t0 = D.now();
      App.flying++;
      const off = D.onFrame((now) => {
        const k = Math.min(1, (now - t0) / dur);
        fn(ease(k));
        if (k >= 1) {
          off();
          App.flying--;
          res();
        }
      });
    });
  App.wait = (ms) => new Promise((r) => D.after(ms, r));
  // A space between Chinese and Latin text, as everywhere else in the UI
  // ("我的 NAS"), when a name is dropped into a sentence.
  App.space = (t) => t.replace(/([一-鿿])([A-Za-z0-9])/g, '$1 $2').replace(/([A-Za-z0-9])([一-鿿])/g, '$1 $2');
  App.R = R;
  App.ov = ov;
  App.inside = inside;
  App.grow = grow;

  App.PEOPLE = PEOPLE;
  App.SCENES = SCENES;
  App.ball = ball;
  App.dot = dot;
  App.h = h;
  window.App = App;
})();
