// A LAN on a phone (DESIGN.md §14.6; design/demos/web/m3-12-join-phone.html):
// no canvas, a list by person, mine first; a device's card rises from the
// bottom; the dock at the bottom says how to get Deneb onto a computer
// (there is no Deneb for phones). A click on the LAN's name lists my LANs.
import { App, G, esc, spaced, glyphOf, nameOf, people, serviceName, serviceIcon, ago } from './shell.ts';
import { listedLANs, sharedOnly } from './model.ts';
import { phoneDock } from './download.ts';
import { ApiError } from './api.ts';
import type { Ctx } from './main.ts';
import type { Route } from './router.ts';
import type { UserView, DeviceView, LANView, SharedDevice } from './model.ts';

const DASH = `<svg class="dash" viewBox="0 0 40 40" width="40" height="40" aria-hidden="true"><rect x="0.75" y="0.75" width="38.5" height="38.5" rx="11.25" fill="none" stroke="currentColor" stroke-width="1.5" stroke-dasharray="4 3"/></svg>`;

export function phoneLan(root: HTMLElement, ctx: Ctx, route: Route): () => void {
  const lanID = route.page === 'lan' ? route.lan : '';
  const page = document.createElement('div');
  page.className = 'm-lan';
  root.replaceChildren(page);
  const P = App.PEOPLE;
  page.innerHTML = `<div class="m-head"><button class="m-title" type="button" aria-haspopup="dialog"></button><span class="stack"></span></div><div class="m-sub"><span class="dot"></span><span class="m-sub-text"></span></div><div class="m-list" role="list"></div><div class="m-scrim"></div>`;
  page.appendChild(phoneDock());
  const { rise } = host(page);

  function draw(v: UserView): void {
    people(v);
    const lan = v.lans.find((l) => l.id === lanID) as LANView | undefined;
    const title = page.querySelector('.m-title') as HTMLElement;
    if (!lan) {
      title.textContent = '没有这个局域网';
      return;
    }
    title.innerHTML = `${esc(spaced(lan.name))}${G.ui('chevron', 16)}`;
    const joined = lan.members.filter((m) => m.state === 'joined').map((m) => m.person);
    (page.querySelector('.stack') as HTMLElement).innerHTML = joined.map((k) => App.ball(k)).join('');
    const devs = lan.devices.filter((ld) => !ld.pending).map((ld) => v.devices.find((d) => d.id === ld.device)).filter(Boolean) as DeviceView[];
    const on = devs.filter((d) => d.online).length;
    (page.querySelector('.dot') as HTMLElement).dataset.tone = on === devs.length ? 'ok' : 'off';
    (page.querySelector('.m-sub-text') as HTMLElement).textContent = !devs.length ? `${joined.length} 个人，还没有设备` : on === devs.length ? `${joined.length} 个人，${devs.length} 台设备都在线` : `${joined.length} 个人，${devs.length} 台设备，${on} 台在线`;
    const order = [v.me, ...joined.filter((k) => k !== v.me)];
    const row = (d: DeviceView) =>
      `<div class="m-row" data-k="${esc(d.id)}" role="button" tabindex="0" aria-label="${esc(d.displayName || d.name)}，${d.online ? '在线' : ago(d.lastSeen)}"><div class="tile-m"><div class="tile" style="--c:${d.online ? P[d.owner].c : 'var(--offline-grey)'}">${G.device(glyphOf(d.kind), 22)}</div></div>` +
      `<div><b>${esc(spaced(d.displayName || d.name))}</b><small>${d.online ? '在线' : esc(ago(d.lastSeen))}</small></div></div>`;
    const empty = `<div class="m-row pending" data-k="self"><div class="tile-m"><div class="tile" style="--c:${P[v.me].c}">${DASH}${G.device('laptop', 22)}</div></div><div><b>我的电脑</b><small>在电脑上装好 Deneb，就会出现在这里</small></div></div>`;
    (page.querySelector('.m-list') as HTMLElement).innerHTML = order
      .map((k) => {
        const mine = devs.filter((d) => d.owner === k);
        if (!mine.length && k !== v.me) return '';
        return `<div class="m-gh">${App.ball(k)}${esc(spaced(nameOf(v, k)))}</div><div class="m-card">${mine.length ? mine.map(row).join('') : empty}</div>`;
      })
      .join('');
    for (const r of page.querySelectorAll('.m-row:not(.pending)')) {
      const open = () => openCard(v, (r as HTMLElement).dataset.k as string, lan);
      r.addEventListener('click', open);
      r.addEventListener('keydown', (e) => (e as KeyboardEvent).key === 'Enter' && open());
    }
  }

  // The card, as the desktop's (§14.6 "看和管"): for one of mine, a switch
  // for each service it opens to the LAN, and who may use it, a click on a
  // ball turning them away (and back).
  function openCard(v: UserView, id: string, lan: LANView): void {
    const d = v.devices.find((x) => x.id === id);
    if (!d) return;
    const mine = d.owner === v.me;
    const svcs = lan.devices.find((ld) => ld.device === id)?.services || [];
    const rows = svcs.map((s, i) =>
      mine
        ? `<div class="row">${G.ui(serviceIcon(s.kind), 18)}<span>${esc(serviceName(s))}</span><button class="sw ${s.on ? 'on' : ''}" data-i="${i}" role="switch" aria-checked="${s.on}" aria-label="${esc(serviceName(s))}"></button></div>`
        : s.on
          ? `<div class="row">${G.ui(serviceIcon(s.kind), 18)}<span>${esc(serviceName(s))}</span></div>`
          : `<div class="row na">${G.ui(serviceIcon(s.kind), 18)}<span>${esc(serviceName(s))}</span><em>没开放</em></div>`,
    );
    const refused = new Set(d.refused || []);
    const others = lan.members.filter((m) => m.state === 'joined' && m.person !== v.me).map((m) => m.person);
    const who = mine && others.length ? `<div class="label">谁能用</div><div class="people">${others.map((k) => `<button class="chip ${refused.has(k) ? 'blocked' : ''}" type="button" data-k="${esc(k)}" aria-pressed="${refused.has(k)}">${App.ball(k, refused.has(k) ? 'off' : '')}${esc(spaced(nameOf(v, k)))}</button>`).join('')}</div><div class="hint">点一下头像，就不让他用了</div>` : '';
    const s = rise(
      `<div class="top"><div class="tile-s"><div class="tile" style="--c:${P[d.owner].c}">${G.device(glyphOf(d.kind), 20)}</div></div><div><b>${esc(spaced(d.displayName || d.name))}</b><span>${mine ? '我的设备' : esc(spaced(nameOf(v, d.owner) + '的设备'))}</span></div><button class="icon-btn sheet-close" type="button" aria-label="关闭">${G.ui('close', 16)}</button></div>` +
        `<div class="status"><span class="badge" style="--c:${d.online ? '#22C55E' : '#a1a1aa'}">${G.ui('online', 16)}</span><div>${d.online ? '在线' : esc(ago(d.lastSeen))}<small>${mine ? '别人能用你开放的服务' : '在装了 Deneb 的电脑上就能用'}</small></div></div>` +
        (rows.length ? `<div class="label">${mine ? '开放给大家的' : '开放给你的'}</div><div class="rows">${rows.join('')}</div>` : '') +
        who,
    );
    const said = (e: unknown) => {
      // The dock's line says it for a moment: the phone has no toasts
      // over its list (§14.6).
      const t = page.querySelector('.m-dock-text') as HTMLElement | null;
      if (!t) return;
      const was = t.innerHTML;
      t.textContent = e instanceof ApiError ? e.message : '没能改，再试一次';
      setTimeout(() => (t.innerHTML = was), 3000);
    };
    for (const sw of s.querySelectorAll('.sw[data-i]') as NodeListOf<HTMLElement>)
      sw.onclick = () => {
        const svc = svcs[+(sw.dataset.i as string)];
        const on = !sw.classList.contains('on');
        sw.classList.toggle('on', on);
        sw.setAttribute('aria-checked', String(on));
        ctx.op('service.set', { lan: lanID, device: id, service: { kind: svc.kind, name: svc.name, ports: svc.ports }, on }).catch((e) => {
          sw.classList.toggle('on', !on);
          said(e);
        });
      };
    for (const ch of s.querySelectorAll('.chip[data-k]') as NodeListOf<HTMLElement>)
      ch.onclick = () => {
        const k = ch.dataset.k as string;
        const b = !ch.classList.contains('blocked');
        ch.classList.toggle('blocked', b);
        ch.querySelector('.ball')?.classList.toggle('off', b);
        ch.setAttribute('aria-pressed', String(b));
        ctx.op('device.refuse', { device: id, person: k, refuse: b }).catch((e) => {
          ch.classList.toggle('blocked', !b);
          ch.querySelector('.ball')?.classList.toggle('off', !b);
          said(e);
        });
      };
  }
  // My LANs and my devices' pages, to go to another.
  (page.querySelector('.m-title') as HTMLElement).addEventListener('click', () => switcher(rise, ctx, 'lan:' + lanID));

  draw(ctx.view());
  const off = ctx.onView(draw);
  return () => {
    off();
    page.remove();
  };
}

