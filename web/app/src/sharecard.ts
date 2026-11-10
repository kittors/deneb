// Sharing on the web's pages (DESIGN.md §6.11, as the desktop's
// cmd/deneb/sharecards.go and share.go): a device of mine's "分享给" in its
// card, a person's card, the seats' card and the line under "邀请朋友"; and
// sharing, taking back and freeing a seat behind them, with their toasts.
// The cards are HTML for the page's sheet plus a wire function; what is
// open in them (a chip, the picker) is kept here, so that a card drawn
// again from a new view keeps it.
import { App, G, esc, spaced, nameOf, glyphOf, serviceName, serviceIcon, freeing } from './shell.ts';
import { hold, failed, pop } from './ops.ts';
import { D } from './clock.ts';
import type { HeldOp } from './ops.ts';
import { serviceKey } from './share.ts';
import type { Ctx } from './main.ts';
import type { UserView, DeviceView, PersonID, Seats, ShareView } from './model.ts';

// eslint-disable-next-line @typescript-eslint/no-explicit-any
type Any = any;
type At = { x?: number; parent?: HTMLElement };

export const can = (v: UserView, f: string): boolean => !!v.features?.includes(f);
const nm = (d: DeviceView) => d.displayName || d.name;
const tileOf = (v: UserView, d: DeviceView) => ({ type: glyphOf(d.kind), owner: d.publicLAN ? 'lan:' + d.publicLAN : d.owner === v.me ? v.me : d.owner });

// ---- Seats (pricing §1.4, §1.5) -------------------------------------------------------

// seats are mine as the page counts them: the server's, less the people
// whose seat I am freeing while the toast offers undo.
export function seats(v: UserView): Seats {
  const s = v.seats;
  if (!s || !freeing.size) return s;
  const people = (s.people || []).filter((p) => !freeing.has(p));
  const used = s.used - ((s.people || []).length - people.length);
  return { ...s, people, used, left: Math.max(s.limit - used, 0) };
}
export const seatsOn = (v: UserView): boolean => can(v, 'seats') && !!v.seats?.enforced;
export const seatsFull = (v: UserView): boolean => seatsOn(v) && seats(v).left <= 0;
// fullFor: bringing who in finds no seat of mine (full, and not on my list).
export const fullFor = (v: UserView, who: PersonID): boolean => seatsFull(v) && !(seats(v).people || []).includes(who);
// seatLine is the small line under "邀请朋友": only with one or two left.
export function seatLine(v: UserView): string {
  const s = seats(v);
  return seatsOn(v) && s.left > 0 && s.left <= 2 ? `还能加 ${s.left} 人` : '';
}
// freed forgets the people freed once the server no longer counts them.
export function freed(v: UserView): void {
  for (const who of freeing) if (!(v.seats?.people || []).includes(who)) freeing.delete(who);
}

// ---- Who a device can go to ---------------------------------------------------------

// usesAlready: who sees d already, in a LAN it is in where members see each
// other's devices, and I do not refuse them on it.
export function usesAlready(v: UserView, d: DeviceView, who: PersonID): boolean {
  if (d.refused?.includes(who)) return false;
  return v.lans.some((l) => l.mode !== 'share' && l.devices.some((x) => x.device === d.id && !x.pending) && l.members.some((m) => m.person === who && m.state === 'joined'));
}

// whyNot is why d cannot go to who, as the line under the balls says it
// while the hand holds it over them; '' when it can.
export function whyNot(v: UserView, d: DeviceView, who: PersonID): string {
  const name = nameOf(v, who);
  if (d.shares?.some((s) => s.person === who)) return spaced(name + '已经有了');
  if (usesAlready(v, d, who)) return spaced(name + '已经能用了');
  if (fullFor(v, who)) return '位置满了';
  return '';
}

// openOf is what a share opens: what d opens to its LANs now; in no LAN,
// what it has opened before (its catalog). DESIGN §6.11 "开放哪几项".
export function openOf(v: UserView, d: DeviceView): string[] {
  const keys: string[] = [];
  let inLAN = false;
  for (const l of v.lans) {
    const ld = l.devices.find((x) => x.device === d.id);
    if (!ld) continue;
    inLAN = true;
    for (const s of ld.services || []) if (s.on && !keys.includes(serviceKey(s))) keys.push(serviceKey(s));
  }
  if (!inLAN) for (const s of d.services || []) keys.push(serviceKey(s));
  return keys;
}

