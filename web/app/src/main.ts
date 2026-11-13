// The Web app's entry (DESIGN.md §14): the theme, the session and the
// stream of the user's view, and the pages by path. The invite page and the
// login are here (join.ts); the LAN canvas, scene 4's console and the relays
// are pages the canvas modules register (registerPage), drawn in the
// desktop's look; until one is registered, a plain stand-in shows the page's
// contents so that every path works.
import { api, ApiError, stream, whenLoggedOut } from './api.ts';
import { deletingAccount } from './account.ts';
import type { StreamState } from './api.ts';
import type { DeviceView, LANView, PersonID, UserView } from './model.ts';
import { listedLANs, sharedOnly } from './model.ts';
import { current, go, onRoute, parse } from './router.ts';
import type { Route } from './router.ts';
import { mountJoin, mountLogin, mountLoginLink } from './join.ts';
import { detectPlatform, downloadCard, esc, phoneDock, pickExample, spaced } from './download.ts';
import { after, recording } from './clock.ts';

// ---- Theme: the system's, or ?theme= (the recorder sets it) -------------------------------
const query = new URLSearchParams(location.search);
const dark = matchMedia('(prefers-color-scheme: dark)');
function applyTheme(): void {
  const t = query.get('theme');
  document.documentElement.dataset.theme = t === 'dark' || t === 'light' ? t : dark.matches ? 'dark' : 'light';
}
applyTheme();
dark.addEventListener('change', applyTheme);

// ---- Session and view ------------------------------------------------------------------
export interface Ctx {
  view(): UserView; // the latest view
  onView(fn: (v: UserView) => void): () => void; // called on every new one
  streamState(): StreamState;
  onStreamState(fn: (s: StreamState) => void): () => void; // the stream down and back (the frame says so)
  op: typeof api.op;
  go: typeof go;
  email?: string; // the email logged in with, this visit (the download card's hint)
  justJoined?: string; // the LAN joined a moment ago: the page opens the download card
  phone(): boolean; // narrower than 768: the phone's layout (§14.6)
}

type Page = (root: HTMLElement, ctx: Ctx, route: Route) => () => void;
const pages = new Map<Route['page'], Page>();

// registerPage puts a module's page in place of the stand-in: 'lan',
// 'share', 'relays', 'ops'. Registered after the first render, the page
// shows at once.
export function registerPage(name: Route['page'], page: Page): void {
  pages.set(name, page);
  if (current().page === name) render(current());
}

let view: UserView | null = null;
let state: StreamState = 'retrying';
let stopStream: (() => void) | null = null;
const viewFns = new Set<(v: UserView) => void>();
const stateFns = new Set<(s: StreamState) => void>();
let email: string | undefined;
let justJoined: string | undefined;
// Signed in a moment ago: the page that comes fades in over the login
// page's background (DESIGN §14.14).
let signedIn = false;

const phoneQuery = matchMedia('(max-width: 767px)');
const ctx: Ctx = {
  view: () => view as UserView,
  onView(fn) {
    viewFns.add(fn);
    return () => viewFns.delete(fn);
  },
  streamState: () => state,
  onStreamState(fn) {
    stateFns.add(fn);
    return () => stateFns.delete(fn);
  },
  op: api.op,
  go,
  get email() {
    return email;
  },
  get justJoined() {
    return justJoined;
  },
  phone: () => phoneQuery.matches,
};

function setView(v: UserView): void {
  if (view && v.version < view.version) return;
  view = v;
  for (const fn of viewFns) fn(v);
}

// session loads the view and keeps it fresh; null when logged out.
async function session(): Promise<UserView | null> {
  if (view) return view;
  try {
    setView(await api.userView());
  } catch (err) {
    if (err instanceof ApiError && err.status === 401) return null;
    throw err;
  }
  stopStream?.();
  stopStream = stream(view!.version, setView, (s) => {
    state = s;
    for (const fn of stateFns) fn(s);
  });
  return view;
}