// ---- Shared by the phone's pages ----------------------------------------------------------

// The card rises from the bottom, as the desktop's opens from its corner;
// the scrim or the ✕ puts it away.
function host(page: HTMLElement): { rise: (html: string) => HTMLElement; close: (now?: boolean) => void } {
  const scrim = page.querySelector('.m-scrim') as HTMLElement;
  let sheet: HTMLElement | null = null;
  function close(now = false): void {
    if (!sheet) return;
    const s = sheet;
    sheet = null;
    if (now) return void s.remove();
    s.animate([{ transform: 'none' }, { transform: 'translateY(100%)' }], { duration: 300, easing: 'cubic-bezier(0.4, 0, 1, 1)', fill: 'forwards' });
    scrim.classList.remove('on');
    scrim.animate([{ opacity: 1 }, { opacity: 0 }], { duration: 300, easing: 'ease-in', fill: 'forwards' });
    setTimeout(() => s.remove(), 310);
  }
  function rise(html: string): HTMLElement {
    close(true);
    sheet = App.h(`<div class="sheet m-sheet" role="dialog">${html}</div>`) as HTMLElement;
    page.appendChild(sheet);
    sheet.animate([{ transform: 'translateY(100%)' }, { transform: 'none' }], { duration: 420, easing: 'cubic-bezier(0.2, 0.8, 0.2, 1)' });
    scrim.classList.add('on');
    scrim.animate([{ opacity: 0 }, { opacity: 1 }], { duration: 300, easing: 'ease-out', fill: 'forwards' });
    sheet.querySelector('.sheet-close')?.addEventListener('click', () => close());
    return sheet;
  }
  scrim.addEventListener('click', () => close());
  return { rise, close };
}

