// The invite page and the login, in one card (DESIGN.md §14.5, §14.6):
// a friend's link opens here, and a newcomer's first stop is this card.
// Each step replaces the card's contents, fading them in while its height
// follows; the email is the one field to fill in, the code six boxes that
// send themselves once full.
import { PRIVACY_URL, goneNote, wireGone, fadeIn, takeFade } from './account.js';
import { api, ApiError } from './api.js';
                                                                  
import { colorIndex } from './model.js';
import tokens from './tokens.js';
import { after } from './clock.js';
import { esc, maskEmail, spaced, star } from './download.js';

                              
                                    
                                                     
                                                                                
                                                   
 

                               
                
                                            
 

const NOTE = '装上 Deneb，几个人的电脑就像在同一个家里，能直接互相用。';

function ball(p        , me                      , size         )         {
  const c = tokens.color.owners[colorIndex(me ?? '', p.id)];
  const s = size ? ` style="--c:${c};width:${size}px;height:${size}px"` : ` style="--c:${c}"`;
  return `<span class="ball"${s} aria-hidden="true">${esc(p.initial || p.displayName.slice(0, 1))}</span>`;
}

// page builds the frame: the mark top left, the card, the line about Deneb.
function page(root             )                                                            {
  const el = document.createElement('div');
  el.className = 'join-page';
  el.innerHTML =
    `<div class="brand">${star(18)}<span>Deneb</span></div>` +
    `<main class="join-card" aria-live="polite"></main>` +
    `<p class="join-note">${esc(spaced(NOTE))}</p>` +
    // The privacy notes, on GitHub (M7, DESIGN §6.15).
    `<p class="join-privacy"><a href="${PRIVACY_URL}" target="_blank" rel="noopener">隐私说明</a></p>`;
  root.replaceChildren(el);
  return { card: el.querySelector('.join-card')               , note: el.querySelector('.join-note')               , destroy: () => el.remove() };
}

// swap puts new contents in the card: they fade in, its height follows on
// a smooth curve (as the desktop's card does when its device changes).
function swap(card             , html        , cls = 'jc-in')              {
  const h0 = card.offsetHeight;
  const next = document.createElement('div');
  next.className = cls;
  next.innerHTML = html;
  card.replaceChildren(next);
  const h1 = card.offsetHeight;
  if (h0 > 0 && Math.abs(h1 - h0) > 1) card.animate([{ height: h0 + 'px' }, { height: h1 + 'px' }], { duration: 260, easing: 'cubic-bezier(0.3, 0.3, 0.2, 1)' });
  if (h0 > 0) next.animate([{ opacity: 0 }, { opacity: 1 }], { duration: 180, easing: 'ease-out' });
  return next;
}

function shake(el             )       {
  el.animate([{ transform: 'none' }, { transform: 'translateX(-6px)' }, { transform: 'translateX(6px)' }, { transform: 'translateX(-4px)' }, { transform: 'translateX(4px)' }, { transform: 'none' }], { duration: 360, easing: 'ease-out' });
}

const head = (who        , title        , sub        ) =>
  `<div class="jc-head">${who}<div><h1 class="jc-title">${esc(spaced(title))}</h1><p class="jc-sub">${esc(spaced(sub))}</p></div></div>`;

const looksLikeEmail = (s        ) => /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(s.trim());