// safeNext is where a login goes on to (DESIGN §14.14): a page of the app's
// own, never the login page itself, an invite (which has its own way in) or
// another site.
export function safeNext(next?: string): string {
  if (!next || !next.startsWith('/') || next.startsWith('//')) return '/';
  const q = next.indexOf('?');
  const page = parse(q < 0 ? next : next.slice(0, q), q < 0 ? '' : next.slice(q)).page;
  return page === 'login' || page === 'loginLink' || page === 'join' || page === 'notFound' ? '/' : next;
}

// toLogin sends a visit without a session to the login page, to come back
// to path after. Whoever finds the session gone first sends it, once: the
// stream's 401 and a page's own check both can, and the second must not
// send it from the login page to itself (it came back to /login).
function toLogin(path: string): void {
  const here = current().page;
  if (here === 'login' || here === 'loginLink' || here === 'join') return;
  const back = safeNext(path);
  go(back === '/' ? '/login' : '/login?next=' + encodeURIComponent(back), true);
}

whenLoggedOut(() => {
  stopStream?.();
  stopStream = null;
  view = null;
  // The account just deleted: its page goes to the login itself, once it
  // faded (account.ts, DESIGN §6.15).
  if (deletingAccount()) return;
  toLogin(location.pathname + location.search);
});

// ---- Pages ---------------------------------------------------------------------------------
const root = document.getElementById('app') as HTMLElement;
let unmount: (() => void) | null = null;
let rendering = 0;

async function render(r: Route): Promise<void> {
  const n = ++rendering;
  unmount?.();
  unmount = null;
  const mount = (fn: () => () => void) => {
    if (n !== rendering) return;
    unmount = fn();
    if (signedIn) {
      // Hidden until the clock's next tick, then in: on the clock, as all
      // that moves on the page (clock.ts).
      signedIn = false;
      root.style.opacity = '0';
      after(0, () => {
        root.style.opacity = '';
        root.animate([{ opacity: 0 }, { opacity: 1 }], { duration: 260, easing: 'ease-out' });
      });
    }
  };
  switch (r.page) {
    case 'join': {
      const v = await session().catch(() => null);
      return mount(() =>
        mountJoin(root, {
          token: r.token,
          me: v?.me,
          onJoined: async (lan, _me: PersonID, mail) => {
            email = mail ?? email;
            justJoined = lan;
            view = null; // a new person, or a new LAN: load it again
            await session().catch(() => null);
            go('/lan/' + encodeURIComponent(lan));
          },
          onOpen: (lan) => go('/lan/' + encodeURIComponent(lan)),
        }),
      );
    }
    case 'login': {
      mount(() =>
        mountLogin(root, {
          next: r.next,
          // Signed in: the view loads while the tick shows; then the login
          // page fades out and the app comes in, with no blank between.
          onDone: async (_me, mail) => {
            email = mail;
            view = null;
            await session().catch(() => null);
            return () => {
              signedIn = true;
              go(safeNext(r.next), true);
            };
          },
        }),
      );
      // Signed in already (another tab, a bookmark): on to where it goes.
      session().then(
        (v) => v && n === rendering && current().page === 'login' && go(safeNext(r.next), true),
        () => {},
      );
      return;
    }
    case 'loginLink':
      // The link in the code's mail (login-link.md): a click logs in, and
      // on to the first page, as after the code.
      return mount(() =>
        mountLoginLink(root, {
          session: () => session().then((v) => (v ? (v.people.find((p) => p.id === v.me)?.email ?? '') : null)),
          enter: () => {
            signedIn = true;
            go('/', true);
          },
          onDone: async (_me, mail) => {
            email = mail;
            view = null;
            await session().catch(() => null);
            return () => {
              signedIn = true;
              go('/', true);
            };
          },
        }),
      );
  }
  // Every other page wants a session.
  let v: UserView | null;
  try {
    v = await session();
  } catch {
    return mount(() => message('连不上服务器', '检查一下网络，过一会儿刷新'));
  }
  if (!v) return toLogin(location.pathname + location.search);
  if (r.page === 'home') {
    const first = listedLANs(v)[0];
    if (first) return go('/lan/' + encodeURIComponent(first.id), true);
    // Only what others share with me (a studio's member): 共享给我.
    if (v.sharedWithMe?.length || v.lans.some((l) => sharedOnly(v!, l))) return go('/shared', true);
    if (v.lans[0]) return go('/lan/' + encodeURIComponent(v.lans[0].id), true);
    // No LAN yet: the first page, in the frame of every page (welcome.ts,
    // DESIGN §14.14).
    await pagesReady;
    if (n !== rendering) return;
    const welcome = pages.get('home');
    return mount(() => (welcome ? welcome(root, ctx, r) : message('你还没有局域网', '朋友发来的邀请链接，在这里打开就能加入')));
  }
  // The deployment's console, its public relays among it, is its operators'
  // only: for anyone else there is no such page (DESIGN §14.15).
  if ((r.page === 'console' || r.page === 'ops') && !v.operator) return mount(() => message('没有这个页面', '回到首页看看'));
  if (r.page === 'notFound') return mount(() => message('没有这个页面', '回到首页看看'));
  // The canvas pages come with pages.ts; wait for it rather than flash the
  // stand-in first.
  await pagesReady;
  if (n !== rendering) return;
  const page = pages.get(r.page);
  mount(() => (page ? page(root, ctx, r) : standIn(r)));
}

