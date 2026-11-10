// What the relays page (relays.ts) and the deployment's page (operate.ts)
// share (DESIGN.md §14.12, the approved M4-11 to M4-13; design/demos/web/
// webrelays.js is the design's drawing of it): the words for a relay, the
// share bar that says how many devices go first to which relay (one bar
// for all, percentages, a standby one dashed at its end), its three
// buttons in a card ("多用 / 少用 / 只当备用"), a capsule that goes with the
// hand onto a LAN's row, and a name changed in place.
import { App, G, esc, spaced, sideRowAt } from './shell.js';
import { D } from './clock.js';
                                                  

// eslint-disable-next-line @typescript-eslint/no-explicit-any
               
                                   

// ---- Words ---------------------------------------------------------------------------

export function fmtBytes(b        )         {
  if (b < 1e6) return `${Math.max(0, Math.round(b / 1e3))} KB`;
  if (b < 1e9) return `${Math.round(b / 1e6)} MB`;
  if (b < 1e12) return `${(b / 1e9).toFixed(b < 1e10 ? 1 : 0)} GB`;
  return `${(b / 1e12).toFixed(1)} TB`;
}
export const fmtRate = (bps        )         => fmtBytes(bps) + '/s';
export function fmtUptime(s        )         {
  if (s < 3600) return '刚上线';
  if (s < 86400) return `已经运行 ${Math.floor(s / 3600)} 小时`;
  return `已经运行 ${Math.floor(s / 86400)} 天`;
}
// How long a relay that is not up has been quiet (§6.12 M4-02: "刚停",
// "3 分钟没报到").
export function fmtAgo(iso         )         {
  if (!iso) return '有一阵没报到';
  const m = Math.round((Date.now() - Date.parse(iso)) / 60000);
  if (m < 1) return '刚停';
  if (m < 60) return `${m} 分钟没报到`;
  if (m < 1440) return `${Math.round(m / 60)} 小时没报到`;
  return `${Math.round(m / 1440)} 天没报到`;
}
// busy is how busy, in words: full when it takes no more devices.
export function busy(cpu         , full          )         {
  if (full) return '满了，新连上来的先走别的';
  if (cpu == null) return '—';
  if (cpu < 0.5) return '不忙';
  if (cpu < 0.8) return '有点忙';
  return '很忙';
}
export const isBusy = (cpu         , full          )          => !!full || (cpu ?? 0) >= 0.5;
export const limitWords = (bps         )         => (bps ? `每条连接 ${fmtRate(bps)}` : '不限速');
// "今天", "昨天", "11月6日" (§6.12 不让用的).
export function day(iso        )         {
  const d = new Date(iso), now = new Date();
  const same = (a      , b      ) => a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();
  if (same(d, now)) return '今天';
  if (same(d, new Date(now.getTime() - 86400e3))) return '昨天';
  return `${d.getMonth() + 1}月${d.getDate()}日`;
}

export const addrOf = (r       )         => r.ipv4 || r.hostName || '';
// title is a relay on its ball and its card's head: its name, or its
// address while it has none (§6.12).
export const title = (r       )         => r.name || addrOf(r);
// said is a relay in a sentence: its name, or whose server it is when it
// has only an address ("我的服务器", "阿杰的服务器").
export function said(v          , r       )         {
  if (r.name && r.name !== addrOf(r)) return r.name;
  if (r.source === 'public') return '公共中转站';
  if (!r.owner || r.owner === v.me) return '我的服务器';
  const p = v.people.find((x) => x.id === r.owner);
  return `${p ? p.displayName || p.name : '别人'}的服务器`;
}