// login runs the email and code steps in the card, then calls done.
function loginSteps(card             , who        , title        , done                                       )       {
  let email = '';
  const askEmail = (err = '') => {
    const c = swap(
      card,
      head(who, title, '以后在电脑上登录 Deneb，也用这个邮箱') +
        `<form class="jc-form" novalidate>` +
        `<label class="field"><span class="sr">你的邮箱</span><input name="email" type="email" inputmode="email" autocomplete="email" placeholder="你的邮箱" aria-label="你的邮箱" value="${esc(email)}" required /></label>` +
        `<p class="jc-error" role="alert">${esc(err)}</p>` +
        `<div class="jc-foot"><button class="btn primary" type="submit" disabled>发验证码</button></div></form>`,
    );
    const form = c.querySelector('form')                   ;
    const input = form.querySelector('input')                    ;
    const btn = form.querySelector('button')                     ;
    const sync = () => (btn.disabled = !looksLikeEmail(input.value));
    input.addEventListener('input', sync);
    sync();
    input.focus();
    form.addEventListener('submit', async (e) => {
      e.preventDefault();
      if (!looksLikeEmail(input.value)) return shake(input.parentElement               );
      email = input.value.trim();
      btn.disabled = true;
      try {
        const r = await api.loginEmail(email);
        if (r.step === 'done' && r.person) return done(r.person, email);
        askCode();
      } catch (err) {
        btn.disabled = false;
        const p = form.querySelector('.jc-error')               ;
        p.textContent = err instanceof ApiError ? err.message : '没能发出验证码';
        shake(input.parentElement               );
      }
    });
  };

  const askCode = () => {
    const boxes = [0, 1, 2, 3, 4, 5]
      // No maxlength: a phone's one-time-code fill, or a paste, puts the
      // whole code in one box, which spreads it over the six.
      .map((i) => `<input class="cb" inputmode="numeric" pattern="[0-9]*" aria-label="验证码第 ${i + 1} 位"${i === 0 ? ' autocomplete="one-time-code"' : ' autocomplete="off"'} />`)
      .join('');
    const c = swap(
      card,
      head(who, '输入验证码', `发到了 ${maskEmail(email)}`) +
        `<div class="code-boxes" role="group" aria-label="6 位验证码">${boxes}</div>` +
        `<p class="jc-error" role="alert"></p>` +
        `<div class="jc-foot"><span class="hint"></span></div>`,
    );
    const inputs = Array.from(c.querySelectorAll                  ('input.cb'));
    const group = c.querySelector('.code-boxes')               ;
    const error = c.querySelector('.jc-error')               ;
    const hint = c.querySelector('.hint')               ;
    let busy = false;
    // "没收到？" counts down 60 seconds, then offers to send again.
    let left = 60;
    const tick = () => {
      if (!hint.isConnected) return;
      if (left > 0) {
        hint.textContent = `没收到？${left} 秒后可以重发`;
        left--;
        after(1000, tick);
      } else {
        hint.innerHTML = `没收到？<button class="link" type="button">重新发</button>`;
        hint.querySelector('button')?.addEventListener('click', async () => {
          try {
            await api.loginEmail(email);
            left = 60;
            tick();
          } catch (err) {
            error.textContent = err instanceof ApiError ? err.message : '没能发出验证码';
          }
        });
      }
    };
    tick();
    const value = () => inputs.map((x) => x.value).join('');
    const mark = () => {
      const at = Math.min(value().length, 5);
      inputs.forEach((x, i) => x.classList.toggle('at', i === at && document.activeElement === x));
    };
    const submit = async () => {
      const code = value();
      if (code.length !== 6 || busy) return;
      busy = true;
      try {
        const r = await api.loginCode(email, code);
        if (r.step === 'done' && r.person) return done(r.person, email);
        throw new ApiError(400, 'invalid', '不对，再看看邮件');
      } catch (err) {
        busy = false;
        // Wrong: the boxes shake and empty, and say so.
        error.textContent = err instanceof ApiError && err.code !== 'invalid' ? err.message : '不对，再看看邮件';
        shake(group);
        for (const x of inputs) x.value = '';
        inputs[0].focus();
        mark();
      }
    };
    // Typing fills a box and moves on; a paste of the whole code (or the
    // phone's one-time-code fill) spreads over the boxes; full, it sends.
    inputs.forEach((x, i) => {
      x.addEventListener('input', (e) => {
        const ie = e              ;
        // One key over a filled box replaces its digit; more is a paste.
        const typed = ie.inputType === 'insertText' && ie.data && ie.data.length === 1 ? ie.data.replace(/\D/g, '') : null;
        const digits = typed ?? x.value.replace(/\D/g, '');
        if (digits.length > 1) {
          for (let k = 0; k < digits.length && i + k < 6; k++) inputs[i + k].value = digits[k];
          inputs[Math.min(5, i + digits.length)].focus();
        } else {
          x.value = digits;
          if (digits && i < 5) inputs[i + 1].focus();
        }
        error.textContent = '';
        mark();
        if (value().length === 6) void submit();
      });
      x.addEventListener('keydown', (e) => {
        if (e.key === 'Backspace' && !x.value && i > 0) {
          inputs[i - 1].value = '';
          inputs[i - 1].focus();
          e.preventDefault();
          mark();
        } else if (e.key === 'ArrowLeft' && i > 0) inputs[i - 1].focus();
        else if (e.key === 'ArrowRight' && i < 5) inputs[i + 1].focus();
      });
      x.addEventListener('focus', () => {
        // Always the first empty box: the code is typed in order.
        const first = inputs.findIndex((y) => !y.value);
        if (first >= 0 && first < i) inputs[first].focus();
        mark();
      });
      x.addEventListener('blur', mark);
    });
    inputs[0].focus();
    mark();
  };

  askEmail();
}

// mountLogin is the card for logging in without an invite.
export function mountLogin(root             , o              )             {
  const { card, destroy } = page(root);
  // An account just deleted (M7): the page says so, and how to stop its relays.
  const gone = goneNote();
  if (gone) {
    card.insertAdjacentHTML('beforebegin', gone.line);
    if (gone.relays) {
      card.insertAdjacentHTML('afterend', gone.relays);
      wireGone(card.parentElement               );
    }
  }
  // In from a window that faded to this page's background: logged out, or
  // the account deleted (DESIGN §6.15).
  if (takeFade() || gone) fadeIn(card.parentElement               , 260);
  const who = `<span class="jc-mark">${star(24, '#fff')}</span>`;
  loginSteps(card, who, '登录 Deneb', o.onDone);
  return destroy;
}

