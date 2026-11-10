// The web version's demo scaffolding (DESIGN.md §14): the browser the page
// sits in, the demo bar for the web's scenes, the invite page with its
// login in one card, and typing on the demo's clock. Shared by
// design/demos/web/m3-*.html, on top of design/demos/v3 (the desktop's
// look) and web/tokens.css (the desktop's colors, generated).
(function () {
  const Web = (window.Web = {});
  const $ = (s, r = document) => r.querySelector(s);

  // ---- Glyphs the web needs beyond the desktop's ---------------------------------------
  const MORE = {
    download: '<path d="M12 4.5v10.5M7.5 10.5 12 15l4.5-4.5"/><path d="M5 19.5h14"/>',
    copy: '<rect x="8.5" y="8.5" width="11" height="11" rx="2.2"/><path d="M15.5 8.5V6.2a1.7 1.7 0 0 0-1.7-1.7H6.2a1.7 1.7 0 0 0-1.7 1.7v7.6a1.7 1.7 0 0 0 1.7 1.7h2.3"/>',
    check: '<path d="m5.5 12.5 4 4 9-9"/>',
    online: '<circle cx="12" cy="12" r="3.2" fill="currentColor" stroke="none"/><path d="M7.2 7.2a6.8 6.8 0 0 0 0 9.6M16.8 7.2a6.8 6.8 0 0 1 0 9.6"/>',
    // Outline twins of the desktop's solid glyphs, for the sidebar.
    relayLine: '<path d="M3 12h18M7 8l-4 4 4 4M17 8l4 4-4 4"/>',
    share: '<rect x="3.5" y="6.5" width="10" height="10" rx="2.5"/><path d="M15.5 11.5h5M18 9l2.5 2.5L18 14"/>',
    // 运营 (the deployment's page): a pulse, the public relays' load.
    ops: '<path d="M3.5 12.5h4l2.2-5.5 4.3 11 2.3-5.5h4.2"/>',
  };
  const ui0 = G.ui;
  G.ui = (k, size = 18) =>
    MORE[k] ? `<svg viewBox="0 0 24 24" width="${size}" height="${size}" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${MORE[k]}</svg>` : ui0(k, size);

  // ---- The web's scenes, for the demo bar -----------------------------------------------
  // Numbered after the desktop's M3 scenes (M3-01 to M3-10, delivered first).
  Web.SCENES = [
    { file: 'm3-11-join.html', n: 'M3-11', title: '在浏览器里接受邀请' },
    { file: 'm3-12-join-phone.html', n: 'M3-12', title: '在手机上接受邀请' },
    { file: 'm3-13-lan.html', n: 'M3-13', title: '网页上的局域网' },
    { file: 'm3-14-share.html', n: 'M3-14', title: '网页后台把设备分给某个人' },
    { file: 'm3-15-relays.html', n: 'M3-15', title: '网页上管中转站' },
    { file: 'm4-11-weights.html', n: 'M4-11', title: '调权重' },
    { file: 'm4-12-relay-lan.html', n: 'M4-12', title: '把中转站拖进局域网' },
    { file: 'm4-13-ops.html', n: 'M4-13', title: '运营页' },
  ];
  // The demo bar under the window lists the web's scenes, not the
  // desktop's. (App calls buildBar from its constructor.)
  App.prototype.buildBar = function (root) {
    const h = App.h;
    const S = Web.SCENES;
    const i = S.findIndex((s) => location.pathname.endsWith('/' + s.file));
    const prev = S[(i - 1 + S.length) % S.length], next = S[(i + 1) % S.length];
    const qs = () => `?theme=${D.theme.get()}`;
    const bar = h(`<div class="demo-bar"></div>`);
    bar.appendChild(h(`<a class="icon-btn" title="${prev.title}">${G.ui('chevronLeft', 16)}</a>`)).onclick = () => (location.href = prev.file + qs());
    bar.appendChild(h(`<span class="bar-title">${i >= 0 ? S[i].n + ' · ' + S[i].title : ''}</span>`));
    bar.appendChild(h(`<a class="icon-btn" title="${next.title}">${G.ui('chevron', 16)}</a>`)).onclick = () => (location.href = next.file + qs());
    for (const c of this.o.controls || []) {
      bar.appendChild(h(`<span class="bar-sep"></span>`));
      const b = h(`<button class="bar-btn">${c.label}</button>`);
      b.onclick = () => c.onClick(b);
      bar.appendChild(b);
      c.el = b;
    }
    bar.appendChild(h(`<span class="bar-sep"></span>`));
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
  };

  // ---- The browser around the app -----------------------------------------------------------
  // The app's window goes inside a browser: an address bar on top, the
  // page under it. It is laid out at its real size and zoomed to fit, as
  // the desktop's window is (App.fit).
  Web.browser = function (app, { url }) {
    const frame = App.h(`<div class="browser"><div class="b-bar"><div class="lights"><i></i><i></i><i></i></div><div class="url">${G.ui('lock', 12)}<span class="url-text">${url}</span></div></div></div>`);
    $('#app').appendChild(frame);
    frame.appendChild(app.win);
    app.o.stageEl = frame;
    app.fit = function () {
      const W = 1180, H = 786;
      this.zoom = Math.min((innerWidth - 64) / W, (innerHeight - 112) / H, 1.3);
      frame.style.zoom = this.zoom;
      frame.style.left = Math.max(0, (innerWidth / this.zoom - W) / 2) + 'px';
      frame.style.top = Math.max(0, (innerHeight / this.zoom - H - 56 / this.zoom) / 2) + 'px';
      this.win.style.zoom = '';
      this.win.style.left = '0px';
      this.win.style.top = '46px';
      if (this.layer) this.layer.resize();
    };
    app.fit();
    return {
      frame,
      url(u) {
        const t = $('.url-text', frame);
        if (t.textContent === u) return;
        t.textContent = u;
        t.animate([{ opacity: 0.3 }, { opacity: 1 }], { duration: 160, easing: 'ease-out' });
      },
    };
  };

  // Who is looking: the sidebar's foot shows them (the desktop shows
  // kittors).
  Web.setMe = function (app, k) {
    const p = App.PEOPLE[k];
    const me = $('.side .me', app.win);
    me.querySelector('.ball').outerHTML = App.ball(k);
    me.querySelector('span:not(.ball)').textContent = p.full || p.name;
  };

  // ---- Typing, on the demo's clock -------------------------------------------------------
  // The recorder has no keyboard: a scene types by calling these, and the
  // letters come one by one on the virtual clock, as a hand types.
  Web.type = function (text, put, { per = 75, done } = {}) {
    let i = 0;
    const step = () => {
      i++;
      put(text.slice(0, i));
      if (i < text.length) D.after(per + ((i * 37) % 3) * 14, step);
      else if (done) done();
    };
    D.after(per, step);
  };

  // ---- A card whose contents change in place (§6.5) --------------------------------------
  // The new contents fade in, and its height follows on a smooth curve.
  Web.swap = function (card, html, cls = 'jc-in') {
    const h0 = card.offsetHeight;
    const next = App.h(`<div class="${cls}">${html}</div>`);
    for (const c of Array.from(card.children)) c.remove();
    card.appendChild(next);
    const h1 = card.offsetHeight;
    if (Math.abs(h1 - h0) > 1) card.animate([{ height: h0 + 'px' }, { height: h1 + 'px' }], { duration: 260, easing: 'cubic-bezier(0.3, 0.3, 0.2, 1)' });
    next.animate([{ opacity: 0 }, { opacity: 1 }], { duration: 180, easing: 'ease-out' });
    return next;
  };

  // ---- The invite page, with the login in its card (§14.5) -------------------------------
  // o: { from, lan, members, devices, me, email, masked, onJoined }
  Web.joinPage = function (frame, o) {
    const P = App.PEOPLE;
    const page = App.h(
      `<div class="page page-join" data-k="join-page"><div class="brand">${G.star(18)}<span>Deneb</span></div>` +
        `<div class="join-card" data-k="join-card"></div>` +
        `<div class="join-note">装上 Deneb，几个人的电脑就像在同一个家里，能直接互相用。</div></div>`
    );
    frame.appendChild(page);
    const card = $('.join-card', page);
    const from = P[o.from];
    const names = o.members.map((k) => P[k].full || P[k].name).join('、');
    const head = (title, sub) => `<div class="jc-head">${App.ball(o.from)}<div><div class="jc-title">${App.space(title)}</div><div class="jc-sub">${App.space(sub)}</div></div></div>`;

    const invite = () =>
      head(`${from.full || from.name} 邀请你加入「${o.lan}」`, `里面有 ${o.members.length} 个人，${o.devices} 台设备`) +
      `<div class="jc-people"><span class="stack">${o.members.map((k) => App.ball(k)).join('')}</span><span>${names}</span></div>` +
      `<div class="jc-foot"><button class="btn primary" data-act="join">加入</button></div>`;
    card.appendChild(App.h(`<div class="jc-in">${invite()}</div>`));

    let email = '';
    const api = {
      page,
      card,
      // 1. The email.
      askEmail() {
        Web.swap(card, head(`用邮箱加入「${o.lan}」`, '以后在电脑上登录 Deneb，也用这个邮箱') + `<div class="field focus" data-k="email"><span class="val"><span class="ph">你的邮箱</span></span><span class="caret"></span></div>` + `<div class="jc-foot"><button class="btn primary" data-act="send" disabled style="opacity:.45">发验证码</button></div>`);
        wire();
      },
      typeEmail() {
        const v = $('.field .val', card);
        const btn = $('[data-act="send"]', card);
        Web.type(o.email, (s) => {
          v.textContent = s;
          if (s.length === 1) btn.animate([{ opacity: 0.45 }, { opacity: 1 }], { duration: 160, easing: 'ease-out', fill: 'forwards' }), (btn.disabled = false);
        });
      },
      // 2. The code: six boxes, sent as soon as they are full.
      askCode() {
        Web.swap(card, head('输入验证码', `发到了 ${o.masked}`) + `<div class="code-boxes">${[0, 1, 2, 3, 4, 5].map((i) => `<div class="cb" data-k="cb${i}"></div>`).join('')}</div>` + `<div class="jc-foot"><span class="hint">没收到？60 秒后可以重发</span></div>`);
        $('.cb', card).classList.add('at');
      },
      typeCode(code, done) {
        const boxes = Array.from(card.querySelectorAll('.cb'));
        Web.type(code, (s) => {
          boxes.forEach((b, i) => {
            b.textContent = s[i] || '';
            b.classList.toggle('at', i === s.length);
          });
        }, { per: 110, done: () => D.after(260, () => api.joined(done)) });
      },
      // 3. In: the inviter and me side by side, mine bouncing in.
      joined(done) {
        const c = Web.swap(card, `<div class="pair">${App.ball(o.from).replace('<span', '<span data-k="pair-from"')}${App.ball(o.me).replace('<span', '<span data-k="pair-me"')}</div><div><div class="jc-title">${App.space('你加入了「' + o.lan + '」')}</div><div class="jc-sub" style="text-align:center">把这台电脑也加进来，就能用里面的设备了</div></div>`, 'jc-joined');
        const mine = c.querySelector('.pair .ball:last-child');
        mine.animate([{ transform: 'scale(0.3)', opacity: 0 }, { transform: 'scale(1.12)', opacity: 1, offset: 0.6 }, { transform: 'none' }], { duration: 540, delay: 120, easing: 'cubic-bezier(0.3, 1.2, 0.5, 1)', fill: 'backwards' });
        D.after(1300, () => done && done());
      },
      // The page gives way to the app.
      leave(win) {
        // One after the other: two pictures fading across each other read
        // as a muddle (as the card's contents, app.js sheet()).
        page.animate([{ opacity: 1, transform: 'none' }, { opacity: 0, transform: 'scale(0.985)' }], { duration: 200, easing: 'ease-in', fill: 'forwards' });
        win.style.visibility = '';
        win.animate([{ opacity: 0 }, { opacity: 1 }], { duration: 300, delay: 180, easing: 'ease-out', fill: 'backwards' });
        D.after(220, () => (page.style.display = 'none'));
      },
    };
    function wire() {
      const send = $('[data-act="send"]', card);
      if (send) send.onclick = () => !send.disabled && api.askCode();
    }
    $('[data-act="join"]', card).onclick = () => api.askEmail();
    return api;
  };
})();
