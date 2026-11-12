// The account on the web (M7, DESIGN §6.15, §14.13), as the desktop's: the
// sidebar's last row opens the account card (退出登录, 隐私说明, 注销账号);
// 注销账号 is a page over the window, a dry run first; the login page says
// when an account was just deleted, and how to stop one's relays.
import { App, G, esc, spaced } from './shell.js';
import { api, ApiError } from './api.js';
import { D } from './clock.js';
                                     
                                                            

                // eslint-disable-line @typescript-eslint/no-explicit-any

export const PRIVACY_URL = 'https://github.com/kittors/deneb/blob/main/PRIVACY.md';
// sessionStorage: the relays of an account just deleted, for the login page.
const GONE = 'deneb.gone';
const RELAY_STOP = 'docker rm -f deneb-relay && docker volume rm deneb-relay';

let card                     = null;
let away                              = null;
// deleting is set while the account is being deleted: the session ends
// with it, and this page, not the stream's logout, takes the window to the
// login, once it faded (main.ts).
let deleting = false;
export const deletingAccount = ()          => deleting;

// FADE (sessionStorage) tells the login page it comes after the window
// faded away, logged out: it comes in over the page's background.
const FADE = 'deneb.fade';

// takeFade reports, once, whether the login page comes after a logout.
export function takeFade()          {
  const on = sessionStorage.getItem(FADE) !== null;
  sessionStorage.removeItem(FADE);
  return on;
}

// fadeOut takes el away to transparent on the app's clock (D).
export function fadeOut(el             , ms = 180)                {
  return new Promise((done) => {
    const t0 = D.now();
    const stop = D.onFrame((now        ) => {
      const k = Math.min(1, (now - t0) / ms);
      el.style.opacity = String(1 - k * k * k);
      if (k >= 1) {
        stop();
        done();
      }
    });
  });
}

// fadeIn brings el in from transparent on the app's clock (D), which the
// recordings drive a frame at a time, as the canvas's own motion is.
export function fadeIn(el             , ms = 180)       {
  el.style.opacity = '0';
  const t0 = D.now();
  const stop = D.onFrame((now        ) => {
    const k = Math.min(1, (now - t0) / ms);
    el.style.opacity = k >= 1 ? '' : String(1 - Math.pow(1 - k, 3));
    if (k >= 1) stop();
  });
}

// wireAccount makes the sidebar's last row (my ball, name, gear) the
// account card's button, once.
export function wireAccount(app     , ctx     )       {
  const foot = app.win.querySelector('.side .me')                      ;
  if (!foot || foot.dataset.acct) return;
  foot.dataset.acct = '1';
  foot.setAttribute('role', 'button');
  foot.setAttribute('aria-label', '账号');
  foot.tabIndex = 0;
  foot.addEventListener('click', () => (card ? closeCard() : openCard(app, ctx)));
  foot.addEventListener('keydown', (e) => e.key === 'Enter' && (card ? closeCard() : openCard(app, ctx)));
}

function openCard(app     , ctx     )       {
  const v = ctx.view();
  const me = v.people.find((p) => p.id === v.me);
  const foot = app.win.querySelector('.side .me')               ;
  foot.classList.add('on');
  const wr = app.win.getBoundingClientRect();
  const fr = foot.getBoundingClientRect();
  const item = (a        , label        , line        , cls = '', tail = '', tag = 'button', attrs = '') =>
    `<${tag} class="acct-item ${cls}" data-a="${a}" ${attrs}><span class="acct-words"><b>${esc(label)}</b><span>${esc(line)}</span></span>${tail}</${tag}>`;
  card = App.h(
    `<div class="acct-card" role="dialog" aria-label="账号">` +
      `<div class="acct-who">${App.ball(v.me)}<div><b>${esc(me ? spaced(me.displayName || me.name) : '')}</b><span>${esc(me?.email || '')}</span></div></div>` +
      `<div class="acct-rule"></div>` +
      item('logout', '退出登录', '这个浏览器退出，别的设备照常') +
      `<div class="acct-rule"></div>` +
      item('privacy', '隐私说明', '记了什么、存多久、谁看得到、怎么删', '', G.ui('external', 16), 'a', `href="${PRIVACY_URL}" target="_blank" rel="noopener"`) +
      `<div class="acct-rule"></div>` +
      item('delete', '注销账号', '账号和名下的东西一起删掉，不能恢复', 'danger') +
      `</div>`,
  )               ;
  card.style.left = `${fr.left - wr.left}px`;
  card.style.bottom = `${wr.bottom - fr.top + 8}px`;
  app.win.appendChild(card);
  card.animate([{ opacity: 0, transform: 'translateY(8px) scale(0.96)' }, { opacity: 1, transform: 'none' }], { duration: 200, easing: 'cubic-bezier(0.2, 0.8, 0.2, 1)' });
  (card.querySelector('[data-a="logout"]')               ).onclick = async () => {
    closeCard();
    // The window fades to the page's background, and the login comes in
    // over it, as the desktop's does (DESIGN §6.15).
    const win = app.win               ;
    await fadeOut(win);
    sessionStorage.setItem(FADE, '1');
    api.logout().finally(() => {
      win.style.opacity = '';
      // The stream's logout may have gone there already.
      if (location.pathname !== '/login') ctx.go('/login', true);
    });
  };
  (card.querySelector('[data-a="privacy"]')               ).onclick = () => closeCard();
  (card.querySelector('[data-a="delete"]')               ).onclick = () => {
    closeCard();
    openDelete(app, ctx);
  };
  // A click anywhere else, or Esc, puts it away.
  away = (e       ) => {
    if (e instanceof KeyboardEvent ? e.key === 'Escape' : card && !card.contains(e.target        ) && !foot.contains(e.target        )) closeCard();
  };
  document.addEventListener('pointerdown', away, true);
  document.addEventListener('keydown', away, true);
}

