// Demo scaffolding: the stage that scales the design to the browser, the
// theme, the demo bar outside the product UI, gestures, small animated
// widgets, and the hooks the recorder drives.
(function () {
  const D = (window.D = window.D || {});
  const q = new URLSearchParams(location.search);

  D.SCENES = [
    { file: '01-direct-relay.html', title: '直连与中继' },
    { file: '02-drag-relay.html', title: '拖入中继' },
    { file: '03-multi-network.html', title: '同时在两个网络' },
    { file: '04-device-share.html', title: '管理员分享设备' },
    { file: '05-lan-members.html', title: '成员、设备与端口' },
  ];

  D.$ = (sel, root = document) => root.querySelector(sel);
  D.$$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));
  D.el = (html) => {
    const t = document.createElement('template');
    t.innerHTML = html.trim();
    return t.content.firstElementChild;
  };

  // ---- Theme ---------------------------------------------------------------
  const sys = matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark';
  document.documentElement.dataset.theme = q.get('theme') || sys;
  D.theme = {
    get: () => document.documentElement.dataset.theme,
    set(t) {
      document.documentElement.dataset.theme = t;
      for (const fn of themeFns) fn(t);
    },
    toggle() {
      this.set(this.get() === 'dark' ? 'light' : 'dark');
    },
  };
  const themeFns = [];
  D.onTheme = (fn) => themeFns.push(fn);

  // ---- Stage ---------------------------------------------------------------
  // The design is laid out at a fixed size and scaled as a whole, so every
  // demo shows exactly as specified at any browser size.
  D.stage = function (el, w, h) {
    const st = { el, w, h, scale: 1 };
    function fit() {
      const bar = 72;
      const s = Math.min((innerWidth - 64) / w, (innerHeight - bar - 40) / h, 1.6);
      const x = (innerWidth - w * s) / 2;
      const y = Math.max(20, (innerHeight - bar - h * s) / 2 + 8);
      el.style.width = w + 'px';
      el.style.height = h + 'px';
      el.style.transform = `translate(${x}px, ${y}px) scale(${s})`;
      st.scale = s;
    }
    addEventListener('resize', fit);
    fit();
    return st;
  };

  // local maps a pointer event into the design coordinates of ref.
  D.local = (e, ref) => {
    const r = ref.getBoundingClientRect();
    const s = r.width / ref.offsetWidth;
    return { x: (e.clientX - r.left) / s, y: (e.clientY - r.top) / s };
  };

  // ---- Demo bar --------------------------------------------------------------
  // Sits outside the window: scene navigation, simulated outside events,
  // replay and theme. Nothing here belongs to the product.
  D.demoBar = function ({ index, controls = [], onReplay }) {
    const bar = D.el('<div class="demo-bar"></div>');
    const prev = D.SCENES[index - 1];
    const next = D.SCENES[index + 1];
    const nav = (s, icon) => {
      const b = D.el(`<button class="icon" ${s ? '' : 'disabled style="opacity:.3"'}>${D.icon(icon, 16)}</button>`);
      if (s) b.onclick = () => (location.href = s.file + location.search.replace(/[?&]record[^&]*/, ''));
      return b;
    };
    bar.append(nav(prev, 'chevronLeft'));
    bar.append(D.el(`<span class="title">${String(index + 1).padStart(2, '0')} · ${D.SCENES[index].title}</span>`));
    bar.append(nav(next, 'chevronRight'));
    const api = {};
    for (const c of controls) {
      bar.append(D.el('<span class="sep"></span>'));
      if (c.type === 'seg') {
        const g = D.el('<span class="group"></span>');
        if (c.label) g.append(D.el(`<span class="label">${c.label}</span>`));
        const btns = c.options.map((o) => {
          const b = D.el(`<button data-id="${o.id}">${o.text}</button>`);
          b.onclick = () => {
            set(o.id);
            c.onChange(o.id);
          };
          g.append(b);
          return b;
        });
        const set = (id) => btns.forEach((b) => b.classList.toggle('on', b.dataset.id === id));
        set(c.value);
        api[c.id] = { set };
        bar.append(g);
      } else if (c.type === 'button') {
        const b = D.el(`<button id="${c.id || ''}">${c.icon ? D.icon(c.icon, 16) : ''}${c.text || ''}</button>`);
        b.onclick = c.onClick;
        bar.append(b);
        api[c.id] = { el: b };
      }
    }
    bar.append(D.el('<span class="sep"></span>'));
    const replay = D.el(`<button class="icon" title="重播">${D.icon('replay', 16)}</button>`);
    replay.onclick = onReplay;
    bar.append(replay);
    const theme = D.el(`<button class="icon" title="主题"></button>`);
    const paint = () => (theme.innerHTML = D.icon(D.theme.get() === 'dark' ? 'sun' : 'moon', 16));
    theme.onclick = () => {
      D.theme.toggle();
      paint();
    };
    paint();
    bar.append(theme);
    document.body.append(bar);
    return api;
  };

  // ---- Gestures ---------------------------------------------------------------
  // drag implements the four states of DESIGN.md §6.1: press, start past a
  // threshold, move with velocity, and end, cancel (Escape) or click.
  D.drag = function (el, o) {
    el.addEventListener('pointerdown', (e) => {
      if (e.button !== 0) return;
      if (o.enabled && !o.enabled()) return;
      e.preventDefault();
      e.stopPropagation();
      const ref = o.ref();
      const p0 = D.local(e, ref);
      let dragging = false;
      let samples = [{ x: p0.x, y: p0.y, t: D.now() }];
      const vel = () => {
        const a = samples[0];
        const b = samples[samples.length - 1];
        const dt = (b.t - a.t) / 1000;
        return dt > 0.001 ? { x: (b.x - a.x) / dt, y: (b.y - a.y) / dt } : { x: 0, y: 0 };
      };
      o.onPress && o.onPress(p0, e);
      try {
        el.setPointerCapture(e.pointerId);
      } catch (_) {}
      const move = (ev) => {
        const p = D.local(ev, ref);
        const now = D.now();
        samples.push({ x: p.x, y: p.y, t: now });
        samples = samples.filter((s) => now - s.t <= 90);
        if (!dragging && Math.hypot(p.x - p0.x, p.y - p0.y) > (o.threshold || 4)) {
          dragging = true;
          o.onStart && o.onStart(p0, p);
        }
        if (dragging) o.onMove && o.onMove(p, vel());
      };
      const done = () => {
        el.removeEventListener('pointermove', move);
        el.removeEventListener('pointerup', up);
        el.removeEventListener('pointercancel', cancel);
        removeEventListener('keydown', key, true);
      };
      const up = (ev) => {
        done();
        const p = D.local(ev, ref);
        if (dragging) o.onEnd && o.onEnd(p, vel());
        else {
          o.onRelease && o.onRelease();
          o.onClick && o.onClick(p);
        }
      };
      const cancel = () => {
        done();
        if (dragging) o.onCancel && o.onCancel();
        else o.onRelease && o.onRelease();
      };
      const key = (ev) => {
        if (ev.key === 'Escape' && dragging) {
          ev.preventDefault();
          done();
          o.onCancel && o.onCancel();
        }
      };
      el.addEventListener('pointermove', move);
      el.addEventListener('pointerup', up);
      el.addEventListener('pointercancel', cancel);
      addEventListener('keydown', key, true);
    });
  };

  // ---- Animated widgets ------------------------------------------------------

  // Roll shows a line of text whose changed characters roll, as MyGo's
  // ContentTransition{Numeric: true}; characters that stay, stay.
  D.Roll = class {
    constructor(el, text = '') {
      this.el = el;
      this.el.classList.add('num');
      this.el.style.display = 'inline-flex';
      this.text = '';
      this.cells = [];
      this.set(text, true);
    }
    set(text, instant = false, down = false) {
      text = String(text);
      if (text === this.text) return;
      const old = this.text;
      this.text = text;
      if (instant || old.length !== text.length) {
        // A different length re-lays the line: cross-fade it as a whole.
        const fresh = D.el(`<span style="display:inline-flex">${[...text].map((c) => `<span class="roll-c" style="display:inline-block;white-space:pre">${c}</span>`).join('')}</span>`);
        const prev = this.el.firstElementChild;
        this.el.innerHTML = '';
        this.el.append(fresh);
        if (!instant && prev) fresh.animate([{ opacity: 0, transform: 'translateY(35%)' }, { opacity: 1, transform: 'none' }], { duration: 260, easing: 'cubic-bezier(.2,.8,.2,1)' });
        return;
      }
      const cells = this.el.firstElementChild.children;
      [...text].forEach((c, i) => {
        if (old[i] === c) return;
        const cell = cells[i];
        const w = D.el(`<span style="display:inline-block;position:relative;overflow:hidden;vertical-align:bottom;white-space:pre"></span>`);
        const a = D.el(`<span style="display:inline-block">${old[i]}</span>`);
        const b = D.el(`<span style="display:inline-block;position:absolute;left:0;top:0">${c}</span>`);
        w.append(a, b);
        cell.replaceWith(w);
        w.classList.add('roll-c');
        const dir = down ? -1 : 1;
        const opts = { duration: 320, easing: 'cubic-bezier(.2,.8,.2,1)', fill: 'forwards' };
        a.animate([{ transform: 'translateY(0)', opacity: 1 }, { transform: `translateY(${-100 * dir}%)`, opacity: 0 }], opts);
        const anim = b.animate([{ transform: `translateY(${100 * dir}%)`, opacity: 0 }, { transform: 'translateY(0)', opacity: 1 }], opts);
        anim.onfinish = () => {
          if (!w.isConnected) return;
          const n = D.el(`<span class="roll-c" style="display:inline-block;white-space:pre">${c}</span>`);
          w.replaceWith(n);
        };
      });
    }
  };

  // Morph swaps the content of a pill: the old content fades up and out,
  // the new fades in, and the pill's width springs to fit, as MyGo's
  // ContentTransition with a Transition{Size} on the pill.
  D.Morph = class {
    constructor(el, html, padX = 14) {
      this.el = el;
      this.padX = padX;
      if (getComputedStyle(el).position === 'static') el.style.position = 'relative';
      el.style.overflow = 'hidden';
      el.style.padding = '0';
      this.cur = null;
      this.w = new D.Spring(0, D.Smooth);
      this.unsub = D.onFrame(() => {
        if (!this.el.isConnected) return;
        this.el.style.width = this.w.value.toFixed(2) + 'px';
      });
      this.set(html, true);
    }
    set(html, instant = false) {
      if (this.cur && this.cur.__html === html) return;
      const n = D.el(`<span class="morph-c" style="position:absolute;left:50%;top:0;bottom:0;display:inline-flex;align-items:center;gap:8px;white-space:nowrap;transform:translateX(-50%)">${html}</span>`);
      n.__html = html;
      this.el.append(n);
      // offsetWidth is the layout width, unaffected by the stage's scale.
      const w = n.offsetWidth + this.padX * 2;
      if (instant) this.w.jump(w);
      else this.w.to(w, D.Smooth);
      const old = this.cur;
      this.cur = n;
      if (!instant) {
        n.animate([{ opacity: 0, transform: 'translate(-50%, 6px)' }, { opacity: 1, transform: 'translate(-50%, 0)' }], { duration: 260, delay: 60, easing: 'cubic-bezier(.2,.8,.2,1)', fill: 'backwards' });
      }
      if (old) {
        if (instant) old.remove();
        else {
          const a = old.animate([{ opacity: 1, transform: 'translate(-50%, 0)' }, { opacity: 0, transform: 'translate(-50%, -6px)' }], { duration: 160, easing: 'ease-out', fill: 'forwards' });
          a.onfinish = () => old.remove();
        }
      }
    }
    scale() {
      const r = this.el.getBoundingClientRect();
      return this.el.offsetWidth ? r.width / this.el.offsetWidth : 1;
    }
  };

  // toast shows one notice at a time at (x, bottom) of a container, with an
  // optional undo; it springs in from below and fades out.
  let currentToast = null;
  D.toast = function (container, { text, undo, x, bottom = 24, duration = 4000 }) {
    if (currentToast) currentToast.close();
    const t = D.el(`<div class="toast"><span>${text}</span>${undo ? '<button class="undo">撤销</button>' : ''}</div>`);
    t.style.left = x + 'px';
    t.style.bottom = bottom + 'px';
    t.style.transform = 'translateX(-50%)';
    t.style.paddingRight = undo ? '6px' : '16px';
    container.append(t);
    const y = new D.Spring(14, D.Snappy).to(0);
    const op = new D.Tween(0, 160).to(1);
    let closing = false;
    const off = D.onFrame(() => {
      t.style.transform = `translate(-50%, ${y.value.toFixed(2)}px)`;
      t.style.opacity = op.value.toFixed(3);
      if (closing && op.settled()) {
        off();
        t.remove();
      }
    });
    const timer = D.after(duration, () => api.close());
    const api = {
      close() {
        if (closing) return;
        closing = true;
        timer.cancel();
        y.to(8, D.Smooth);
        op.to(0, 160);
        if (currentToast === api) currentToast = null;
      },
    };
    if (undo)
      t.querySelector('.undo').onclick = () => {
        api.close();
        undo();
      };
    currentToast = api;
    return api;
  };

  // ---- Recorder hooks ----------------------------------------------------------
  if (D.recording) {
    document.body.classList.add('recording');
    const cur = D.el(`<div class="fake-cursor"><span class="press"></span><svg width="22" height="24" viewBox="0 0 22 24"><path d="M5 2.5v17.2l4.3-4.1 2.9 6.4 2.9-1.3-2.8-6.2 6-.1z" fill="#111" stroke="#fff" stroke-width="1.4" stroke-linejoin="round"/></svg></div>`);
    document.body.append(cur);
    const press = cur.querySelector('.press');
    const pos = (e) => {
      cur.classList.add('seen');
      cur.style.transform = `translate(${e.clientX}px, ${e.clientY}px)`;
    };
    addEventListener('pointermove', pos, true);
    addEventListener(
      'pointerdown',
      (e) => {
        pos(e);
        press.animate([{ opacity: 0, transform: 'scale(.5)' }, { opacity: 1, transform: 'scale(1)' }], { duration: 120, easing: 'ease-out', fill: 'forwards' });
      },
      true
    );
    addEventListener(
      'pointerup',
      () => {
        press.animate([{ opacity: 1, transform: 'scale(1)' }, { opacity: 0, transform: 'scale(1.3)' }], { duration: 260, easing: 'ease-out', fill: 'forwards' });
      },
      true
    );
  }

  // __point returns the viewport point of design coordinates (x, y) in the
  // element matched by sel, or of the element's center when x is omitted.
  window.__point = (sel, x, y) => {
    const el = document.querySelector(sel);
    if (!el) return null;
    const r = el.getBoundingClientRect();
    if (x == null) return { x: r.left + r.width / 2, y: r.top + r.height / 2 };
    const s = r.width / el.offsetWidth;
    return { x: r.left + x * s, y: r.top + y * s };
  };
})();