// ---- Shares --------------------------------------------------------------------------

                                           
// sharesOf turns weights into percentages over the ones not standby
// (summing to 100; the last takes what rounding leaves).
export function sharesOf(weights                                  )                         {
  const act = weights.filter((w) => w.weight > 0);
  const sum = act.reduce((a, w) => a + w.weight, 0);
  const out                         = {};
  for (const w of weights) out[w.id] = 0;
  let given = 0;
  act.forEach((w, i) => {
    const s = i === act.length - 1 ? 100 - given : Math.round((w.weight / sum) * 100);
    out[w.id] = s;
    given += s;
  });
  return out;
}
// spread gives id the goal (5% steps); the others not standby share the
// rest as they shared before.
export function spread(list         , id        , goal        )                         {
  goal = Math.round(goal / 5) * 5;
  const others = list.filter((x) => x.id !== id && x.share > 0);
  const out                         = {};
  for (const x of list) out[x.id] = x.share;
  out[id] = goal;
  const rest = 100 - goal;
  const had = others.reduce((a, x) => a + x.share, 0) || 1;
  let given = 0;
  others.forEach((x, i) => {
    const v = i === others.length - 1 ? rest - given : Math.round(((x.share / had) * rest) / 5) * 5;
    out[x.id] = v;
    given += v;
  });
  return out;
}
// renorm: the ones not standby fill all of it again, as they shared (one
// left the bar).
export function renorm(list         )                         {
  const act = list.filter((x) => x.share > 0);
  const had = act.reduce((a, x) => a + x.share, 0) || 1;
  const out                         = {};
  for (const x of list) out[x.id] = x.share;
  let given = 0;
  act.forEach((x, i) => {
    const v = i === act.length - 1 ? 100 - given : Math.round(((x.share / had) * 100) / 5) * 5;
    out[x.id] = v;
    given += v;
  });
  return out;
}

// ---- The share bar (M4-11, M4-13) ----------------------------------------------------------

                          
             
                
               
                                           
                                  
                                                                
                 
                                                    
                                                                    
 

const GAP = 6;
const mctx = document.createElement('canvas').getContext('2d')                            ;
function measure(t        , weight = 400)         {
  mctx.font = `${weight} 12px -apple-system, "PingFang SC", "Helvetica Neue", sans-serif`;
  return mctx.measureText(t).width;
}
const sel = (kind        , id        ) => `.wseg.${kind}[data-r="${CSS.escape(id)}"]`;

// ShareBar is the bar in slot: a segment for each relay that takes a
// share, as wide as its share, "名字 百分比" when it fits (the name over it
// on the pointer when not); a dashed pill at the end for each standby one;
// a handle between segments that trades their shares, 5% a step, none
// under 10%; and a line under it that says it in words. changed gets the
// new shares once a change is let go (the page sends them as weights).
export class ShareBar {
  slot             ;
  app     ;
  items = new Map                 ();
  order           = [];
  bar                     = null;
  say                                              ;
  changed                                          ;
  busy = false; // a change in the hand or on its way: the server's word waits
  gen = 0; // the tween that moves things now (an older one stands still)
  growing = false; // just come: its room opens on grow()
  constructor(app     , slot             , say                 , changed                     ) {
    this.app = app;
    this.slot = slot;
    this.say = say;
    this.changed = changed;
  }
  list()            {
    return this.order.map((id) => this.items.get(id)           );
  }
  live()            {
    return this.list().filter((r) => !r.gone);
  }
  item(id        )                      {
    const r = this.items.get(id);
    return r && !r.gone ? r : undefined;
  }
  // only says whether id is the one relay not standby (it can give no
  // share away, and must not go standby).
  only(id        )          {
    const act = this.live().filter((r) => r.share > 0);
    return act.length === 1 && act[0].id === id;
  }

  // set takes what the server says (in order, with shares); anim: what
  // changed moves there smoothly, one coming in grows from nothing, one
  // going shrinks away, the bar opens its room or gives it back.
  set(next           , anim         )       {
    const had = !!this.bar;
    const smooth = anim && had && next.length >= 2;
    const ids = new Set(next.map((n) => n.id));
    const moved                         = {};
    for (const n of next) {
      const it = this.items.get(n.id);
      if (!it) {
        this.items.set(n.id, { ...n, shareT: smooth ? 0 : undefined, spareT: smooth ? 0 : n.share <= 0 ? 1 : 0 });
        if (smooth) moved[n.id] = n.share;
        continue;
      }
      it.title = n.title;
      it.said = n.said;
      if (it.gone) {
        it.gone = false;
        moved[n.id] = n.share;
      } else if (!this.busy && !it.held && it.share !== n.share) moved[n.id] = n.share;
    }
    const old = this.order;
    const order = next.map((n) => n.id);
    for (const id of old) {
      if (ids.has(id)) continue;
      const it = this.items.get(id)           ;
      if (!smooth) {
        this.items.delete(id);
        continue;
      }
      // It shrinks where it was.
      it.gone = true;
      moved[id] = 0;
      const before = old.slice(0, old.indexOf(id)).reverse().find((x) => order.includes(x));
      order.splice(before ? order.indexOf(before) + 1 : 0, 0, id);
    }
    this.order = order;
    this.draw(anim && old.length > 0);
    if (Object.keys(moved).length) void this.shares(moved, smooth ? 500 : 1);
  }

