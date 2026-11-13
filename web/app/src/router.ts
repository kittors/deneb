// The app's pages are paths (deneb-web answers every one with the app):
//
//	/i/<token>, /join/<token>  an invite link (the server's links are /i/)
//	/login                     log in
//	/login/link#<token>        the link in the code's mail (login-link.md)
//	/                          home: the first LAN, or log in
//	/lan/<id>                  a LAN, without "this computer" (§14.4)
//	/share/<lan>               scene 4's console: the studio's devices to members
//	/mine                      my devices, wherever they are
//	/shared                    共享给我: the devices others share with me
//	/relays                    relays: the public ones, and my own
//	/ops                       the deployment's console (its operators only): 概况
//	/ops/people, /ops/devices, /ops/log   its 用户, 设备, 记录
//	/ops/relays                the public relays (M4-13)
export type Route =
  | { page: 'join'; token: string }
  | { page: 'login'; next?: string }
  | { page: 'loginLink' }
  | { page: 'home' }
  | { page: 'lan'; lan: string }
  | { page: 'share'; lan?: string }
  | { page: 'mine' }
  | { page: 'shared' }
  | { page: 'relays' }
  | { page: 'ops' }
  | { page: 'console'; part: 'overview' | 'people' | 'devices' | 'log' }
  | { page: 'notFound'; path: string };

export function parse(path: string, search = ''): Route {
  const parts = path.split('/').filter(Boolean).map((p) => decodeURIComponent(p));
  const q = new URLSearchParams(search);
  switch (parts[0]) {
    case undefined:
      return { page: 'home' };
    case 'i':
    case 'join':
      return parts[1] ? { page: 'join', token: parts[1] } : { page: 'notFound', path };
    case 'login':
      if (parts[1] === 'link' && !parts[2]) return { page: 'loginLink' };
      return { page: 'login', next: q.get('next') ?? undefined };
    case 'lan':
      return parts[1] ? { page: 'lan', lan: parts[1] } : { page: 'home' };
    case 'share':
      return { page: 'share', lan: parts[1] };
    case 'mine':
      return { page: 'mine' };
    case 'shared':
      return { page: 'shared' };
    case 'relays':
      return { page: 'relays' };
    case 'ops':
      if (parts[1] === 'relays') return { page: 'ops' };
      if (parts[1] === 'people' || parts[1] === 'devices' || parts[1] === 'log') return { page: 'console', part: parts[1] };
      return { page: 'console', part: 'overview' };
  }
  return { page: 'notFound', path };
}

export function current(): Route {
  return parse(location.pathname, location.search);
}

const listeners = new Set<(r: Route) => void>();

// go shows another page, as a link would, keeping the query (?theme=,
// ?record for the recorder).
export function go(path: string, replace = false): void {
  const url = path + (path.includes('?') ? '' : keep());
  if (replace) history.replaceState(null, '', url);
  else history.pushState(null, '', url);
  const r = current();
  for (const fn of listeners) fn(r);
}

function keep(): string {
  const q = new URLSearchParams(location.search);
  const kept = new URLSearchParams();
  for (const k of ['theme', 'record']) if (q.has(k)) kept.set(k, q.get(k) ?? '');
  const s = kept.toString();
  return s ? '?' + s : '';
}

export function onRoute(fn: (r: Route) => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

addEventListener('popstate', () => {
  const r = current();
  for (const fn of listeners) fn(r);
});
