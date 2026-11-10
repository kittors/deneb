// The app's pages are paths (deneb-web answers every one with the app):
//
//	/i/<token>, /join/<token>  an invite link (the server's links are /i/)
//	/login                     log in
//	/                          home: the first LAN, or log in
//	/lan/<id>                  a LAN, without "this computer" (§14.4)
//	/share/<lan>               scene 4's console: the studio's devices to members
//	/mine                      my devices, wherever they are
//	/shared                    共享给我: the devices others share with me
//	/relays                    relays: the public ones, and my own
//	/ops                       the deployment's page (its admin only)
                   
                                   
                                    
                    
                                
                                   
                    
                      
                      
                   
                                       

export function parse(path        , search = '')        {
  const parts = path.split('/').filter(Boolean).map((p) => decodeURIComponent(p));
  const q = new URLSearchParams(search);
  switch (parts[0]) {
    case undefined:
      return { page: 'home' };
    case 'i':
    case 'join':
      return parts[1] ? { page: 'join', token: parts[1] } : { page: 'notFound', path };
    case 'login':
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
      return { page: 'ops' };
  }
  return { page: 'notFound', path };
}

export function current()        {
  return parse(location.pathname, location.search);
}

const listeners = new Set                    ();

// go shows another page, as a link would, keeping the query (?theme=,
// ?record for the recorder).
export function go(path        , replace = false)       {
  const url = path + (path.includes('?') ? '' : keep());
  if (replace) history.replaceState(null, '', url);
  else history.pushState(null, '', url);
  const r = current();
  for (const fn of listeners) fn(r);
}

function keep()         {
  const q = new URLSearchParams(location.search);
  const kept = new URLSearchParams();
  for (const k of ['theme', 'record']) if (q.has(k)) kept.set(k, q.get(k) ?? '');
  const s = kept.toString();
  return s ? '?' + s : '';
}

export function onRoute(fn                    )             {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

addEventListener('popstate', () => {
  const r = current();
  for (const fn of listeners) fn(r);
});