          draw(anim         )       {
    const shown = this.live().length >= 2;
    if (!shown) {
      const el = this.slot.firstChild                      ;
      this.bar = null;
      if (!el) return;
      if (anim) {
        const h0 = this.slot.offsetHeight;
        this.slot.style.overflow = 'hidden';
        el.animate([{ opacity: 1 }, { opacity: 0 }], { duration: 200, easing: 'ease-in', fill: 'forwards' });
        this.slot.animate([{ height: h0 + 'px' }, { height: '0px' }], { duration: 360, easing: 'cubic-bezier(0.3, 0.3, 0.2, 1)', fill: 'forwards' }).finished.then((a) => {
          if (!this.bar) el.remove();
          a.cancel();
          this.slot.style.overflow = '';
        });
      } else el.remove();
      return;
    }
    let grow = false;
    if (!this.bar) {
      for (const a of this.slot.getAnimations()) a.cancel();
      this.slot.style.overflow = '';
      this.slot.replaceChildren(App.h(`<div class="wbar-wrap"><div class="wbar" role="group" aria-label="分到多少"></div><p class="wsay"></p></div>`));
      this.bar = this.slot.querySelector('.wbar')               ;
      grow = anim;
    }
    const bar = this.bar;
    for (const id of this.order) {
      if (!bar.querySelector(sel('act', id))) bar.appendChild(App.h(`<div class="wseg act" data-r="${esc(id)}" data-k="seg-${esc(id)}"><span class="nm"></span><span class="pct"></span></div>`));
      if (!bar.querySelector(sel('spare', id))) bar.appendChild(App.h(`<div class="wseg spare" data-r="${esc(id)}" data-k="spare-${esc(id)}"><span class="nm"></span></div>`));
    }
    for (const el of [...bar.querySelectorAll('.wseg')]                 ) if (!this.order.includes(el.dataset.r          )) el.remove();
    if (!bar.querySelector('.wtip')) bar.appendChild(App.h(`<div class="wtip" data-k="wtip" aria-hidden="true"></div>`));
    for (const el of bar.querySelectorAll('.wseg.act')                                                 ) {
      if (el.__tip) continue;
      el.__tip = true;
      el.addEventListener('mouseenter', () => {
        if (!el.classList.contains('narrow')) return;
        const t = bar.querySelector('.wtip')               ;
        t.textContent = el.dataset.name || '';
        t.style.left = parseFloat(el.style.left) + parseFloat(el.style.width) / 2 + 'px';
        t.classList.add('on');
      });
      el.addEventListener('mouseleave', () => bar.querySelector('.wtip')?.classList.remove('on'));
    }
    this.layout();
    this.growing = grow;
  }
  // grow opens the room of a bar that has just come (what is under it
  // slides down), then shows it: called once the page has measured where
  // things go, so that its well grows the same way at the same time.
  grow()       {
    if (!this.growing || !this.bar) return;
    this.growing = false;
    const h = this.slot.offsetHeight;
    this.slot.style.overflow = 'hidden';
    this.slot.animate([{ height: '0px' }, { height: h + 'px' }], { duration: 360, easing: 'cubic-bezier(0.3, 0.3, 0.2, 1)' }).finished.then(() => (this.slot.style.overflow = ''));
    (this.slot.firstChild               ).animate([{ opacity: 0 }, { opacity: 1 }], { duration: 300, delay: 120, easing: 'ease-out', fill: 'backwards' });
  }

