// The website's few behaviours (DESIGN.md §14.16). Every page reads without
// them; they add: the top bar frosted once the page goes under it, content
// rising in once, the real window's loops (in view only, with a button to
// stop them), the system's own download button and files, the version and
// the files' sizes from the download site, 复制, the folded help, this
// site's own address in the docs. With ?record, the recorder's hooks
// (web/probe/record-web.mjs): a virtual clock for CSS and Web animations,
// timers, scrolling and the loops, a drawn pointer, __point, and the page's
// plan from /site/rec/<page>.js.
(() => {
  const q = new URLSearchParams(location.search);
  const recording = q.has('record');
  const still = q.has('still') || recording;
  const page = document.body.dataset.page || '';
  if (still) document.documentElement.classList.add('still');
  // A theme the address forces (the stills, the recordings; theme.js sets
  // the colors): the pictures go with it.
  const theme = q.get('theme');
  if (theme === 'light' || theme === 'dark') {
    for (const s of document.querySelectorAll('picture source[media]')) s.media = theme === 'dark' ? 'all' : 'not all';
  }
  // Signed in, the server marks the page (cmd/deneb-web/site.go); ?signedin
  // for the stills.
  if (q.has('signedin')) document.documentElement.setAttribute('data-signed-in', '');

  // ---- Time: real, or the recorder's frames -------------------------------
  let vt = 0;
  const timers = [];
  const scrolls = [];
  const later = (ms, fn) => {
    if (!recording) return setTimeout(fn, ms);
    timers.push({ at: vt + ms, fn });
  };
  if (recording) {
    const sync = () => {
      for (const a of document.getAnimations()) {
        if (a.__vt0 == null) {
          a.__vt0 = vt;
          a.pause();
        }
        const local = vt - a.__vt0;
        a.currentTime = local;
        const end = a.effect && a.effect.getComputedTiming().endTime;
        if (end != null && isFinite(end) && local >= end) a.finish();
      }
    };
    window.__advance = async (ms) => {
      vt += ms;
      for (let i = scrolls.length - 1; i >= 0; i--) {
        const k = scrolls[i];
        const p = Math.min(1, (vt - k.t0) / k.ms);
        const e = p < 0.5 ? 4 * p * p * p : 1 - Math.pow(-2 * p + 2, 3) / 2;
        scrollTo(0, k.from + (k.to - k.from) * e);
        if (p >= 1) scrolls.splice(i, 1);
      }
      sync();
      for (let i = timers.length - 1; i >= 0; i--) if (timers[i].at <= vt) timers.splice(i, 1)[0].fn();
      if (window.__loops) await window.__loops();
      await new Promise((r) => setTimeout(r, 0));
      sync();
    };
    // Links pressed in a recording stay on the page.
    addEventListener('click', (e) => e.target.closest('a[href]') && e.preventDefault(), true);
    // A scroll on the virtual clock: to y, eased, over ms.
    window.__scroll = (to, ms) => scrolls.push({ from: scrollY, to, t0: vt, ms });
    window.__point = (sel, x, y) => {
      const el = document.querySelector(sel);
      if (!el) return null;
      const r = el.getBoundingClientRect();
      if (x == null) return { x: r.left + r.width / 2, y: r.top + r.height / 2 };
      return { x: r.left + x, y: r.top + y };
    };
    const cur = document.createElement('div');
    cur.className = 'fake-cursor';
    cur.innerHTML = '<span class="press"></span><svg width="22" height="24" viewBox="0 0 22 24"><path d="M5 2.5v17.2l4.3-4.1 2.9 6.4 2.9-1.3-2.8-6.2 6-.1z" fill="#111" stroke="#fff" stroke-width="1.4" stroke-linejoin="round"/></svg>';
    document.body.append(cur);
    const press = cur.querySelector('.press');
    addEventListener('pointermove', (e) => {
      cur.classList.add('seen');
      cur.style.transform = `translate(${e.clientX}px, ${e.clientY}px)`;
    }, true);
    addEventListener('pointerdown', () => press.animate([{ opacity: 0, transform: 'scale(.5)' }, { opacity: 1, transform: 'scale(1)' }], { duration: 120, easing: 'ease-out', fill: 'forwards' }), true);
    addEventListener('pointerup', () => press.animate([{ opacity: 1, transform: 'scale(1)' }, { opacity: 0, transform: 'scale(1.3)' }], { duration: 260, easing: 'ease-out', fill: 'forwards' }), true);
  }

  // The page's behaviours, once a recording's plan is in (its data stands in
  // for the download site's, which a recording does not have).
  let plan = {};
  const given = (k) => q.get(k) || (plan.data && plan.data[k]);
  const start = () => {
    // ---- The top bar: frosted once the page goes under it -----------------
    // Over a near-black block it goes dark with it, so that it is never a
    // grey band over black.
    const bar = document.querySelector('.top');
    const darks = [...document.querySelectorAll('section.dark')];
    const glass = () => {
      if (!bar) return;
      bar.classList.toggle('glass', scrollY > 0);
      const y = bar.offsetHeight / 2;
      const over = darks.some((d) => {
        const r = d.getBoundingClientRect();
        return r.top <= y && r.bottom > y;
      });
      if (over) bar.dataset.theme = 'dark';
      else delete bar.dataset.theme;
    };
    addEventListener('scroll', glass, { passive: true });
    addEventListener('resize', glass, { passive: true });
    glass();

    // ---- Content rises in, the first time it comes into view ---------------
    // Each block's headline, line and picture one after another (--n).
    for (const block of document.querySelectorAll('section')) {
      block.querySelectorAll('.reveal').forEach((el, i) => el.style.setProperty('--n', i));
    }
    const reveal = () => {
      const io = new IntersectionObserver((es) => {
        for (const e of es) if (e.isIntersecting) {
          e.target.classList.add('seen');
          io.unobserve(e.target);
        }
      }, { rootMargin: '0px 0px -12% 0px' });
      for (const el of document.querySelectorAll('.reveal')) io.observe(el);
    };
    if (!still && 'IntersectionObserver' in window) reveal();
    else for (const el of document.querySelectorAll('.reveal')) el.classList.add('seen');
    // A recording of the rising in: as a first visit has it.
    if (recording) window.__reveal = () => {
      document.documentElement.classList.remove('still');
      for (const el of document.querySelectorAll('.reveal')) el.classList.remove('seen');
      reveal();
    };

    // ---- The loops: the real window moving ---------------------------------
    // Over its picture once it plays, and only while it is in view; a button
    // stops it and lets it go on. Not when motion is to be reduced, data
    // saved, or for the stills: the picture alone. The first screen's goes
    // with the system's light or dark; the near-black blocks' are dark.
    const calm = matchMedia('(prefers-reduced-motion: reduce)').matches || (navigator.connection && navigator.connection.saveData);
    const loops = [];
    if (!q.has('still') && !calm) for (const box of document.querySelectorAll('[data-loop]')) {
      const name = box.dataset.loop;
      const v = document.createElement('video');
      v.muted = true;
      v.loop = true;
      v.playsInline = true;
      v.setAttribute('aria-hidden', 'true');
      const dark = matchMedia('(prefers-color-scheme: dark)');
      const theme = () => (box.closest('section.dark') ? 'dark' : q.get('theme') || (dark.matches ? 'dark' : 'light'));
      const loop = { box, v, inView: false, stopped: false, t0: null };
      const go = () => {
        if (recording) return;
        if (loop.inView && !loop.stopped) v.play().catch(() => {});
        else v.pause();
      };
      const load = () => {
        const t = theme();
        box.classList.remove('playing');
        v.innerHTML = `<source src="/site/media/${name}-${t}.webm" type="video/webm" /><source src="/site/media/${name}-${t}.mp4" type="video/mp4" />`;
        v.load();
        go();
      };
      v.addEventListener('playing', () => box.classList.add('playing'));
      if (recording) v.addEventListener('loadeddata', () => box.classList.add('playing'));
      const b = document.createElement('button');
      b.type = 'button';
      b.className = 'pause';
      b.setAttribute('aria-label', '暂停');
      b.innerHTML = '<svg class="stop" viewBox="0 0 12 12" width="12" height="12" aria-hidden="true"><rect x="2.5" y="2" width="2.4" height="8" rx="0.8" fill="currentColor"/><rect x="7.1" y="2" width="2.4" height="8" rx="0.8" fill="currentColor"/></svg><svg class="go" viewBox="0 0 12 12" width="12" height="12" aria-hidden="true"><path d="M3.5 2.3v7.4a.6.6 0 0 0 .9.5l6-3.7a.6.6 0 0 0 0-1L4.4 1.8a.6.6 0 0 0-.9.5Z" fill="currentColor"/></svg>';
      b.addEventListener('click', () => {
        loop.stopped = !loop.stopped;
        box.classList.toggle('paused', loop.stopped);
        b.setAttribute('aria-label', loop.stopped ? '播放' : '暂停');
        go();
      });
      box.append(v, b);
      load();
      if (!box.closest('section.dark')) dark.addEventListener('change', load);
      if ('IntersectionObserver' in window) {
        new IntersectionObserver((es) => {
          loop.inView = es[es.length - 1].isIntersecting;
          // A recording's loop starts where a visit's does: in view.
          if (loop.inView && loop.t0 == null) loop.t0 = vt;
          go();
        }, { threshold: 0.2 }).observe(box);
      } else {
        loop.inView = true;
        go();
      }
      loops.push(loop);
    }
    // In a recording the loops go by the recorder's clock: each frame seeks
    // them to where they would be, to the middle of the film's frame there
    // (30 a second), so that it never lands on the edge between two and
    // the motion keeps its even step.
    if (recording) window.__loops = async () => {
      await Promise.all(loops.map((l) => {
        const d = l.v.duration;
        if (!d || !isFinite(d)) return null;
        const at = l.t0 == null ? 0 : ((vt - l.t0) / 1000) % d;
        const t = Math.min(d - 1 / 60, (Math.floor(at * 30) + 0.5) / 30);
        if (Math.abs(l.v.currentTime - t) < 0.001) return null;
        return new Promise((r) => {
          l.v.addEventListener('seeked', r, { once: true });
          l.v.currentTime = t;
        });
      }));
    };

    // ---- This site's own address in the docs -------------------------------
    // The docs write deneb.app, as all demos do; served for real, they say
    // where they are.
    if (!still && location.protocol === 'https:') for (const el of document.querySelectorAll('.host')) el.textContent = location.host;

    // ---- This computer's system -------------------------------------------
    // ?os= mac, windows, linux, linux-arm64, phone or none, for the stills.
    const ua = navigator.userAgent;
    const forced = q.get('os');
    // iPadOS asks for pages as a Mac does: a Mac with a touch screen is one.
    const phone = forced ? forced === 'phone' : /Android|iPhone|iPad|iPod|Mobile/i.test(ua) || (/Macintosh/i.test(ua) && navigator.maxTouchPoints > 1);
    const os = forced || (phone ? null : /Mac/i.test(ua) ? 'mac' : /Windows/i.test(ua) ? 'windows' : /Linux|X11|CrOS/i.test(ua) ? (/aarch64|arm64/i.test(ua) ? 'linux-arm64' : 'linux') : null);
    const SYS = {
      mac: { name: 'macOS（Apple 芯片）', button: '下载 macOS 版', files: ['Deneb.dmg'] },
      windows: { name: 'Windows（64 位）', button: '下载 Windows 版', files: ['Deneb-Setup.exe'] },
      linux: { name: 'Linux（x86_64）', button: '下载 Linux 版', files: ['deneb.deb', 'denebd.deb'] },
      'linux-arm64': { name: 'Linux（ARM64）', button: '下载 Linux 版', files: ['deneb-arm64.deb', 'denebd-arm64.deb'] },
    };
    const DL = '/download/';
    // The home page's main button names the system; it goes to the download
    // page, where the files are, and what to do if the system stops them.
    for (const a of document.querySelectorAll('[data-dl]')) a.textContent = SYS[os] ? SYS[os].button : '下载 Deneb';
    const onPhone = document.querySelector('.mine.phone');
    if (onPhone && phone) onPhone.hidden = false;
    // A file's size as the download site says, each file its own (HEAD;
    // ?sizes=<file>:<bytes>,… for the stills), in MB as the Finder counts;
    // not shown when unknown.
    const sizes = Object.fromEntries(String(given('sizes') || '').split(',').filter(Boolean).map((p) => p.split(':')).map(([f, n]) => [f, +n]));
    const sizeOf = (file, el) => {
      const show = (n) => {
        if (!n) return;
        el.textContent = n >= 1e6 ? (n / 1e6).toFixed(1) + ' MB' : Math.max(1, Math.round(n / 1e3)) + ' KB';
        el.hidden = false;
        if (el.previousElementSibling && el.previousElementSibling.tagName === 'DT') el.previousElementSibling.hidden = false;
      };
      if (sizes[file]) show(sizes[file]);
      else if (!still) fetch(DL + file, { method: 'HEAD' }).then((r) => r.ok && show(+r.headers.get('content-length'))).catch(() => {});
    };
    const mine = document.querySelector('.mine:not(.phone)');
    if (mine && SYS[os]) {
      const s = SYS[os];
      mine.querySelector('[data-name]').textContent = s.name;
      if (s.files.length === 1) {
        const [f] = s.files;
        mine.querySelector('[data-file]').textContent = f;
        mine.querySelector('[data-get]').href = DL + f;
        sizeOf(f, mine.querySelector('[data-size]'));
      } else {
        // Linux: the window's package and the engine's, each with its size
        // and 下载, and the one command that installs both (as the add-device
        // dialog has it); no one main button for two files.
        const cmd = 'sudo apt install ' + s.files.map((f) => './' + f).join(' ');
        mine.classList.add('two');
        mine.querySelector('[data-get]').remove();
        mine.querySelector('.meta').outerHTML =
          `<ul class="files">${s.files.map((f) => `<li><span class="mono">${f}</span><span class="size" hidden></span><a class="link" href="${DL + f}" aria-label="下载 ${f}">下载</a></li>`).join('')}</ul>` +
          `<p class="how-to">需同时安装这两个包。在下载目录中运行：</p><div class="cmd"><code>${cmd}</code><button class="link" type="button" data-copy="${cmd}">复制</button></div>`;
        mine.querySelectorAll('.files li').forEach((li, i) => sizeOf(s.files[i], li.querySelector('.size')));
      }
      mine.hidden = false;
    }
    // The version, from the update manifest the release puts up last.
    const ver = document.querySelector('[data-version]');
    if (ver) {
      const show = (v) => {
        if (!v) return;
        ver.textContent = '版本 ' + v;
        ver.hidden = false;
      };
      if (given('version')) show(given('version'));
      else if (!still) fetch(DL + 'update-darwin-arm64.json').then((r) => (r.ok ? r.json() : null)).then((m) => m && show(m.version)).catch(() => {});
    }

    // ---- 复制: "已复制" for 2 s ---------------------------------------------
    const link = location.protocol.startsWith('http') ? location.origin + '/download' : 'https://deneb.app/download';
    for (const b of document.querySelectorAll('[data-copy], [data-copy-link]')) {
      b.addEventListener('click', () => {
        const text = b.hasAttribute('data-copy-link') ? link : b.dataset.copy;
        const done = () => {
          // Its own word back 2 s after the last click.
          b.dataset.label ??= b.textContent;
          const n = (b.copies = (b.copies || 0) + 1);
          b.textContent = '已复制';
          later(2000, () => n === b.copies && (b.textContent = b.dataset.label));
        };
        if (recording) return done();
        // Not allowed to copy: the command selected, to copy by hand.
        const failed = () => {
          const code = b.parentElement.querySelector('code');
          if (code) getSelection().selectAllChildren(code);
        };
        if (!navigator.clipboard) return failed();
        navigator.clipboard.writeText(text).then(done, failed);
      });
    }

    // ---- The folded help: opens and closes in height ------------------------
    for (const b of document.querySelectorAll('[data-fold]')) {
      const help = document.getElementById(b.dataset.fold);
      b.addEventListener('click', () => {
        const open = help.hidden;
        b.setAttribute('aria-expanded', String(open));
        help.getAnimations().forEach((a) => a.cancel());
        help.hidden = false;
        const h = help.offsetHeight;
        const shut = { height: '0px', paddingTop: '0px', paddingBottom: '0px', marginTop: '0px', opacity: 0 };
        const full = { height: h + 'px', paddingTop: '16px', paddingBottom: '16px', marginTop: '8px', opacity: 1 };
        // Held at its end until it is put away: closed, it never shows again
        // for a frame between the end and hidden.
        const a = help.animate(open ? [shut, full] : [full, shut], { duration: 300, easing: 'cubic-bezier(0.3, 0.3, 0.2, 1)', fill: 'forwards' });
        a.finished.then(() => {
          if (!open) help.hidden = true;
          a.cancel();
        }, () => {});
      });
    }

    if (recording) {
      window.RECORDING = plan.actions ? plan : { duration: 4000, actions: [] };
      window.__ready = true;
    }
  };

  if (recording) {
    // The plan of this page's recording, by ?record=<name>, else its first.
    const s = document.createElement('script');
    s.src = `/site/rec/${page}.js`;
    s.onload = s.onerror = () => {
      const plans = window.SITE_RECORDINGS || {};
      plan = plans[q.get('record')] || plans.default || {};
      start();
    };
    document.head.append(s);
  } else start();
})();
