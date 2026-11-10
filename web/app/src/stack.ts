// The header's stack of balls on a LAN's page (DESIGN.md §6.7, §6.11; the
// desktop's stackBall): a member's ball is solid; someone waiting for me,
// the owner, to let them in is dashed (a click lets them in, the × on the
// pointer turns them away); someone I invited by name is dashed too (the ×
// takes the invite back). A click on a member opens their card; a member
// dragged onto a LAN's row in the sidebar is invited there. While one of
// my devices is in the hand the stack spreads out so that each ball it
// can go to shows whole, the others fade, and the line under it says what
// letting go does.
import { App, G, esc, spaced, nameOf, sideRowAt, sideRow } from './shell.ts';
import { D } from './clock.ts';
import type { UserView, LANView, PersonID } from './model.ts';

// eslint-disable-next-line @typescript-eslint/no-explicit-any
type Any = any;
type P2 = { x: number; y: number };

export type How = 'member' | 'waits' | 'invited';
export interface Ball {
  id: PersonID;
  how: How;
  invite?: string;
}

// balls are the LAN's people as the stack shows them to v's person.
export function ballsOf(v: UserView, lan: LANView): Ball[] {
  const out: Ball[] = [];
  for (const m of lan.members) {
    if (m.state === 'joined') out.push({ id: m.person, how: 'member' });
    else if (m.state === 'pending' && lan.owner === v.me) out.push({ id: m.person, how: 'waits' });
  }
  for (const inv of lan.invited || []) if (inv.invitedBy === v.me && !out.some((b) => b.id === inv.person)) out.push({ id: inv.person, how: 'invited', invite: inv.invite });
  return out;
}

export interface StackHandlers {
  person(id: PersonID): void; // a member's ball clicked
  approve(id: PersonID, el: HTMLElement): void; // a dashed ball clicked (waits)
  reject(id: PersonID): void; // its × (waits)
  unInvite(b: Ball): void; // its × (invited)
  // A member held over a LAN's row: what letting go there would do, and
  // doing it. why: '' lights the row; else the row stays dark and the
  // capsule says why (and dropping does nothing).
  inviteTo?(id: PersonID, lan: string): { why: string; say: string };
  invite?(id: PersonID, lan: string): void;
}

export class Stack {
  el: HTMLElement;
  hint: HTMLElement;
  balls: Ball[] = [];
  spreadOn = false;
  drawn = false;
  drawnHint = false;
  app: Any;
  v: () => UserView;
  h: StackHandlers;
  constructor(app: Any, v: () => UserView, h: StackHandlers) {
    this.app = app;
    this.v = v;
    this.h = h;
    const head = app.win.querySelector('.head');
    this.el = head.querySelector('.stack');
    this.el.className = 'stack live';
    this.el.replaceChildren();
    this.hint = App.h(`<div class="stack-hint" aria-live="polite"></div>`);
    head.querySelector('.right').appendChild(this.hint);
  }