  layout()       {
    const bar = this.bar;
    if (!bar || !bar.isConnected) return;
    const use = this.list();
    const width = bar.clientWidth;
    let right = width;
    for (const r of use.slice().reverse()) {
      const t = r.spareT ?? (r.share <= 0 && !r.gone ? 1 : 0);
      const el = bar.querySelector(sel('spare', r.id))               ;
      if (!el) continue;
      const text = spaced(`${r.title} · 备用`);
      const w = Math.ceil(measure(text)) + 26;
      if (t <= 0.001) {
        el.style.display = 'none';
        continue;
      }
      el.style.display = '';
      right -= (w + GAP) * t;
      el.style.left = right + GAP * t + 'px';
      el.style.width = w + 'px';
      el.style.opacity = String(t);
      (el.querySelector('.nm')               ).textContent = text;
      el.setAttribute('aria-label', `${r.title}，只当备用`);
    }
    const area = right;
    const act = use.filter((r) => (r.shareT ?? r.share) > 0.01);
    const total = act.reduce((a, r) => a + (r.shareT ?? r.share), 0) || 1;
    let x = 0;
    for (const r of use) {
      const el = bar.querySelector(sel('act', r.id))               ;
      if (!el) continue;
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
      el.style.opacity = String(Math.min(1, s / 8));
      // Held, the number shows where it will land; moving on its own, it
      // rolls with the width.
      const pct = r.held ? r.share : Math.round(s);
      const segW = w - gl - gr;
      const name = spaced(r.title);
      const both = measure(name, 500) + 6 + measure(`${pct}%`) + 20 <= segW;
      (el.querySelector('.nm')               ).textContent = both ? name : '';
      (el.querySelector('.pct')               ).textContent = both || measure(`${pct}%`) + 12 <= segW ? `${pct}%` : '';
      el.classList.toggle('narrow', !both);
      el.dataset.name = name;
      el.setAttribute('aria-label', `${r.title} ${pct}%`);
      x += w;
    }
    const sayEl = bar.parentNode?.querySelector('.wsay')                      ;
    const live = use.filter((r) => !r.gone);
    if (sayEl) {
      const s = spaced(this.say(live.filter((r) => r.share > 0), live.filter((r) => r.share <= 0)));
      if (sayEl.textContent !== s) sayEl.textContent = s;
    }
    this.placeHandles();
  }

          placeHandles()       {
    const bar = this.bar               ;
    const act = this.list().filter((r) => (r.shareT ?? r.share) > 0.01);
    const hs = [...bar.querySelectorAll('.whandle')]                 ;
    for (let i = 0; i < act.length - 1; i++) {
      let h = hs[i];
      if (!h) {
        h = App.h(`<div class="whandle" data-k="handle-${i}" role="separator" aria-label="拖动调整分到多少"><i></i></div>`)               ;
        bar.appendChild(h);
        hs.push(h);
        this.wire(h);
      }
      const el = bar.querySelector(sel('act', act[i].id))               ;
      h.style.left = parseFloat(el.style.left) + parseFloat(el.style.width) + GAP / 2 + 'px';
      if (!h.classList.contains('held')) {
        h.dataset.a = act[i].id;
        h.dataset.b = act[i + 1].id;
      }
      h.style.display = act[i].gone || act[i + 1].gone ? 'none' : '';
    }
    for (let i = Math.max(0, act.length - 1); i < hs.length; i++) if (!hs[i].classList.contains('held')) hs[i].style.display = 'none';
  }

  // A handle in the hand: the two segments beside it trade, following the
  // hand (no jumps); the share lands on 5% steps, none under 10%. Let go,
  // it is sent; no toast (dragging back is the undo, as a service's switch).
          wire(h             )       {
    const bar = this.bar               ;
    let a         , b         , sum = 0, left = 0, span = 0;
    this.app.drag(h, {
      ref: bar,
      start: () => {
        a = this.items.get(h.dataset.a          )           ;
        b = this.items.get(h.dataset.b          )           ;
        sum = a.share + b.share;
        const ea = bar.querySelector(sel('act', a.id))               ;
        const eb = bar.querySelector(sel('act', b.id))               ;
        left = parseFloat(ea.style.left);
        span = parseFloat(eb.style.left) + parseFloat(eb.style.width) - left;
        h.classList.add('held');
        this.busy = true;
        this.gen++;
      },
      move: (p    ) => {
        const f = D.clamp((p.x - left) / span, 0, 1);
        const raw = D.clamp(f * sum, 10, sum - 10);
        a.held = b.held = true;
        a.shareT = raw;
        b.shareT = sum - raw;
        a.share = D.clamp(Math.round(raw / 5) * 5, 10, sum - 10);
        b.share = sum - a.share;
        this.layout();
      },
      end: async () => {
        h.classList.remove('held');
        a.held = b.held = false;
        await this.shares({ [a.id]: a.share, [b.id]: b.share }, 240);
        this.commit();
      },
    });
  }