// ---- Sharing, taking back, freeing a seat ---------------------------------------------

// shareTo gives d to who; their ball takes it with a spring. Undo takes it
// back at once, its seat with it.
export function shareTo(app: Any, ctx: Ctx, v: UserView, d: DeviceView, who: PersonID, o: At & { ball?: Element | null } = {}): void {
  pop(o.ball || null);
  let id = '';
  const sent = ctx
    .op('share.create', { device: d.id, person: who, services: openOf(v, d) })
    .then((r) => (id = r.result?.share || ''))
    .catch((e) => failed(app, e, '没能分享，再试一次', o));
  app.toast(spaced(`已把${nm(d)}分享给${nameOf(v, who)}`), {
    dev: tileOf(v, d),
    x: o.x,
    parent: o.parent,
    undo: async () => {
      await sent;
      if (!id) return;
      await ctx.op('share.revoke', { share: id }).catch((e) => failed(app, e, '没能撤销，再试一次', o));
      ctx.op('share.release', { share: id }).catch(() => {});
    },
  });
}

// revoke takes d back from who at once. Undo gives it back as it was;
// once the toast goes without it, the seat it kept can go (share.release).
export function revoke(app: Any, ctx: Ctx, v: UserView, d: DeviceView, who: PersonID, o: At = {}): void {
  const sh = d.shares?.find((s) => s.person === who);
  if (!sh) return;
  if (open === d.id + '/' + who) open = '';
  let undone = false;
  ctx.op('share.revoke', { share: sh.id }).catch((e) => failed(app, e, '没能收回，再试一次', o));
  app.toast(spaced(`已从${nameOf(v, who)}收回${nm(d)}`), {
    dev: tileOf(v, d),
    x: o.x,
    parent: o.parent,
    undo: () => {
      undone = true;
      ctx.op('share.create', { device: d.id, person: who }).catch((e) => failed(app, e, '没能撤销，再试一次', o));
    },
    gone: () => {
      if (!undone) ctx.op('share.release', { share: sh.id }).catch(() => {});
    },
  });
}

// freeSeat takes who out of every LAN of mine and back from every device I
// gave them, once the toast goes; until then they are out of my seats on
// the page, and undo is nothing done.
export function freeSeat(app: Any, ctx: Ctx, v: UserView, who: PersonID, changed: () => void, o: At = {}): void {
  const ops: HeldOp[] = [];
  for (const l of v.lans) {
    if (l.owner !== v.me) continue;
    const m = l.members.find((x) => x.person === who);
    if (m) ops.push([m.state === 'pending' ? 'member.reject' : 'member.remove', { lan: l.id, person: who }]);
  }
  for (const d of v.devices)
    for (const s of d.shares || [])
      if (s.person === who) {
        ops.push(['share.revoke', { share: s.id }]);
        ops.push(['share.release', { share: s.id }]);
      }
  freeing.add(who);
  changed();
  const back = () => {
    freeing.delete(who);
    changed();
  };
  hold(app, ctx, ops, spaced(`已移出${nameOf(v, who)}`), { ...o, undo: back, failed: back });
}

// ---- A device of mine: 分享给 ----------------------------------------------------------

// What is open in the cards: a share's chip ("dev/person": what it opens to
// them, below), and the picker of a device ("+").
let open = '';
let pick = '';
let note: { who: string; until: number } | null = null;

export function oneWay(v: UserView, shares: ShareView[]): string {
  if (!shares.length) return '';
  return shares.length === 1 ? spaced(`只有${nameOf(v, shares[0].person)}能连你，反过来不行`) : '只有他们能连你，反过来不行';
}

const X = () => `<button class="x" type="button" aria-label="收回" title="收回">${G.ui('close', 10)}</button>`;

