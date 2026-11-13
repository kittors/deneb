// A new account's first page (DESIGN.md §14.14): the frame of every page,
// its sidebar and account row, and in the middle the two things to do
// first: put Deneb on the computers, and make a LAN or join a friend's.
// Once there is a LAN, home is its canvas and this page is not shown.
import { frame, clear, esc, spaced } from './shell.js';
import { appIcon, detectPlatform, downloadURL, DOWNLOADS, icon, phoneDock } from './download.js';
import { ApiError } from './api.js';
import { D } from './clock.js';
                                              
                                     
                                         
                                           

const ALL             = ['macos', 'windows', 'linux'];
const CHECK = `<svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m5.5 12.5 4 4 9-9"/></svg>`;
const LINK = `<svg viewBox="0 0 24 24" width="20" height="20" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M10 14a4.5 4.5 0 0 0 6.4 0l2.8-2.8a4.5 4.5 0 0 0-6.4-6.4l-1.2 1.2"/><path d="M14 10a4.5 4.5 0 0 0-6.4 0l-2.8 2.8a4.5 4.5 0 0 0 6.4 6.4l1.2-1.2"/></svg>`;
const PLUS = `<svg viewBox="0 0 24 24" width="20" height="20" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" aria-hidden="true"><path d="M12 5v14M5 12h14"/></svg>`;

// The invite's token in what was pasted: a link of this deployment (/i/…,
// /join/…), the app's deneb://invite/…, or the token alone.
export function inviteToken(s        )                {
  const t = s.trim();
  const m = t.match(/(?:\/i\/|\/join\/|deneb:\/\/invite\/)([A-Za-z0-9_-]+)/) || t.match(/^([A-Za-z0-9_-]{5,})$/);
  return m ? m[1] : null;
}

export function welcomePage(root             , ctx     , _route       )             {
  // This batch's recordings (rec-welcome.ts) start on this page.
  if (D.recording && /^welcome/.test(new URLSearchParams(location.search).get('record') || '')) import('./rec-welcome.js');
  if (ctx.phone()) return phoneWelcome(root, ctx);
  const app = frame(root, ctx, 'home');
  clear();
  app.setStack([]);
  app.setTitle('欢迎来到 Deneb', '在网页上管你的局域网和设备：谁能用、开放哪些服务、邀请谁', 'none');
  const el = document.createElement('div');
  el.className = 'welcome';
  app.canvas.appendChild(el);
  const off = draw(el, ctx);
  return () => {
    off();
    el.remove();
  };
}