function closeCard()       {
  if (!card) return;
  const c = card;
  card = null;
  c.parentElement?.querySelector('.side .me')?.classList.remove('on');
  if (away) {
    document.removeEventListener('pointerdown', away, true);
    document.removeEventListener('keydown', away, true);
    away = null;
  }
  c.animate([{ opacity: 1, transform: 'none' }, { opacity: 0, transform: 'translateY(6px) scale(0.97)' }], { duration: 160, easing: 'ease-in', fill: 'forwards' });
  D.after(170, () => c.remove());
}

// ---- 注销账号 -------------------------------------------------------------------------------

// openDelete is the page over the window: what goes, the way with my LANs
// that have others in them, my email again, and the red button.
export function openDelete(app     , ctx     )       {
  const v0 = ctx.view();
  const email = v0.people.find((p) => p.id === v0.me)?.email || '';
  const page = App.h(
    `<div class="acct-del" role="dialog" aria-label="注销账号">` +
      `<button class="acct-back" type="button">${G.ui('chevronLeft', 16)}返回</button>` +
      `<div class="acct-col"><div class="acct-wait"><span class="spin"></span>正在看会删掉什么…</div></div>` +
      `</div>`,
  )               ;
  app.win.appendChild(page);
  page.animate([{ opacity: 0 }, { opacity: 1 }], { duration: 200, easing: 'ease-out' });
  const col = page.querySelector('.acct-col')               ;
  let blocked           = [];
  let owned                        = 'transfer';
  let acc                         = null;
  let sending = false;
  const close = () => {
    if (sending) return;
    document.removeEventListener('keydown', esc_, true);
    page.animate([{ opacity: 1 }, { opacity: 0 }], { duration: 160, easing: 'ease-in', fill: 'forwards' });
    D.after(170, () => page.remove());
  };
  const esc_ = (e               ) => e.key === 'Escape' && close();
  document.addEventListener('keydown', esc_, true);
  (page.querySelector('.acct-back')               ).onclick = close;

  const v = () => ctx.view();
  const lanName = (id        ) => v().lans.find((l) => l.id === id)?.name || '';
  const personName = (id        ) => {
    const p = v().people.find((x) => x.id === id);
    return p ? spaced(p.displayName || p.name) : '';
  };
  const relayName = (id        ) => v().relays.find((r) => r.id === id)?.name || '我的服务器';
  const devName = (id        ) => {
    const d = v().devices.find((x) => x.id === id) || (v().loggedOutDevices || []).find((x) => x.id === id);
    return d ? spaced(d.displayName || d.name) : '';
  };

  // dryRun asks what deleting would delete; with owned unset, which LANs
  // are mine with others in them.
  async function dryRun(first         )                {
    try {
      if (D.recording) await D.wait(first ? 700 : 300); // the recording shows the wait
      const r = await ctx.op('account.delete', first ? { dryRun: true } : { dryRun: true, ownedLANs: owned });
      const a = r.result?.account;
      if (!a) return;
      if (first && a.blocked?.length) {
        blocked = a.blocked;
        return dryRun(false);
      }
      acc = a;
      render();
    } catch (e) {
      if (e instanceof ApiError && e.code === 'forbidden') return refused(e.message);
      col.innerHTML = `<div class="acct-wait trouble">${esc(e instanceof ApiError ? e.message : '连不上 Deneb 服务器，过一会儿再试')}</div>`;
    }
  }

  function refused(msg        )       {
    col.innerHTML = `<h1>注销账号</h1><p class="acct-refused">${esc(msg)}</p><button class="btn ghost-btn acct-refused-back" type="button">返回</button>`;
    (col.querySelector('.acct-refused-back')               ).onclick = close;
  }

  const row = (icon        , what        , line        ) =>
    `<div class="acct-row"><span class="acct-ico">${G.ui(icon, 16)}</span><span><b>${esc(what)}</b>${line ? `<span>${esc(line)}</span>` : ''}</span></div>`;
  const quoted = (names          ) => names.map((n) => `「${n}」`).join('、');

  function render()       {
    const a = acc                   ;
    // My own first, then the logged out ones, then my LANs' public ones,
    // as 我的设备 shows them; the server lists them by id.
    const rank = (id        ) => {
      const d = v().devices.find((x) => x.id === id);
      return d ? (d.publicLAN ? 2 : 0) : 1;
    };
    const order = new Map(v().devices.map((d, i) => [d.id, i]));
    const devs = [...a.devices].sort((x, y) => rank(x) - rank(y) || (order.get(x) ?? 0) - (order.get(y) ?? 0)).map(devName);
    const list = devs.length > 4 ? devs.slice(0, 4).join('、') + `……等 ${devs.length} 台` : devs.join('、');
    const alone = a.lans.filter((id) => !blocked.includes(id));
    const parts = [a.shares ? `${a.shares} 次分享` : '', a.invites ? `${a.invites} 个邀请` : ''].filter(Boolean);
    const typed = (col.querySelector('.acct-email')                           )?.value ?? '';
    // The first answer fades in where the spinner was.
    const first = !!col.querySelector('.acct-wait');
    col.innerHTML =
      `<h1>注销账号</h1><p class="acct-sub">${esc(email)} 和它名下的东西会一起删掉，删了不能恢复。</p>` +
      `<div class="acct-goes">` +
      (devs.length ? row('screen', `${devs.length} 台设备会登出并删掉`, list) : '') +
      (alone.length ? row('people', `${quoted(alone.map(lanName))}会删掉`, '只有你在里面') : '') +
      (a.left.length ? row('globe', `会退出${quoted(a.left.map(lanName))}`, '别人的局域网，里面的人照常用') : '') +
      (parts.length ? row('inbox', `${parts.join('、')}会删掉`, '给出的和收到的') : '') +
      (a.relays.length ? row('arrows', `${quoted(a.relays.map(relayName))}中转站会删掉`, '') : '') +
      `</div>` +
      (blocked.length ? ownedHTML(a) : '') +
      (a.relaysInUse?.length ? `<div class="acct-inuse"><span class="dot"></span>你的${esc(quoted(a.relaysInUse.map(relayName)))}正被别人用着，注销后他们会改走公共中转站，可能慢一些。</div>` : '') +
      `<label class="acct-label" for="acct-email">再输一遍你的邮箱，确认是你</label>` +
      `<input id="acct-email" class="acct-email" type="email" autocomplete="off" placeholder="你的邮箱" aria-label="你的邮箱">` +
      `<p class="acct-trouble" hidden></p>` +
      `<button class="acct-go" type="button" disabled>注销账号</button>` +
      `<p class="acct-note">注销后不能恢复</p>` +
      `<button class="acct-later" type="button">算了</button>`;
    const input = col.querySelector('.acct-email')                    ;
    const go = col.querySelector('.acct-go')                     ;
    input.value = typed;
    const ok = () => input.value.trim().toLowerCase() === email.toLowerCase() && !!email;
    const sync = () => (go.disabled = !ok() || sending);
    input.addEventListener('input', sync);
    input.addEventListener('keydown', (e) => e.key === 'Enter' && ok() && send());
    go.onclick = () => ok() && send();
    sync();
    if (first) fadeIn(col);
    (col.querySelector('.acct-later')               ).onclick = close;
    for (const b of col.querySelectorAll('.acct-choice')) {
      (b               ).onclick = () => {
        const want = (b               ).dataset.o                         ;
        if (want === owned || sending) return;
        owned = want;
        dryRun(false);
      };
    }
  }

  function ownedHTML(a                 )         {
    const lans = blocked.map((id) => v().lans.find((l) => l.id === id)).filter(Boolean)                    ;
    const dots = (l                          ) => `<span class="dots">${l.members.filter((m) => m.state === 'joined').slice(0, 3).map((m) => App.dot(App.PEOPLE[m.person]?.c || 'var(--ink-3)')).join('')}</span>`;
    const others = (l                          ) => l.members.filter((m) => m.state === 'joined' && m.person !== v().me).length;
    let line = '';
    if (owned === 'transfer') line = (a.transferred || []).map((t) => `${lanName(t.lan)}转给${personName(t.to)}`).join('，');
    else {
      const people = new Set        ();
      for (const l of lans) for (const m of l.members) if (m.state === 'joined' && m.person !== v().me) people.add(m.person);
      line = `${lans.map((l) => l.name).join('、')}会删掉，里面的 ${people.size} 个人都会失去它`;
    }
    const choice = (o        , title        , sub        ) =>
      `<button class="acct-choice ${o === owned ? 'on' : ''} ${o}" data-o="${o}" type="button" aria-pressed="${o === owned}"><span class="radio"></span><span><b>${esc(title)}</b><span>${esc(sub)}</span></span></button>`;
    return (
      `<div class="acct-label">你建的局域网里还有别人</div>` +
      lans.map((l) => `<div class="acct-lan">${dots(l)}<span>${esc(spaced(l.name))}</span><span class="n">还有 ${others(l)} 个人</span></div>`).join('') +
      `<div class="acct-choices">${choice('transfer', '转给最早加入的人', '局域网留着，大家照常用')}${choice('delete', '整个删掉', '里面的人都会失去它')}</div>` +
      `<p class="acct-owned-line">${esc(spaced(line))}</p>`
    );
  }

  async function send()                {
    if (sending) return;
    sending = true;
    const go = col.querySelector('.acct-go')                     ;
    const trouble = col.querySelector('.acct-trouble')               ;
    trouble.hidden = true;
    go.disabled = true;
    go.classList.add('sending');
    go.innerHTML = `<span class="spin white"></span>正在注销…`;
    const relays = (acc?.relays || []).map(relayName);
    // What the login page will say, before the session ends with the
    // account; taken back if it does not.
    deleting = true;
    sessionStorage.setItem(GONE, JSON.stringify(relays));
    try {
      if (D.recording) await D.wait(1200);
      const input = col.querySelector('.acct-email')                    ;
      await ctx.op('account.delete', blocked.length ? { confirm: input.value.trim(), ownedLANs: owned } : { confirm: input.value.trim() });
      sending = false;
      document.removeEventListener('keydown', esc_, true);
      // Gone: the page fades to the login page's own background, and the
      // login comes in over it, rather than cut to it.
      const from = getComputedStyle(page).backgroundColor, to = getComputedStyle(document.body).backgroundColor;
      for (const el of [col, page.querySelector('.acct-back')               ]) el.animate([{ opacity: 1 }, { opacity: 0 }], { duration: 200, easing: 'ease-in', fill: 'forwards' });
      page.animate([{ backgroundColor: from }, { backgroundColor: to }], { duration: 260, easing: 'ease-in-out', fill: 'forwards' });
      await D.wait(270);
      deleting = false;
      ctx.go('/login', true);
    } catch (e) {
      deleting = false;
      sessionStorage.removeItem(GONE);
      sending = false;
      go.classList.remove('sending');
      go.textContent = '注销账号';
      go.disabled = false;
      trouble.textContent = e instanceof ApiError ? e.message : '连不上 Deneb 服务器，过一会儿再试';
      trouble.hidden = false;
    }
  }

  dryRun(true);
}