// shareSection is "分享给" for my device d (or a public one I manage).
// members: the LAN on screen's, first among whom it can go to.
export function shareSection(v: UserView, d: DeviceView, members: PersonID[] = []): string {
  if (!can(v, 'share')) return '';
  const shares = d.shares || [];
  let html = `<div class="label">分享给</div><div class="pchips" data-dev="${esc(d.id)}">`;
  for (const sh of shares) {
    const refused = !!d.refused?.includes(sh.person);
    const on = open === d.id + '/' + sh.person;
    html += `<span class="pchip${on ? ' open' : ''}" data-who="${esc(sh.person)}" data-dev="${esc(d.id)}" role="button" tabindex="0" aria-expanded="${on}" aria-label="${esc(spaced(nameOf(v, sh.person)))}">${App.ball(sh.person, refused ? 'off' : '')}<span class="pn">${esc(spaced(nameOf(v, sh.person)))}</span>${X()}</span>`;
  }
  const alone = !shares.length;
  html += `<button class="pchip plus${alone ? ' alone' : ''}${pick === d.id ? ' on' : ''}" type="button" data-dev="${esc(d.id)}" aria-label="分享给别人">${G.ui('plus', 14)}${alone ? '<span>分享给别人</span>' : ''}</button></div>`;
  html += opened(v, d);
  if (pick === d.id) html += picker(v, d, members);
  const line = oneWay(v, shares);
  if (line) html += `<div class="hint">${esc(line)}</div>`;
  return html;
}

// opened is what the open chip's share opens to its person, a switch each,
// as "开放给大家的" is.
function opened(v: UserView, d: DeviceView): string {
  const sh = (d.shares || []).find((s) => open === d.id + '/' + s.person);
  if (!sh) return '';
  const on = new Set(sh.services.map(serviceKey));
  const cat = d.services || [];
  const rows = cat.length
    ? cat
        .map((s, i) => {
          const k = on.has(serviceKey(s));
          return `<div class="row">${G.ui(serviceIcon(s.kind), 18)}<span>${esc(serviceName(s))}</span><button class="sw ${k ? 'on' : ''}" data-own data-ssw="${i}" data-dev="${esc(d.id)}" data-share="${esc(sh.id)}" role="switch" aria-checked="${k}" aria-label="${esc(serviceName(s))}"></button></div>`;
        })
        .join('')
    : `<div class="row na"><span>这台设备还没有能开放的服务</span></div>`;
  return `<div class="share-open"><div class="label">${esc(spaced(`开放给${nameOf(v, sh.person)}的`))}</div><div class="rows">${rows}</div></div>`;
}

// candidates are whom d can go to: everyone I see but me, its owner and
// those who have it or see it already; the LAN on screen's members first.
function candidates(v: UserView, d: DeviceView, members: PersonID[]): PersonID[] {
  const out: PersonID[] = [];
  const add = (id: PersonID) => {
    if (id === v.me || id === d.owner || out.includes(id) || d.shares?.some((s) => s.person === id) || usesAlready(v, d, id)) return;
    if (v.people.some((p) => p.id === id)) out.push(id);
  };
  for (const id of members) add(id);
  for (const p of [...v.people].sort((a, b) => (a.displayName || a.name).localeCompare(b.displayName || b.name))) add(p.id);
  return out;
}

function picker(v: UserView, d: DeviceView, members: PersonID[]): string {
  const who = candidates(v, d, members);
  if (!who.length) return `<div class="picker"><p class="say">能分享的人都有了</p></div>`;
  const now = D.now();
  return (
    `<div class="picker">` +
    who
      .map((k) => {
        const full = fullFor(v, k);
        const said = note && note.who === k && now < note.until ? '位置满了' : spaced(nameOf(v, k));
        return `<button class="pchip pick${full ? ' full' : ''}" type="button" data-pick="${esc(k)}" data-dev="${esc(d.id)}" aria-label="${esc(spaced(nameOf(v, k)))}">${App.ball(k)}<span class="pn">${esc(said)}</span></button>`;
      })
      .join('') +
    `</div>`
  );
}

