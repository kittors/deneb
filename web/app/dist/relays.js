// The relays page (DESIGN.md §14.8, design/demos/web/m3-15-relays.html): two
// wells as scene 4's console has, my relays, which I manage, and the public
// ones, which I only see. A relay is the desktop's white glossy ball. My
// relay's card says how it is doing now and whom it carried this month,
// with who is on it: a click on someone's ball turns them away (relay.kick)
// and a second click lets them back (relay.unkick), with undo. The last
// cell sets up my own server: one line to run, after which the new relay
// shows up by itself (the view brings it).
import { frame, clear, follow, App, G, esc, spaced, nameOf } from './shell.js';
import { D } from './clock.js';
import { api, ApiError } from './api.js';
                                     
                                         
                                                                                                                      

                                       

const RELAY = (size = 20) => G.device('relay', size);
const RING = (box = 44) => {
  const c = box / 2, r = c - 1.75;
  return `<svg class="ring" viewBox="0 0 ${box} ${box}" width="${box}" height="${box}" aria-hidden="true"><circle cx="${c}" cy="${c}" r="${r}" fill="none" stroke="currentColor" stroke-width="1.5" stroke-dasharray="4 3.5"/></svg>`;
};
// Just set up: "刚上线" for ten minutes, unless someone is on it already.
const FRESH = 10 * 60 * 1000;

export function fmtBytes(b        )         {
  if (b < 1e6) return `${Math.max(0, Math.round(b / 1e3))} KB`;
  if (b < 1e9) return `${Math.round(b / 1e6)} MB`;
  return `${(b / 1e9).toFixed(b < 1e10 ? 1 : 0)} GB`;
}
const fmtRate = (bps        ) => fmtBytes(bps) + '/s';
function fmtUptime(s        )         {
  if (s < 3600) return '刚上线';
  if (s < 86400) return `已经运行 ${Math.floor(s / 3600)} 小时`;
  return `已经运行 ${Math.floor(s / 86400)} 天`;
}
function fmtAgo(iso         )         {
  if (!iso) return '有一阵没报到了';
  const m = Math.max(1, Math.round((Date.now() - Date.parse(iso)) / 60000));
  if (m < 60) return `${m} 分钟前还在`;
  if (m < 1440) return `${Math.round(m / 60)} 小时前还在`;
  return `${Math.round(m / 1440)} 天前还在`;
}
// How busy, in words: the load's CPU, and full when it takes no more.
function busy(cpu         , full          )         {
  if (full) return '满了，新设备改走别处';
  if (cpu == null) return '—';
  if (cpu < 0.5) return '不忙';
  if (cpu < 0.8) return '有点忙';
  return '很忙';
}
const limitWords = (bps         ) => (bps ? `每条连接 ${fmtRate(bps)}` : '不限速');

