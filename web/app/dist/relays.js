// The relays page (DESIGN.md §14.8, §14.12; the approved M3-15, M4-11 and
// M4-12, design/demos/web/m4-11-weights.html and m4-12-relay-lan.html): two
// wells as scene 4's console has. My relays, which I manage: each cell
// shows, as small chips, the LANs that use it; a cell dragged onto a LAN's
// row in the sidebar puts it in that LAN (relay.add), a chip dragged out of
// its cell takes it out (relay.remove), both with undo. Over the cells,
// once two or more are in LANs, the share bar says how many devices go
// first to which (relay.setWeight). The public ones, which I only see. A
// card for each, as the desktop's M4-02, with the web's two more parts:
// which LANs use it, and its share. The last cell sets up my own server:
// one line to run, after which the new relay shows up by itself.
import { frame, clear, follow, App, G, esc, spaced, nameOf } from './shell.js';
import { sharedOnly } from './model.js';
import { D } from './clock.js';
import { api, ApiError } from './api.js';
import { hold } from './ops.js';
                                     
                                         
                                                                                                                                                         
import { ShareBar, sharesOf, spread, renorm, carry, seg, wireSeg, title, said, addrOf, fmtBytes, fmtUptime, fmtAgo, busy, isBusy, limitWords, day, RELAY, RING, orb, head, status, rows, rename, GREEN, GREY } from './relaykit.js';
                                             

                                       

// Just set up: "刚上线" for ten minutes, unless someone is on it already.
const FRESH = 10 * 60 * 1000;
const TX = 330; // where its toasts go