// wireShare makes a card's "分享给" (and a person's card's chips) work.
// redraw draws the card again from the latest view; view gives it.
export function wireShare(app: Any, ctx: Ctx, view: () => UserView, el: HTMLElement, redraw: () => void, at: At = {}): void {
  const dev = (id: string) => view().devices.find((x) => x.id === id);
  for (const chip of el.querySelectorAll('.pchip[data-who]') as NodeListOf<HTMLElement>) {
    const key = chip.dataset.dev + '/' + chip.dataset.who;
    const toggle = () => {
      open = open === key ? '' : key;
      pick = '';
      redraw();
    };
    chip.addEventListener('click', (e) => {
      if ((e.target as Element).closest('.x')) return;
      toggle();
    });
    chip.addEventListener('keydown', (e) => (e as KeyboardEvent).key === 'Enter' && toggle());
    (chip.querySelector('.x') as HTMLElement).onclick = (e) => {
      e.stopPropagation();
      const d = dev(chip.dataset.dev as string);
      if (d) revoke(app, ctx, view(), d, chip.dataset.who as string, at);
    };
  }
  for (const b of el.querySelectorAll('.pchip.plus') as NodeListOf<HTMLElement>)
    b.onclick = () => {
      pick = pick === b.dataset.dev ? '' : (b.dataset.dev as string);
      open = '';
      redraw();
    };
  for (const b of el.querySelectorAll('.pchip.pick') as NodeListOf<HTMLElement>)
    b.onclick = () => {
      const v = view(), d = dev(b.dataset.dev as string), who = b.dataset.pick as string;
      if (!d) return;
      if (fullFor(v, who)) {
        // Someone new while my seats are full: it says so where they are.
        note = { who, until: D.now() + 1600 };
        redraw();
        D.after(1650, () => note?.who === who && D.now() >= note.until && ((note = null), redraw()));
        return;
      }
      pick = '';
      shareTo(app, ctx, v, d, who, at);
      redraw();
    };
  for (const sw of el.querySelectorAll('.sw[data-ssw]') as NodeListOf<HTMLElement>)
    sw.onclick = () => {
      const d = dev(sw.dataset.dev as string);
      const s = d?.services?.[+(sw.dataset.ssw as string)];
      if (!d || !s) return;
      const on = !sw.classList.contains('on');
      sw.classList.toggle('on', on);
      sw.setAttribute('aria-checked', String(on));
      ctx.op('service.set', { device: d.id, share: sw.dataset.share, service: { kind: s.kind, name: s.name, ports: s.ports }, on }).catch((e) => {
        sw.classList.toggle('on', !on);
        failed(app, e, '没能改，再试一次', at);
      });
    };
}

// forget closes what is open in the cards (another card, another page).
export function forget(): void {
  open = pick = '';
  note = null;
}

// ---- A person's card (a click on their ball) ------------------------------------------

// personCard: what I gave them (chips, each opening what it opens to them,
// × taking it back), which way it goes; and what they gave me, a click
// going to 共享给我 with that device's card open. role: their second line.
export function personCard(v: UserView, who: PersonID, role: string, o: { dragHint?: boolean } = {}): string {
  const mine = v.devices.filter((d) => (d.shares || []).some((s) => s.person === who));
  const name = nameOf(v, who);
  let html =
    `<div class="top person-top">${App.ball(who, 'big')}<div><b>${esc(spaced(name))}</b><span>${esc(spaced(role))}</span></div>` +
    `<button class="icon-btn sheet-close" title="关闭" aria-label="关闭">${G.ui('close', 16)}</button></div>` +
    `<div class="label">${esc(spaced(`你分享给${name}的`))}</div>`;
  if (!mine.length) {
    // Where there are devices of mine to drag (a LAN's page); elsewhere the
    // section waits for something to show.
    if (o.dragHint !== false) html += `<p class="say">${esc(spaced(`把你的设备拖到${name}身上，就只给${name}用那一台`))}</p>`;
    else html = html.slice(0, html.lastIndexOf('<div class="label">'));
  } else {
    html += `<div class="pchips">`;
    for (const d of mine) {
      const t = tileOf(v, d);
      const on = open === d.id + '/' + who;
      html += `<span class="pchip devchip${on ? ' open' : ''}" data-who="${esc(who)}" data-dev="${esc(d.id)}" role="button" tabindex="0" aria-expanded="${on}" aria-label="${esc(spaced(nm(d)))}"><span class="tile-xs"><span class="tile" style="--c:${App.PEOPLE[t.owner].c}">${G.device(t.type, 14)}</span></span><span class="pn">${esc(spaced(nm(d)))}</span>${X()}</span>`;
    }
    html += `</div>`;
    for (const d of mine) html += opened(v, d);
    html += `<div class="hint">${esc(spaced(`只有${name}能连你，反过来不行`))}</div>`;
  }
  const theirs = (v.sharedWithMe || []).filter((s) => s.from === who);
  if (theirs.length) {
    html += `<div class="label">${esc(spaced(`${name}分享给你的`))}</div><div class="pchips">`;
    for (const s of theirs)
      html += `<button class="pchip devchip given" type="button" data-given="${esc(s.device.id)}"><span class="tile-xs"><span class="tile" style="--c:${App.PEOPLE[who].c}">${G.device(glyphOf(s.device.kind), 14)}</span></span><span class="pn">${esc(spaced(s.device.displayName || s.device.name))}</span></button>`;
    html += `</div>`;
  }
  return html;
}