export function relaysPage(root             , ctx     , _route       )             {
  const app = frame(root, ctx, 'relays');
  clear();
  app.setStack([]);
  app.setTitle('中转站', '连不上的时候，自动经中转站，不用选', 'ok');
  const cv              = app.canvas;
  const P = App.PEOPLE;
  const traced = ['.relays-page .rwell', '.relays-page .rcell'];
  app.o.trace = (app.o.trace || []).concat(traced);

  let v           = ctx.view();
  let usage = new Map                     ();
  // The install command shows once, when it is made: kept for this visit,
  // so that the waiting cell can show it again.
  let made                                                            = null;
  // People let back on a relay this visit stay on its card until it closes,
  // though they are not on it yet (their devices come back by themselves).
  const letBack = new Map                        ();
  // The kick the toast on screen is about: letting them back takes it away,
  // so it never says "已不让…" of someone who is back.
  let toastKick                = null;
  let gone = false;

  const mine = ()         =>
    (v.myRelays || [])
      .map((rv) => ({ v: rv, r: v.relays.find((x) => x.id === rv.id) }))
      .filter((m)            => !!m.r)
      .sort((a, b) => (a.v.since || '').localeCompare(b.v.since || '') || a.r.id.localeCompare(b.r.id));
  const pubs = ()          => v.relays.filter((r) => r.source === 'public');
  const waiting = ()                  => (v.relayJoins || []).filter((j) => !j.relay);
  let known = new Set(mine().map((m) => m.r.id));

  // Who is on a relay: its connected people, and those turned away (still
  // shown, greyed, to be let back).
  const kicksOf = (id         )              => (usage.get(id)?.kicks || []).filter((k) => !k.liftedAt && k.person);
  const onIt = (id         )             => {
    const u = usage.get(id);
    const set             = [];
    for (const c of u?.connected || []) if (!set.includes(c.person)) set.push(c.person);
    return set;
  };
  const sub = (m      )         => {
    if (!m.v.online) return '不在线';
    const n = usage.get(m.r.id)?.load?.people ?? onIt(m.r.id).length;
    if (!n && m.v.since && Date.now() - Date.parse(m.v.since) < FRESH) return '刚上线';
    return `${n} 个人在用`;
  };

  // ---- The wells -----------------------------------------------------------------------
  const page              = App.h(
    `<div class="relays-page">` +
      `<section class="rwell mine" data-k="mine" aria-label="我的中转站"><div class="card-label"><span>我的中转站</span><span class="n-mine"></span></div><div class="rgrid g-mine" role="list"></div></section>` +
      `<section class="rwell pub" data-k="pub" aria-label="公共中转站"><div class="card-label"><span>公共中转站</span><span class="n-pub"></span></div><div class="rgrid g-pub" role="list"></div></section>` +
      `</div>`,
  );
  cv.appendChild(page);
  const $ = (s        , r             = page) => r.querySelector(s)               ;
  const orb = (online         ) => `<div class="orb">${RELAY()}<span class="on${online ? '' : ' off'}"></span></div>`;
  const cell = (k        , name        , small        , online         , label        ) =>
    `<div class="rcell" data-k="${esc(k)}" role="button" tabindex="0" aria-label="${esc(label)}">${orb(online)}<div>${esc(spaced(name))}<small>${esc(spaced(small))}</small></div></div>`;
  const addCell = () =>
    waiting().length || made
      ? `<div class="rcell add-cell" data-k="join" role="button" tabindex="0" aria-label="等待安装：在服务器上运行的那一行命令还没跑"><div class="orb add wait">${RING()}${G.ui('relayLine', 18)}</div><div>等待安装<small>命令还没跑</small></div></div>`
      : `<div class="rcell add-cell" data-k="add" role="button" tabindex="0" aria-label="用我自己的服务器：一行命令装好"><div class="orb add">${RING()}${G.ui('plus', 18)}</div><div>用我自己的服务器<small>一行命令装好</small></div></div>`;

  // A well grows and shrinks with what is in it, on a smooth curve (§5),
  // from the height it shows now: no frame jumps.
  function fill(well             , grid             , html        )       {
    const h0 = well.getBoundingClientRect().height;
    for (const a of well.getAnimations()) a.cancel();
    grid.innerHTML = html;
    const h1 = well.getBoundingClientRect().height;
    if (h0 && Math.abs(h1 - h0) > 1) well.animate([{ height: h0 + 'px' }, { height: h1 + 'px' }], { duration: 360, easing: 'cubic-bezier(0.3, 0.3, 0.2, 1)' });
  }
  function drawMine()       {
    const ms = mine();
    fill(
      $('.rwell.mine'),
      $('.g-mine'),
      ms.map((m) => cell(m.r.id, m.r.name, sub(m), m.v.online, `${m.r.name}，我的中转站，${sub(m)}`)).join('') + addCell(),
    );
    $('.n-mine').textContent = `${ms.length} 个`;
    wire();
    mark();
  }
  function drawPub()       {
    const ps = pubs();
    fill($('.rwell.pub'), $('.g-pub'), ps.length ? ps.map((r) => cell(r.id, r.name, '公共 · 自动兜底', true, `${r.name}，公共中转站，自动兜底`)).join('') : `<p class="say">这里没有公共中转站</p>`);
    $('.n-pub').textContent = `${ps.length} 个`;
    wire();
    mark();
  }
  // The second lines only, when the numbers change: the cells stay.
  function relabel()       {
    for (const m of mine()) {
      const c = $(`.rcell[data-k="${CSS.escape(m.r.id)}"]`);
      if (!c) continue;
      const s = sub(m);
      const sm = c.querySelector('small')               ;
      if (sm.textContent !== spaced(s)) {
        sm.textContent = spaced(s);
        sm.animate([{ opacity: 0.2 }, { opacity: 1 }], { duration: 200, easing: 'ease-out' });
      }
      c.setAttribute('aria-label', `${m.r.name}，我的中转站，${s}`);
      c.querySelector('.on')?.classList.toggle('off', !m.v.online);
    }
  }

  let openKey                = null;
  function mark()       {
    for (const c of page.querySelectorAll             ('.rcell')) c.classList.toggle('sel', c.dataset.k === openKey || (openKey === 'join' && c.dataset.k === 'add'));
  }
  function wire()       {
    for (const c of page.querySelectorAll             ('.rcell')) {
      if ((c                                       ).__wired) continue;
      (c                                       ).__wired = true;
      const go = () => {
        const k = c.dataset.k          ;
        if (k === 'add' || k === 'join') return void openAdd();
        if (mine().some((m) => m.r.id === k)) return openMine(k);
        if (pubs().some((r) => r.id === k)) return openPub(k);
      };
      c.addEventListener('click', go);
      c.addEventListener('keydown', (e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          go();
        }
      });
    }
  }

  // ---- Cards -----------------------------------------------------------------------------
  const head = (orbHTML        , name        , line        ) =>
    `<div class="top"><div class="orb-s">${orbHTML}</div><div><b>${esc(spaced(name))}</b><span>${esc(spaced(line))}</span></div><button class="icon-btn sheet-close" type="button" title="关闭" aria-label="关闭">${G.ui('close', 16)}</button></div>`;
  const sheet = (key        , html        )              => {
    openKey = key;
    mark();
    return app.sheet(`<div class="relays-card">${html}</div>`, key);
  };

  function openMine(id         )       {
    const m = mine().find((x) => x.r.id === id);
    if (!m) return;
    const u = usage.get(id);
    const load = u?.load;
    // Everyone on it but me: I cannot turn myself away.
    const people = onIt(id).filter((p) => p !== v.me);
    const kicked = kicksOf(id);
    const back = letBack.get(id) || new Set          ();
    // One order for all of them, so that a ball turned away greys where it
    // is rather than moving.
    const who = [...new Set          ([...people, ...back, ...kicked.map((k) => k.person            )])].filter((p) => p !== v.me).sort();
    const status = m.v.online
      ? `<div class="status"><span class="badge" style="--c:#22C55E">${G.ui('online', 16)}</span><div>在线<small>${esc(load ? fmtUptime(load.uptime) : m.v.since && Date.now() - Date.parse(m.v.since) < FRESH ? '刚上线' : '一切正常')}</small></div></div>`
      : `<div class="status"><span class="badge" style="--c:#a1a1aa">${G.ui('online', 16)}</span><div>不在线<small>${esc(fmtAgo(m.v.lastSeen))}，用它的设备改走公共中转站</small></div></div>`;
    const now =
      `<div class="label">现在</div><div class="rows">` +
      `<div class="row">${G.ui('people', 18)}<span>在用</span><span class="num">${load ? `${load.people} 个人 · ${load.devices} 台设备` : `${people.length} 个人`}</span></div>` +
      `<div class="row">${G.ui('bolt', 18)}<span>带宽</span><span class="num">${load ? esc(fmtRate(load.bytesPerSec)) : '—'}</span></div>` +
      `<div class="row">${G.ui('screen', 18)}<span>忙不忙</span><span class="num">${esc(busy(load?.cpu, load?.full || m.v.full))}</span></div></div>`;
    const lines = (u?.lans || []).filter((l) => l.lan && l.bytes > 0).sort((a, b) => b.bytes - a.bytes);
    const lanDots = (lan        ) => {
      const l = v.lans.find((x) => x.id === lan);
      const ps = l ? l.members.filter((mm) => mm.state === 'joined').slice(0, 3).map((mm) => mm.person) : [];
      return `<span class="mdots">${ps.map((p) => App.dot(P[p] ? P[p].c : '#a1a1aa')).join('')}</span>`;
    };
    const month =
      `<div class="label">这个月替谁转了</div>` +
      (lines.length
        ? `<div class="rows">${lines.map((l) => `<div class="row">${lanDots(l.lan          )}<span>${esc(spaced(v.lans.find((x) => x.id === l.lan)?.name || '别的局域网'))}</span><span class="num">${esc(fmtBytes(l.bytes))}</span></div>`).join('')}</div>`
        : `<p class="say">还没有替谁转过。</p>`);
    const chip = (p          ) => {
      const off = kicked.some((k) => k.person === p);
      return `<button class="chip ${off ? 'blocked' : ''}" type="button" data-k="${esc(p)}" aria-pressed="${off}" aria-label="${esc(nameOf(v, p))}${off ? '，已不让用，点一下放回' : '，点一下就不让他用'}">${App.ball(p, off ? 'off' : '')}${esc(spaced(nameOf(v, p)))}</button>`;
    };
    const users =
      `<div class="label">谁在用</div>` +
      (who.length ? `<div class="people">${who.map(chip).join('')}</div><div class="hint">点一下头像，就不让他用这个中转站了</div>` : `<p class="say">还没有人在用。</p>`);
    const inLANs = m.v.lans.map((l) => v.lans.find((x) => x.id === l)?.name).filter(Boolean)            ;
    const detail =
      `<div class="card-foot"><button class="more" type="button">更多${G.ui('chevron', 14)}</button></div>` +
      `<div class="detail"><p>地址 ${esc(m.r.ipv4 || m.r.hostName)} · 端口 ${m.r.derpPort}<br />权重 ${m.v.weight} · ${m.v.capacity ? `最多 ${m.v.capacity} 台设备` : '不限人数'}<br />${inLANs.length ? esc(spaced('在 ' + inLANs.join('、') + ' 里')) : '还没加进局域网'}</p></div>`;
    const s = sheet(id, head(orb(m.v.online), m.r.name, m.r.source === 'home' ? '家里的设备' : '我的服务器') + status + now + month + users + detail);
    for (const ch of s.querySelectorAll             ('.chip')) ch.addEventListener('click', () => toggleKick(id, ch.dataset.k            ));
  }

  function openPub(id         )       {
    const r = pubs().find((x) => x.id === id);
    if (!r) return;
    sheet(
      id,
      head(orb(true), r.name, '公共中转站') +
        `<div class="rows"><div class="row">${G.ui('bolt', 18)}<span>限速</span><span class="num">${esc(limitWords(r.rateLimit))}</span></div></div>` +
        `<p class="say">自动兜底，不用管。你自己的中转站连不上时，自动改走这里。</p>`,
    );
  }

  async function openAdd()                {
    const show = () => {
      const html =
        head(`<div class="orb add wait">${RING(40)}${G.ui('relayLine', 18)}</div>`, '用我自己的服务器', '装好后，它自己出现在这里') +
        (made
          ? `<div class="label">在服务器上运行这一行</div><div class="cmd" tabindex="0" aria-label="安装命令">${esc(made.command)}</div>` +
            `<div class="cmd-foot"><p class="hint">1 小时内有效，只能用一次。要先装好 Docker。</p><button class="btn primary" type="button" data-act="copy">${G.ui('copy', 15)}复制</button></div>` +
            `<div class="card-foot"><span></span><button class="out-btn" type="button" data-act="revoke">不装了，收回命令</button></div>`
          : `<p class="say">正在生成命令…</p>`);
      const s = sheet('join', html);
      const b = s.querySelector('[data-act="copy"]')                            ;
      b?.addEventListener('click', async () => {
        try {
          await navigator.clipboard.writeText(made .command);
          b.innerHTML = `${G.ui('check', 15)}已复制`;
        } catch {
          // No clipboard here: the line is selectable for the hand to copy.
          const sel = getSelection();
          const range = document.createRange();
          range.selectNodeContents(s.querySelector('.cmd')        );
          sel?.removeAllRanges();
          sel?.addRange(range);
          b.innerHTML = '已选中，按 ⌘C';
        }
        b.animate([{ opacity: 0.4 }, { opacity: 1 }], { duration: 160, easing: 'ease-out' });
      });
      s.querySelector('[data-act="revoke"]')?.addEventListener('click', () => void revokeCmd());
    };
    if (made) return show();
    show();
    drawMine();
    try {
      const res = await ctx.op('relay.join', {});
      const j = res.result?.relayJoin;
      if (!j) throw new ApiError(500, 'internal', '没拿到命令');
      made = { id: j.id, command: j.command, expiresAt: j.expiresAt };
    } catch (e) {
      if (gone) return;
      if (openKey === 'join') app.closeSheet();
      drawMine();
      app.toast(e instanceof ApiError ? e.message : '没能生成命令，再试一次', { parent: cv, x: 330 });
      return;
    }
    if (gone) return;
    drawMine();
    if (openKey === 'join') show();
  }

  async function revokeCmd()                {
    if (!made) return;
    const id = made.id;
    made = null;
    if (openKey === 'join') app.closeSheet();
    drawMine();
    try {
      await ctx.op('relay.revokeJoin', { join: id });
    } catch (e) {
      app.toast(e instanceof ApiError ? e.message : '没能收回，再试一次', { parent: cv, x: 330 });
    }
  }

  // ---- Turning someone away, and back -------------------------------------------------------
  async function toggleKick(id         , p          )                {
    const m = mine().find((x) => x.r.id === id);
    if (!m) return;
    const k = kicksOf(id).find((x) => x.person === p);
    const name = nameOf(v, p);
    if (k) {
      // Let back: the ball takes its color again at once.
      await unkick(id, k.id, p);
      return;
    }
    const u = usage.get(id);
    try {
      const res = await ctx.op('relay.kick', { relay: id, person: p });
      const kid = res.result?.kick;
      if (u && kid) {
        // Shown at once; the next usage brings the server's own.
        u.kicks = [...(u.kicks || []), { id: kid, person: p, at: new Date().toISOString() }];
        u.connected = (u.connected || []).filter((c) => c.person !== p);
        if (u.load) u.load.people = Math.max(0, u.load.people - 1);
      }
      letBack.get(id)?.delete(p);
      if (openKey === id) openMine(id);
      relabel();
      toastKick = kid ?? null;
      app.toast(`已不让${name}用${m.r.name}中转站`, {
        parent: cv,
        x: 330,
        undo: () => kid && void unkick(id, kid, p),
        gone: () => {
          if (toastKick === kid) toastKick = null;
        },
      });
      void refresh();
    } catch (e) {
      app.toast(e instanceof ApiError ? e.message : '没能做成，再试一次', { parent: cv, x: 330 });
    }
  }
  async function unkick(id         , kid        , p          )                {
    try {
      await ctx.op('relay.unkick', { kick: kid });
    } catch (e) {
      app.toast(e instanceof ApiError ? e.message : '没能放回，再试一次', { parent: cv, x: 330 });
      return;
    }
    if (toastKick === kid) {
      toastKick = null;
      if (app.toastEl) app.dropToast(app.toastEl);
    }
    const u = usage.get(id);
    if (u) u.kicks = (u.kicks || []).map((x) => (x.id === kid ? { ...x, liftedAt: new Date().toISOString() } : x));
    if (!letBack.has(id)) letBack.set(id, new Set());
    letBack.get(id) .add(p);
    if (openKey === id) openMine(id);
    void refresh();
  }

  // ---- Usage: now, and every 10 seconds while the page is open --------------------------------
  let timer                            = null;
  async function refresh()                {
    try {
      const u = await api.relayUsage();
      if (gone) return;
      usage = new Map(u.relays.map((r) => [r.relay, r]));
      relabel();
      if (openKey && openKey !== 'join' && mine().some((m) => m.r.id === openKey)) openMine(openKey);
    } catch {
      // Kept as it was: the next try comes in 10 seconds.
    }
  }
  const loop = () => {
    if (gone) return;
    void refresh();
    timer = D.after(10000, loop);
  };
  app.onSheetClose = () => {
    openKey = null;
    letBack.clear();
    mark();
  };

  drawMine();
  drawPub();
  loop();

  // ---- The server's word: a new relay bounces in -------------------------------------------
  const off = ctx.onView((nv) => {
    follow(nv);
    v = nv;
    const ms = mine();
    const fresh = ms.filter((m) => !known.has(m.r.id));
    known = new Set(ms.map((m) => m.r.id));
    if (made && !waiting().some((j) => j.id === made .id)) made = null; // used (or gone)
    drawMine();
    drawPub();
    for (const m of fresh) {
      const c = $(`.rcell[data-k="${CSS.escape(m.r.id)}"] .orb`);
      // Joined: a bounce (§5 Bouncy).
      c?.animate([{ transform: 'scale(0.6)', opacity: 0 }, { transform: 'scale(1.08)', opacity: 1, offset: 0.55 }, { transform: 'none' }], { duration: 560, easing: 'cubic-bezier(0.3, 1.2, 0.5, 1)' });
      app.toast(`${m.r.name}中转站上线了`, { parent: cv, x: 330 });
      if (openKey === 'join') openMine(m.r.id);
    }
    if (fresh.length) void refresh();
    else if (openKey && openKey !== 'join') {
      if (mine().some((m) => m.r.id === openKey)) openMine(openKey);
      else if (pubs().some((r) => r.id === openKey)) openPub(openKey);
      else app.closeSheet();
    }
  });

  return () => {
    gone = true;
    off();
    timer?.cancel();
    app.o.trace = (app.o.trace || []).filter((s        ) => !traced.includes(s));
    clear();
  };
}