  // draw shows balls, keyed: a new one comes in on a spring, one gone
  // shrinks away, one let in fills from dashed to solid.
  draw(balls: Ball[]): void {
    const v = this.v();
    // The first time, the balls are simply there (the page came in whole).
    const first = !this.drawn;
    this.drawn = true;
    const was = new Map([...this.el.children].map((c) => [(c as HTMLElement).dataset.k as string, c as HTMLElement]));
    const keep = new Set<string>();
    let prev: HTMLElement | null = null;
    for (const b of balls) {
      keep.add(b.id);
      let el = was.get(b.id);
      const fresh = !el;
      if (!el) {
        el = App.h(`<span class="sball" data-k="${esc(b.id)}" role="button" tabindex="0"></span>`) as HTMLElement;
        this.wire(el);
      }
      const old = el.dataset.how;
      el.dataset.how = b.how;
      el.dataset.invite = b.invite || '';
      el.classList.toggle('me', b.id === v.me);
      const label = b.how === 'waits' ? spaced(nameOf(v, b.id) + '想加入，点一下同意') : b.how === 'invited' ? spaced(`已邀请${nameOf(v, b.id)}，等他接受`) : spaced(nameOf(v, b.id));
      el.title = b.how === 'member' ? '' : label;
      el.setAttribute('aria-label', label);
      if (fresh || old !== b.how) el.innerHTML = App.ball(b.id) + (b.how !== 'member' ? `<span class="dash" style="--c:${App.PEOPLE[b.id]?.c}">${esc(App.PEOPLE[b.id]?.ch || '')}</span><button class="x" type="button" aria-label="${b.how === 'waits' ? '拒绝' : '收回邀请'}">${G.ui('close', 10)}</button>` : '');
      if (prev ? prev.nextSibling !== el : this.el.firstChild !== el) this.el.insertBefore(el, prev ? prev.nextSibling : this.el.firstChild);
      if (fresh && !first) el.animate([{ transform: 'scale(0.5)', opacity: 0 }, { transform: 'none', opacity: 1 }], { duration: 420, easing: 'cubic-bezier(0.3, 1.3, 0.5, 1)' });
      else if (old === 'waits' && b.how === 'member') el.animate([{ transform: 'scale(0.88)' }, { transform: 'scale(1.08)', offset: 0.5 }, { transform: 'none' }], { duration: 560, easing: 'cubic-bezier(0.3, 1.2, 0.5, 1)' });
      prev = el;
    }
    for (const [k, el] of was)
      if (!keep.has(k) && !el.classList.contains('leaving')) {
        el.classList.add('leaving');
        el.animate([{ transform: 'none', opacity: 1 }, { transform: 'scale(0.5)', opacity: 0 }], { duration: 220, easing: 'ease-in', fill: 'forwards' }).finished.then(() => el.remove());
      }
    this.balls = balls;
  }

  private wire(el: HTMLElement): void {
    const id = () => el.dataset.k as string;
    const how = () => el.dataset.how as How;
    el.addEventListener('click', (e) => {
      if ((el as Any).__dragged) return ((el as Any).__dragged = false);
      if ((e.target as Element).closest('.x')) {
        e.stopPropagation();
        const b = this.balls.find((x) => x.id === id());
        if (!b) return;
        if (b.how === 'waits') this.h.reject(b.id);
        else if (b.how === 'invited') this.h.unInvite(b);
        return;
      }
      if (how() === 'waits') return this.h.approve(id(), el);
      if (how() === 'member' && id() !== this.v().me) this.h.person(id());
    });
    el.addEventListener('keydown', (e) => (e as KeyboardEvent).key === 'Enter' && el.click());
    // A member, onto a LAN's row: invited there (§6.7). What follows the hand
    // is a capsule, their ball and name.
    let g: HTMLElement | null = null;
    let row: { id: string; el: HTMLElement } | null = null;
    this.app.drag(el, {
      ignore: '.x',
      start: () => {
        if (how() !== 'member' || id() === this.v().me || !this.h.invite) return;
        g = App.h(`<div class="mpill">${App.ball(id())}<span>${esc(spaced(nameOf(this.v(), id())))}</span><span class="rnote"></span></div>`) as HTMLElement;
        this.app.dragLayer.appendChild(g);
        el.classList.add('ghost');
      },
      move: (p: P2) => {
        if (!g) return;
        g.style.left = p.x + 'px';
        g.style.top = p.y + 'px';
        const r = sideRowAt(p);
        const what = r && this.h.inviteTo ? this.h.inviteTo(id(), r.id) : null;
        if (row && row.el !== r?.el) row.el.classList.remove('target');
        row = r;
        if (r) r.el.classList.toggle('target', !!what && !what.why);
        note(g, what ? what.why || what.say : '', !!what && !what.why);
      },
      end: async (p: P2 | null, cancel: boolean) => {
        if (!g) return;
        row?.el.classList.remove('target');
        const r = !cancel && p ? sideRowAt(p) : null;
        const what = r && this.h.inviteTo ? this.h.inviteTo(id(), r.id) : null;
        const pill = g;
        g = null;
        note(pill, '', false);
        if (r && what && !what.why) {
          const to = this.app.rectOf(r.el.querySelector('.dots') || r.el);
          await fly(pill, { x: to.x + to.w / 2, y: to.y + to.h / 2 }, 0.3, true);
          pill.remove();
          el.classList.remove('ghost');
          this.h.invite!(id(), r.id);
          return;
        }
        // Back to the stack; a row that has them already gives a little shake.
        if (r && what && what.why && /已经在了/.test(what.why)) r.el.animate([{ transform: 'none' }, { transform: 'translateX(-5px)' }, { transform: 'translateX(5px)' }, { transform: 'translateX(-3px)' }, { transform: 'none' }], { duration: 360, easing: 'ease-out' });
        const home = this.app.rectOf(el);
        await fly(pill, { x: home.x + home.w / 2, y: home.y + home.h / 2 }, 0.7, true);
        pill.remove();
        el.classList.remove('ghost');
      },
    });
  }