// goneNote is what the login page says once the account was deleted, read
// once: the line over the card, and for one who ran relays of their own,
// the card under it with the commands that stop them.
export function goneNote()                                          {
  const raw = sessionStorage.getItem(GONE);
  if (raw === null) return null;
  sessionStorage.removeItem(GONE);
  let relays           = [];
  try {
    relays = JSON.parse(raw);
  } catch {
    // nothing to say of relays
  }
  const line = `<p class="join-gone"><svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M5 12.5l4.5 4.5L19 7.5"/></svg>账号已经注销</p>`;
  if (!relays.length) return { line, relays: '' };
  const names = relays.map((n) => `「${esc(n)}」`).join('、');
  return {
    line,
    relays:
      `<div class="join-gone-relays"><p>${names}中转站已经删掉，可那台机器上的容器还在跑，可以在上面停掉它：</p>` +
      `<pre>${esc(RELAY_STOP.replace(' && ', '\n'))}</pre><div class="r"><button class="btn ghost-btn copy-stop" type="button">复制</button></div></div>`,
  };
}

// wireGone gives the relays' card its copy button: it says it was copied,
// a moment (there is no toast on the login page).
export function wireGone(el             )       {
  const b = el.querySelector('.copy-stop')                            ;
  if (!b) return;
  b.onclick = () => {
    navigator.clipboard?.writeText(RELAY_STOP).catch(() => undefined);
    b.textContent = '已复制';
    D.after(2000, () => (b.textContent = '复制'));
  };
}