  // shares moves to new shares smoothly (Snappy's 500 ms, eased in and
  // out): the percentages roll, a standby pill comes in or goes, one
  // leaving shrinks to nothing and is gone.
  shares(next                        , dur = 500)                {
    const gen = ++this.gen;
    const use = this.list();
    const from = new Map(use.map((r) => [r.id, { s: r.shareT ?? r.share, t: r.spareT ?? (r.share <= 0 && !r.gone ? 1 : 0) }]));
    const goal = new Map                ();
    for (const r of use) {
      if (next[r.id] != null) r.share = next[r.id];
      goal.set(r.id, r.share <= 0 && !r.gone ? 1 : 0);
    }
    return App.tween(dur, (k        ) => {
      if (gen !== this.gen) return;
      for (const r of use) {
        const f = from.get(r.id) ;
        r.shareT = f.s + (r.share - f.s) * k;
        r.spareT = f.t + (goal.get(r.id)  - f.t) * k;
      }
      this.layout();
    }, D.Ease.inOut).then(() => {
      if (gen !== this.gen) return;
      for (const r of use) {
        delete r.shareT;
        if (r.gone) {
          this.items.delete(r.id);
          this.order = this.order.filter((x) => x !== r.id);
          for (const el of this.bar?.querySelectorAll(`.wseg[data-r="${CSS.escape(r.id)}"]`) || []) el.remove();
        }
      }
      this.layout();
    });
  }
  // commit hands the shares over (the page sends them as weights).
  commit()       {
    const out                         = {};
    for (const r of this.live()) out[r.id] = r.share;
    this.busy = false;
    this.changed(out);
  }

  // "多用 / 少用 / 只当备用": a share twice as big (90% at most; a standby
  // one gets back what it had), half as big (10% at least), or none; the
  // others make room as they shared.
  async more(id        )                {
    const r = this.item(id);
    if (!r) return;
    // The others all standby: it takes all of it.
    const alone = !this.live().some((x) => x.id !== id && x.share > 0);
    await this.move(spread(this.live(), id, alone ? 100 : r.share > 0 ? Math.min(90, r.share * 2) : r.prev || 20));
  }
  async less(id        )                {
    const r = this.item(id);
    if (!r || r.share <= 0 || this.only(id)) return;
    await this.move(spread(this.live(), id, Math.max(10, r.share / 2)));
  }
  async spare(id        )                {
    const r = this.item(id);
    if (!r || r.share <= 0 || this.only(id)) return;
    r.prev = r.share;
    await this.move(spread(this.live(), id, 0));
  }
          async move(next                        )                {
    this.busy = true;
    await this.shares(next);
    this.commit();
  }
}

// seg is "分到多少" in a card: the three buttons, and what it means now.
// Standby, "少用" has nothing to give; "只当备用" is pressed.
export function seg(share        , say        )         {
  return (
    `<div class="label">分到多少</div><div class="seg" role="group" aria-label="分到多少">` +
    `<button type="button" data-act="more">多用</button><button type="button" data-act="less" ${share > 0 ? '' : 'disabled'}>少用</button>` +
    `<button type="button" data-act="spare" class="${share > 0 ? '' : 'on'}" aria-pressed="${share <= 0}">只当备用</button></div>` +
    `<div class="hint">${esc(spaced(say))}</div>`
  );
}
// wireSeg: a press springs the button, the bar moves, then again(): the
// card says it as it is now.
export function wireSeg(s             , bar          , id        , again            )       {
  for (const b of s.querySelectorAll                   ('.seg button')) {
    b.onclick = async () => {
      const act = b.dataset.act;
      b.animate([{ transform: 'scale(0.96)' }, { transform: 'none' }], { duration: 180, easing: 'ease-out' });
      if (act === 'more') await bar.more(id);
      else if (act === 'less') await bar.less(id);
      else if (act === 'spare') await bar.spare(id);
      again();
    };
  }
}

