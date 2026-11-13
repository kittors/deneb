// The control server's Web API (/api/v1, proto/web.go), on this page's own
// origin (deneb-web passes it on). The session is a cookie the browser
// keeps; every write is JSON, which the server's cross-site check expects.
import { Paths } from './model.js';
                                                                                                                                                                                    

// ApiError is an answer the server refused, with its code and its words
// for people ("已经有了", "6 个位置都用上了").
export class ApiError extends Error {
  status        ;
  code           ;
  constructor(status        , code           , message        ) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
  }
}

let onUnauthorized             = () => {};
// whenLoggedOut is called when the server answers 401: the session is gone.
export function whenLoggedOut(fn            )       {
  onUnauthorized = fn;
}

async function call   (method                , path        , body          )             {
  const init              = { method, credentials: 'same-origin', cache: 'no-store' };
  if (method === 'POST') {
    init.headers = { 'Content-Type': 'application/json' };
    init.body = JSON.stringify(body ?? {});
  }
  let res          ;
  try {
    res = await fetch(path, init);
  } catch {
    throw new ApiError(0, 'internal', '连不上服务器，检查一下网络');
  }
  const text = await res.text();
  let data         ;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = undefined;
    }
  }
  if (!res.ok) {
    const e = (data                         )?.error;
    if (res.status === 401) onUnauthorized();
    throw new ApiError(res.status, e?.code ?? 'internal', e?.message ?? (res.status >= 500 ? '服务器出了点问题，过一会儿再试' : '没能做成'));
  }
  return data     ;
}

// An op's id makes a retry safe: the server applies an id once.
function opID()         {
  const b = new Uint8Array(12);
  crypto.getRandomValues(b);
  return 'w' + Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('');
}

export const api = {
  loginEmail: (email        ) => call                ('POST', Paths.loginEmail, { email }),
  loginCode: (email        , code        ) => call                ('POST', Paths.loginCode, { email, code }),
  // The link in the code's mail (login-link.md): what it logs in as, using
  // nothing up; then the login, which uses it and the code.
  loginLinkInfo: (token        ) => call                  ('POST', Paths.loginLinkInfo, { token }),
  loginLink: (token        ) => call                ('POST', Paths.loginLink, { token }),
  logout: () => call         ('POST', Paths.logout),
  // invite tells what a link is for, logged in or not.
  invite: (token        ) => call               ('GET', Paths.invites + encodeURIComponent(token)),
  userView: () => call          ('GET', Paths.userView),
  op: (op        , args         = {}) => call          ('POST', Paths.ops, { id: opID(), op, args }),
  relayUsage: () => call            ('GET', Paths.relayUsage),
  operatorUsage: () => call               ('GET', Paths.operatorUsage),
  // The mock's stand-ins (deneb-web -mock) for what happens away from the
  // browser: Deneb installed on this computer, a relay set up.
  mock: {
    deviceUp: () => call                    ('POST', '/api/v1/_mock/device-up'),
    relayUp: (join         ) => call                   ('POST', '/api/v1/_mock/relay-up', { join: join ?? '' }),
  },
};

                                                            

// stream keeps the view fresh: the server sends a new one whenever it
// changes (and at once if ours is older than since). A broken stream comes
// back after 1, 2 and then every 4 seconds. It returns a stop function.
export function stream(since        , onView                       , onState                           = () => {})             {
  let stopped = false;
  let ctl                         = null;
  let tries = 0;
  let timer                                           ;

  const retry = () => {
    if (stopped) return;
    onState('retrying');
    const ms = [1000, 2000, 4000][Math.min(tries, 2)];
    tries++;
    timer = setTimeout(run, ms);
  };

  async function run()                {
    if (stopped) return;
    ctl = new AbortController();
    let res          ;
    try {
      res = await fetch(Paths.stream + '?since=' + since, { credentials: 'same-origin', cache: 'no-store', signal: ctl.signal });
    } catch {
      return retry();
    }
    if (res.status === 401) {
      onState('loggedOut');
      onUnauthorized();
      return;
    }
    if (!res.ok || !res.body) return retry();
    const reader = res.body.pipeThrough(new TextDecoderStream()).getReader();
    let buf = '';
    for (;;) {
      let chunk                                  ;
      try {
        chunk = await reader.read();
      } catch {
        break;
      }
      if (chunk.done) break;
      buf += chunk.value;
      let nl        ;
      while ((nl = buf.indexOf('\n')) >= 0) {
        const line = buf.slice(0, nl).trim();
        buf = buf.slice(nl + 1);
        if (!line) continue;
        let f             ;
        try {
          f = JSON.parse(line)               ;
        } catch {
          continue;
        }
        if (f.loggedOut) {
          stopped = true;
          onState('loggedOut');
          onUnauthorized();
          return;
        }
        if (f.userView) {
          tries = 0;
          since = f.userView.version;
          onState('live');
          onView(f.userView);
        }
      }
    }
    retry();
  }

  void run();
  return () => {
    stopped = true;
    if (timer) clearTimeout(timer);
    ctl?.abort();
  };
}