// The title's list: my LANs, then 我的设备 and 共享给我 (the sidebar's,
// which a phone has no room for).
function switcher(rise: (html: string) => HTMLElement, ctx: Ctx, on: string): void {
  const v = ctx.view();
  const row = (id: string, label: string) => `<button class="row m-lan-row" type="button" data-k="${esc(id)}"><span>${esc(spaced(label))}</span>${id === on ? G.ui('check', 16) : ''}</button>`;
  const lans = listedLANs(v);
  const s = rise(
    (lans.length ? `<div class="label">我的局域网</div><div class="rows">${lans.map((l) => row('lan:' + l.id, l.name)).join('')}</div>` : '') +
      `<div class="label">设备</div><div class="rows">${row('mine', '我的设备')}${row('shared', '共享给我')}</div>`,
  );
  for (const b of s.querySelectorAll('.m-lan-row'))
    b.addEventListener('click', () => {
      const k = (b as HTMLElement).dataset.k as string;
      ctx.go(k.startsWith('lan:') ? '/lan/' + encodeURIComponent(k.slice(4)) : '/' + k);
    });
}

// A page of the phone's own: a title that opens the switcher, a line, the
// list, the scrim and the download dock.
function shell(root: HTMLElement, cls: string): HTMLElement {
  const page = document.createElement('div');
  page.className = 'm-lan ' + cls;
  root.replaceChildren(page);
  page.innerHTML = `<div class="m-head"><button class="m-title" type="button" aria-haspopup="dialog"></button><span class="stack"></span></div><div class="m-sub"><span class="dot"></span><span class="m-sub-text"></span></div><div class="m-list" role="list"></div><div class="m-scrim"></div>`;
  page.appendChild(phoneDock());
  return page;
}
const line = (page: HTMLElement, tone: string, text: string) => {
  (page.querySelector('.dot') as HTMLElement).dataset.tone = tone;
  (page.querySelector('.m-sub-text') as HTMLElement).textContent = text;
};
const tileRow = (id: string, color: string, kind: string, name: string, sub: string) =>
  `<div class="m-row" data-k="${esc(id)}" role="button" tabindex="0" aria-label="${esc(name)}，${esc(sub)}"><div class="tile-m"><div class="tile" style="--c:${color}">${G.device(glyphOf(kind), 22)}</div></div><div><b>${esc(spaced(name))}</b><small>${esc(sub)}</small></div></div>`;
