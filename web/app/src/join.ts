// The invite page and the login (DESIGN.md §14.5, §14.6, §14.14). A
// friend's link opens the invite card, which asks for the email and the
// code in the card; the login page is a column in the middle of the page,
// as the desktop's sign-in (§7.3), each step sliding in from the right. The
// email is the one field to fill in; the code is six boxes that send
// themselves once full.
import { PRIVACY_URL, goneNote, wireGone, fadeIn, takeFade } from './account.ts';
import { api, ApiError } from './api.ts';
import type { InvitePreview, Person, PersonID } from './model.ts';
import { colorIndex } from './model.ts';
import tokens from './tokens.js';
import { after, now, recording, wait } from './clock.ts';
import { appIcon, esc, spaced, star } from './download.ts';
import { go } from './router.ts';

export interface JoinOptions {
  token: string; // the link's token
  me?: PersonID; // logged in already: no login steps
  onJoined(lan: string, me: PersonID, email?: string): void; // in: show the LAN
  onOpen(lan: string): void; // already in: open it
}

export interface LoginOptions {
  next?: string;
  // Signed in: what to load before the app shows, and how to show it.
  onDone(me: PersonID, email: string): Promise<void | (() => void)> | void;
}

const NOTE = '装上 Deneb，几个人的电脑就像在同一个家里，能直接互相用。';
// The login page's foot (§14.14): what Deneb is, in a sentence.
const LOGIN_NOTE = '装上 Deneb，几台电脑不管在哪，都像在同一个家里，能直接互相用。';
const CHECK = `<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m5.5 12.5 4 4 9-9"/></svg>`;
const SPIN = `<span class="spin" aria-hidden="true"></span>`;
const OFFLINE = '连不上 Deneb 服务器，过一会儿再试';
// The pages of one browser talk on this (login-link.md §4): the page the
// link in the mail opens asks who waits for a code, and says when it
// logged in; a page waiting for the code answers, and goes in too.
const LOGIN_CHANNEL = 'deneb-login';
const channel = (): BroadcastChannel | null => (typeof BroadcastChannel === 'function' ? new BroadcastChannel(LOGIN_CHANNEL) : null);
const sameEmail = (a: string, b: string) => a.trim().toLowerCase() === b.trim().toLowerCase();

function ball(p: Person, me: PersonID | undefined, size?: number): string {
  const c = tokens.color.owners[colorIndex(me ?? '', p.id)];
  const s = size ? ` style="--c:${c};width:${size}px;height:${size}px"` : ` style="--c:${c}"`;
  return `<span class="ball"${s} aria-hidden="true">${esc(p.initial || p.displayName.slice(0, 1))}</span>`;
}

// page builds the invite's frame: the mark top left, the card, the line
// about Deneb.
function page(root: HTMLElement): { card: HTMLElement; note: HTMLElement; destroy(): void } {
  const el = document.createElement('div');
  el.className = 'join-page';
  el.innerHTML =
    `<div class="brand">${star(18)}<span>Deneb</span></div>` +
    `<main class="join-card" aria-live="polite"></main>` +
    `<p class="join-note">${esc(spaced(NOTE))}</p>` +
    // The privacy notes, on GitHub (M7, DESIGN §6.15).
    `<p class="join-privacy"><a href="${PRIVACY_URL}" target="_blank" rel="noopener">隐私说明</a></p>`;
  root.replaceChildren(el);
  return { card: el.querySelector('.join-card') as HTMLElement, note: el.querySelector('.join-note') as HTMLElement, destroy: () => el.remove() };
}

