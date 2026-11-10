// The control server's Web API (/api/v1, proto/web.go), on this page's own
// origin (deneb-web passes it on). The session is a cookie the browser
// keeps; every write is JSON, which the server's cross-site check expects.
import { Paths } from './model.ts';
import type {
  ErrorBody, ErrorCode, InvitePreview, OpArgs, OpResult, OpType, RelayUsage, StreamFrame, UserView, WebLoginResult,
} from './model.ts';

// ApiError is an answer the server refused, with its code and its words
// for people ("已经有了", "6 个位置都用上了").
export class ApiError extends Error {
  status: number;
  code: ErrorCode;
  constructor(status: number, code: ErrorCode, message: string) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
  }
}

let onUnauthorized: () => void = () => {};
// whenLoggedOut is called when the server answers 401: the session is gone.
export function whenLoggedOut(fn: () => void): void {
  onUnauthorized = fn;
}

async function call<T>(method: 'GET' | 'POST', path: string, body?: unknown): Promise<T> {
  const init: RequestInit = { method, credentials: 'same-origin', cache: 'no-store' };
  if (method === 'POST') {
    init.headers = { 'Content-Type': 'application/json' };
    init.body = JSON.stringify(body ?? {});
  }
  let res: Response;
  try {
    res = await fetch(path, init);
  } catch {
    throw new ApiError(0, 'internal', '连不上服务器，检查一下网络');
  }
  const text = await res.text();
  let data: unknown;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = undefined;
    }
  }
  if (!res.ok) {
    const e = (data as ErrorBody | undefined)?.error;
    if (res.status === 401) onUnauthorized();
    throw new ApiError(res.status, e?.code ?? 'internal', e?.message ?? (res.status >= 500 ? '服务器出了点问题，过一会儿再试' : '没能做成'));
  }
  return data as T;
}

// An op's id makes a retry safe: the server applies an id once.
function opID(): string {
  const b = new Uint8Array(12);
  crypto.getRandomValues(b);
  return 'w' + Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('');
}

export const api = {
  loginEmail: (email: string) => call<WebLoginResult>('POST', Paths.loginEmail, { email }),
  loginCode: (email: string, code: string) => call<WebLoginResult>('POST', Paths.loginCode, { email, code }),
  logout: () => call<unknown>('POST', Paths.logout),
  // invite tells what a link is for, logged in or not.
  invite: (token: string) => call<InvitePreview>('GET', Paths.invites + encodeURIComponent(token)),
  userView: () => call<UserView>('GET', Paths.userView),
  op: (op: OpType, args: OpArgs = {}) => call<OpResult>('POST', Paths.ops, { id: opID(), op, args }),
  relayUsage: () => call<RelayUsage>('GET', Paths.relayUsage),
  // The mock's stand-ins (deneb-web -mock) for what happens away from the
  // browser: Deneb installed on this computer, a relay set up.
  mock: {
    deviceUp: () => call<{ device: string }>('POST', '/api/v1/_mock/device-up'),
    relayUp: (join?: string) => call<{ relay: string }>('POST', '/api/v1/_mock/relay-up', { join: join ?? '' }),
  },
};

export type StreamState = 'live' | 'retrying' | 'loggedOut';

// stream keeps the view fresh: the server sends a new one whenever it
// changes (and at once if ours is older than since). A broken stream comes
// back after 1, 2 and then every 4 seconds. It returns a stop function.
export function stream(since: number, onView: (v: UserView) => void, onState: (s: StreamState) => void = () => {}): () => void {
  let stopped = false;
  let ctl: AbortController | null = null;
  let tries = 0;
  let timer: ReturnType<typeof setTimeout> | undefined;

  const retry = () => {
    if (stopped) return;
    onState('retrying');
    const ms = [1000, 2000, 4000][Math.min(tries, 2)];
    tries++;
    timer = setTimeout(run, ms);
  };

  async function run(): Promise<void> {
    if (stopped) return;
    ctl = new AbortController();
    let res: Response;
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
      let chunk: ReadableStreamReadResult<string>;
      try {
        chunk = await reader.read();
      } catch {
        break;
      }
      if (chunk.done) break;
      buf += chunk.value;
      let nl: number;
      while ((nl = buf.indexOf('\n')) >= 0) {
        const line = buf.slice(0, nl).trim();
        buf = buf.slice(nl + 1);
        if (!line) continue;
        let f: StreamFrame;
        try {
          f = JSON.parse(line) as StreamFrame;
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