// ---- A capsule that goes with the hand onto a LAN's row (M4-12) ------------------------------

                        
                                         
               
                                                                      
                                                                   
                                                            
                                                                                   
                                                           
                                                                            
                                    
                                                       
 

const flash = 'color-mix(in srgb, var(--primary) 16%, var(--bg))';
// rowFlash: the soft blue a LAN's row takes something with (as a LAN that
// comes in).
export const rowFlash = (el                )       => {
  el?.animate([{ background: flash }, { background: flash, offset: 0.3 }], { duration: 1100, easing: 'ease-out' });
};

// carry makes el draggable as a capsule (in the drag layer, canvas
// coordinates); a click is click. The element (or what is pulled out of
// it) stays where it was, faint, while the capsule is away.
export function carry(app     , el             , c                    , click            , ignore         )       {
  let g                     = null, row                                         = null, cur               = null, home     = { x: 0, y: 0 }, out = false;
  let grab = { x: 0, y: 0, t: 0 };
  const note = (t        , go         ) => {
    const n = (g               ).querySelector('.rnote')               ;
    const s = spaced(t);
    if (n.textContent !== s && t) n.textContent = s;
    n.classList.toggle('go', go);
    n.style.opacity = t ? '1' : '0';
  };
  const place = (p    , s = 1) => {
    (g               ).style.left = p.x + 'px';
    (g               ).style.top = p.y + 'px';
    (g               ).style.transform = `translate(-50%, -50%) rotate(-2deg) scale(${s})`;
  };
  // Into a row it speeds up as it shrinks away; back home it starts from
  // where the hand let it go, from rest, and settles (a long way takes a
  // little longer).
  const fly = async (to    , s1        , fade         ) => {
    const a = { x: parseFloat((g               ).style.left), y: parseFloat((g               ).style.top) };
    const dist = Math.hypot(to.x - a.x, to.y - a.y);
    await App.tween(fade ? 300 : Math.min(480, 280 + dist * 0.3), (k        ) => {
      place({ x: a.x + (to.x - a.x) * k, y: a.y + (to.y - a.y) * k }, 1 + (s1 - 1) * k);
      if (fade) (g               ).style.opacity = String(1 - 0.85 * k);
    }, fade ? D.Ease.in : D.Ease.inOut);
  };
  const center = (e         ) => {
    const r = app.rectOf(e);
    return { x: r.x + r.w / 2, y: r.y + r.h / 2 };
  };
  const done = () => {
    g?.remove();
    g = null;
    (cur?.out?.el || el).classList.remove('ghost');
    cur = null;
    document.body.classList.remove('carrying');
  };
  // A click (the hand's, or a screen reader's) opens; not the one a drag
  // ends with. One on what is pulled out of it (a chip) is that one's.
  el.addEventListener('click', (e) => {
    if ((el       ).__dragged) return;
    e.stopPropagation();
    click();
  });
  app.drag(el, {
    ignore,
    start: (p0    ) => {
      cur = c();
      if (!cur) return;
      home = center(cur.out ? cur.out.el : el.querySelector('.orb') || el);
      // It starts where the thing was and comes under the hand.
      grab = { x: p0.x - home.x, y: p0.y - home.y, t: D.now() };
      g = App.h(`<div class="rpill ${cur.cls || ''}" data-k="rpill">${cur.html}<span class="rnote"></span></div>`)               ;
      app.dragLayer.appendChild(g);
      place(home);
      g.animate([{ opacity: 0.6 }, { opacity: 1 }], { duration: 120, easing: 'ease-out' });
      (cur.out?.el || el).classList.add('ghost');
      // What the hand passes over meanwhile does not light up under it.
      document.body.classList.add('carrying');
    },
    move: (p    ) => {
      if (!g || !cur) return;
      const k = D.Ease.out(Math.max(0, 1 - (D.now() - grab.t) / 220));
      place({ x: p.x - grab.x * k, y: p.y - grab.y * k });
      if (cur.out) {
        out = !inside(app, cur.out.el.closest('.rcell')               , p);
        return note(out ? cur.out.say : '', out);
      }
      const r = sideRowAt(p);
      if (row && row.el !== r?.el) row.el.classList.remove('target');
      row = r;
      const what = r && cur.over ? cur.over(r.id) : null;
      const go = !!what && !what.why && !!what.say;
      if (r) r.el.classList.toggle('target', go);
      note(what ? what.why || what.say || '' : cur.away || '', go);
    },
    end: async (p           , cancel         ) => {
      if (!g || !cur) return;
      row?.el.classList.remove('target');
      row = null;
      note('', false);
      const it = cur;
      if (it.out) {
        if (!cancel && p && out) {
          const at = { x: parseFloat(g.style.left), y: parseFloat(g.style.top) };
          await App.tween(220, (k        ) => {
            place(at, 1 - 0.5 * k);
            (g               ).style.opacity = String(1 - k);
          }, D.Ease.in);
          g.remove();
          g = null;
          cur = null;
          document.body.classList.remove('carrying');
          it.out.drop();
          return;
        }
        await fly(center(it.out.el), 1, false);
        return done();
      }
      const r = !cancel && p ? sideRowAt(p) : null;
      const what = r && it.over ? it.over(r.id) : null;
      if (r && what && !what.why && what.say && it.drop) {
        await fly(center(r.el.querySelector('.dots') || r.el), 0.3, true);
        done();
        rowFlash(r.el);
        it.drop(r.id);
        return;
      }
      await fly(home, 1, false);
      done();
    },
  });
}
function inside(app     , el             , p    )          {
  const r = app.rectOf(el);
  return p.x >= r.x - 4 && p.x <= r.x + r.w + 4 && p.y >= r.y - 4 && p.y <= r.y + r.h + 4;
}

