// 查外网地址的服务 on the public relays' page (0.1.11, the "stun" feature;
// design/plan/stun-servers-20261118.md §4): the STUN servers devices ask for
// their public address besides the relays. A second well under the public
// relays', one row a server: a dot (green: its address found; amber: the
// last lookup failed and the address from before is kept; grey: none yet,
// devices skip it), the name, and how the last lookup went. "去掉" at a
// row's end, held with undo as the page's other undoable changes (ops.ts
// hold); the last one asks again first. "加一个" opens into a field in
// place; the server looks the name up before it adds it, and says so when
// it cannot.
import { esc, spaced } from './shell.js';
import { D } from './clock.js';
import { ApiError } from './api.js';
import { hold } from './ops.js';
                                     
                                                         

// The motion tokens (web/tokens.css), read once.
const root = getComputedStyle(document.documentElement);
const tok = (n        , d        ) => parseFloat(root.getPropertyValue(n)) || d;
const SWAP_OUT = tok('--motion-swap-out', 90);
const SWAP_IN = tok('--motion-swap-in', 160);
const FADE = tok('--motion-fade', 160);
const SMOOTH = tok('--motion-smooth', 500);
const BUSY_DELAY = tok('--motion-busy-delay', 200);
const BUSY_HOLD = tok('--motion-busy-hold', 400);
const GAP = tok('--space-16', 16); // between the two wells, as the console's cards
const EASE = root.getPropertyValue('--ease-smooth').trim() || 'ease-out';

const LIMIT = 8; // the server's (control/stun.go)
const STUN_PORT = 3478;
const ARM_FOR = 3000; // "再点一下" waits this long

// The plus of "加一个", its strokes to the box's edges: its left edge on
// the label's, as the dots'.
const PLUS = `<svg class="stun-plus" viewBox="0 0 12 12" aria-hidden="true"><path d="M6 .75v10.5M.75 6h10.5" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>`;

                                  
                
              
               
             
              
              
 

const nameOf = (s              )         => (s.port === STUN_PORT ? s.hostName : s.server);

// ago says how long ago, as the console does: "6 分钟前".
function ago(iso        )         {
  // The wall clock: the page's own clock stands still while recording.
  const s = Math.max(0, Date.now() - Date.parse(iso)) / 1000;
  if (s < 60) return '刚刚';
  if (s < 3600) return `${Math.floor(s / 60)} 分钟前`;
  if (s < 86400) return `${Math.floor(s / 3600)} 小时前`;
  return `${Math.floor(s / 86400)} 天前`;
}

function day(iso        )         {
  const d = new Date(iso);
  return `${d.getMonth() + 1} 月 ${d.getDate()} 日`;
}

function itemOf(s              )       {
  const addr = s.ipv4 || s.ipv6 || '';
  let tone       = 'ok';
  let sub        ;
  if (!addr) {
    tone = 'off';
    sub = '还没查到地址，设备先不问它';
  } else if (s.lookupError) {
    tone = 'warn';
    sub = `没查到地址，先用上次的 ${addr}`;
  } else sub = s.lookedUp ? `${addr} · ${ago(s.lookedUp)}查过` : addr;
  const tip = [s.server, s.ipv4 ? `IPv4 ${s.ipv4}` : '', s.ipv6 ? `IPv6 ${s.ipv6}` : '', s.lookupError ? `上次查：${s.lookupError}` : '', s.addedAt ? `${day(s.addedAt)}加的` : '']
    .filter(Boolean)
    .join('\n');
  return { key: s.server, name: nameOf(s), tone, sub: spaced(sub), tip };
}

// swap changes an element's words one thing at a time, with the frame's
// swapText (v3-app.ts, design/components.md 换字): the old fade out, then
// the new fade in; from nothing, the new at once. Not animated, they are
// simply set (the first drawing).
const swapText = (window                                                                    ).swapText;
function swap(el             , text        , anim         )       {
  if (anim) swapText(el, text);
  else {
    for (const a of el.getAnimations()) a.cancel();
    const w = el                                                         ;
    w._want = text;
    w._swapping = false; // a swap cut off here would wait for its end forever
    el.textContent = text;
  }
}

                            
                  
                            
                  
 