// mountJoin is the invite page: what the link is for, then the login if
// need be, then in.
export function mountJoin(root             , o             )             {
  const { card, destroy } = page(root);
  let me = o.me;
  let gone = false;
  card.innerHTML = `<div class="jc-in"><p class="jc-sub">正在打开邀请…</p></div>`;

  const joined = async (preview               , email         ) => {
    if (!me) return;
    try {
      await api.op('invite.accept', { invite: o.token });
    } catch (err) {
      if (err instanceof ApiError && err.code === 'conflict' && /已经/.test(err.message)) return o.onOpen(preview.lan);
      swap(card, head(ball(preview.inviter, me), '没能加入', err instanceof ApiError ? err.message : '出了点问题，过一会儿再试'));
      return;
    }
    // In: the inviter and me side by side, mine bouncing in (§5 Bouncy).
    const mine         = { id: me, name: '', displayName: '我', initial: (email ?? '?').slice(0, 1).toUpperCase() };
    const c = swap(
      card,
      `<div class="pair">${ball(preview.inviter, me, 48)}${ball(mine, me, 48)}</div>` +
        `<div><h1 class="jc-title">${esc(spaced(`你加入了「${preview.lanName}」`))}</h1><p class="jc-sub">把这台电脑也加进来，就能用里面的设备了</p></div>`,
      'jc-joined',
    );
    const b = c.querySelector('.pair .ball:last-child')               ;
    b.animate([{ transform: 'scale(0.3)', opacity: 0 }, { transform: 'scale(1.12)', opacity: 1, offset: 0.6 }, { transform: 'none' }], { duration: 540, delay: 120, easing: 'cubic-bezier(0.3, 1.2, 0.5, 1)', fill: 'backwards' });
    after(1300, () => !gone && me && o.onJoined(preview.lan, me, email));
  };

  const show = (p               ) => {
    const from = p.inviter.displayName;
    const who = ball(p.inviter, me);
    if (p.alreadyIn) {
      const c = swap(card, head(who, `你已经在「${p.lanName}」里了`, `${from} 邀请过你`) + `<div class="jc-foot"><button class="btn primary" type="button">打开${esc(p.lanName)}</button></div>`);
      c.querySelector('button')?.addEventListener('click', () => o.onOpen(p.lan));
      return;
    }
    if (p.state !== 'pending') {
      const title = p.state === 'expired' ? '这个邀请过期了' : p.state === 'revoked' ? `${from} 收回了这个邀请` : '这个邀请已经用过了';
      swap(card, head(who, title, `请 ${from} 再发一个`));
      return;
    }
    // The second line says what is in it (DESIGN §14.5: "里面有 3 个人，5 台
    // 设备", its members' balls under it); the inviter's seats full, that
    // joining waits for one (§6.11).
    const people = p.members || [];
    const what = people.length ? `里面有 ${people.length} 个人，${p.devices ?? 0} 台设备` : '加入后，装上 Deneb 就能用里面的设备';
    const sub = p.seatsFull ? `${from} 的位置满了，腾出来就能加入` : p.needsApproval ? `${what}，加入后要等 ${from} 同意` : what;
    const stack = people.length ? `<div class="jc-stack" aria-label="${esc(people.map((x) => x.displayName || x.name).join('、'))}">${people.slice(0, 8).map((x) => ball(x, me)).join('')}</div>` : '';
    const c = swap(card, head(who, `${from} 邀请你加入「${p.lanName}」`, sub) + stack + `<div class="jc-foot"><button class="btn primary" type="button" data-act="join">加入</button></div>`);
    // Not focused on arrival: a ring round the button before anyone
    // touched the page reads as something already pressed.
    const btn = c.querySelector('[data-act="join"]')                     ;
    btn.addEventListener('click', () => {
      btn.disabled = true;
      if (me) return void joined(p);
      loginSteps(card, who, `用邮箱加入「${p.lanName}」`, (person, email) => {
        me = person;
        void joined(p, email);
      });
    });
  };

  api.invite(o.token).then(
    (p) => !gone && show(p),
    (err) => {
      if (gone) return;
      const missing = err instanceof ApiError && err.code === 'not_found';
      swap(card, head(`<span class="jc-mark">${star(24, '#fff')}</span>`, missing ? '这个邀请链接不存在' : '打不开这个邀请', missing ? '检查一下链接有没有复制完整' : err instanceof ApiError ? err.message : '过一会儿再试'));
    },
  );
  return () => {
    gone = true;
    destroy();
  };
}