// ---- Drawing -----------------------------------------------------------------------------

// The orb of a relay: the desktop's white glossy ball, its dot green when
// it is up, amber when busy, grey when not up.
export const RELAY = (size = 20)         => G.device('relay', size);
export const orb = (state                       , size = 20)         => `<div class="orb">${RELAY(size)}<span class="on${state === 'on' ? '' : ' ' + state}"></span></div>`;
export const RING = (box = 44)         => {
  const c = box / 2, r = c - 1.75;
  return `<svg class="ring" viewBox="0 0 ${box} ${box}" width="${box}" height="${box}" aria-hidden="true"><circle cx="${c}" cy="${c}" r="${r}" fill="none" stroke="currentColor" stroke-width="1.5" stroke-dasharray="4 3.5"/></svg>`;
};
// A card's head: the orb, the name (a button that turns into a field when
// it may be changed), the second line, the close button.
export const head = (orbHTML        , name        , line        , renamable = false)         =>
  `<div class="top"><div class="orb-s">${orbHTML}</div><div>${renamable ? `<button class="name-edit" type="button" title="改名">${esc(spaced(name))}</button>` : `<b>${esc(spaced(name))}</b>`}<span>${esc(spaced(line))}</span></div><button class="icon-btn sheet-close" type="button" title="关闭" aria-label="关闭">${G.ui('close', 16)}</button></div>`;
export const status = (word        , small        , color        )         =>
  `<div class="status"><span class="badge" style="--c:${color}">${G.ui('online', 16)}</span><div>${esc(spaced(word))}<small>${esc(spaced(small))}</small></div></div>`;
export const rows = (rs                            )         =>
  `<div class="rows">${rs.map(([icon, a, b]) => `<div class="row">${icon}<span>${esc(spaced(a))}</span><span class="num">${esc(spaced(b))}</span></div>`).join('')}</div>`;
export const GREEN = '#22C55E', AMBER = '#F59E0B', GREY = '#a1a1aa';

// rename turns a name into a field in place (§6.9): Enter or a click away
// keeps it, Esc goes back.
export function rename(el             , name        , save                                  , redraw            )       {
  const input = App.h(`<input class="name-input" type="text" maxlength="40" aria-label="名字">`)                    ;
  input.value = name;
  el.replaceWith(input);
  input.focus();
  input.select();
  let done = false;
  const end = (keep         ) => {
    if (done) return;
    done = true;
    const to = input.value.trim();
    if (keep && to && to !== name) void save(to);
    redraw();
  };
  input.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') end(true);
    else if (e.key === 'Escape') {
      e.stopPropagation();
      end(false);
    }
  });
  input.addEventListener('blur', () => end(true));
}