const status = (online: boolean, t: string | undefined, s: string) =>
  `<div class="status"><span class="badge" style="--c:${online ? '#22C55E' : '#a1a1aa'}">${G.ui('online', 16)}</span><div>${online ? '在线' : esc(ago(t))}<small>${esc(s)}</small></div></div>`;
function onRows(page: HTMLElement, open: (id: string) => void): void {
  for (const r of page.querySelectorAll('.m-row:not(.pending)')) {
    const go = () => open((r as HTMLElement).dataset.k as string);
    r.addEventListener('click', go);
    r.addEventListener('keydown', (e) => (e as KeyboardEvent).key === 'Enter' && go());
  }
}

// 共享给我 on a phone (DESIGN.md §14.11): by who gave them, each row whether
// it is online; the card says what I may use, and that it is one way.
// Letting one go (不要了, with undo) is the computer's page's.
export function phoneShared(root: HTMLElement, ctx: Ctx): () => void {
  const page = shell(root, 'm-shared');
  const P = App.PEOPLE;
  const { rise } = host(page);
  const fromOf = (v: UserView, s: SharedDevice) => s.fromLAN || nameOf(v, s.from) || '别人';
  function draw(v: UserView): void {
    people(v);
    const title = page.querySelector('.m-title') as HTMLElement;
    title.innerHTML = `${esc(spaced('共享给我'))}${G.ui('chevron', 16)}`;
    const list = v.sharedWithMe || [];
    const froms = [...new Set(list.map((s) => s.from))];
    (page.querySelector('.stack') as HTMLElement).innerHTML = froms.map((k) => App.ball(k)).join('');
    const on = list.filter((s) => s.device.online).length;
    const member = v.lans.find((l) => sharedOnly(v, l) && !l.pending);
    if (!list.length) line(page, 'off', member ? `已加入${member.name}，${nameOf(v, member.owner)}分给你的设备会出现在这里` : '别人分给你的设备会出现在这里');
    else line(page, on === list.length ? 'ok' : 'off', on === list.length ? (list.length === 1 ? '1 台设备，在线' : `${list.length} 台设备都在线`) : `${list.length} 台设备，${on} 台在线`);
    const groups = [...new Set(list.map((s) => fromOf(v, s)))];
    (page.querySelector('.m-list') as HTMLElement).innerHTML = groups
      .map((g) => {
        const in_ = list.filter((s) => fromOf(v, s) === g);
        return `<div class="m-gh">${App.ball(in_[0].from)}${esc(spaced(g))}</div><div class="m-card">${in_.map((s) => tileRow(s.device.id, s.device.online ? P[s.from].c : 'var(--offline-grey)', s.device.kind, s.device.displayName || s.device.name, s.device.online ? '在线' : ago(s.device.lastSeen))).join('')}</div>`;
      })
      .join('');
    onRows(page, (id) => {
      const s = list.find((x) => x.device.id === id);
      if (!s) return;
      const rows = s.services.map((x) => `<div class="row">${G.ui(serviceIcon(x.kind), 18)}<span>${esc(serviceName(x))}</span></div>`);
      rise(
        `<div class="top"><div class="tile-s"><div class="tile" style="--c:${P[s.from].c}">${G.device(glyphOf(s.device.kind), 20)}</div></div><div><b>${esc(spaced(s.device.displayName || s.device.name))}</b><span>${esc(spaced(s.fromLAN ? s.fromLAN + '的设备' : nameOf(v, s.from) + '分享给你的'))}</span></div><button class="icon-btn sheet-close" type="button" aria-label="关闭">${G.ui('close', 16)}</button></div>` +
          status(s.device.online, s.device.lastSeen, '在装了 Deneb 的电脑上就能用') +
          (rows.length ? `<div class="label">你可以用</div><div class="rows">${rows.join('')}</div>` : '') +
          `<div class="hint">只有你能连它，反过来不行</div>`,
      );
    });
  }
  (page.querySelector('.m-title') as HTMLElement).addEventListener('click', () => switcher(rise, ctx, 'shared'));
  draw(ctx.view());
  const off = ctx.onView(draw);
  return () => {
    off();
    page.remove();
  };
}