// swap puts new contents in the card: they fade in, its height follows on
// a smooth curve (as the desktop's card does when its device changes).
function swap(card: HTMLElement, html: string, cls = 'jc-in'): HTMLElement {
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

// slide puts the login page's next step in: it comes in from the right as
// the last one goes out to the left, or the other way when going back
// (§7.3: 16 px, fading, 220 ms).
function slide(holder: HTMLElement, html: string, back: boolean): HTMLElement {
  const next = document.createElement('div');
  next.className = 'si-in';
  next.innerHTML = html;
  const old = holder.lastElementChild as HTMLElement | null;
  holder.appendChild(next);
  if (old) {
    const dx = back ? -16 : 16;
    old.classList.add('si-out');
    // The old one is mostly gone before the new one shows: two titles in
    // the middle of the page never read over each other.
    const out = old.animate([{ opacity: 1, transform: 'none' }, { opacity: 0, transform: `translateX(${-dx}px)` }], { duration: 140, easing: 'ease-in', fill: 'forwards' });
    out.finished.then(() => old.remove(), () => old.remove());
    next.animate([{ opacity: 0, transform: `translateX(${dx}px)` }, { opacity: 1, transform: 'none' }], { duration: 220, delay: 120, easing: 'cubic-bezier(0.2, 0.7, 0.2, 1)', fill: 'backwards' });
  }
  return next;
}

function shake(el: HTMLElement): void {
  el.animate([{ transform: 'none' }, { transform: 'translateX(-6px)' }, { transform: 'translateX(6px)' }, { transform: 'translateX(-4px)' }, { transform: 'translateX(4px)' }, { transform: 'none' }], { duration: 360, easing: 'ease-out' });
}

const head = (who: string, title: string, sub: string, extra = '') =>
  `<div class="jc-head">${who}<div><h1 class="jc-title">${esc(spaced(title))}</h1><p class="jc-sub">${esc(spaced(sub))}${extra}</p></div></div>`;

const looksLikeEmail = (s: string) => /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(s.trim());

// Steps is where the login's steps go: the invite card, each step fading in
// in it, or the login page's column, each sliding in. early: the page starts
// what comes after the login (the app's view) while its tick shows.
interface Steps {
  show(html: string, back?: boolean): HTMLElement;
  head(title: string, sub: string, extra?: string): string;
  leave(then: () => void): void; // signed in: what the page does before it goes
  early: boolean;
}

function cardSteps(card: HTMLElement, who: string): Steps {
  return { show: (html) => swap(card, html), head: (t, s, x = '') => head(who, t, s, x), leave: (then) => then(), early: false };
}

function pageSteps(el: HTMLElement, holder: HTMLElement): Steps {
  return {
    show: (html, back = false) => slide(holder, html, back),
    head: (t, s, x = '') => `<h1 class="si-title">${esc(spaced(t))}</h1><p class="si-sub">${esc(spaced(s))}${x}</p>`,
    // The page fades to its background; the app comes in over it (main.ts).
    leave: (then) => {
      const a = el.animate([{ opacity: 1 }, { opacity: 0 }], { duration: 200, easing: 'ease-in', fill: 'forwards' });
      a.finished.then(then, then);
    },
    early: true,
  };
}

// How long a code works, and when asking again sends another: the server
// says (WebLoginResult); these, if it does not (10 minutes, 1 minute: its
// codeLife and codeResend). The page counts from when it asked.
const CODE_LIFE = 10 * 60_000;
const RESEND = 60;
// A small grey spinner, for the lines under the boxes.
const SPIN_GREY = `<span class="spin grey" aria-hidden="true"></span>`;
// Feedback that would only flash when the server answers at once waits this
// long first (DESIGN §14.14: no blank wait, and no flicker either).
const SLOW = 150;
// How to paste here: the keys, on a keyboard's computer; on a phone the
// system offers the code itself.
function pasteTip(): string {
  if (matchMedia('(pointer: coarse)').matches) return '';
  return `复制邮件里的 6 位数字，回到这里按 ${/Mac|iPhone|iPad/i.test(navigator.platform || navigator.userAgent) ? '⌘V' : 'Ctrl+V'}`;
}

type Done = (me: PersonID, email: string) => void | Promise<void | (() => void)>;

// loginSteps asks for the email, then the code, in ui, then calls done. It
// returns what stops its listeners, for the page that goes away meanwhile.
function loginSteps(ui: Steps, title: string, sub: string, done: Done): () => void {
  let email = '';
  let stopCode: (() => void) | null = null;
  const askEmail = (back = false) => {
    const c = ui.show(
      ui.head(title, sub) +
        `<form class="jc-form" novalidate>` +
        `<label class="field"><span class="sr">你的邮箱</span><input name="email" type="email" inputmode="email" autocomplete="email" autocapitalize="none" autocorrect="off" spellcheck="false" placeholder="你的邮箱" aria-label="你的邮箱" value="${esc(email)}" required /></label>` +
        `<p class="jc-error" role="alert"></p>` +
        `<div class="jc-foot"><button class="btn primary" type="submit" disabled>发验证码</button></div></form>`,
      back,
    );
    const form = c.querySelector('form') as HTMLFormElement;
    const input = form.querySelector('input') as HTMLInputElement;
    const btn = form.querySelector('button') as HTMLButtonElement;
    const error = form.querySelector('.jc-error') as HTMLElement;
    let sending = false;
    const sync = () => (btn.disabled = sending || !looksLikeEmail(input.value));
    const wrong = (words: string) => {
      error.textContent = words;
      shake(input.parentElement as HTMLElement);
    };
    input.addEventListener('input', () => {
      error.textContent = '';
      sync();
    });
    // Enter with what is not an email yet: the field shakes and says so
    // (the button, grey, takes no click).
    input.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' && !sending && !looksLikeEmail(input.value)) {
        e.preventDefault();
        wrong('邮箱的样子不对，再看一眼');
      }
    });
    sync();
    input.focus();
    if (back) input.select();
    form.addEventListener('submit', async (e) => {
      e.preventDefault();
      if (sending) return;
      if (!looksLikeEmail(input.value)) return wrong('邮箱的样子不对，再看一眼');
      email = input.value.trim();
      sending = true;
      sync();
      btn.classList.add('sending');
      // The button answers the click at once (it presses, and takes no other
      // click: blue still, not the grey of an email not done); the spinner
      // only if the server takes a moment.
      const slow = after(SLOW, () => sending && (btn.innerHTML = `${SPIN}正在发…`));
      // What the server answers changes the page on the next tick of its
      // clock (clock.ts), as everything that moves on it does.
      try {
        const r = await api.loginEmail(email);
        after(0, () => {
          slow.cancel();
          if (r.step === 'done' && r.person) done(r.person, email);
          else askCode((r.codeLife || 0) * 1000 || CODE_LIFE, r.resendAfter || RESEND);
        });
      } catch (err) {
        after(0, () => {
          slow.cancel();
          sending = false;
          btn.classList.remove('sending');
          btn.textContent = '发验证码';
          sync();
          wrong(err instanceof ApiError ? err.message : OFFLINE);
        });
      }
    });
  };

  const askCode = (codeLife: number, wait: number) => {
    let life = codeLife;
    const boxes = [0, 1, 2, 3, 4, 5]
      // No maxlength: a phone's one-time-code fill, or a paste, puts the
      // whole code in one box, which spreads it over the six.
      .map((i) => `<input class="cb" inputmode="numeric" pattern="[0-9]*" aria-label="验证码第 ${i + 1} 位"${i === 0 ? ' autocomplete="one-time-code"' : ' autocomplete="off"'} />`)
      .join('');
    // The whole email: the one who typed it sees where the code went
    // (DESIGN §14.14, the user's to decide).
    const c = ui.show(
      ui.head('输入验证码', `发到了 ${email}`, `<button class="link back" type="button">换个邮箱</button>`) +
        `<div class="code-boxes" role="group" aria-label="6 位验证码">${boxes}</div>` +
        `<p class="jc-error" role="status"></p>` +
        `<p class="jc-hint"></p>`,
    );
    const inputs = Array.from(c.querySelectorAll<HTMLInputElement>('input.cb'));
    const group = c.querySelector('.code-boxes') as HTMLElement;
    const line = c.querySelector('.jc-error') as HTMLElement;
    const hint = c.querySelector('.jc-hint') as HTMLElement;
    let busy = false;
    let over = false; // signed in, gone back to the email, or the page gone
    // The line under the boxes: how to paste, checking, wrong, signed in.
    const say = (html: string, tone: 'bad' | 'ok' | 'wait' | 'tip' | '' = 'bad') => {
      line.innerHTML = html;
      line.className = 'jc-error' + (tone ? ' ' + tone : '');
    };
    const tip = pasteTip();
    if (tip) say(esc(tip), 'tip');
    // The resend line counts down 60 seconds (the server sends no new code
    // sooner), then offers to send again; past the code's 10 minutes it
    // says it ran out, and offers a new one at once.
    let sentAt = now();
    let left = wait;
    let note = '';
    let resending = false;
    let stale = false; // a code past its life was tried: the line says so, red
    let gen = 0;
    const expired = () => now() - sentAt >= life;
    const draw = () => {
      // Run out, the code is no use to paste: the tip goes.
      if (expired() && line.classList.contains('tip')) say('', '');
      hint.classList.toggle('bad', !note && expired() && stale);
      if (note) hint.innerHTML = note;
      else if (expired()) hint.innerHTML = `${stale ? '这个验证码过期了' : '验证码过期了'}，<button class="link" type="button">重发</button>一个新的`;
      else if (left > 0) hint.textContent = `没收到？看看垃圾邮件，${left} 秒后可以重发`;
      else hint.innerHTML = `没收到？看看垃圾邮件，或者<button class="link" type="button">重发</button>`;
      hint.querySelector('button')?.addEventListener('click', resend);
    };
    const count = (g: number) =>
      after(1000, () => {
        if (g !== gen || over || !hint.isConnected) return;
        left = Math.max(0, left - 1);
        draw();
        if (left > 0) count(g);
      });
    const watchLife = (g: number) => after(life, () => g === gen && !over && hint.isConnected && draw());
    const resend = async () => {
      if (resending || over) return;
      resending = true;
      const slow = after(SLOW, () => {
        if (!resending) return;
        note = `${SPIN_GREY}正在重发…`;
        draw();
      });
      try {
        const r = await api.loginEmail(email);
        after(0, () => {
          slow.cancel();
          if (r.codeLife) life = r.codeLife * 1000;
          sent(r.resendAfter || wait);
        });
      } catch (err) {
        after(0, () => {
          slow.cancel();
          resending = false;
          note = '';
          draw();
          say(esc(err instanceof ApiError ? err.message : OFFLINE));
        });
      }
    };
    const sent = (again: number) => {
      resending = false;
      stale = false;
      sentAt = now();
      const g = ++gen;
      left = again;
      note = '又发了一封，看看邮箱';
      // A new code to paste: the tip is back, in place of what was said.
      say(tip ? esc(tip) : '', tip ? 'tip' : '');
      draw();
      after(3000, () => {
        if (g !== gen || over) return;
        note = '';
        draw();
      });
      count(g);
      watchLife(g);
      inputs[0].focus();
    };
    draw();
    count(gen);
    watchLife(gen);
    const value = () => inputs.map((x) => x.value).join('');
    const mark = () => {
      const at = Math.min(value().length, 5);
      inputs.forEach((x, i) => x.classList.toggle('at', i === at && document.activeElement === x));
    };
    // A code pasted anywhere on the page fills the boxes (after copying it
    // from the mail, ⌘V, wherever the focus is); in a box, the box's own
    // input does it.
    const onPaste = (e: ClipboardEvent) => {
      if (over || busy) return;
      const t = e.target as HTMLElement | null;
      if (t && t.classList && t.classList.contains('cb')) return;
      const m = (e.clipboardData?.getData('text') || '').replace(/\s/g, '').match(/\d{6}/);
      if (!m) return;
      e.preventDefault();
      m[0].split('').forEach((d, i) => (inputs[i].value = d));
      inputs[5].focus();
      mark();
      void submit();
    };
    // Logged in meanwhile, in this browser, by the link in the mail
    // (login-link.md §4)? Its page says so on the channel; coming back to
    // this page asks the server too, for a tab the browser froze or one
    // without the channel. In, this page goes in as with the code.
    let looking = false;
    const lookIn = async (byLink: boolean) => {
      if (over || looking) return;
      looking = true;
      try {
        const v = await api.userView();
        after(0, () => !over && signedIn(v.me, byLink));
      } catch {
        // Not yet: the code, as before.
      } finally {
        looking = false;
      }
    };
    const chan = channel();
    if (chan)
      chan.onmessage = (e: MessageEvent) => {
        const m = e.data as { type?: string } | null;
        if (over || !m) return;
        if (m.type === 'who-waits') chan.postMessage({ type: 'waiting', account: email });
        else if (m.type === 'logged-in') void lookIn(true);
      };
    // Back from the mail: the caret is in the first empty box again.
    const onBack = () => {
      if (over || busy || document.visibilityState === 'hidden') return;
      void lookIn(false);
      if (inputs.includes(document.activeElement as HTMLInputElement)) return;
      const i = inputs.findIndex((y) => !y.value);
      inputs[i < 0 ? 5 : i].focus();
      mark();
    };
    document.addEventListener('paste', onPaste);
    window.addEventListener('focus', onBack);
    document.addEventListener('visibilitychange', onBack);
    const stop = () => {
      over = true;
      chan?.close();
      document.removeEventListener('paste', onPaste);
      window.removeEventListener('focus', onBack);
      document.removeEventListener('visibilitychange', onBack);
    };
    stopCode = stop;
    (c.querySelector('.back') as HTMLElement).addEventListener('click', () => {
      stop();
      askEmail(true);
    });
    // In, by the code or by the link (once: the two may cross).
    let entered = false;
    const signedIn = (person: PersonID, byLink = false) => {
      if (entered) return;
      entered = true;
      stop();
      group.classList.remove('checking');
      group.classList.add('ok');
      for (const x of inputs) {
        x.readOnly = true;
        x.blur();
      }
      say(`${CHECK}${byLink ? '用邮件里的链接登录了' : '登录了'}`, 'ok');
      hint.textContent = '';
      // A tick, then on (§7.3: 0.6 s). The login page starts the app's view
      // at once, so that nothing blank waits after the tick.
      if (!ui.early) {
        after(600, () => ui.leave(() => void done(person, email)));
        return;
      }
      const ready = Promise.resolve(done(person, email)).catch(() => undefined);
      after(600, () => ready.then((show) => after(0, () => ui.leave(() => typeof show === 'function' && show()))));
    };
    const submit = async () => {
      const code = value();
      if (code.length !== 6 || busy || over) return;
      busy = true;
      group.classList.add('checking');
      // Checking: said only if the server takes a moment.
      const slow = after(SLOW, () => busy && !over && say(`${SPIN_GREY}正在核对…`, 'wait'));
      // Wrong: the boxes shake and empty, and say so; past the code's life,
      // that it ran out; too many tries, in the server's words.
      const wrong = (err?: unknown) => {
        slow.cancel();
        busy = false;
        group.classList.remove('checking');
        if (over) return;
        if (err instanceof ApiError && err.code !== 'invalid') say(esc(err.message));
        else if (expired()) {
          // One line says it, red, with the way on (§14.14).
          say('', '');
          stale = true;
          left = 0;
          draw();
        } else say('验证码不对，再看一眼');
        shake(group);
        for (const x of inputs) x.value = '';
        inputs[0].focus();
        mark();
      };
      try {
        const r = await api.loginCode(email, code);
        after(0, () => {
          slow.cancel();
          if (r.step === 'done' && r.person) signedIn(r.person);
          else wrong();
        });
      } catch (err) {
        after(0, () => wrong(err));
      }
    };
    // Typing fills a box and moves on; a paste of the whole code (or the
    // phone's one-time-code fill) spreads over the boxes; full, it sends.
    inputs.forEach((x, i) => {
      x.addEventListener('input', (e) => {
        const ie = e as InputEvent;
        // One key over a filled box replaces its digit; more is a paste: a
        // run of six digits in it, if there is one ("验证码 123456").
        const typed = ie.inputType === 'insertText' && ie.data && ie.data.length === 1 ? ie.data.replace(/\D/g, '') : null;
        const run = typed == null ? x.value.replace(/\s/g, '').match(/\d{6}/) : null;
        const digits = typed ?? (run ? run[0] : x.value.replace(/\D/g, ''));
        if (digits.length > 1) {
          const from = run ? 0 : i;
          for (let k = 0; k < digits.length && from + k < 6; k++) inputs[from + k].value = digits[k];
          inputs[Math.min(5, from + digits.length)].focus();
        } else {
          x.value = digits;
          if (digits && i < 5) inputs[i + 1].focus();
        }
        if (line.classList.contains('bad')) say(tip ? esc(tip) : '', tip ? 'tip' : '');
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
  return () => stopCode?.();
}

// The login link's page going back to the login page: the same frame, so
// its step goes out to the left and the login's first step comes in from
// the right, as a step does (§14.14), not a page.
let stepIn = false;

// frame is the login page's (§14.14): the mark top left, the app's icon
// over a column in the middle, the line about Deneb at the foot.
function frame(root: HTMLElement): { el: HTMLElement; main: HTMLElement; holder: HTMLElement } {
  const el = document.createElement('div');
  el.className = 'signin-page';
  el.innerHTML =
    `<div class="brand">${star(18)}<span>Deneb</span></div>` +
    `<main class="signin" aria-live="polite"><div class="si-icon">${appIcon(84)}</div><div class="si-step"></div></main>` +
    `<footer class="si-foot"><span>${esc(spaced(LOGIN_NOTE))}</span><a href="${PRIVACY_URL}" target="_blank" rel="noopener">隐私说明</a></footer>`;
  root.replaceChildren(el);
  const main = el.querySelector('.signin') as HTMLElement;
  return { el, main, holder: main.querySelector('.si-step') as HTMLElement };
}

// mountLogin is the login page: the email, then the code.
export function mountLogin(root: HTMLElement, o: LoginOptions): () => void {
  const { el, main, holder } = frame(root);
  // An account just deleted (M7): the page says so, and how to stop its relays.
  const gone = goneNote();
  if (gone) {
    (main.querySelector('.si-icon') as HTMLElement).insertAdjacentHTML('afterend', gone.line);
    if (gone.relays) {
      main.insertAdjacentHTML('beforeend', gone.relays);
      wireGone(el);
    }
  }
  // In from a window that faded to this page's background: logged out, or
  // the account deleted (DESIGN §6.15).
  if (takeFade() || gone) fadeIn(el, 260);
  const stop = loginSteps(pageSteps(el, holder), '登录 Deneb', '用邮箱就行，我们发一个 6 位验证码', o.onDone);
  if (stepIn) {
    stepIn = false;
    (holder.lastElementChild as HTMLElement).animate([{ opacity: 0, transform: 'translateX(16px)' }, { opacity: 1, transform: 'none' }], { duration: 220, easing: 'cubic-bezier(0.2, 0.7, 0.2, 1)', fill: 'backwards' });
  }
  // This batch's recordings start here (rec-signin.ts).
  if (recording && /^signin/.test(new URLSearchParams(location.search).get('record') || '')) import('./rec-signin.ts');
  return () => {
    stop();
    el.remove();
  };
}

// lines is text for a page's second line, each sentence (or each part
// before a newline) on a line of its own: a line then never breaks in the
// middle of a word ("回到登 / 录页").
function lines(t: string): string {
  return t
    .split(/\n|(?<=。)/)
    .map((x) => x.trim())
    .filter(Boolean)
    .map((x) => esc(spaced(x)))
    .join('<br />');
}

export interface LinkOptions {
  // The email this browser is logged in with already, or null.
  session(): Promise<string | null>;
  enter(): void; // in, as one logged in already: the link left unused
  onDone: LoginOptions['onDone'];
}

// mountLoginLink is the page the link in the code's mail opens (login-link.md
// §4, DESIGN §14.14): whom it logs in as, and a button; a click logs in.
// Opened in the browser whose page waits for the code, it says that page
// goes in too; anywhere else it asks "在这里登录 Deneb？". Never in on
// arrival: a mail scanner opening the link must not use it up, and a mail
// sent on must not log in whoever opens it.
export function mountLoginLink(root: HTMLElement, o: LinkOptions): () => void {
  // The token comes off the address bar at once (out of the history and of
  // screenshots); the tab keeps it in its state, for a reload.
  const state = (history.state ?? {}) as Record<string, unknown>;
  const token = location.hash.replace(/^#/, '') || (typeof state.loginLink === 'string' ? state.loginLink : '');
  if (location.hash || token) history.replaceState({ ...state, loginLink: token || undefined }, '', location.pathname + location.search);
  const { el, holder } = frame(root);
  const ui = pageSteps(el, holder);
  const chan = channel();
  let gone = false;
  let shown = false;
  const show = (html: string): HTMLElement => {
    const c = ui.show(html);
    // The first step fades in: the page was up a moment before it.
    if (!shown) c.animate([{ opacity: 0 }, { opacity: 1 }], { duration: 220, easing: 'ease-out' });
    shown = true;
    return c;
  };
  const stuck = (title: string, sub: string, act = '回到登录页', again?: () => void) => {
    const c = show(ui.head(title, '', lines(sub)) + `<div class="jc-foot ll-foot"><button class="btn primary" type="button">${esc(act)}</button></div>`);
    const btn = c.querySelector('button') as HTMLButtonElement;
    btn.addEventListener('click', () => {
      if (!again) {
        // Out to the left, then the login page's first step in (stepIn).
        const out = c.animate([{ opacity: 1, transform: 'none' }, { opacity: 0, transform: 'translateX(-16px)' }], { duration: 140, easing: 'ease-in', fill: 'forwards' });
        const on = () => {
          stepIn = true;
          go('/login');
        };
        out.finished.then(on, on);
        return;
      }
      btn.disabled = true;
      btn.classList.add('sending');
      btn.innerHTML = `${SPIN}正在打开…`;
      again();
    });
  };
  // Used, run out, or a newer code sent: the server's words, its first part
  // the title, each sentence after it a line of its own. Not answered: again.
  const cannot = (err: unknown) => {
    if (err instanceof ApiError && err.status > 0 && err.status < 500) {
      history.replaceState({ ...state, loginLink: undefined }, '');
      const i = err.message.indexOf('：');
      return i > 0 ? stuck(err.message.slice(0, i), err.message.slice(i + 1)) : stuck('这个登录链接现在用不了', err.message);
    }
    stuck('打不开这个登录链接', err instanceof ApiError ? err.message : OFFLINE, '再试一次', () => void start());
  };

  // Whether the page that asked for the code is in this browser (§4.3): it
  // answers on the channel at once; no answer in 150 ms, it is elsewhere.
  // Wrong either way, nothing is lost: both ask for the click.
  const waiting: string[] = [];
  if (chan)
    chan.onmessage = (e: MessageEvent) => {
      const m = e.data as { type?: string; account?: unknown } | null;
      if (m && m.type === 'waiting' && typeof m.account === 'string') waiting.push(m.account);
    };

  const ask = (account: string, here: boolean) => {
    const label = here ? '登录' : '在这里登录';
    const c = show(
      ui.head(here ? '登录 Deneb' : '在这里登录 Deneb？', '', here ? lines(`用 ${account} 登录\n原来的页面也会自己进去`) : lines(`用 ${account} 登录这个浏览器`)) +
        `<p class="jc-error" role="status"></p>` +
        `<div class="jc-foot ll-foot"><button class="btn primary ll-go" type="button">${label}</button></div>`,
    );
    const btn = c.querySelector('.ll-go') as HTMLButtonElement;
    const line = c.querySelector('.jc-error') as HTMLElement;
    let sending = false;
    btn.addEventListener('click', async () => {
      if (sending) return;
      sending = true;
      btn.disabled = true;
      btn.classList.add('sending');
      line.textContent = '';
      // As the code's button: pressed at once, the spinner only if slow.
      const slow = after(SLOW, () => sending && (btn.innerHTML = `${SPIN}正在登录…`));
      try {
        const r = await api.loginLink(token);
        after(0, () => {
          slow.cancel();
          if (gone || r.step !== 'done' || !r.person) return;
          signedIn(r.person, account, btn);
        });
      } catch (err) {
        after(0, () => {
          slow.cancel();
          if (gone) return;
          sending = false;
          btn.disabled = false;
          btn.classList.remove('sending');
          btn.textContent = label;
          // Used meanwhile, or too many tries: as on arrival. Not answered:
          // the line says so, and the button is there again.
          if (err instanceof ApiError && err.status > 0 && err.status < 500) cannot(err);
          else line.textContent = err instanceof ApiError ? err.message : OFFLINE;
        });
      }
    });
  };

  // In: the pages waiting in this browser are told, the button ticks, the
  // app loads meanwhile; then on, as after the code (§7.3: 0.6 s).
  const signedIn = (person: PersonID, account: string, btn: HTMLButtonElement) => {
    history.replaceState({ ...state, loginLink: undefined }, '');
    chan?.postMessage({ type: 'logged-in' });
    btn.classList.add('done');
    btn.innerHTML = `${CHECK}登录了`;
    const ready = Promise.resolve(o.onDone(person, account)).catch(() => undefined);
    after(600, () => ready.then((show) => after(0, () => ui.leave(() => typeof show === 'function' && show()))));
  };

  const start = async () => {
    if (!token) return stuck('这个登录链接不完整', '检查一下有没有复制完整，或者回到登录页再发一封');
    waiting.length = 0;
    chan?.postMessage({ type: 'who-waits' });
    const asked = now();
    // Nothing yet after a moment: say what it is doing.
    const slow = after(400, () => !gone && !shown && show(`<p class="si-sub ll-wait">${SPIN_GREY}正在打开登录链接…</p>`));
    const [info, mine] = await Promise.all([
      api.loginLinkInfo(token).catch((err: unknown) => (err instanceof Error ? err : new Error(String(err)))),
      o.session().catch(() => null),
    ]);
    if (gone) return slow.cancel();
    // Logged in here already, as the link's email (or the link of no use
    // any more): in, and the link stays unused.
    if (mine != null && (info instanceof Error || sameEmail(mine, info.account))) {
      slow.cancel();
      return ui.leave(() => o.enter());
    }
    if (info instanceof Error) return after(0, () => (slow.cancel(), cannot(info)));
    const here = () => waiting.some((a) => sameEmail(a, info.account));
    if (!here() && chan) await wait(Math.max(0, 150 - (now() - asked)));
    if (gone) return slow.cancel();
    after(0, () => {
      slow.cancel();
      ask(info.account, here());
    });
  };
  void start();
  // This batch's recordings of it (rec-signin.ts).
  if (recording && /^signin/.test(new URLSearchParams(location.search).get('record') || '')) import('./rec-signin.ts');
  return () => {
    gone = true;
    chan?.close();
    el.remove();
  };
}

// mountJoin is the invite page: what the link is for, then the login if
// need be, then in.
export function mountJoin(root: HTMLElement, o: JoinOptions): () => void {
  const { card, destroy } = page(root);
  let me = o.me;
  let gone = false;
  let stopSteps: (() => void) | null = null;
  card.innerHTML = `<div class="jc-in"><p class="jc-sub">正在打开邀请…</p></div>`;

  const joined = async (preview: InvitePreview, email?: string) => {
    if (!me) return;
    try {
      await api.op('invite.accept', { invite: o.token });
    } catch (err) {
      if (err instanceof ApiError && err.code === 'conflict' && /已经/.test(err.message)) return o.onOpen(preview.lan);
      swap(card, head(ball(preview.inviter, me), '没能加入', err instanceof ApiError ? err.message : '出了点问题，过一会儿再试'));
      return;
    }
    // In: the inviter and me side by side, mine bouncing in (§5 Bouncy).
    const mine: Person = { id: me, name: '', displayName: '我', initial: (email ?? '?').slice(0, 1).toUpperCase() };
    const c = swap(
      card,
      `<div class="pair">${ball(preview.inviter, me, 48)}${ball(mine, me, 48)}</div>` +
        `<div><h1 class="jc-title">${esc(spaced(`你加入了「${preview.lanName}」`))}</h1><p class="jc-sub">把这台电脑也加进来，就能用里面的设备了</p></div>`,
      'jc-joined',
    );
    const b = c.querySelector('.pair .ball:last-child') as HTMLElement;
    b.animate([{ transform: 'scale(0.3)', opacity: 0 }, { transform: 'scale(1.12)', opacity: 1, offset: 0.6 }, { transform: 'none' }], { duration: 540, delay: 120, easing: 'cubic-bezier(0.3, 1.2, 0.5, 1)', fill: 'backwards' });
    after(1300, () => !gone && me && o.onJoined(preview.lan, me, email));
  };

  const show = (p: InvitePreview) => {
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
    const btn = c.querySelector('[data-act="join"]') as HTMLButtonElement;
    btn.addEventListener('click', () => {
      btn.disabled = true;
      if (me) return void joined(p);
      stopSteps = loginSteps(cardSteps(card, who), `用邮箱加入「${p.lanName}」`, '以后在电脑上登录 Deneb，也用这个邮箱', (person, email) => {
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
    stopSteps?.();
    destroy();
  };
}