// stunBlock makes the well, after above (the public relays' well) in page.
export function stunBlock(app                                                  , ctx     , page             , above             , toastAt                                    )            {
  const el = document.createElement('section');
  el.className = 'rwell stun';
  el.dataset.k = 'stun';
  el.setAttribute('aria-label', '查外网地址的服务');
  el.innerHTML =
    `<div class="card-label"><span title="${esc('设备向这些服务问自己在外网的地址，好和别的设备直接连上。它们只看得到设备的 IP 和端口。')}">查外网地址的服务</span><span class="n-stun"></span></div>` +
    `<div class="stun-list" role="list"></div>`;
  page.appendChild(el);
  const listEl = el.querySelector('.stun-list')               ;
  const countEl = el.querySelector('.n-stun')               ;

  let list                 = [];
  const hidden = new Set        (); // taken off here, the server told when the toast goes
  let gone = false;
  let editing = false;
  let busy = false;
  let err = '';
  let armed                = null;
  let armTimer                            = null;

  // ---- Where it sits: under the public relays' well; the page scrolls to its foot.
  const place = () => {
    el.style.setProperty('--stun-top', `${above.offsetTop + above.offsetHeight + GAP}px`);
    page.style.setProperty('--ops-end', `${el.offsetTop + el.offsetHeight}px`);
  };
  const ro = new ResizeObserver(place);
  ro.observe(above);
  ro.observe(el);

  // ---- The add row and what went wrong under it ------------------------------------------
  const addBox = document.createElement('div');
  addBox.className = 'stun-addbox';
  addBox.dataset.k = 'add';
  const add = document.createElement('div');
  add.className = 'stun-add';
  addBox.appendChild(add);
  let errEl                     = null;

  const full = () => list.filter((s) => !hidden.has(s.server)).length >= LIMIT;
  function idleAdd(anim         )       {
    const html = `${PLUS}<span class="stun-add-word">加一个</span>`;
    const set = () => {
      add.className = 'stun-add';
      add.innerHTML = html;
      add.setAttribute('role', 'button');
      add.tabIndex = 0;
      markFull();
    };
    if (!anim) return set();
    fadeSwap(add, set);
  }
  function markFull()       {
    if (editing) return;
    const f = full();
    add.setAttribute('aria-disabled', String(f));
    add.title = f ? '最多 8 个，先去掉一个' : '';
    add.setAttribute('aria-label', f ? '加一个：最多 8 个，先去掉一个' : '加一个查外网地址的服务');
  }
  // fadeSwap: the row's insides go out, then the new ones come in.
  function fadeSwap(box             , set            )       {
    const kids = [...box.children]                 ;
    if (!kids.length) {
      set();
      return;
    }
    let left = kids.length;
    for (const k of kids) {
      const a = k.animate([{ opacity: 1 }, { opacity: 0 }], { duration: SWAP_OUT, easing: 'ease-in', fill: 'forwards' });
      a.onfinish = () => {
        if (--left) return;
        set();
        for (const n of box.children) (n               ).animate([{ opacity: 0 }, { opacity: 1 }], { duration: SWAP_IN, easing: 'ease-out' });
      };
    }
  }
  let field                          = null;
  let go                           = null;
  function openAdd()       {
    if (editing || full()) return;
    editing = true;
    add.removeAttribute('role');
    add.removeAttribute('aria-disabled');
    add.removeAttribute('aria-label');
    add.tabIndex = -1;
    add.title = '';
    fadeSwap(add, () => {
      add.className = 'stun-add editing';
      add.innerHTML =
        `${PLUS}<input class="stun-field" type="text" placeholder="stun.example.com" spellcheck="false" autocomplete="off" autocapitalize="off" aria-label="${esc('主机名，或者主机名:端口')}" />` +
        `<button class="btn primary stun-go" type="button"><span class="w">加</span></button><button class="stun-cancel" type="button">取消</button>`;
      field = add.querySelector('.stun-field')                    ;
      go = add.querySelector('.stun-go')                     ;
      field.addEventListener('keydown', (e) => {
        if (e.key === 'Enter') {
          e.preventDefault();
          void submit();
        } else if (e.key === 'Escape') {
          e.preventDefault();
          closeAdd();
        }
      });
      field.addEventListener('input', () => {
        field?.classList.remove('bad');
        if (err) setErr('');
      });
      go.addEventListener('click', () => void submit());
      (add.querySelector('.stun-cancel')               ).addEventListener('click', () => closeAdd());
      field.focus();
    });
  }
  function closeAdd()       {
    if (!editing || busy) return;
    editing = false;
    field = null;
    go = null;
    setErr('');
    idleAdd(true);
    // Back on the row the keyboard was on.
    D.after(SWAP_OUT + SWAP_IN, () => add.isConnected && document.activeElement === document.body && add.focus({ preventScroll: true }));
  }
  // The row opens the field; a click on what is in the field's row (加,
  // 取消, the field) is theirs, and must not open it again as it closes.
  add.addEventListener('click', (e) => {
    if (editing || (e.target           ).closest('button, input')) return;
    openAdd();
  });
  add.addEventListener('keydown', (e) => {
    if (editing || (e.key !== 'Enter' && e.key !== ' ')) return;
    e.preventDefault();
    openAdd();
  });

  // setErr puts what went wrong under the field, or takes it away: the
  // room opens and the words fade in; the words fade out, then it closes.
  function setErr(text        )       {
    if (text === err) return;
    err = text;
    if (text && errEl) return swap(errEl, text, true);
    if (text) {
      const e = document.createElement('p');
      e.className = 'stun-err';
      e.setAttribute('role', 'alert');
      e.textContent = text;
      errEl = e;
      void relayout(() => addBox.appendChild(e), [e]);
      return;
    }
    const e = errEl;
    errEl = null;
    if (!e) return;
    e.animate([{ opacity: 1 }, { opacity: 0 }], { duration: SWAP_OUT, easing: 'ease-in', fill: 'forwards' }).onfinish = () => void relayout(() => e.remove());
  }

  // ---- Adding -------------------------------------------------------------------------------
  async function submit()                {
    if (busy || !field || !go) return;
    const spec = field.value.trim();
    if (!spec) {
      field.focus();
      return;
    }
    busy = true;
    const btn = go;
    const word = btn.querySelector('.w')               ;
    btn.style.width = `${btn.offsetWidth}px`; // the ring comes in its place: the button keeps its size
    let ring                     = null;
    let since = 0;
    const show = D.after(BUSY_DELAY, () => {
      since = D.now();
      word.animate([{ opacity: 1 }, { opacity: 0 }], { duration: SWAP_OUT, easing: 'ease-in', fill: 'forwards' });
      ring = document.createElement('span');
      ring.className = 'spin';
      btn.appendChild(ring);
      ring.animate([{ opacity: 0 }, { opacity: 1 }], { duration: SWAP_IN, delay: SWAP_OUT, easing: 'ease-out', fill: 'backwards' });
    });
    let failed = '';
    let version = 0;
    try {
      version = (await ctx.op('stun.add', { stun: spec })).version;
    } catch (e) {
      failed = e instanceof ApiError ? e.message : '没能加上，再试一次';
    }
    show.cancel();
    if (since) await D.wait(Math.max(0, since + BUSY_HOLD - D.now()));
    if (gone) return;
    const done = () => {
      busy = false;
      if (ring) {
        const r = ring;
        r.animate([{ opacity: 1 }, { opacity: 0 }], { duration: SWAP_OUT, easing: 'ease-in', fill: 'forwards' }).onfinish = () => r.remove();
        word.animate([{ opacity: 0 }, { opacity: 1 }], { duration: SWAP_IN, delay: SWAP_OUT, easing: 'ease-out', fill: 'forwards' });
      }
      btn.style.width = '';
    };
    if (failed) {
      done();
      field?.classList.add('bad');
      setErr(spaced(failed));
      field?.focus();
      field?.select();
      return;
    }
    // The new row comes with the view of its version; then the field goes.
    await untilVersion(version);
    if (gone) return;
    busy = false;
    btn.style.width = '';
    D.after(SMOOTH * 0.6, () => !gone && closeAdd());
  }
  const untilVersion = (v        )                =>
    new Promise((res) => {
      if ((ctx.view().version || 0) >= v) return res();
      const off = ctx.onView((nv) => {
        if ((nv.version || 0) >= v) {
          off();
          res();
        }
      });
      D.after(5000, () => (off(), res()));
    });

  // ---- Removing -----------------------------------------------------------------------------
  function remove(key        )       {
    const shown = list.filter((s) => !hidden.has(s.server));
    const s = shown.find((x) => x.server === key);
    if (!s) return;
    // The last one asks again: with none, devices ask the relays only.
    if (shown.length === 1 && armed !== key) return arm(key);
    // It goes as it asked: the red words fade out with it, not back first.
    armTimer?.cancel();
    armTimer = null;
    if (armed === key) armed = null;
    else disarm();
    hidden.add(key);
    draw(true);
    const back = () => {
      hidden.delete(key);
      if (!gone) draw(true);
    };
    hold(app, ctx, [['stun.remove', { stun: key }]], spaced(`去掉了${nameOf(s)}`), { parent: toastAt.parent, x: toastAt.x, undo: back, failed: back, sent: () => hidden.delete(key) });
  }
  function arm(key        )       {
    armed = key;
    const row = listEl.querySelector             (`.stun-row[data-k="${CSS.escape(key)}"]`);
    if (!row) return;
    const b = row.querySelector('.stun-rm')               ;
    b.classList.add('arm'); // stays shown while it waits
    swap(b, '再点一下，去掉', true);
    const sub = row.querySelector('.stun-sub')               ;
    swap(sub, '去掉以后，设备只问中转站', true);
    // Red with the new words, not on the old ones fading out.
    D.after(SWAP_OUT, () => armed === key && (b.classList.add('red'), sub.classList.add('danger')));
    armTimer?.cancel();
    armTimer = D.after(ARM_FOR, () => disarm());
  }
  function disarm()       {
    armTimer?.cancel();
    armTimer = null;
    if (!armed) return;
    const key = armed;
    armed = null;
    const row = listEl.querySelector             (`.stun-row[data-k="${CSS.escape(key)}"]`);
    const s = list.find((x) => x.server === key);
    if (!row || !s) return;
    const b = row.querySelector('.stun-rm')               ;
    b.classList.remove('arm');
    swap(b, '去掉', true);
    const sub = row.querySelector('.stun-sub')               ;
    swap(sub, itemOf(s).sub, true);
    D.after(SWAP_OUT, () => armed !== key && (b.classList.remove('red'), sub.classList.remove('danger')));
  }

  // ---- Drawing --------------------------------------------------------------------------------
  const rowHTML = (it      ) =>
    `<span class="cons-dot ${it.tone}"></span><div class="stun-name" title="${esc(it.tip)}">${esc(it.name)}</div><div class="stun-sub">${esc(it.sub)}</div>` +
    `<button class="stun-rm" type="button" aria-label="${esc(`去掉 ${it.name}`)}">去掉</button>`;
  function makeRow(it      )              {
    const r = document.createElement('div');
    r.className = 'stun-row';
    r.dataset.k = it.key;
    r.setAttribute('role', 'listitem');
    r.innerHTML = rowHTML(it);
    (r.querySelector('.stun-rm')               ).addEventListener('click', () => remove(it.key));
    return r;
  }
  function freshen(r             , it      , anim         )       {
    if (armed === it.key) return;
    const dot = r.querySelector('.cons-dot')               ;
    dot.className = `cons-dot ${it.tone}`;
    const name = r.querySelector('.stun-name')               ;
    name.title = it.tip;
    swap(name, it.name, anim);
    swap(r.querySelector('.stun-sub')               , it.sub, anim);
  }

  // relayout runs a change to the well's insides with the well's height
  // following and what stays sliding to its new place (Smooth); what comes
  // in fades in as the room opens.
  function relayout(change            , enter                = [], anim = true, late = 0.45)                {
    const kids = () => [...listEl.children]                 ;
    if (!anim) {
      change();
      place();
      return Promise.resolve();
    }
    const h0 = el.getBoundingClientRect().height;
    const before = new Map(kids().map((k) => [k, k.getBoundingClientRect()]));
    for (const a of el.getAnimations()) if ((a                                   ).__lay) a.cancel();
    for (const k of kids()) for (const a of k.getAnimations()) if ((a                                   ).__lay) a.cancel();
    change();
    const h1 = el.getBoundingClientRect().height;
    const lay = (a           ) => (((a                                   ).__lay = true), a);
    let last                   = null;
    if (Math.abs(h1 - h0) > 0.5) last = lay(el.animate([{ height: `${h0}px` }, { height: `${h1}px` }], { duration: SMOOTH, easing: EASE }));
    for (const k of kids()) {
      const b = before.get(k);
      if (!b) continue;
      const a = k.getBoundingClientRect();
      const dy = b.top - a.top;
      if (Math.abs(dy) > 0.5) last = lay(k.animate([{ transform: `translateY(${dy}px)` }, { transform: 'none' }], { duration: SMOOTH, easing: EASE }));
      // A box that grows or shrinks inside (the add row and the words
      // under it) follows the well, not at once.
      if (Math.abs(a.height - b.height) > 0.5) last = lay(k.animate([{ height: `${b.height}px` }, { height: `${a.height}px` }], { duration: SMOOTH, easing: EASE }));
    }
    // What comes in waits until what slides past its place has nearly
    // arrived (Smooth is most of the way by then): nothing shows over it.
    for (const k of enter) k.animate([{ opacity: 0 }, { opacity: 1 }], { duration: FADE, delay: SMOOTH * late, easing: 'ease-out', fill: 'backwards' });
    place();
    return last ? last.finished.then(() => undefined, () => undefined) : Promise.resolve();
  }

  let first = true;
  let gen = 0;
  function draw(anim         )       {
    if (gone) return;
    anim = anim && !first;
    first = false;
    const my = ++gen;
    const items = list.filter((s) => !hidden.has(s.server)).map(itemOf);
    swap(countEl, items.length ? `${items.length} 个` : '', anim); // none: the sentence says so
    const rows = new Map(([...listEl.querySelectorAll('.stun-row')]                 ).filter((r) => !r.classList.contains('leaving')).map((r) => [r.dataset.k          , r]));
    const leaving = [...rows].filter(([k]) => !items.some((it) => it.key === k)).map(([, r]) => r);
    const empty = listEl.querySelector('.stun-empty:not(.leaving)')                      ;
    const emptyGoes = !!empty && items.length > 0;
    const step2 = () => {
      if (gone || my !== gen) return; // a newer draw does it
      const enter                = [];
      void relayout(
        () => {
          for (const r of listEl.querySelectorAll('.leaving')) r.remove();
          for (const r of leaving) r.remove();
          if (emptyGoes) empty?.remove();
          let prev                     = null;
          for (const it of items) {
            let r = rows.get(it.key);
            if (r && r.isConnected) freshen(r, it, anim);
            else {
              r = makeRow(it);
              enter.push(r);
            }
            const want                 = prev ? prev.nextElementSibling : listEl.firstElementChild;
            if (want !== r) listEl.insertBefore(r, want);
            prev = r;
          }
          if (!items.length && !listEl.querySelector('.stun-empty')) {
            const e = document.createElement('div');
            e.className = 'stun-empty';
            e.textContent = '没有查外网地址的服务，设备只问中转站';
            listEl.insertBefore(e, listEl.firstChild);
            enter.push(e);
          }
          if (addBox.parentNode !== listEl || addBox.nextSibling) listEl.appendChild(addBox);
          markFull();
        },
        enter,
        anim,
        // The sentence for none comes where nothing slides past: sooner.
        items.length ? 0.45 : 0.2,
      );
    };
    const out = [...leaving, ...(emptyGoes && empty ? [empty] : [])];
    // Rows still fading out from a draw before this one finish first: a
    // newer view must not cut them off.
    const fading = [...listEl.querySelectorAll             ('.leaving')];
    if (!anim || !out.length && !fading.length) return step2();
    // One thing at a time: what goes fades out first, then the rest close up.
    for (const r of out) {
      r.classList.add('leaving');
      r.animate([{ opacity: 1 }, { opacity: 0 }], { duration: FADE, easing: 'ease-in', fill: 'forwards' });
    }
    const ends = [...fading, ...out].flatMap((r) => r.getAnimations().map((a) => a.finished.then(() => undefined, () => undefined)));
    void Promise.all(ends).then(step2);
  }

  idleAdd(false);
  const update = (v          ) => {
    list = v.operator?.stun || [];
    for (const k of [...hidden]) if (!list.some((s) => s.server === k)) hidden.delete(k);
    if (armed && !list.some((s) => s.server === armed)) disarm();
    draw(true);
  };
  update(ctx.view());
  // The minutes in "6 分钟前查过" move on.
  const tick = () => {
    if (gone) return;
    draw(true);
    timer = D.after(60000, tick);
  };
  let timer = D.after(60000, tick);
  return {
    el,
    update,
    destroy() {
      gone = true;
      ro.disconnect();
      timer.cancel();
      armTimer?.cancel();
    },
  };
}