// 我的设备 on a phone: my devices, each whether it is online; the card
// says which LANs it is in and whom it is shared with (changing those is
// the computer's page's).
export function phoneMine(root: HTMLElement, ctx: Ctx): () => void {
  const page = shell(root, 'm-mine');
  const P = App.PEOPLE;
  const { rise } = host(page);
  const DASH = `<svg class="dash" viewBox="0 0 40 40" width="40" height="40" aria-hidden="true"><rect x="0.75" y="0.75" width="38.5" height="38.5" rx="11.25" fill="none" stroke="currentColor" stroke-width="1.5" stroke-dasharray="4 3"/></svg>`;
  function draw(v: UserView): void {
    people(v);
    (page.querySelector('.m-title') as HTMLElement).innerHTML = `${esc(spaced('我的设备'))}${G.ui('chevron', 16)}`;
    const ds = v.devices.filter((d) => d.owner === v.me && !d.publicLAN);
    const on = ds.filter((d) => d.online).length;
    if (!ds.length) line(page, 'off', '装好 Deneb 的电脑会出现在这里');
    else line(page, on === ds.length ? 'ok' : 'off', on === ds.length ? (ds.length === 1 ? '1 台设备，在线' : `${ds.length} 台设备都在线`) : `${ds.length} 台设备，${on} 台在线`);
    const empty = `<div class="m-row pending" data-k="self"><div class="tile-m"><div class="tile" style="--c:${P[v.me].c}">${DASH}${G.device('laptop', 22)}</div></div><div><b>我的电脑</b><small>在电脑上装好 Deneb，就会出现在这里</small></div></div>`;
    (page.querySelector('.m-list') as HTMLElement).innerHTML = `<div class="m-card">${ds.length ? ds.map((d) => tileRow(d.id, d.online ? P[v.me].c : 'var(--offline-grey)', d.kind, d.displayName || d.name, d.online ? '在线' : ago(d.lastSeen))).join('') : empty}</div>`;
    onRows(page, (id) => {
      const d = ds.find((x) => x.id === id);
      if (!d) return;
      const lans = v.lans.filter((l) => d.lans.includes(l.id));
      const shares = (d.shares || []).filter((s) => s.person !== v.me);
      rise(
        `<div class="top"><div class="tile-s"><div class="tile" style="--c:${P[v.me].c}">${G.device(glyphOf(d.kind), 20)}</div></div><div><b>${esc(spaced(d.displayName || d.name))}</b><span>我的设备</span></div><button class="icon-btn sheet-close" type="button" aria-label="关闭">${G.ui('close', 16)}</button></div>` +
          status(d.online, d.lastSeen, '别人能用你开放的服务') +
          `<div class="label">局域网</div>` +
          (lans.length ? `<div class="rows">${lans.map((l) => `<div class="row"><span>${esc(spaced(l.name))}</span></div>`).join('')}</div>` : `<p class="say">还没加进局域网</p>`) +
          (shares.length ? `<div class="label">分享给</div><div class="rows">${shares.map((s) => `<div class="row">${App.ball(s.person)}<span>${esc(spaced(nameOf(v, s.person)))}</span></div>`).join('')}</div><div class="hint">${esc(spaced(shares.length === 1 ? `只有${nameOf(v, shares[0].person)}能连你，反过来不行` : '只有他们能连你，反过来不行'))}</div>` : ''),
      );
    });
  }
  (page.querySelector('.m-title') as HTMLElement).addEventListener('click', () => switcher(rise, ctx, 'mine'));
  draw(ctx.view());
  const off = ctx.onView(draw);
  return () => {
    off();
    page.remove();
  };
}