  // spread opens the stack out for a held device: its targets whole, the
  // rest faint; off, it closes up again.
  spread(on: boolean, targets: Set<PersonID> = new Set()): void {
    this.spreadOn = on;
    this.el.classList.toggle('spread', on);
    for (const c of this.el.children as HTMLCollectionOf<HTMLElement>) c.classList.toggle('faint', on && !targets.has(c.dataset.k as string));
    this.hint.classList.toggle('on', on);
    if (!on) this.aim('', false);
  }
  // aim rings the ball the hand is over (when it can take the device).
  aim(who: PersonID, ok: boolean): void {
    for (const c of this.el.children as HTMLCollectionOf<HTMLElement>) c.classList.toggle('aim', ok && c.dataset.k === who);
  }
  // say puts text on the line under the balls and "邀请朋友"; go: blue, as
  // what letting go there does.
  say(text: string, go = false): void {
    this.hint.classList.toggle('go', go);
    if (this.hint.textContent !== text) {
      const was = this.hint.textContent;
      this.hint.textContent = text;
      // New words take the old ones' place at once and fade in (not the
      // first words, which come with the page).
      if (text && (was || this.drawnHint)) this.hint.animate([{ opacity: 0, transform: 'translateY(-3px)' }, { opacity: 1, transform: 'none' }], { duration: 160, easing: 'ease-out' });
    }
    this.drawnHint = true;
  }
  // ballAt is the target ball nearest p (canvas coordinates), within 24.
  ballAt(p: P2, targets: Set<PersonID>): { id: PersonID; el: HTMLElement } | null {
    let best = 24, out: { id: PersonID; el: HTMLElement } | null = null;
    for (const c of this.el.children as HTMLCollectionOf<HTMLElement>) {
      if (!targets.has(c.dataset.k as string)) continue;
      const r = this.app.rectOf(c);
      const d = Math.hypot(p.x - (r.x + r.w / 2), p.y - (r.y + r.h / 2));
      if (d < best) (best = d), (out = { id: c.dataset.k as string, el: c });
    }
    return out;
  }
  ball(id: PersonID): HTMLElement | null {
    return this.el.querySelector(`.sball[data-k="${CSS.escape(id)}"]`);
  }
}

// The capsule that goes with the hand: a word beside it, blue when letting
// go there does something.
function note(g: HTMLElement, text: string, go: boolean): void {
  const n = g.querySelector('.rnote') as HTMLElement;
  const t = spaced(text);
  if (n.textContent !== t) n.textContent = t;
  n.classList.toggle('go', go);
  n.style.opacity = text ? '1' : '0';
}
async function fly(g: HTMLElement, to: P2, s1: number, fade: boolean): Promise<void> {
  const a = { x: parseFloat(g.style.left), y: parseFloat(g.style.top) };
  await App.tween(fade && s1 < 0.5 ? 300 : 320, (k: number) => {
    g.style.left = a.x + (to.x - a.x) * k + 'px';
    g.style.top = a.y + (to.y - a.y) * k + 'px';
    g.style.transform = `translate(-50%, -50%) rotate(-2deg) scale(${1 + (s1 - 1) * k})`;
    if (fade && s1 < 0.5) g.style.opacity = String(1 - 0.85 * k);
  }, s1 < 0.5 ? D.Ease.in : D.Ease.out);
}

export { sideRow };