export function relaysPage(root             , ctx     , _route       )             {
  const app = frame(root, ctx, 'relays');
  clear();
  app.setStack([]);
  app.setTitle('中转站', '直接连不上时，自动经中转站，不用选', 'ok');
  const cv              = app.canvas;
  const P = App.PEOPLE;
  const traced = ['.relays-page .rwell', '.relays-page .rcell', '.relays-page .wseg', '.relays-page .lchip', '.rpill'];
  app.o.trace = (app.o.trace || []).concat(traced);

  let v           = ctx.view();
  let usage = new Map                     ();
  // The install command shows once, when it is made: kept for this visit,
  // so that the waiting cell can show it again.
  let made                                                                        = null;
  let gone = false;

  // What I just did and the server has not said yet, shown at once: the
  // LANs a relay is in, its weight; relays being deleted and commands being
  // taken back while their undo is up.
  const lansSent = new Map                   ();
  const weightSent = new Map                 ();
  const deleting = new Set         ();
  const revoking = new Set        ();

  const mine = ()         =>
    (v.myRelays || [])
      .filter((rv) => !deleting.has(rv.id))
      .map((rv) => ({ v: rv, r: v.relays.find((x) => x.id === rv.id) }))
      .filter((m)            => !!m.r)
      .sort((a, b) => (a.v.since || '').localeCompare(b.v.since || '') || a.r.id.localeCompare(b.r.id));
  const mineOf = (id        )                   => mine().find((m) => m.r.id === id);
  // The public ones in the order the deployment's page has them (the
  // oldest first), when this view has their cards; else as the view lists.
  const pubs = ()          => {
    const since = new Map((v.operator?.relays || []).map((rv) => [rv.id, (rv.default ? '0' : '1') + (rv.since || '')]));
    return v.relays.filter((r) => r.source === 'public').sort((a, b) => (since.get(a.id) || '').localeCompare(since.get(b.id) || ''));
  };
  const lanOf = (id        )                      => v.lans.find((l) => l.id === id);
  const lansOf = (m      )           => (lansSent.get(m.r.id) ?? m.v.lans).filter((l) => !!lanOf(l));
  const weightOf = (m      )         => weightSent.get(m.r.id) ?? m.v.weight;
  const inUse = ()         => mine().filter((m) => lansOf(m).length > 0);
  const sharesNow = ()                         => sharesOf(inUse().map((m) => ({ id: m.r.id, weight: weightOf(m) })));
  const weightsNow = ()                         => Object.fromEntries(mine().map((m) => [m.r.id, weightOf(m)]));
  // The LANs a relay may go into: mine in the sidebar, not one I wait for.
  const lanChoices = ()            => v.lans.filter((l) => !sharedOnly(v, l) && !l.pending);
  const waiting = ()                  => (v.relayJoins || []).filter((j) => !j.relay && !revoking.has(j.id) && Date.parse(j.expiresAt) > Date.now());
  let known = new Set(mine().map((m) => m.r.id));

  // Who is on a relay: its connected people, and those turned away.
  const kicksOf = (id         )              => (usage.get(id)?.kicks || []).filter((k) => !k.liftedAt && k.person);
  const onIt = (id         )             => {
    const set             = [];
    for (const c of usage.get(id)?.connected || []) if (!set.includes(c.person)) set.push(c.person);
    return set;
  };
  const stateOf = (m      )                        => {
    if (!m.v.online) return 'off';
    const l = usage.get(m.r.id)?.load;
    return isBusy(l?.cpu, l?.full || m.v.full) ? 'busy' : 'on';
  };

  // Everything goes to the server one after another, in the order done (a
  // LAN's undo after the LAN).
  let chain                   = Promise.resolve();
  const send = (op        , args        )                    => {
    const p = chain.then(() => ctx.op(op, args));
    chain = p.catch(() => undefined);
    return p;
  };
  const failed = (e         , text        ) => app.toast(e instanceof ApiError ? e.message : text, { parent: cv, x: TX });

  // ---- The wells -----------------------------------------------------------------------
  const page              = App.h(
    `<div class="relays-page">` +
      `<section class="rwell mine" data-k="mine" aria-label="我的中转站"><div class="card-label"><span>我的中转站</span><span class="n-mine"></span></div><div class="wbar-slot"></div><div class="rgrid g-mine" role="list"></div></section>` +
      `<section class="rwell pub" data-k="pub" aria-label="公共中转站"><div class="card-label"><span>公共中转站</span><span class="n-pub"></span></div><div class="rgrid g-pub" role="list"></div></section>` +
      `</div>`,
  );
  cv.appendChild(page);
  const $ = (s        , r             = page) => r.querySelector(s)               ;
  const dots = (lan        ) => {
    const l = lanOf(lan);
    const ps = l ? l.members.filter((m) => m.state === 'joined').slice(0, 3).map((m) => m.person) : [];
    return `<span class="mdots">${ps.map((p) => App.dot(P[p] ? P[p].c : GREY)).join('')}</span>`;
  };
  const lanName = (lan        ) => lanOf(lan)?.name || '';
  const chip = (id        , lan        ) => `<span class="lchip" data-l="${esc(lan)}" data-k="${esc(id + '/' + lan)}">${dots(lan)}${esc(spaced(lanName(lan)))}</span>`;
  const mineCell = (m      ) => {
    const ls = lansOf(m);
    const label = `${title(m.r)}，我的服务器，${ls.length ? spaced(ls.map(lanName).join('、') + '在用') : '还没进局域网'}`;
    return (
      `<div class="rcell" data-k="${esc(m.r.id)}" role="button" tabindex="0" aria-label="${esc(label)}">${orb(stateOf(m))}<div class="rname">${esc(spaced(title(m.r)))}</div>` +
      (ls.length ? `<div class="lchips">${ls.map((l) => chip(m.r.id, l)).join('')}</div>` : `<small class="none">还没进局域网<br />拖到左边的局域网上</small>`) +
      `</div>`
    );
  };
  const pubCell = (r       ) => `<div class="rcell" data-k="${esc(r.id)}" role="button" tabindex="0" aria-label="${esc(`${r.name}，公共中转站，一直兜底`)}">${orb('on')}<div class="rname">${esc(spaced(title(r)))}</div><small>${esc(spaced('公共 · 一直兜底'))}</small></div>`;
  const addCell = () =>
    waiting().length || made
      ? `<div class="rcell add-cell" data-k="join" role="button" tabindex="0" aria-label="等待安装：在服务器上运行的那一行命令还没跑"><div class="orb add wait">${RING()}${G.ui('relayLine', 18)}</div><div>等待安装<small>命令还没跑</small></div></div>`
      : `<div class="rcell add-cell" data-k="add" role="button" tabindex="0" aria-label="用我自己的服务器：一行命令装好"><div class="orb add">${RING()}${G.ui('plus', 18)}</div><div>用我自己的服务器<small>一行命令装好</small></div></div>`;

  // ---- The share bar (M4-11) --------------------------------------------------------------
  // My relays in a LAN share the devices by their weight; a standby one
  // (weight 0) waits at the bar's end; the public ones come after all.
  const barSay = (act           , spare           )         => {
    // All of them standby: they are still tried before the public ones.
    if (!act.length) return `直接连不上时，先经${spare.map((r) => r.said).join('、')}（都只当备用）；都用不了时，自动经公共中转站。`;
    let s = act.length >= 2 ? '直接连不上时，' + act.map((r, i) => (i ? `${r.share}% 先经${r.said}` : `${r.share}% 的设备先经${r.said}`)).join('，') : `直接连不上时，设备都先经${act[0].said}`;
    if (spare.length) s += `；${spare.map((r) => r.said).join('、')}只当备用，${act.length === 1 ? act[0].said : '别的'}用不了时才用`;
    return s + `；${spare.length ? '自己的都' : '都'}用不了时，自动经公共中转站。`;
  };
  const bar = new ShareBar(app, $('.wbar-slot'), barSay, (shares) => void weigh(shares).catch(() => undefined));
  const barItems = ()            => {
    const s = sharesNow();
    return inUse().map((m) => ({ id: m.r.id, title: title(m.r), said: said(v, m.r), share: s[m.r.id] }));
  };

  // mark shows new weights at once; send them after whatever went before.
                                                        // relay, weight, what was sent before
  function mark(next                               )         {
    const out         = [];
    if (!next) return out;
    for (const [id, w] of Object.entries(next)) {
      const m = mineOf(id);
      if (!m || weightOf(m) === w) continue;
      out.push([id, w, weightSent.get(id)]);
      weightSent.set(id, w);
    }
    return out;
  }
  function unmark(ch        )       {
    for (const [id, w, was] of ch) {
      if (weightSent.get(id) !== w) continue;
      if (was == null) weightSent.delete(id);
      else weightSent.set(id, was);
    }
  }
  async function sendWeights(ch        )                {
    for (const [id, w] of ch) await send('relay.setWeight', { relay: id, weight: w });
  }
  // weigh: the bar let go, or a button in the card: no toast (it is undone
  // the way it was done, as a service's switch).
  async function weigh(next                        )                {
    const ch = mark(next);
    try {
      await sendWeights(ch);
    } catch (e) {
      unmark(ch);
      drawMine({ anim: true });
      again();
      failed(e, '没能改好，再试一次');
      throw e;
    }
  }

  // ---- Drawing ----------------------------------------------------------------------
  // A well grows and shrinks with what is in it, on a smooth curve (§5),
  // from the height it shows now; cells that move glide to their place.
  let later = false;
  function drawMine(o                                      = {})       {
    if (gone) return;
    // Something in the hand: it is drawn again once it is let go.
    if (app.handling > 0) {
      if (!later) {
        later = true;
        const wait = () => (gone ? undefined : app.handling > 0 ? D.after(120, wait) : ((later = false), drawMine({ anim: true })));
        D.after(120, wait);
      }
      return;
    }
    const well = $('.rwell.mine');
    const h0 = well.getBoundingClientRect().height;
    for (const a of well.getAnimations()) a.cancel();
    const before = new Map([...well.querySelectorAll             ('.rcell')].map((c) => [c.dataset.k, c.getBoundingClientRect()]));
    // A LAN's chip in its cell, where it is in the cell (the cell may move
    // too, on its own).
    const inCell = (ch             ) => {
      const r = ch.getBoundingClientRect(), c = (ch.closest('.rcell')               ).getBoundingClientRect();
      return { x: r.left - c.left, y: r.top - c.top };
    };
    const chips0 = new Map([...well.querySelectorAll             ('.lchips .lchip')].map((ch) => [ch.dataset.k, inCell(ch)]));
    const ms = mine();
    $('.g-mine').innerHTML = ms.map(mineCell).join('') + addCell();
    $('.n-mine').textContent = `${ms.length} 个`;
    bar.set(barItems(), !!o.anim);
    const h1 = well.getBoundingClientRect().height;
    if (h0 && Math.abs(h1 - h0) > 1 && !o.still) well.animate([{ height: h0 + 'px' }, { height: h1 + 'px' }], { duration: 360, easing: 'cubic-bezier(0.3, 0.3, 0.2, 1)' });
    bar.grow();
    if (!o.still) {
      for (const c of well.querySelectorAll             ('.rcell')) {
        const b = before.get(c.dataset.k);
        if (!b) continue;
        const a = c.getBoundingClientRect();
        const dx = b.left - a.left, dy = b.top - a.top;
        if (Math.abs(dx) + Math.abs(dy) > 0.5) c.animate([{ transform: `translate(${dx}px, ${dy}px)` }, { transform: 'none' }], { duration: 360, easing: 'cubic-bezier(0.3, 0.3, 0.2, 1)' });
      }
      // The chips left in a cell slide to their new places (one went, or came).
      for (const ch of well.querySelectorAll             ('.lchips .lchip')) {
        const b = chips0.get(ch.dataset.k);
        if (!b) continue;
        const a = inCell(ch);
        const dx = b.x - a.x, dy = b.y - a.y;
        if (Math.abs(dx) + Math.abs(dy) > 0.5) ch.animate([{ transform: `translate(${dx}px, ${dy}px)` }, { transform: 'none' }], { duration: 360, easing: 'cubic-bezier(0.3, 0.3, 0.2, 1)' });
      }
    }
    wire();
    mark2();
    expiry();
  }
  function drawPub()       {
    const ps = pubs();
    $('.g-pub').innerHTML = ps.length ? ps.map(pubCell).join('') : `<p class="say">这里没有公共中转站</p>`;
    $('.n-pub').textContent = `${ps.length} 个`;
    wire();
    mark2();
  }
  // The dots only, when usage brings how busy they are: the cells stay.
  function relabel()       {
    for (const m of mine()) {
      const dot = $(`.rcell[data-k="${CSS.escape(m.r.id)}"] .orb .on`);
      if (!dot) continue;
      const st = stateOf(m);
      dot.classList.toggle('off', st === 'off');
      dot.classList.toggle('busy', st === 'busy');
    }
  }
  // A command not run within its hour: its cell goes by itself.
  let expTimer                            = null;
  function expiry()       {
    expTimer?.cancel();
    const ends = (v.relayJoins || []).filter((j) => !j.relay && !revoking.has(j.id)).map((j) => Date.parse(j.expiresAt)).filter((t) => t > Date.now());
    if (!ends.length) return;
    expTimer = D.after(Math.min(...ends) - Date.now() + 50, () => {
      if (gone) return;
      if (made && Date.parse(made.expiresAt) <= Date.now()) {
        made = null;
        if (openKey === 'join') app.closeSheet();
        app.toast('那行命令过期了', { parent: cv, x: TX });
      }
      drawMine({ anim: true });
    });
  }

  let openKey                = null;
  function mark2()       {
    for (const c of page.querySelectorAll             ('.rcell')) c.classList.toggle('sel', c.dataset.k === openKey || (openKey === 'join' && c.dataset.k === 'add'));
  }
  const wired = (el             ) => {
    const w = el                                       ;
    if (w.__wired) return true;
    w.__wired = true;
    return false;
  };
  function wire()       {
    for (const c of page.querySelectorAll             ('.rcell')) {
      if (wired(c)) continue;
      const k = c.dataset.k          ;
      const go = () => {
        if (k === 'add' || k === 'join') return void openAdd();
        if (mineOf(k)) return openMine(k);
        if (pubs().some((r) => r.id === k)) return openPub(k);
      };
      c.addEventListener('keydown', (e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          go();
        }
      });
      if (k === 'add' || k === 'join') {
        c.addEventListener('click', go);
        continue;
      }
      const isPub = !!c.closest('.rwell.pub');
      // Held, it goes with the hand as a capsule (M4-12): onto a LAN's row,
      // that LAN uses it; a public one need not go anywhere.
      carry(
        app,
        c,
        () => {
          if (isPub) {
            const r = pubs().find((x) => x.id === k);
            return r ? { html: `<div class="orb">${RELAY(16)}</div><span>${esc(spaced(title(r)))}</span>`, away: '公共中转站一直都在，不用拖', over: () => ({ why: '公共中转站一直都在，不用拖' }) } : null;
          }
          const m = mineOf(k);
          if (!m) return null;
          return {
            html: `<div class="orb">${RELAY(16)}</div><span>${esc(spaced(title(m.r)))}</span>`,
            over: (lan) => {
              const l = lanOf(lan);
              if (!l || l.pending || sharedOnly(v, l)) return null;
              return lansOf(m).includes(lan) ? { why: `${l.name}已经在用了` } : { say: `松手，${l.name}就用上它` };
            },
            drop: (lan) => void join(k, lan),
          };
        },
        go,
        '.lchip',
      );
      // A LAN's chip pulled out of its cell: that LAN no longer uses it.
      for (const ch of c.querySelectorAll             ('.lchip')) {
        const lan = ch.dataset.l          ;
        carry(
          app,
          ch,
          () => ({ html: `${dots(lan)}<span>${esc(spaced(lanName(lan)))}</span>`, cls: 'lan', out: { el: ch, say: `松手，${lanName(lan)}就不用它了`, drop: () => void leave(k, lan, { flown: true }) } }),
          go,
        );
      }
    }
  }

  // ---- Into a LAN, out of it (M4-12) ----------------------------------------------------------
  const popIn = (el                ) =>
    el?.animate([{ transform: 'scale(0.5)', opacity: 0 }, { transform: 'scale(1.1)', opacity: 1, offset: 0.6 }, { transform: 'none' }], { duration: 480, easing: 'cubic-bezier(0.3, 1.2, 0.5, 1)' });
  const cellChip = (id        , lan        ) => page.querySelector(`.rcell[data-k="${CSS.escape(id)}"] .lchip[data-l="${CSS.escape(lan)}"]`);
  // join: the LAN uses it. Newly in use, it takes an equal share and the
  // others make room as they shared; undo puts all as it was.
  async function join(id         , lan        , o                                                        = {})                {
    const m = mineOf(id), l = lanOf(lan);
    if (!m || !l || lansOf(m).includes(lan)) return;
    const before = weightsNow();
    const lans0 = lansSent.get(id);
    let next                                = o.restore || null;
    if (!next && !lansOf(m).length) {
      const s0 = sharesNow();
      const list = inUse().map((x) => ({ id: x.r.id, share: s0[x.r.id] }));
      const act = list.filter((x) => x.share > 0);
      if (list.length) next = spread([...list, { id, share: 0 }], id, 100 / (act.length + 1));
      else if (weightOf(m) === 0) next = { [id]: 100 };
    }
    const lans = [...lansOf(m), lan];
    lansSent.set(id, lans);
    const ch = mark(next);
    drawMine({ anim: true });
    popIn(cellChip(id, lan));
    again(true);
    if (!o.quiet) app.toast(spaced(`${l.name}用上了${said(v, m.r)}`), { parent: cv, x: TX, undo: () => void leave(id, lan, { quiet: true, restore: before }) });
    try {
      await send('relay.add', { relay: id, lan });
      await sendWeights(ch);
    } catch (e) {
      if (lansSent.get(id) === lans) lans0 ? lansSent.set(id, lans0) : lansSent.delete(id);
      unmark(ch);
      drawMine({ anim: true });
      again();
      failed(e, '没能加进去，再试一次');
    }
  }
  // leave: the LAN no longer uses it; out of use, the others fill its share.
  function leave(id         , lan        , o                                                                         = {})       {
    const m = mineOf(id), l = lanOf(lan);
    if (!m || !l || !lansOf(m).includes(lan)) return;
    const before = weightsNow();
    const lans0 = lansSent.get(id);
    const rest = lansOf(m).filter((x) => x !== lan);
    let next                                = o.restore || null;
    if (!next && !rest.length) {
      const s0 = sharesNow();
      const list = inUse().filter((x) => x.r.id !== id).map((x) => ({ id: x.r.id, share: s0[x.r.id] }));
      if (list.some((x) => x.share > 0)) next = renorm(list);
    }
    const go = async () => {
      if (gone) return;
      lansSent.set(id, rest);
      const ch = mark(next);
      drawMine({ anim: true });
      again(true);
      if (!o.quiet) app.toast(spaced(`${l.name}不用${said(v, m.r)}了`), { parent: cv, x: TX, undo: () => void join(id, lan, { quiet: true, restore: before }) });
      try {
        await send('relay.remove', { relay: id, lan });
        await sendWeights(ch);
      } catch (e) {
        if (lansSent.get(id) === rest) lans0 ? lansSent.set(id, lans0) : lansSent.delete(id);
        unmark(ch);
        drawMine({ anim: true });
        again();
        failed(e, '没能拿出来，再试一次');
      }
    };
    const c = o.flown ? null : cellChip(id, lan);
    if (c) c.animate([{ transform: 'none', opacity: 1 }, { transform: 'scale(0.5)', opacity: 0 }], { duration: 220, easing: 'ease-in', fill: 'forwards' }).finished.then(go);
    else void go();
  }

  // ---- Cards -----------------------------------------------------------------------------
  const sheet = (key        , html        )              => {
    openKey = key;
    mark2();
    return app.sheet(`<div class="relays-card">${html}</div>`, key);
  };
  // again: the card on screen says it as it is now; not while a name is
  // being changed in it, or the LANs to add are open (the hand is there).
  const again = (force = false) => {
    if (!openKey || app.sheetEl.style.display === 'none') return;
    if (openKey === 'join') return;
    if (!force && app.sheetEl.querySelector('.name-input, .lan-row .pick')) return;
    if (mineOf(openKey)) openMine(openKey);
    else if (pubs().some((r) => r.id === openKey)) openPub(openKey);
    else app.closeSheet();
  };

  // Who is on the card while it is open: people keep their place when they
  // are turned away or let back (a grey chip, again in color).
  let cardFor                 = null;
  let cardWho             = [];
  let cardOff             = [];
  const kickedAt = new Map                  ();

  function openMine(id         )       {
    const m = mineOf(id);
    if (!m) return;
    if (cardFor !== id) {
      cardFor = id;
      cardWho = [];
      cardOff = [];
    }
    const u = usage.get(id);
    const load = u?.load;
    const kicked = kicksOf(id);
    for (const k of kicked) kickedAt.set(k.person            , k.at);
    // Everyone on it but me: I cannot turn myself away.
    for (const p of onIt(id)) if (p !== v.me && !cardWho.includes(p) && !cardOff.includes(p)) cardWho.push(p);
    for (const k of kicked) if (k.person !== v.me && !cardWho.includes(k.person            ) && !cardOff.includes(k.person            )) cardOff.push(k.person            );
    const isOff = (p          ) => kicked.some((k) => k.person === p);
    const fresh = !!m.v.since && Date.now() - Date.parse(m.v.since) < FRESH;
    const st = m.v.online
      ? status('在线', load ? fmtUptime(load.uptime) : fresh ? '刚上线' : '一切正常', GREEN)
      : status('连不上', `${fmtAgo(m.v.lastSeen)}，经它的连线先走公共中转站`, GREY);
    // 分到多少: once two or more are in use, and it is not the only one
    // taking a share.
    const ls = lansOf(m);
    const use = inUse();
    const s = sharesNow();
    const share = s[id] ?? 0;
    let segHTML = '';
    if (ls.length && use.length >= 2 && !bar.only(id)) {
      const others = use.filter((x) => x.r.id !== id && (s[x.r.id] ?? 0) > 0);
      segHTML = seg(share, share > 0 ? `大约 ${share}% 的设备先经它` : `只当备用：${others.length === 1 ? said(v, others[0].r) : '别的都'}用不了时才用它`);
    }
    const free = lanChoices().filter((l) => !ls.includes(l.id));
    const lanRow =
      `<div class="label">哪些局域网在用</div><div class="lan-row">` +
      ls.map((l) => `<span class="lchip" data-l="${esc(l)}">${dots(l)}${esc(spaced(lanName(l)))}<button class="x" type="button" title="不用了" aria-label="${esc(spaced(lanName(l) + '不用它了'))}">${G.ui('close', 12)}</button></span>`).join('') +
      (free.length ? `<button class="lchip add" type="button">${G.ui('plus', 13)}局域网</button>` : '') +
      `</div>` +
      (ls.length ? '' : `<div class="hint">还没进局域网，拖到左边的局域网上就用上了</div>`);
    const now =
      `<div class="label">现在</div>` +
      (m.v.online
        ? rows([
            [G.ui('people', 18), '在用', load ? (load.people ? `${load.people} 个人 · ${load.devices} 台设备` : '还没有') : onIt(id).length ? `${onIt(id).length} 个人` : '还没有'],
            [G.ui('bolt', 18), '忙不忙', busy(load?.cpu, load?.full || m.v.full)],
          ])
        : rows([
            [G.ui('people', 18), '在用', '—'],
            [G.ui('bolt', 18), '忙不忙', '—'],
          ]));
    const lines = (u?.lans || []).filter((l) => l.lan && l.bytes > 0 && lanOf(l.lan)).sort((a, b) => b.bytes - a.bytes);
    const shares = (u?.shares || []).reduce((a, l) => a + l.bytes, 0);
    const month =
      `<div class="label">这个月替你转了</div>` +
      (lines.length || shares > 0
        ? `<div class="rows">${lines.map((l) => `<div class="row">${dots(l.lan          )}<span>${esc(spaced(lanName(l.lan          )))}</span><span class="num">${esc(fmtBytes(l.bytes))}</span></div>`).join('')}` +
          (shares > 0 ? `<div class="row">${G.ui('share', 18)}<span>分享</span><span class="num">${esc(fmtBytes(shares))}</span></div>` : '') +
          `</div>`
        : `<p class="say">还没有替谁转过。</p>`);
    const pchip = (p          , date         ) => {
      const off = isOff(p);
      const at = off && date ? kickedAt.get(p) : undefined;
      return `<button class="chip ${off ? 'blocked' : ''}" type="button" data-k="${esc(p)}" aria-pressed="${off}" aria-label="${esc(spaced(nameOf(v, p)))}${off ? '，已不让用，点一下就又让他用' : '，点一下就不让他用'}">${App.ball(p, off ? 'off' : '')}${esc(spaced(nameOf(v, p)))}${at ? `<span class="gb">${esc(day(at))}</span>` : ''}</button>`;
    };
    const who = m.v.online
      ? `<div class="label">谁在用</div>` + (cardWho.length ? `<div class="people">${cardWho.map((p) => pchip(p, false)).join('')}</div><div class="hint">点一下，就不让他用这个中转站</div>` : `<p class="say">还没有人在用。</p>`)
      : '';
    const offs = cardOff.length ? `<div class="label">不让用的</div><div class="people">${cardOff.map((p) => pchip(p, true)).join('')}</div>` : '';
    const detail =
      `<p class="say">内容全程加密，你也看不到；你能看到谁在用、用了多少。</p>` +
      `<div class="card-foot"><button class="more" type="button">更多${G.ui('chevron', 14)}</button><button class="out-btn" type="button" data-act="delete">删除</button></div>` +
      `<div class="detail"><p>${esc(spaced(`地址 ${addrOf(m.r)} · 端口 ${m.r.derpPort}`))}<br />${esc(spaced(limitWords(m.r.rateLimit) + (m.v.capacity ? ` · 同时最多 ${m.v.capacity} 台设备` : '')))}</p></div>`;
    const renamable = (v.features || []).includes('rename');
    const el = sheet(id, head(orb(stateOf(m)), title(m.r), '我的服务器', renamable) + st + segHTML + lanRow + now + month + who + offs + detail);
    wireSeg(el, bar, id, () => again(true));
    for (const c of el.querySelectorAll             ('.people .chip')) c.addEventListener('click', () => void toggleKick(id, c.dataset.k            ));
    for (const x of el.querySelectorAll             ('.lan-row .lchip .x')) x.onclick = () => leave(id, (x.closest('.lchip')               ).dataset.l          );
    const add = el.querySelector             ('.lan-row .lchip.add');
    if (add)
      add.onclick = () => {
        // "+ 局域网": my LANs it is not in; a click puts it in.
        add.replaceWith(
          ...free.map((l) => {
            const b = App.h(`<button class="lchip add pick" type="button" data-l="${esc(l.id)}">${dots(l.id)}${esc(spaced(l.name))}</button>`)               ;
            b.onclick = () => void join(id, l.id);
            b.animate([{ opacity: 0, transform: 'scale(0.9)' }, { opacity: 1, transform: 'none' }], { duration: 200, easing: 'ease-out' });
            return b;
          }),
        );
      };
    const nb = el.querySelector             ('.name-edit');
    if (nb) nb.onclick = () => rename(nb, m.r.name || '', (to) => send('relay.rename', { relay: id, name: to }).catch((e) => failed(e, '没能改名，再试一次')), () => again(true));
    (el.querySelector('[data-act="delete"]')               ).onclick = () => remove(id);
  }

  function openPub(id         )       {
    const r = pubs().find((x) => x.id === id);
    if (!r) return;
    sheet(
      id,
      head(orb('on'), title(r), '公共中转站') +
        rows([[G.ui('bolt', 18), '限速', limitWords(r.rateLimit)]]) +
        `<p class="say">自动兜底，不用管。直接连不上，或者你自己的中转站用不了时，自动经它中转。内容全程加密，中转站看不到。</p>`,
    );
  }

  // ---- "用我自己的服务器": a line to run, the waiting cell (M3-15, M4-05) ----------------------
  async function openAdd()                {
    const show = () => {
      const j = made ? null : waiting()[0];
      const left = (iso        ) => Math.max(1, Math.round((Date.parse(iso) - Date.now()) / 60000));
      const top = head(`<div class="orb add wait">${RING(40)}${G.ui('relayLine', 18)}</div>`, '用我自己的服务器', '装好后，它自己出现在这里');
      let html        ;
      if (made) {
        const just = Date.now() - made.at < 60000;
        html =
          top +
          `<div class="label">在服务器上运行这一行</div><div class="cmd" tabindex="0" aria-label="安装命令">${esc(made.command)}</div>` +
          `<div class="cmd-foot"><p class="hint">${just ? '1 小时内有效，只能用一次，要先装好 Docker' : esc(spaced(`还有 ${left(made.expiresAt)} 分钟有效，只能用一次`))}</p><button class="btn primary" type="button" data-act="copy">${G.ui('copy', 15)}复制</button></div>` +
          (just ? `<p class="say">不装也能用：直接连不上时，自动经公共中转站</p>` : `<div class="card-foot"><span></span><button class="out-btn" type="button" data-act="revoke">收回这行命令</button></div>`);
      } else if (j) {
        html =
          top +
          `<p class="say">命令只显示一次。还没装的话，收回它，再要一行。</p><div class="hint">${esc(spaced(`还有 ${left(j.expiresAt)} 分钟有效，只能用一次`))}</div>` +
          `<div class="card-foot"><span></span><button class="out-btn" type="button" data-act="revoke">收回这行命令</button></div>`;
      } else html = top + `<p class="say">正在生成命令…</p>`;
      const s = sheet('join', html);
      const b = s.querySelector('[data-act="copy"]')                            ;
      b?.addEventListener('click', async () => {
        try {
          await navigator.clipboard.writeText(made .command);
          b.innerHTML = `${G.ui('check', 15)}已复制`;
        } catch {
          // No clipboard here: the line is selected for the hand to copy.
          const sel = getSelection();
          const range = document.createRange();
          range.selectNodeContents(s.querySelector('.cmd')        );
          sel?.removeAllRanges();
          sel?.addRange(range);
          b.innerHTML = '已选中，按 ⌘C';
        }
        b.animate([{ opacity: 0.4 }, { opacity: 1 }], { duration: 160, easing: 'ease-out' });
      });
      s.querySelector('[data-act="revoke"]')?.addEventListener('click', () => revokeCmd());
    };
    if (made || waiting().length) return show();
    show();
    try {
      const res = await ctx.op('relay.join', {});
      const j = res.result?.relayJoin;
      if (!j) throw new ApiError(500, 'internal', '没拿到命令');
      made = { id: j.id, command: j.command, expiresAt: j.expiresAt, at: Date.now() };
    } catch (e) {
      if (gone) return;
      if (openKey === 'join') app.closeSheet();
      failed(e, '没能生成命令，再试一次');
      return;
    }
    if (gone) return;
    drawMine({ anim: true });
    if (openKey === 'join') show();
  }
  // Taken back: the cell goes at once; the server hears of it when the
  // toast goes without undo.
  function revokeCmd()       {
    const j = made ? (v.relayJoins || []).find((x) => x.id === made .id) || { id: made.id } : waiting()[0];
    if (!j) return;
    const was = made;
    made = null;
    revoking.add(j.id);
    if (openKey === 'join') app.closeSheet();
    drawMine({ anim: true });
    hold(app, ctx, [['relay.revokeJoin', { join: j.id }]], '已收回这行命令', {
      parent: cv,
      x: TX,
      undo: () => {
        revoking.delete(j.id);
        made = was;
        drawMine({ anim: true });
      },
      failed: () => {
        revoking.delete(j.id);
        made = was;
        drawMine({ anim: true });
      },
    });
  }

  // ---- Deleting one: gone at once, the server hears of it when the toast goes -----------------
  function remove(id         )       {
    const m = mineOf(id);
    if (!m) return;
    const name = said(v, m.r);
    const s0 = sharesNow();
    const rest = inUse().filter((x) => x.r.id !== id).map((x) => ({ id: x.r.id, share: s0[x.r.id] }));
    const ch = lansOf(m).length && (s0[id] ?? 0) > 0 && rest.some((x) => x.share > 0) ? mark(renorm(rest)) : [];
    deleting.add(id);
    app.closeSheet();
    drawMine({ anim: true });
    const back = () => {
      deleting.delete(id);
      unmark(ch);
      drawMine({ anim: true });
    };
    hold(app, ctx, [['relay.delete', { relay: id }], ...ch.map(([rid, w])                   => ['relay.setWeight', { relay: rid, weight: w }])], spaced(`已删除${name}`), { parent: cv, x: TX, undo: back, failed: back });
  }

  // ---- Turning someone away, and back -------------------------------------------------------
  let toastKick                = null;
  async function toggleKick(id         , p          )                {
    const m = mineOf(id);
    if (!m) return;
    const k = kicksOf(id).find((x) => x.person === p);
    if (k) return void unkick(id, k.id, p, false);
    await kick(id, p, false);
  }
  async function kick(id         , p          , quiet         )                {
    const m = mineOf(id);
    if (!m) return;
    const u = usage.get(id);
    try {
      const res = await send('relay.kick', { relay: id, person: p });
      const kid = res.result?.kick;
      if (u && kid) {
        // Shown at once; the next usage brings the server's own.
        u.kicks = [...(u.kicks || []), { id: kid, person: p, at: new Date().toISOString() }];
        u.connected = (u.connected || []).filter((c) => c.person !== p);
        if (u.load) u.load.people = Math.max(0, u.load.people - 1);
      }
      again();
      if (!quiet && kid) {
        toastKick = kid;
        app.toast(spaced(`已不让${nameOf(v, p)}用${said(v, m.r)}`), {
          parent: cv,
          x: TX,
          undo: () => void unkick(id, kid, p, true),
          gone: () => {
            if (toastKick === kid) toastKick = null;
          },
        });
      }
      void refresh();
    } catch (e) {
      failed(e, '没能做成，再试一次');
    }
  }
  async function unkick(id         , kid        , p          , quiet         )                {
    const m = mineOf(id);
    try {
      await send('relay.unkick', { kick: kid });
    } catch (e) {
      failed(e, '没能做成，再试一次');
      return;
    }
    const u = usage.get(id);
    if (u) u.kicks = (u.kicks || []).map((x) => (x.id === kid ? { ...x, liftedAt: new Date().toISOString() } : x));
    if (toastKick === kid) toastKick = null;
    again();
    if (!quiet && m) app.toast(spaced(`又让${nameOf(v, p)}用${said(v, m.r)}了`), { parent: cv, x: TX, undo: () => void kick(id, p, true) });
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
      again();
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
    cardFor = null;
    mark2();
  };

  drawMine({ still: true });
  drawPub();
  loop();
  // ?record=<scene>: the scene's script (DESIGN §11), recorded on this page.
  if (D.recording) {
    const recs                                         = {
      weights: () => import('./rec-relays-weights.js'),
      lan: () => import('./rec-relays-lan.js'),
    };
    const which = new URLSearchParams(location.search).get('record') || '';
    if (recs[which]) recs[which]().then(() => ((window                                      ).__ready = true));
  }

  // ---- The server's word: what I did comes back; a new relay bounces in ------------------------
  const same = (a          , b          ) => a.length === b.length && a.every((x) => b.includes(x));
  const off = ctx.onView((nv) => {
    follow(nv);
    v = nv;
    for (const [id, ls] of lansSent) {
      const rv = (v.myRelays || []).find((x) => x.id === id);
      if (!rv || same(rv.lans, ls)) lansSent.delete(id);
    }
    for (const [id, w] of weightSent) {
      const rv = (v.myRelays || []).find((x) => x.id === id);
      if (!rv || rv.weight === w) weightSent.delete(id);
    }
    for (const id of deleting) if (!(v.myRelays || []).some((x) => x.id === id)) deleting.delete(id);
    const ms = mine();
    const fresh = ms.filter((m) => !known.has(m.r.id));
    known = new Set(ms.map((m) => m.r.id));
    if (made && !(v.relayJoins || []).some((j) => j.id === made .id && !j.relay)) made = null; // used (or gone)
    drawMine({ anim: true });
    drawPub();
    for (const m of fresh) {
      const c = $(`.rcell[data-k="${CSS.escape(m.r.id)}"] .orb`);
      // Joined: a bounce (§5 Bouncy).
      c?.animate([{ transform: 'scale(0.6)', opacity: 0 }, { transform: 'scale(1.08)', opacity: 1, offset: 0.55 }, { transform: 'none' }], { duration: 560, easing: 'cubic-bezier(0.3, 1.2, 0.5, 1)' });
      app.toast(spaced(`${said(v, m.r)}上线了`), { parent: cv, x: TX });
      if (openKey === 'join') openMine(m.r.id);
    }
    if (fresh.length) void refresh();
    else again();
  });

  return () => {
    gone = true;
    off();
    timer?.cancel();
    expTimer?.cancel();
    app.o.trace = (app.o.trace || []).filter((s        ) => !traced.includes(s));
    clear();
  };
}
