// The relays pages of the web (DESIGN.md §14.8, §14.9; design/plan/m4/
// web-relays.md), shared by m4-11, m4-12 and m4-13: the wells and cells of
// M3-15, the share bar (M4-11), a relay dragged onto a LAN in the sidebar
// and a LAN chip dragged out of its cell (M4-12), the card with the
// desktop's sections (§6.12, M4-02) and "多用 / 少用 / 只当备用".
// Names as the desktop's batch 1 has them: a relay's name when it has one;
// an address only on its ball and the card's title; "我的服务器" in a
// sentence. Latin, digits and Han apart by a space (App.space).
(function () {
  const WR = (window.WR = {});
  const P = App.PEOPLE;
  const $ = (s, r) => r.querySelector(s);
  const sp = (t) => App.space(t);
  WR.RELAY = (size = 20) => G.device('relay', size);
  WR.RING = (size = 44) => `<svg class="ring" viewBox="0 0 ${size} ${size}" width="${size}" height="${size}" aria-hidden="true"><circle cx="${size / 2}" cy="${size / 2}" r="${size / 2 - 1.75}" fill="none" stroke="currentColor" stroke-width="1.5" stroke-dasharray="4 3.5"/></svg>`;
  WR.title = (r) => r.name || r.addr;
  WR.said = (r) => r.name || (!r.owner || r.owner === 'me' ? '我的服务器' : `${P[r.owner].name}的服务器`);

  // A page of relays: wells of cells, a share bar over the first well when
  // two or more relays share, a card for each. cfg:
  //   wells: [{ key, label, x, w, cols, relays, add?, bar?, drag? ('lan' | 'fixed') }]
  //   lans: { id: { name, dots } }        the sidebar's LANs (M4-12)
  //   barSay(active, spare)              the line under the bar
  //   card(r, api)                        the card's HTML for a relay
  //   sub(r)                              a cell's second line (no LAN chips)
  WR.page = function (app, cfg) {
    const win = app.win, cv = app.canvas;
    const api = { app, cfg, wells: {} };
    const zoom = () => app.zoom || 1;
    const rectOf = (el) => {
      const r = el.getBoundingClientRect(), w = win.getBoundingClientRect();
      return { x: (r.left - w.left) / zoom(), y: (r.top - w.top) / zoom(), w: r.width / zoom(), h: r.height / zoom() };
    };
    const center = (el) => {
      const r = rectOf(el);
      return { x: r.x + r.w / 2, y: r.y + r.h / 2 };
    };
    const hit = (el, p, pad = 0) => {
      const r = rectOf(el);
      return p.x >= r.x - pad && p.x <= r.x + r.w + pad && p.y >= r.y - pad && p.y <= r.y + r.h + pad;
    };
    api.rectOf = rectOf;
    const lanOf = (id) => cfg.lans[id];
    const dots = (l) => `<span class="mdots">${l.dots.map((k) => App.dot(P[k].c)).join('')}</span>`;
    const lchip = (r, id) => `<span class="lchip" data-l="${id}" data-r="${r.id}">${dots(lanOf(id))}${sp(lanOf(id).name)}</span>`;
    api.lchip = lchip;
    api.dots = dots;

    // ---- Wells and cells ------------------------------------------------------------------
    const cellHTML = (W, r) => {
      const busy = r.busy && r.busy !== '不忙' ? ' busy' : '';
      let below;
      if (W.drag === 'lan') below = r.lans.length ? `<div class="lchips">${r.lans.map((id) => lchip(r, id)).join('')}</div>` : `<small class="none">还没进局域网<br />拖到左边的局域网上</small>`;
      else below = `<small>${sp(cfg.sub ? cfg.sub(r, W) : '')}</small>`;
      return `<div class="rcell" data-k="${r.id}" data-w="${W.key}"><div class="orb">${WR.RELAY()}<span class="on${busy}"></span></div><div class="rname">${sp(WR.title(r))}</div>${below}</div>`;
    };
    const addHTML = (W) =>
      W.pending
        ? `<div class="rcell add-cell" data-k="join" data-w="${W.key}"><div class="orb add wait">${WR.RING()}${G.ui('relayLine', 18)}</div><div>等待安装<small>命令还没跑</small></div></div>`
        : `<div class="rcell add-cell" data-k="add" data-w="${W.key}"><div class="orb add">${WR.RING()}${G.ui('plus', 18)}</div><div>${W.add.label}<small>${W.add.sub}</small></div></div>`;
    for (const W of cfg.wells) {
      const el = App.h(`<section class="rwell" data-k="well-${W.key}" style="left:${W.x}px;width:${W.w}px"><div class="card-label"><span>${W.label}</span><span class="n"></span></div><div class="wbar-slot"></div><div class="rgrid" style="grid-template-columns:repeat(${W.cols},1fr)"></div></section>`);
      cv.appendChild(el);
      W.el = el;
      api.wells[W.key] = W;
    }
    app.o.trace = (app.o.trace || []).concat(['.rcell', '.rwell', '.wseg', '.lchip', '.rpill']);

    // Draw a well again, its height easing to the new one (§5).
    api.draw = function (key, o = {}) {
      const W = api.wells[key], el = W.el;
      const h0 = el.offsetHeight;
      const before = new Map([...el.querySelectorAll('.rcell')].map((c) => [c.dataset.k, c.getBoundingClientRect()]));
      $('.rgrid', el).innerHTML = W.relays.map((r) => cellHTML(W, r)).join('') + (W.add ? addHTML(W) : '');
      $('.n', el).textContent = `${W.relays.length} 个`;
      if (W.bar) api.bar(W, o.barAnim);
      const h1 = el.offsetHeight;
      if (h0 && Math.abs(h1 - h0) > 1 && !o.still) el.animate([{ height: h0 + 'px' }, { height: h1 + 'px' }], { duration: 360, easing: 'cubic-bezier(0.3, 0.3, 0.2, 1)' });
      // A bar coming in opens its own room (the cells under it slide down
      // as the well grows), then fades in.
      if (W.barGrow) {
        W.barGrow = false;
        const slot = $('.wbar-slot', el), h = slot.offsetHeight;
        slot.style.overflow = 'hidden';
        slot.animate([{ height: '0px' }, { height: h + 'px' }], { duration: 360, easing: 'cubic-bezier(0.3, 0.3, 0.2, 1)' }).finished.then(() => (slot.style.overflow = ''));
        slot.firstChild.animate([{ opacity: 0 }, { opacity: 1 }], { duration: 300, delay: 120, easing: 'ease-out', fill: 'backwards' });
      }
      // Cells that moved (a neighbour grew or shrank) glide to their new place.
      if (!o.still)
        for (const c of el.querySelectorAll('.rcell')) {
          const b = before.get(c.dataset.k);
          if (!b) continue;
          const a = c.getBoundingClientRect();
          const dx = (b.left - a.left) / zoom(), dy = (b.top - a.top) / zoom();
          if (Math.abs(dx) + Math.abs(dy) > 0.5) c.animate([{ transform: `translate(${dx}px, ${dy}px)` }, { transform: 'none' }], { duration: 360, easing: 'cubic-bezier(0.3, 0.3, 0.2, 1)' });
        }
      wire(W);
      select(api.selected);
    };
    function select(k) {
      api.selected = k;
      for (const c of win.querySelectorAll('.rcell')) c.classList.toggle('sel', c.dataset.k === k);
    }
    api.select = select;
    const findRelay = (k) => {
      for (const W of cfg.wells) {
        const r = W.relays.find((x) => x.id === k);
        if (r) return { W, r };
      }
      return null;
    };
    api.find = findRelay;

    // ---- The share bar (M4-11) ----------------------------------------------------------------
    // Relays in use (in a LAN, for my own; all public ones) share the devices
    // by their weight; a standby one (weight 0) waits at the bar's end.
    const inUse = (W) => W.relays.filter((r) => (W.drag === 'lan' ? r.lans.length > 0 : true));
    const GAP = 6;
    api.bar = function (W, anim) {
      const slot = $('.wbar-slot', W.el);
      const use = inUse(W);
      const shown = use.length >= 2;
      if (!shown) {
        if (slot.firstChild) {
          const h0 = slot.offsetHeight;
          slot.replaceChildren();
          if (anim) slot.animate([{ height: h0 + 'px', opacity: 1 }, { height: '0px', opacity: 0 }], { duration: 300, easing: 'ease-in' });
        }
        return;
      }
      let grow = false;
      if (!slot.firstChild) {
        slot.innerHTML = `<div class="wbar-wrap"><div class="wbar"></div><p class="wsay"></p></div>`;
        grow = anim;
      }
      const bar = $('.wbar', slot);
      // Elements: a segment per relay in use, a standby pill per relay.
      for (const r of use) {
        if (!$(`.wseg.act[data-r="${r.id}"]`, bar)) bar.appendChild(App.h(`<div class="wseg act" data-r="${r.id}" data-k="seg-${r.id}"><span class="nm"></span><span class="pct"></span></div>`));
        if (!$(`.wseg.spare[data-r="${r.id}"]`, bar)) bar.appendChild(App.h(`<div class="wseg spare" data-r="${r.id}" data-k="spare-${r.id}"><span class="nm"></span></div>`));
      }
      for (const el of [...bar.querySelectorAll('.wseg')]) if (!use.some((r) => r.id === el.dataset.r)) el.remove();
      if (!$('.wtip', bar)) bar.appendChild(App.h(`<div class="wtip" data-k="wtip"></div>`));
      for (const el of bar.querySelectorAll('.wseg.act')) {
        if (el.__tip) continue;
        el.__tip = true;
        const tip = () => $('.wtip', bar);
        el.addEventListener('mouseenter', () => {
          if (!el.classList.contains('narrow')) return;
          const t = tip();
          t.textContent = el.dataset.name;
          t.style.left = parseFloat(el.style.left) + parseFloat(el.style.width) / 2 + 'px';
          t.classList.add('on');
        });
        el.addEventListener('mouseleave', () => tip().classList.remove('on'));
      }
      W.barEl = bar;
      layoutBar(W);
      wireHandles(W);
      if (grow) W.barGrow = true;
    };
    // Where each part goes, for shares s (active, summing to 100) and t
    // (how far each standby pill has come in, 0..1).
    function layoutBar(W) {
      const bar = W.barEl;
      if (!bar || !bar.isConnected) return;
      const use = inUse(W);
      const width = bar.clientWidth;
      const spareW = (r) => Math.ceil(measure(`${sp(WR.title(r))} · 备用`)) + 26;
      let right = width;
      const spares = [];
      for (const r of use.slice().reverse()) {
        const t = r.spareT ?? (r.share <= 0 ? 1 : 0);
        const w = spareW(r);
        const el = $(`.wseg.spare[data-r="${r.id}"]`, bar);
        if (t <= 0.001) {
          el.style.display = 'none';
          continue;
        }
        el.style.display = '';
        right -= (w + GAP) * t;
        el.style.left = right + GAP * t + 'px';
        el.style.width = w + 'px';
        el.style.opacity = t;
        $('.nm', el).textContent = sp(`${WR.title(r)} · 备用`);
        spares.push(r);
      }
      const area = right;
      const act = use.filter((r) => (r.shareT ?? r.share) > 0.01);
      const total = act.reduce((a, r) => a + (r.shareT ?? r.share), 0) || 1;
      let x = 0;
      for (const r of use) {
        const el = $(`.wseg.act[data-r="${r.id}"]`, bar);
        const s = r.shareT ?? r.share;
        if (s <= 0.01) {
          el.style.display = 'none';
          continue;
        }
        el.style.display = '';
        const w = (s / total) * area;
        const last = act[act.length - 1] === r;
        const gl = x > 0 ? GAP / 2 : 0, gr = last ? 0 : GAP / 2;
        el.style.left = x + gl + 'px';
        el.style.width = Math.max(0, w - gl - gr) + 'px';
        el.style.opacity = Math.min(1, s / 8);
        // Held, the number shows where it will land (5% steps); moving on its
        // own, it rolls with the width.
        const pct = r.held ? r.share : Math.round(s);
        const segW = w - gl - gr;
        const name = sp(WR.title(r));
        const both = measure(name, 500) + 6 + measure(`${pct}%`) + 20 <= segW;
        $('.nm', el).textContent = both ? name : '';
        $('.pct', el).textContent = both || measure(`${pct}%`) + 12 <= segW ? `${pct}%` : '';
        // Too narrow for its name: the name shows over it on hover.
        el.classList.toggle('narrow', !both);
        el.dataset.name = name;
        x += w;
      }
      const say = $('.wsay', bar.parentNode);
      if (say) say.textContent = sp(cfg.barSay(use.filter((r) => r.share > 0), use.filter((r) => r.share <= 0)));
      placeHandles(W);
    }
    api.layoutBar = layoutBar;
    const mctx = document.createElement('canvas').getContext('2d');
    function measure(t, weight = 400) {
      mctx.font = `${weight} 12px -apple-system, "PingFang SC", "Helvetica Neue", sans-serif`;
      return mctx.measureText(t).width;
    }
    // Handles sit in the gaps between active segments.
    function placeHandles(W) {
      const bar = W.barEl;
      const act = inUse(W).filter((r) => (r.shareT ?? r.share) > 0.01);
      const hs = [...bar.querySelectorAll('.whandle')];
      for (let i = 0; i < act.length - 1; i++) {
        let h = hs[i];
        if (!h) {
          h = App.h(`<div class="whandle" data-k="handle-${i}"><i></i></div>`);
          bar.appendChild(h);
          hs.push(h);
          h.__new = true;
        }
        const el = $(`.wseg.act[data-r="${act[i].id}"]`, bar);
        h.style.left = parseFloat(el.style.left) + parseFloat(el.style.width) + GAP / 2 + 'px';
        h.dataset.a = act[i].id;
        h.dataset.b = act[i + 1].id;
        h.style.display = '';
      }
      for (let i = Math.max(0, act.length - 1); i < hs.length; i++) hs[i].style.display = 'none';
    }
    // Drag a handle: the two segments beside it trade, following the hand
    // (no jumps); the share lands on 5% steps, none under 10%, eased there
    // when let go.
    function wireHandles(W) {
      for (const h of W.barEl.querySelectorAll('.whandle')) {
        if (h.__wired) continue;
        h.__wired = true;
        let a, b, sum, x0, left;
        app.drag(h, {
          ref: W.barEl,
          start: (p0) => {
            a = findRelay(h.dataset.a).r;
            b = findRelay(h.dataset.b).r;
            sum = a.share + b.share;
            const ea = $(`.wseg.act[data-r="${a.id}"]`, W.barEl), eb = $(`.wseg.act[data-r="${b.id}"]`, W.barEl);
            left = parseFloat(ea.style.left);
            x0 = { w: parseFloat(eb.style.left) + parseFloat(eb.style.width) - left };
            h.classList.add('held');
          },
          move: (p) => {
            const f = D.clamp((p.x - left) / x0.w, 0, 1);
            const raw = D.clamp(f * sum, 10, sum - 10);
            a.held = b.held = true;
            a.shareT = raw;
            b.shareT = sum - raw;
            a.share = D.clamp(Math.round(raw / 5) * 5, 10, sum - 10);
            b.share = sum - a.share;
            layoutBar(W);
          },
          end: async () => {
            h.classList.remove('held');
            a.held = b.held = false;
            await api.shares(W, { [a.id]: a.share, [b.id]: b.share }, 240);
            if (cfg.onWeights) cfg.onWeights([a, b]);
            if (api.selected === a.id || api.selected === b.id) api.reopen();
          },
        });
      }
    }
    // Shares to new values, smoothly (Snappy's 500 ms, eased in and out): the
    // percentages roll, a standby pill comes in or goes.
    api.shares = function (W, next, dur = 500) {
      const use = inUse(W);
      const from = new Map(use.map((r) => [r.id, { s: r.shareT ?? r.share, t: r.spareT ?? (r.share <= 0 ? 1 : 0) }]));
      for (const r of use) {
        r.share = next[r.id] != null ? next[r.id] : r.share;
        r.spareGoal = r.share <= 0 ? 1 : 0;
      }
      return App.tween(dur, (k) => {
        for (const r of use) {
          const f = from.get(r.id);
          r.shareT = f.s + (r.share - f.s) * k;
          r.spareT = f.t + (r.spareGoal - f.t) * k;
        }
        layoutBar(W);
      }, D.Ease.inOut).then(() => {
        for (const r of use) delete r.shareT;
        layoutBar(W);
      });
    };
    // "多用 / 少用 / 只当备用" (the card's three buttons).
    api.more = function (W, r) {
      const use = inUse(W);
      const act = use.filter((x) => x.share > 0 && x !== r);
      const goal = r.share > 0 ? Math.min(90, r.share * 2) : r.prev || 20;
      return api.shares(W, spread(use, r, goal, act));
    };
    api.less = function (W, r) {
      const use = inUse(W);
      const act = use.filter((x) => x.share > 0 && x !== r);
      return api.shares(W, spread(use, r, Math.max(10, r.share / 2), act));
    };
    api.spare = function (W, r) {
      const use = inUse(W);
      const act = use.filter((x) => x.share > 0 && x !== r);
      if (r.share > 0) r.prev = r.share;
      return api.shares(W, spread(use, r, 0, act));
    };
    // r takes goal (5% steps); the others share the rest as they did.
    function spread(use, r, goal, others) {
      goal = Math.round(goal / 5) * 5;
      const out = { [r.id]: goal };
      const rest = 100 - goal;
      const had = others.reduce((a, x) => a + x.share, 0) || 1;
      let given = 0;
      others.forEach((x, i) => {
        const v = i === others.length - 1 ? rest - given : Math.round(((x.share / had) * rest) / 5) * 5;
        out[x.id] = v;
        given += v;
      });
      for (const x of use) if (out[x.id] == null) out[x.id] = x.share;
      return out;
    }

    // ---- Cards ----------------------------------------------------------------------
    api.open = function (k) {
      const f = findRelay(k);
      if (!f) return;
      select(k);
      const s = app.sheet(cfg.card(f.r, f.W, api), k);
      wireCard(s, f.W, f.r);
    };
    api.reopen = () => app.sheetKey && findRelay(app.sheetKey) && app.sheetEl.style.display !== 'none' && api.open(app.sheetKey);
    function wireCard(s, W, r) {
      const seg = s.querySelector('.seg');
      if (seg) {
        for (const b of seg.querySelectorAll('button')) {
          b.onclick = async () => {
            const act = b.dataset.act;
            b.animate([{ transform: 'scale(0.96)' }, { transform: 'none' }], { duration: 180, easing: 'ease-out' });
            if (act === 'more') await api.more(W, r);
            else if (act === 'less') await api.less(W, r);
            else if (act === 'spare' && r.share > 0) await api.spare(W, r);
            api.open(r.id);
          };
        }
      }
      for (const ch of s.querySelectorAll('.chip[data-k]')) ch.onclick = () => api.kick(r, ch.dataset.k, ch);
      for (const x of s.querySelectorAll('.lan-row .lchip .x')) x.onclick = () => api.leave(r, x.closest('.lchip').dataset.l);
      const add = s.querySelector('.lan-row .lchip.add');
      if (add) add.onclick = () => api.addMenu(r, add);
      if (cfg.wire) cfg.wire(s, W, r, api);
    }
    // Who uses it: a click turns them away, again lets them back (M4-02).
    api.kick = function (r, k, chipEl, o = {}) {
      r.kicked = r.kicked || {};
      const on = !r.kicked[k];
      r.kicked[k] = on;
      chipEl.classList.toggle('blocked', on);
      chipEl.querySelector('.ball').classList.toggle('off', on);
      if (cfg.onKick) cfg.onKick(r, k, on);
      // Undo just puts it back, with no word of its own.
      if (!o.quiet) app.toast(on ? `已不让${P[k].name}用${WR.said(r)}` : `又让${P[k].name}用${WR.said(r)}了`, { undo: () => chipEl.isConnected && api.kick(r, k, chipEl, { quiet: true }) });
    };

    // ---- Into a LAN, out of it (M4-12) ----------------------------------------------------------
    const sideRow = (id) => win.querySelector(`.side .nav[data-id="${id}"]`);
    const lanAt = (p) => Object.keys(cfg.lans).find((id) => hit(sideRow(id), p, 2));
    const lightRow = (id) => {
      for (const l of Object.keys(cfg.lans)) sideRow(l).classList.toggle('target', l === id);
    };
    function pill(html, cls = '') {
      const g = App.h(`<div class="rpill ${cls}">${html}<span class="rnote"></span></div>`);
      win.appendChild(g);
      return g;
    }
    const placeAt = (g, p, s = 1) => {
      g.style.left = p.x + 'px';
      g.style.top = p.y + 'px';
      g.style.transform = `translate(-50%, -50%) rotate(-2deg) scale(${s})`;
    };
    const note = (g, text, go) => {
      const n = $('.rnote', g);
      if (n.textContent !== text) n.textContent = sp(text);
      n.classList.toggle('go', !!go);
      n.style.opacity = text ? 1 : 0;
    };
    async function fly(g, from, to, s0, s1, fade) {
      await App.tween(320, (t) => {
        placeAt(g, { x: from.x + (to.x - from.x) * t, y: from.y + (to.y - from.y) * t }, s0 + (s1 - s0) * t);
        if (fade) g.style.opacity = 1 - 0.85 * t;
      }, fade ? D.Ease.in : D.Ease.out);
    }
    // The LAN takes it: the capsule flies into the row; the row's soft blue
    // flash; the chip pops into the cell; a toast with undo.
    api.join = function (r, id, o = {}) {
      if (r.lans.includes(id)) return;
      const W = findRelay(r.id).W;
      const was = inUse(W).includes(r);
      r.lans.push(id);
      // Newly in use: it takes an equal share, the others make room as they share.
      if (!was) {
        const others = inUse(W).filter((x) => x !== r && x.share > 0);
        if (!others.length) r.share = 100;
        else {
          const next = spread(inUse(W), r, 100 / (others.length + 1), others);
          for (const x of inUse(W)) x.share = next[x.id];
        }
      }
      if (cfg.onJoin) cfg.onJoin(r, id, W);
      api.draw(W.key, { barAnim: true });
      const c = win.querySelector(`.rcell[data-k="${r.id}"] .lchip[data-l="${id}"]`);
      if (c) c.animate([{ transform: 'scale(0.5)', opacity: 0 }, { transform: 'scale(1.1)', opacity: 1, offset: 0.6 }, { transform: 'none' }], { duration: 480, easing: 'cubic-bezier(0.3, 1.2, 0.5, 1)' });
      const row = sideRow(id);
      row.animate([{ background: 'color-mix(in srgb, var(--primary) 16%, var(--bg))' }, { background: 'color-mix(in srgb, var(--primary) 16%, var(--bg))', offset: 0.3 }], { duration: 1100, easing: 'ease-out' });
      if (!o.quiet) app.toast(`${lanOf(id).name}用上了${WR.said(r)}`, { undo: () => api.leave(r, id, { quiet: true }) });
      api.reopen();
    };
    api.leave = function (r, id, o = {}) {
      if (!r.lans.includes(id)) return;
      const W = findRelay(r.id).W;
      const c = win.querySelector(`.rcell[data-k="${r.id}"] .lchip[data-l="${id}"]`);
      const done = () => {
        r.lans = r.lans.filter((x) => x !== id);
        // Out of use: the others share all of it again.
        if (!inUse(W).includes(r) && r.share > 0) {
          const others = inUse(W).filter((x) => x.share > 0);
          const had = others.reduce((a, x) => a + x.share, 0) || 1;
          let given = 0;
          others.forEach((x, i) => {
            x.share = i === others.length - 1 ? 100 - given : Math.round((x.share / had) * 100 / 5) * 5;
            given += x.share;
          });
          r.prev = r.share;
          r.share = 0;
        }
        if (cfg.onLeave) cfg.onLeave(r, id, W);
        api.draw(W.key, { barAnim: true });
        if (!o.quiet) app.toast(`${lanOf(id).name}不用${WR.said(r)}了`, { undo: () => api.join(r, id, { quiet: true }) });
        api.reopen();
      };
      if (c && !o.flown) c.animate([{ transform: 'none', opacity: 1 }, { transform: 'scale(0.5)', opacity: 0 }], { duration: 220, easing: 'ease-in', fill: 'forwards' }).finished.then(done);
      else done();
    };
    // "+ 局域网" in the card: my LANs it is not in, a click puts it in.
    api.addMenu = function (r, btn) {
      const free = Object.keys(cfg.lans).filter((id) => !r.lans.includes(id));
      const row = btn.closest('.lan-row');
      if (!free.length) return;
      btn.replaceWith(...free.map((id) => {
        const el = App.h(`<button class="lchip add pick" data-l="${id}">${dots(lanOf(id))}${sp(lanOf(id).name)}</button>`);
        el.onclick = () => api.join(r, id);
        el.animate([{ opacity: 0, transform: 'scale(0.9)' }, { opacity: 1, transform: 'none' }], { duration: 200, easing: 'ease-out' });
        return el;
      }));
      row.querySelectorAll('.pick').forEach((x) => (x.style.cursor = 'pointer'));
    };

    // Cells: a click opens the card; mine drag onto a LAN; a LAN chip drags out.
    function wire(W) {
      for (const c of W.el.querySelectorAll('.rcell')) {
        const k = c.dataset.k;
        if (k === 'add' || k === 'join') {
          c.onclick = () => cfg.openAdd && cfg.openAdd(W, api);
          continue;
        }
        if (!W.drag) {
          c.onclick = () => api.open(k);
          continue;
        }
        const r = W.relays.find((x) => x.id === k);
        let g = null, home = null, grab = null;
        app.drag(c, {
          ref: win,
          ignore: '.lchip',
          click: () => api.open(k),
          start: (p0) => {
            home = center($('.orb', c));
            grab = { x: p0.x - home.x, y: p0.y - home.y, t: D.now() };
            g = pill(`<div class="orb">${WR.RELAY(16)}</div><span>${sp(WR.title(r))}</span>`);
            placeAt(g, home);
            g.animate([{ opacity: 0.6 }, { opacity: 1 }], { duration: 120, easing: 'ease-out' });
            c.classList.add('ghost');
          },
          move: (p) => {
            const k2 = D.Ease.out(Math.max(0, 1 - (D.now() - grab.t) / 220));
            placeAt(g, { x: p.x - grab.x * k2, y: p.y - grab.y * k2 });
            if (W.drag === 'fixed') return note(g, '公共中转站一直都在，不用拖');
            const id = lanAt(p);
            const already = id && r.lans.includes(id);
            lightRow(id && !already ? id : null);
            note(g, id ? (already ? `${lanOf(id).name}已经在用了` : `松手，${lanOf(id).name}就用上它`) : '', id && !already);
          },
          end: async (p, cancel) => {
            lightRow(null);
            const id = !cancel && p && W.drag === 'lan' ? lanAt(p) : null;
            const at = { x: parseFloat(g.style.left), y: parseFloat(g.style.top) };
            note(g, '');
            if (id && !r.lans.includes(id)) {
              const to = center($('.mdots', sideRow(id)) || sideRow(id));
              await fly(g, at, to, 1, 0.3, true);
              g.remove();
              c.classList.remove('ghost');
              api.join(r, id);
              return;
            }
            await fly(g, at, center($('.orb', c)), 1, 1, false);
            g.remove();
            c.classList.remove('ghost');
          },
        });
        for (const ch of c.querySelectorAll('.lchip')) {
          const id = ch.dataset.l;
          let gg = null, out = false, home2 = null;
          app.drag(ch, {
            ref: win,
            click: () => api.open(k),
            start: () => {
              home2 = center(ch);
              gg = pill(`${dots(lanOf(id))}<span>${sp(lanOf(id).name)}</span>`, 'lan');
              placeAt(gg, home2);
              ch.classList.add('ghost');
            },
            move: (p) => {
              placeAt(gg, p);
              out = !hit(c, p, 4);
              note(gg, out ? `松手，${lanOf(id).name}就不用它了` : '', out);
            },
            end: async (p, cancel) => {
              const at = { x: parseFloat(gg.style.left), y: parseFloat(gg.style.top) };
              note(gg, '');
              if (!cancel && p && out) {
                await App.tween(220, (t) => {
                  placeAt(gg, at, 1 - 0.5 * t);
                  gg.style.opacity = 1 - t;
                }, D.Ease.in);
                gg.remove();
                api.leave(r, id, { flown: true });
                return;
              }
              await fly(gg, at, home2, 1, 1, false);
              gg.remove();
              ch.classList.remove('ghost');
            },
          });
        }
      }
    }

    // ---- "用我自己的服务器" / "加一台": a line to run, the waiting cell (M3-15, M4-05) ----
    api.command = function (W, o) {
      W.pending = true;
      api.draw(W.key);
      select('join');
      const html =
        `<div class="top"><div class="orb-s"><div class="orb add wait">${WR.RING(40)}${G.ui('relayLine', 18)}</div></div><div><b>${sp(o.title)}</b><span>${sp(o.sub)}</span></div><button class="icon-btn sheet-close" title="关闭">${G.ui('close', 16)}</button></div>` +
        `<div class="label">在服务器上运行这一行</div><div class="cmd">${o.cmd}</div>` +
        `<div class="cmd-foot"><p class="hint">1 小时内有效，只能用一次，要先装好 Docker</p><button class="btn primary" data-act="copy">${G.ui('copy', 15)}复制</button></div>` +
        (o.foot ? `<p class="say">${sp(o.foot)}</p>` : '');
      const s = app.sheet(html, 'join');
      const b = s.querySelector('[data-act="copy"]');
      b.onclick = () => {
        b.innerHTML = `${G.ui('check', 15)}已复制`;
        b.animate([{ opacity: 0.4 }, { opacity: 1 }], { duration: 160, easing: 'ease-out' });
      };
    };
    // The command ran: the waiting cell fills in with the new relay, a bounce.
    api.installed = function (W, r, o = {}) {
      if (!W.pending) return;
      W.pending = false;
      W.relays.push(r);
      api.draw(W.key, { barAnim: true });
      const c = win.querySelector(`.rcell[data-k="${r.id}"] .orb`);
      if (c) c.animate([{ transform: 'scale(0.6)', opacity: 0 }, { transform: 'scale(1.08)', opacity: 1, offset: 0.55 }, { transform: 'none' }], { duration: 560, easing: 'cubic-bezier(0.3, 1.2, 0.5, 1)' });
      if (o.toast) app.toast(o.toast);
      if (app.sheetKey === 'join') api.open(r.id);
    };

    app.onSheetClose = () => select(null);
    for (const W of cfg.wells) api.draw(W.key, { still: true });
    return api;
  };

  // ---- kittors's relays page (M4-11, M4-12): his LANs, the line under the bar, the cards ----
  WR.USER_SIDE = [
    { group: '局域网' },
    { id: 'all', icon: 'people', label: '全部' },
    { id: 'weekend', dots: ['me', 'mia', 'jie'], label: '周末联机' },
    { id: 'team', dots: ['me', 'lin'], label: '工作室' },
    { id: 'new', icon: 'plus', label: '新建局域网' },
    { gap: true },
    { group: '设备' },
    { id: 'mine', icon: 'home', label: '我的设备', count: 4 },
    { id: 'shared', icon: 'inbox', label: '共享给我', count: 2 },
    { id: 'relays', icon: 'relayLine', label: '中转站', on: true },
    { gap: true },
    { group: '管理工作室' },
    { id: 'share', icon: 'share', label: '设备共享' },
  ];
  // The public relays: the same three in every scene (all see them, §14.8;
  // the deployment's admin runs them, M4-13).
  WR.PUBLIC = [
    { id: 'pub-hk', name: '香港', addr: '103.42.31.155', speed: '很快' },
    { id: 'pub-tokyo', name: '东京', addr: '203.0.113.66', speed: '很快' },
    { id: 'pub-fra', name: '法兰克福', addr: '203.0.113.20', speed: '有点慢' },
  ];
  WR.USER_LANS = { weekend: { name: '周末联机', dots: ['me', 'mia', 'jie'] }, team: { name: '工作室', dots: ['me', 'lin'] } };
  // Who goes first where, the standby ones, and the public one always last.
  WR.userBarSay = (act, spare) => {
    const said = WR.said;
    let s = act.length >= 2 ? '直接连不上时，' + act.map((r, i) => (i ? `${r.share}% 先经${said(r)}` : `${r.share}% 的设备先经${said(r)}`)).join('，') : `直接连不上时，设备都先经${said(act[0])}`;
    if (spare.length) s += `；${spare.map(said).join('、')}只当备用，${act.length === 1 ? said(act[0]) : '别的'}用不了时才用`;
    return s + `；${spare.length ? '自己的都' : '都'}用不了时，自动经公共中转站。`;
  };
  WR.userCard = (r, W, api) => {
    const P = App.PEOPLE, said = WR.said, chev = G.ui('chevron', 14);
    if (W.key === 'pub')
      return (
        WR.head(WR.orb(), r.name, '公共中转站') +
        WR.status('在线', `离你${r.speed || '很快'}`) +
        WR.rows([[G.ui('bolt', 18), '限速', '每条连接 2 MB/s']]) +
        `<p class="say">自动兜底，不用管。直接连不上，或者你自己的中转站用不了时，自动经它中转。内容全程加密，中转站看不到。</p>`
      );
    const n = W.relays.filter((x) => x.lans.length).length;
    const others = W.relays.filter((x) => x !== r && x.lans.length && x.share > 0);
    const say = !r.lans.length ? '还没进局域网，不分设备' : r.share > 0 ? `大约 ${r.share}% 的设备先经它` : `只当备用：${others.length === 1 ? said(others[0]) : '别的都'}用不了时才用它`;
    const chips = r.users.map((k) => `<button class="chip ${r.kicked && r.kicked[k] ? 'blocked' : ''}" data-k="${k}">${App.ball(k, r.kicked && r.kicked[k] ? 'off' : '')}${P[k].name}</button>`).join('');
    return (
      WR.head(WR.orb(), WR.title(r), '我的服务器') +
      WR.status('在线', r.up) +
      (r.lans.length && n >= 2 ? WR.seg(r, n, say) : '') +
      WR.lanRow(r, api, Object.keys(api.cfg.lans).length) +
      `<div class="label">现在</div>` +
      WR.rows([
        [G.ui('people', 18), '在用', r.users.length ? `${r.users.length} 个人 · ${r.devices} 台设备` : '还没有'],
        [G.ui('bolt', 18), '忙不忙', r.busy],
      ]) +
      (r.users.length ? `<div class="label">谁在用</div><div class="people">${chips}</div><div class="hint">点一下，就不让他用这个中转站</div>` : '') +
      `<div class="card-foot"><button class="more">更多${chev}</button><button class="out-btn">删除</button></div><div class="detail"><p>地址 ${r.addr} · 端口 47445<br />不限速</p></div>`
    );
  };
  WR.userPage = (app, mine) =>
    WR.page(app, {
      lans: WR.USER_LANS,
      barSay: WR.userBarSay,
      sub: () => '公共 · 一直兜底',
      wells: [
        { key: 'mine', label: '我的中转站', x: 28, w: 400, cols: 2, relays: mine, drag: 'lan', bar: true, add: { label: '用我自己的服务器', sub: '一行命令装好' } },
        { key: 'pub', label: '公共中转站', x: 448, w: 172, cols: 1, relays: WR.PUBLIC.map((r) => ({ ...r })), drag: 'fixed' },
      ],
      card: WR.userCard,
      openAdd: (W, api) => api.command(W, { title: '用我自己的服务器', sub: '装好后，它自己出现在这里', cmd: 'docker run -d --name deneb-relay --restart unless-stopped --network host -v deneb-relay:/data deneb-relay:v0 --join deneb-join-q7Lm2xVt9K', foot: '不装也能用：直接连不上时，自动经公共中转站' }),
    });

  // ---- Card parts, as the desktop's M4-02 -------------------------------------------------------
  WR.head = (orbHTML, name, sub) => `<div class="top"><div class="orb-s">${orbHTML}</div><div><b>${App.space(name)}</b><span>${App.space(sub)}</span></div><button class="icon-btn sheet-close" title="关闭">${G.ui('close', 16)}</button></div>`;
  WR.orb = (busy) => `<div class="orb">${WR.RELAY()}<span class="on${busy ? ' busy' : ''}"></span></div>`;
  WR.status = (t, s, c = '#22C55E') => `<div class="status"><span class="badge" style="--c:${c}">${G.ui('online', 16)}</span><div>${App.space(t)}<small>${App.space(s)}</small></div></div>`;
  WR.rows = (rows) => `<div class="rows">${rows.map(([icon, a, b]) => `<div class="row">${icon}<span>${App.space(a)}</span><span class="num">${App.space(b)}</span></div>`).join('')}</div>`;
  // The LANs it is in, each with a ×, and "+ 局域网" (M4-12's click instead of a drag).
  WR.lanRow = (r, api, all) =>
    `<div class="label">哪些局域网在用</div><div class="lan-row">` +
    r.lans.map((id) => `<span class="lchip" data-l="${id}">${api.dots(api.cfg.lans[id])}${App.space(api.cfg.lans[id].name)}<button class="x" title="不用了">${G.ui('close', 12)}</button></span>`).join('') +
    (all > r.lans.length ? `<button class="lchip add">${G.ui('plus', 13)}局域网</button>` : '') +
    `</div>` +
    (r.lans.length ? '' : `<div class="hint">还没进局域网，拖到左边的局域网上就用上了</div>`);
  // "分到多少": the three buttons and what they mean now.
  WR.seg = (r, n, say) =>
    `<div class="label">分到多少</div><div class="seg">` +
    (n >= 2 ? `<button data-act="more">多用</button><button data-act="less" ${r.share > 0 ? '' : 'disabled'}>少用</button>` : '') +
    `<button data-act="spare" class="${r.share > 0 ? '' : 'on'}">只当备用</button></div><div class="hint">${App.space(say)}</div>`;
})();