function message(title: string, sub: string): () => void {
  const el = document.createElement('div');
  el.className = 'join-page';
  el.innerHTML = `<main class="join-card"><div class="jc-in"><h1 class="jc-title">${esc(title)}</h1><p class="jc-sub">${esc(sub)}</p></div></main>`;
  root.replaceChildren(el);
  return () => el.remove();
}

// standIn shows a page's contents plainly until its canvas module is in:
// the LAN's devices, my empty spot when I have none there, and the download
// card (or the phone's dock).
function standIn(r: Route): () => void {
  const el = document.createElement('div');
  el.className = 'standin';
  root.replaceChildren(el);
  let card: HTMLElement | null = null;
  const draw = (v: UserView) => {
    if (r.page !== 'lan') {
      el.innerHTML = `<h1>${esc(r.page)}</h1><p>这一页由画布模块提供。</p>`;
      return;
    }
    const lan: LANView | undefined = v.lans.find((l) => l.id === r.lan);
    if (!lan) {
      el.innerHTML = `<h1>没有这个局域网</h1>`;
      return;
    }
    const devs = lan.devices.map((d) => v.devices.find((x) => x.id === d.device)).filter(Boolean) as DeviceView[];
    const mine = devs.filter((d) => d.owner === v.me);
    const online = devs.filter((d) => d.online).length;
    const people = lan.members.filter((m) => m.state === 'joined').length;
    el.innerHTML =
      `<header><h1>${esc(lan.name)}</h1><p>${people} 个人，${devs.length === 0 ? '还没有设备' : `${devs.length} 台设备${online === devs.length ? '都在线' : `，${online} 台在线`}`}</p></header>` +
      `<ul class="standin-devs">` +
      (mine.length ? '' : `<li class="standin-slot" data-k="self"><b>这台电脑</b><small>装上 Deneb 就能加进来</small></li>`) +
      devs.map((d) => `<li data-k="${esc(d.id)}"><b>${esc(spaced(d.displayName))}</b><small>${d.online ? '在线' : '不在线'}</small></li>`).join('') +
      `</ul>`;
    if (!mine.length && !card) {
      if (ctx.phone() || detectPlatform() == null) card = phoneDock();
      else {
        card = downloadCard({ lanName: lan.name, example: pickExample(devs, v.me), email, onClose: () => card?.remove() });
        card.classList.add('standin-sheet');
      }
    }
    if (mine.length && card) {
      card.remove();
      card = null;
    }
    if (card) el.appendChild(card);
  };
  if (view) draw(view);
  const off = ctx.onView(draw);
  return () => {
    off();
    el.remove();
  };
}

// The canvas modules (pages.ts: the LAN, scene 4's console, the relays)
// register their pages; loaded lazily, so that they can import this module
// back. Without them every page still works, on the stand-in.
const pagesReady: Promise<unknown> = import('./pages.ts').catch(() => {});

onRoute((r) => void render(r));
void render(current());

// The recorder waits for this before it starts (design/tools/record.mjs).
if (recording) window.__ready = true;