// The device of 共享给我 to open when that page comes (a chip of "Mia 分享给
// 你的" went there).
export let openOnShared = '';
export const takeOpenOnShared = (): string => {
  const id = openOnShared;
  openOnShared = '';
  return id;
};
export function wireGiven(ctx: Ctx, el: HTMLElement): void {
  for (const b of el.querySelectorAll('.pchip.given') as NodeListOf<HTMLElement>)
    b.onclick = () => {
      openOnShared = b.dataset.given as string;
      ctx.go('/shared');
    };
}

// ---- The seats' card (a click on the dashed "邀请朋友", or on someone
// waiting while the seats are full) --------------------------------------------------

export const seatState = { freeing: false, forWho: '' };

export function seatCard(v: UserView, inviteHere: boolean): string {
  const s = seats(v);
  const full = s.left <= 0;
  const title = full ? `${s.limit} 个位置都用上了` : `还能加 ${s.left} 人`;
  let html = `<div class="top seat-top"><div><b class="seat-title">${esc(title)}</b></div><button class="icon-btn sheet-close" title="关闭" aria-label="关闭">${G.ui('close', 16)}</button></div>`;
  if (seatState.forWho && full) html += `<p class="seat-for">${esc(spaced(`腾出一个位置，${nameOf(v, seatState.forWho)}就能加入`))}</p>`;
  // Me first, as the desktop's row of balls has it.
  const held = [...(s.people || [])].sort((a, b) => (a === v.me ? -1 : b === v.me ? 1 : 0));
  html += `<div class="seat-balls">${held
    .map((k) => `<span class="seat-ball" data-k="${esc(k)}" aria-label="${esc(spaced(nameOf(v, k)))}">${App.ball(k)}${seatState.freeing && full && k !== v.me ? `<button class="x" type="button" data-free="${esc(k)}" aria-label="${esc(spaced('移出' + nameOf(v, k)))}">${G.ui('close', 10)}</button>` : ''}</span>`)
    .join('')}</div>`;
  if (seatState.freeing && full) html += `<p class="say">移出的人离开你的局域网，分给他的设备也收回</p>`;
  html += `<div class="seat-foot">`;
  if (!full) html += inviteHere ? `<button class="btn primary sm" type="button" data-act="invite">${G.ui('plus', 14)}邀请朋友</button>` : '';
  else if (seatState.freeing) html += `<button class="btn ghost sm" type="button" data-act="done">好了</button>`;
  else html += `<button class="btn primary sm" type="button" data-act="free">腾一个位置</button>`;
  return html + `</div>`;
}

export function wireSeats(app: Any, ctx: Ctx, view: () => UserView, el: HTMLElement, redraw: () => void, invite: () => void, at: At = {}): void {
  (el.querySelector('[data-act="free"]') as HTMLElement | null)?.addEventListener('click', () => {
    seatState.freeing = true;
    redraw();
  });
  (el.querySelector('[data-act="done"]') as HTMLElement | null)?.addEventListener('click', () => {
    seatState.freeing = false;
    redraw();
  });
  (el.querySelector('[data-act="invite"]') as HTMLElement | null)?.addEventListener('click', () => {
    seatState.freeing = false;
    seatState.forWho = '';
    app.closeSheet();
    invite();
  });
  for (const x of el.querySelectorAll('[data-free]') as NodeListOf<HTMLElement>)
    x.onclick = () => {
      const who = x.dataset.free as string;
      const b = x.closest('.seat-ball') as HTMLElement;
      b.animate([{ transform: 'none', opacity: 1 }, { transform: 'scale(0.5)', opacity: 0 }], { duration: 220, easing: 'ease-in', fill: 'forwards' });
      D.after(200, () => freeSeat(app, ctx, view(), who, redraw, at));
    };
}