// draw puts the two cards in el, and keeps them up with the view.
function draw(el             , ctx     , phone = false)             {
  let v = ctx.view();
  const me = v.people.find((p) => p.id === v.me);
  const myName = me ? me.displayName || me.name : '我';
  const email = ctx.email || me?.email;
  const platform = detectPlatform();
  let all = platform == null;
  let downloaded = false;
  let joining = false;
  let making = false;

  el.innerHTML =
    `<section class="wc-card wc-install" aria-label="把电脑装上 Deneb"></section>` +
    `<section class="wc-card wc-lan" aria-label="建一个局域网，或者加入朋友的">` +
    `<h2 class="wc-h">建一个局域网，或者加入朋友的</h2>` +
    `<div class="wc-ways">` +
    `<button class="wc-way" type="button" data-way="new"><span class="wc-way-ico">${PLUS}</span><b>新建局域网</b><span class="wc-way-sub">先建好，再把电脑放进来、邀请朋友</span></button>` +
    `<div class="wc-way" role="button" tabindex="0" data-way="join"><span class="wc-way-ico">${LINK}</span><b>加入朋友的</b><span class="wc-way-sub">朋友发来的邀请链接，粘贴到这里</span></div>` +
    `</div><p class="wc-line" role="status"></p></section>`;
  const install = el.querySelector('.wc-install')               ;
  const line = el.querySelector('.wc-line')               ;

  // The first card: download for this computer, or which of mine are in.
  const mine = () => v.devices.filter((d) => d.owner === v.me && !d.publicLAN);
  const drawInstall = () => {
    const ds = mine();
    const done = ds.length > 0;
    const btn = (p          , primary         ) =>
      `<a class="btn ${primary ? 'primary' : 'ghost'} wc-dl" href="${downloadURL(p)}" download data-p="${p}">${icon('download', 16)}下载 ${DOWNLOADS[p].label}</a>`;
    const buttons = phone
      ? ''
      : all
        ? ALL.map((p) => btn(p, p === platform)).join('')
        : btn(platform            , true) + `<button class="wc-more" type="button">也有 ${ALL.filter((p) => p !== platform).map((p) => DOWNLOADS[p].label).join('、')}</button>`;
    const who = email ? `用 ${email} 登录` : '用这个邮箱登录';
    const head = done
      ? ds
          .slice(0, 3)
          .map((d) => `<span class="wc-dev"><span class="wc-dot ${d.online ? 'on' : ''}"></span>${esc(spaced(d.displayName || d.name))}</span>`)
          .join('') + (ds.length > 3 ? `<span class="wc-dev">等 ${ds.length} 台</span>` : '') + `<span class="wc-done-words">装好了</span>`
      : '';
    install.innerHTML =
      `<div class="wc-top"><div class="wc-icon">${appIcon(46)}</div><div class="wc-title"><h2 class="wc-h">把电脑装上 Deneb</h2>` +
      `<p class="wc-sub">${done ? `再装别的电脑，也${esc(who)}` : `想连起来的每台电脑都装一个，${esc(who)}`}</p></div>` +
      (done ? `<span class="wc-tick" aria-label="装好了">${CHECK}</span>` : '') +
      `</div>` +
      (done ? `<p class="wc-devs">${head}</p>` : '') +
      (phone ? '' : `<div class="wc-buttons">${buttons}</div>`) +
      (downloaded && !done ? `<p class="wc-after" role="status">装好打开以后，${esc(who)}，它会自己出现在这里。</p>` : '');
    install.querySelector('.wc-more')?.addEventListener('click', () => {
      all = true;
      drawInstall();
      // The other two come in beside it, one after the other.
      [...install.querySelectorAll             ('.wc-dl:not(.primary)')].forEach((b, i) =>
        b.animate([{ opacity: 0, transform: 'translateX(-6px)' }, { opacity: 1, transform: 'none' }], { duration: 200, delay: i * 50, easing: 'ease-out', fill: 'backwards' }),
      );
    });
    for (const a of install.querySelectorAll                   ('a.wc-dl'))
      a.addEventListener('click', () => {
        if (downloaded) return;
        downloaded = true;
        // After the browser has taken the click: the line opens under the
        // buttons.
        D.after(0, () => {
          drawInstall();
          const line = install.querySelector('.wc-after')                      ;
          if (!line) return;
          // Its exact height: the line ends where it then stays, to the pixel.
          const h = line.getBoundingClientRect().height;
          line.animate(
            [
              { height: '0px', marginTop: '0px', paddingTop: '0px', paddingBottom: '0px', opacity: 0 },
              { height: h + 'px', marginTop: '14px', paddingTop: '10px', paddingBottom: '10px', opacity: 1 },
            ],
            { duration: 240, easing: 'cubic-bezier(0.3, 0.3, 0.2, 1)' },
          );
        });
      });
  };
  drawInstall();
  if (phone) install.appendChild(phoneDock());

  // leave fades the cards out, then another page takes the canvas (its own
  // fades in over it, shell.ts).
  const leave = (then            ) => {
    const a = el.animate([{ opacity: 1 }, { opacity: 0 }], { duration: 140, easing: 'ease-in', fill: 'forwards' });
    a.finished.then(then, then);
  };
  // 新建局域网: one named after me, as the desktop's first steps make it;
  // its canvas comes as soon as the view has it.
  const make = async (btn             ) => {
    if (making) return;
    making = true;
    btn.classList.add('busy');
    line.textContent = '';
    try {
      const r = await ctx.op('lan.create', { name: spaced(`${myName}的局域网`) });
      const id = r.result?.lan;
      if (id) {
        await D.wait(0); // on the page's clock (clock.ts)
        const go = () => leave(() => ctx.go('/lan/' + encodeURIComponent(id)));
        if (ctx.view().lans.some((l) => l.id === id)) go();
        else {
          const stop = ctx.onView((nv) => {
            if (nv.lans.some((l) => l.id === id)) {
              stop();
              go();
            }
          });
        }
      }
    } catch (err) {
      await D.wait(0);
      making = false;
      btn.classList.remove('busy');
      line.textContent = err instanceof ApiError ? err.message : '连不上 Deneb 服务器，过一会儿再试';
    }
  };
  // 加入朋友的: the little card opens into a field for the link, in place.
  const openJoin = (way             ) => {
    if (joining) return;
    joining = true;
    way.classList.add('open');
    way.removeAttribute('role');
    way.removeAttribute('tabindex');
    way.innerHTML =
      `<form class="wc-join" novalidate><label class="field"><span class="sr">邀请链接</span><input name="link" type="url" autocomplete="off" placeholder="把邀请链接粘贴到这里" aria-label="邀请链接" /></label>` +
      `<button class="btn primary" type="submit" disabled>打开</button></form>`;
    way.animate([{ opacity: 0.4 }, { opacity: 1 }], { duration: 180, easing: 'ease-out' });
    const form = way.querySelector('form')                   ;
    const input = form.querySelector('input')                    ;
    const btn = form.querySelector('button')                     ;
    input.addEventListener('input', () => {
      btn.disabled = !inviteToken(input.value);
      line.textContent = '';
    });
    input.focus();
    form.addEventListener('submit', (e) => {
      e.preventDefault();
      const t = inviteToken(input.value);
      if (!t) {
        line.textContent = '这不像邀请链接，再看一眼';
        return;
      }
      leave(() => ctx.go('/i/' + encodeURIComponent(t)));
    });
  };
  const ways = el.querySelectorAll             ('.wc-way');
  for (const way of ways) {
    const act = () => (way.dataset.way === 'new' ? make(way) : openJoin(way));
    way.addEventListener('click', (e) => {
      if ((e.target               ).closest('form')) return;
      act();
    });
    way.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' && !(e.target               ).closest('form')) act();
    });
  }

  // A computer of mine signs in meanwhile: the first card says so.
  let had = mine().length;
  const off = ctx.onView((nv          ) => {
    v = nv;
    if (mine().length === had) return;
    had = mine().length;
    // On the page's clock (clock.ts): what the card says fades out; the
    // new words fade in as it grows to them, and the tick pops in.
    D.after(0, () => {
      const out = [...install.children].map((c) => (c               ).animate([{ opacity: 1 }, { opacity: 0 }], { duration: 120, easing: 'ease-in', fill: 'forwards' }));
      const swap = () => {
        const h0 = install.getBoundingClientRect().height;
        drawInstall();
        const h1 = install.getBoundingClientRect().height;
        if (Math.abs(h1 - h0) > 0.5) install.animate([{ height: h0 + 'px' }, { height: h1 + 'px' }], { duration: 260, easing: 'cubic-bezier(0.3, 0.3, 0.2, 1)' });
        for (const c of install.children) (c               ).animate([{ opacity: 0 }, { opacity: 1 }], { duration: 220, easing: 'ease-out' });
        install.querySelector('.wc-tick')?.animate([{ transform: 'scale(0.4)', opacity: 0 }, { transform: 'scale(1.12)', opacity: 1, offset: 0.6 }, { transform: 'none' }], { duration: 440, delay: 80, easing: 'cubic-bezier(0.3, 1.2, 0.5, 1)', fill: 'backwards' });
      };
      (out[0] ? out[0].finished : Promise.resolve()).then(swap, swap);
    });
  });
  return off;
}

// On a phone (§14.6): the same two cards, one over the other, and the dock
// at the bottom for the download link, as there is no Deneb for phones.
function phoneWelcome(root             , ctx     )             {
  const page = document.createElement('div');
  page.className = 'm-page m-welcome';
  page.innerHTML = `<div class="m-wc-head"><h1>欢迎来到 Deneb</h1><p>在网页上管你的局域网和设备：<br />谁能用、开放哪些服务、邀请谁</p></div><div class="welcome phone"></div>`;
  root.replaceChildren(page);
  const off = draw(page.querySelector('.welcome')               , ctx, true);
  return () => {
    off();
    page.remove();
  };
}
