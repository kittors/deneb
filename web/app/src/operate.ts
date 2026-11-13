// The deployment's page, 运营 (DESIGN.md §14.9, §14.12 M4-13; the approved
// design/demos/web/m4-13-ops.html), for its operators only: the public
// relays, as my own ones look on the relays page. A headline over all of
// them; one well, a cell each ("86 个人在用"), the share bar over them
// (relay.setWeight), a card each with who uses it most this month (turned
// away with a click, relay.kick, with undo), and "+ 加一台公共中转站": one
// line to run, after which the new one comes in as standby, until "多用".
// The ones the deployment's seed lists are tried first by every LAN: they
// take no share, and only kicks are the operators' (control §19).
import { frame, clear, follow, App, G, esc, spaced, nameOf } from './shell.ts';
import { D } from './clock.ts';
import { api, ApiError } from './api.ts';
import { hold } from './ops.ts';
import { colorIndex } from './model.ts';
import tokens from './tokens.js';
import type { Ctx } from './main.ts';
import type { Route } from './router.ts';
import type { UserView, Relay, RelayView, RelayMonth, RelayJoinView, PersonID, RelayID, RelayKick, OpType, OpArgs, OpResult, OperatorUsage } from './model.ts';
import { ShareBar, sharesOf, seg, wireSeg, title, addrOf, fmtBytes, fmtRate, fmtUptime, fmtAgo, busy, isBusy, day, RING, orb, head, status, rows, rename, GREEN, AMBER, GREY } from './relaykit.ts';
import type { BarItem } from './relaykit.ts';

type Pub = { r: Relay; v: RelayView };

const FRESH = 10 * 60 * 1000;
const TX = 324;

export function operatePage(root: HTMLElement, ctx: Ctx, _route: Route): () => void {
  if (!ctx.view().operator) {
    // Not an operator (or no more): there is no such page for them.
    ctx.go('/relays', true);
    return () => undefined;
  }
  const app = frame(root, ctx, 'ops');
  clear();
  app.setStack([]);
  const cv: HTMLElement = app.canvas;
  const traced = ['.relays-page .rwell', '.relays-page .rcell', '.relays-page .wseg'];
  app.o.trace = (app.o.trace || []).concat(traced);

  let v: UserView = ctx.view();
  let usage: OperatorUsage | null = null;
  const month = new Map<RelayID, RelayMonth>();
  let made: { id: string; command: string; expiresAt: string; at: number } | null = null;
  let gone = false;
  const weightSent = new Map<RelayID, number>();
  const revoking = new Set<string>(); // commands taken back while their undo is up

  const pubs = (): Pub[] =>
    (v.operator?.relays || [])
      .map((rv) => ({ v: rv, r: v.relays.find((x) => x.id === rv.id) }))
      .filter((p): p is Pub => !!p.r)
      .sort((a, b) => Number(!!b.v.default) - Number(!!a.v.default) || (a.v.since || '').localeCompare(b.v.since || '') || a.r.id.localeCompare(b.r.id));
  const pubOf = (id: string) => pubs().find((p) => p.r.id === id);
  // The ones that share the devices: all but the seed's.
  const shared = () => pubs().filter((p) => !p.v.default);
  const weightOf = (p: Pub) => weightSent.get(p.r.id) ?? p.v.weight;
  const sharesNow = () => sharesOf(shared().map((p) => ({ id: p.r.id, weight: weightOf(p) })));
  const waiting = (): RelayJoinView[] => (v.operator?.joins || []).filter((j) => !j.relay && !revoking.has(j.id) && Date.parse(j.expiresAt) > Date.now());
  let known = new Set(pubs().map((p) => p.r.id));
  const kicksOf = (id: RelayID): RelayKick[] => (month.get(id)?.kicks || []).filter((k) => !k.liftedAt && k.person);
  const stateOf = (p: Pub): 'on' | 'busy' | 'off' => {
    if (!p.v.online) return 'off';
    const l = month.get(p.r.id)?.load;
    return isBusy(l?.cpu, l?.full || p.v.full) ? 'busy' : 'on';
  };

  let chain: Promise<unknown> = Promise.resolve();
  const send = (op: OpType, args: OpArgs): Promise<OpResult> => {
    const p = chain.then(() => ctx.op(op, args));
    chain = p.catch(() => undefined);
    return p;
  };
  const failed = (e: unknown, text: string) => app.toast(e instanceof ApiError ? e.message : text, { parent: cv, x: TX });

  // ---- The headline: all of them in a line ----------------------------------------------------
  function headline(): void {
    const ps = pubs();
    const off = ps.filter((p) => !p.v.online).length;
    const now = usage ? ` · 现在 ${usage.people} 个人、${usage.devices} 台设备在经它们中转` : '';
    const up = !ps.length ? '还没有公共中转站' : off ? `${ps.length} 个公共中转站，${off} 个连不上` : ps.length === 1 ? '1 个公共中转站，在线' : `${ps.length} 个公共中转站都在线`;
    app.setTitle('公共中转站', spaced(up + now), off ? 'warn' : 'ok');
  }

  // ---- The well -----------------------------------------------------------------------
  const page: HTMLElement = App.h(
    `<div class="relays-page">` +
      `<section class="rwell ops" data-k="pub" aria-label="公共中转站"><div class="card-label"><span>公共中转站</span><span class="n-pub"></span></div><div class="wbar-slot"></div><div class="rgrid g-pub" role="list"></div></section>` +
      `</div>`,
  );
  cv.appendChild(page);
  const $ = (s: string, r: ParentNode = page) => r.querySelector(s) as HTMLElement;
  const subOf = (p: Pub): string => {
    if (!p.v.online) return '连不上';
    // The numbers not here yet: the line waits for them (no word to take back).
    if (!usage) return '\u00a0';
    const n = month.get(p.r.id)?.load?.people ?? 0;
    if (n) return `${n} 个人在用`;
    return p.v.since && Date.now() - Date.parse(p.v.since) < FRESH ? '刚上线' : '还没有人在用';
  };
  const cell = (p: Pub) =>
    `<div class="rcell" data-k="${esc(p.r.id)}" role="button" tabindex="0" aria-label="${esc(spaced(`${title(p.r)}，公共中转站，${subOf(p)}`))}">${orb(stateOf(p))}<div class="rname">${esc(spaced(title(p.r)))}</div><small>${esc(spaced(subOf(p)))}</small></div>`;
  const addCell = () =>
    waiting().length || made
      ? `<div class="rcell add-cell" data-k="join" role="button" tabindex="0" aria-label="等待安装：在服务器上运行的那一行命令还没跑"><div class="orb add wait">${RING()}${G.ui('relayLine', 18)}</div><div>等待安装<small>命令还没跑</small></div></div>`
      : `<div class="rcell add-cell" data-k="add" role="button" tabindex="0" aria-label="加一台公共中转站：一行命令装好"><div class="orb add">${RING()}${G.ui('plus', 18)}</div><div>加一台公共中转站<small>一行命令装好</small></div></div>`;

  // ---- The share bar (as M4-11's, over the public ones) -----------------------------------
  const barSay = (act: BarItem[], spare: BarItem[]): string => {
    if (!act.length) return `直接连不上、自己的中转站又用不了时：先经${spare.map((r) => r.said).join('、')}（都只当备用）。`;
    let s = '直接连不上、自己的中转站又用不了时：' + (act.length >= 2 ? act.map((r, i) => (i ? `${r.share}% 先经${r.said}` : `${r.share}% 的设备先经${r.said}`)).join('，') : `设备都先经${act[0].said}`);
    if (spare.length) s += `；${spare.map((r) => r.said).join('、')}只当备用，别的都用不了时才用`;
    return s + '。';
  };
  const bar = new ShareBar(app, $('.wbar-slot'), barSay, (shares) => void weigh(shares));
  const barItems = (): BarItem[] => {
    const s = sharesNow();
    return shared().map((p) => ({ id: p.r.id, title: title(p.r), said: title(p.r), share: s[p.r.id] }));
  };
  async function weigh(next: Record<string, number>): Promise<void> {
    const ch: [string, number][] = [];
    for (const [id, w] of Object.entries(next)) {
      const p = pubOf(id);
      if (!p || weightOf(p) === w) continue;
      ch.push([id, w]);
      weightSent.set(id, w);
    }
    try {
      for (const [id, w] of ch) await send('relay.setWeight', { relay: id, weight: w });
    } catch (e) {
      for (const [id, w] of ch) if (weightSent.get(id) === w) weightSent.delete(id);
      draw({ anim: true });
      again();
      failed(e, '没能改好，再试一次');
    }
  }

  // ---- Drawing --------------------------------------------------------------------------
  let later = false;
  function draw(o: { anim?: boolean; still?: boolean } = {}): void {
    if (gone) return;
    if (app.handling > 0) {
      if (!later) {
        later = true;
        const wait = () => (gone ? undefined : app.handling > 0 ? D.after(120, wait) : ((later = false), draw({ anim: true })));
        D.after(120, wait);
      }
      return;
    }
    const well = $('.rwell.ops');
    const h0 = well.getBoundingClientRect().height;
    for (const a of well.getAnimations()) a.cancel();
    const before = new Map([...well.querySelectorAll<HTMLElement>('.rcell')].map((c) => [c.dataset.k, c.getBoundingClientRect()]));
    const ps = pubs();
    $('.g-pub').innerHTML = ps.map(cell).join('') + addCell();
    $('.n-pub').textContent = `${ps.length} 个`;
    bar.set(barItems(), !!o.anim);
    const h1 = well.getBoundingClientRect().height;
    if (h0 && Math.abs(h1 - h0) > 1 && !o.still) well.animate([{ height: h0 + 'px' }, { height: h1 + 'px' }], { duration: 360, easing: 'cubic-bezier(0.3, 0.3, 0.2, 1)' });
    bar.grow();
    if (!o.still)
      for (const c of well.querySelectorAll<HTMLElement>('.rcell')) {
        const b = before.get(c.dataset.k);
        if (!b) continue;
        const a = c.getBoundingClientRect();
        const dx = b.left - a.left, dy = b.top - a.top;
        if (Math.abs(dx) + Math.abs(dy) > 0.5) c.animate([{ transform: `translate(${dx}px, ${dy}px)` }, { transform: 'none' }], { duration: 360, easing: 'cubic-bezier(0.3, 0.3, 0.2, 1)' });
      }
    wire();
    mark();
    headline();
  }
  // The second lines and dots only, when the numbers change: the cells stay.
  function relabel(): void {
    for (const p of pubs()) {
      const c = $(`.rcell[data-k="${CSS.escape(p.r.id)}"]`);
      if (!c) continue;
      const s = spaced(subOf(p));
      const sm = c.querySelector('small') as HTMLElement;
      if (sm.textContent !== s) {
        sm.textContent = s;
        sm.animate([{ opacity: 0.2 }, { opacity: 1 }], { duration: 200, easing: 'ease-out' });
      }
      const st = stateOf(p);
      const dot = c.querySelector('.orb .on') as HTMLElement;
      dot.classList.toggle('off', st === 'off');
      dot.classList.toggle('busy', st === 'busy');
      c.setAttribute('aria-label', spaced(`${title(p.r)}，公共中转站，${subOf(p)}`));
    }
    headline();
  }

  let openKey: string | null = null;
  function mark(): void {
    for (const c of page.querySelectorAll<HTMLElement>('.rcell')) c.classList.toggle('sel', c.dataset.k === openKey || (openKey === 'join' && c.dataset.k === 'add'));
  }
  function wire(): void {
    for (const c of page.querySelectorAll<HTMLElement>('.rcell')) {
      const w = c as HTMLElement & { __wired?: boolean };
      if (w.__wired) continue;
      w.__wired = true;
      const go = () => {
        const k = c.dataset.k as string;
        if (k === 'add' || k === 'join') return void openAdd();
        if (pubOf(k)) openPub(k);
      };
      c.addEventListener('click', go);
      c.addEventListener('keydown', (e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          go();
        }
      });
    }
  }

  // ---- Cards -----------------------------------------------------------------------------
  const sheet = (key: string, html: string): HTMLElement => {
    openKey = key;
    mark();
    return app.sheet(`<div class="relays-card">${html}</div>`, key);
  };
  // again: the card on screen says it as it is now; not while its name is
  // being changed (the hand is there).
  const again = (force = false) => {
    if (!openKey || openKey === 'join' || app.sheetEl.style.display === 'none') return;
    if (!force && app.sheetEl.querySelector('.name-input')) return;
    if (pubOf(openKey)) openPub(openKey);
    else app.closeSheet();
  };
  // Who keeps a place on the card while it is open (turned away, they grey
  // where they are; let back, they take their color again).
  let cardFor: RelayID | null = null;
  let cardTop: PersonID[] = [];
  let cardOff: PersonID[] = [];
  const topBytes = new Map<PersonID, number>();
  const kickedAt = new Map<PersonID, string>();

  function openPub(id: RelayID): void {
    const p = pubOf(id);
    if (!p) return;
    if (cardFor !== id) {
      cardFor = id;
      cardTop = [];
      cardOff = [];
    }
    const m = month.get(id);
    const load = m?.load;
    const kicked = kicksOf(id);
    for (const k of kicked) kickedAt.set(k.person as PersonID, k.at);
    for (const t of m?.top || []) {
      topBytes.set(t.person.id, t.bytes);
      if (!cardTop.includes(t.person.id) && !cardOff.includes(t.person.id)) cardTop.push(t.person.id);
    }
    for (const k of kicked) if (!cardTop.includes(k.person as PersonID) && !cardOff.includes(k.person as PersonID)) cardOff.push(k.person as PersonID);
    // People who are not in my view (most of them): their names from the
    // numbers.
    for (const t of m?.top || []) {
      const n = t.person.displayName || t.person.name;
      if (!App.PEOPLE[t.person.id]) App.PEOPLE[t.person.id] = { name: n, full: n, ch: t.person.initial || n.slice(0, 1), c: tokens.color.owners[colorIndex(v.me, t.person.id)] };
    }
    const isOff = (x: PersonID) => kicked.some((k) => k.person === x);
    const st = stateOf(p);
    const fresh = !!p.v.since && Date.now() - Date.parse(p.v.since) < FRESH;
    const up = load ? fmtUptime(load.uptime) : fresh ? '刚上线' : '一切正常';
    const stat = st === 'off' ? status('连不上', `${fmtAgo(p.v.lastSeen)}，经它的设备改走别的`, GREY) : st === 'busy' ? status(busy(load?.cpu, load?.full || p.v.full), up, AMBER) : status('在线', up, GREEN);
    const s = sharesNow();
    const share = s[id] ?? 0;
    const segHTML = !p.v.default && shared().length >= 2 && !bar.only(id) ? seg(share, share > 0 ? `大约 ${share}% 的设备先经它` : '只当备用：别的都用不了时才用它') : '';
    const now =
      `<div class="label">现在</div>` +
      rows([
        [G.ui('people', 18), '在用', load && load.people ? `${load.people} 个人 · ${load.devices} 台设备` : st === 'off' ? '—' : '还没有'],
        [G.ui('bolt', 18), '带宽', load && st !== 'off' ? fmtRate(load.bytesPerSec) : '—'],
        [G.ui('screen', 18), '这个月转了', fmtBytes(m?.bytes || 0)],
      ]);
    const name = (x: PersonID) => App.PEOPLE[x]?.name || nameOf(v, x);
    const pchip = (x: PersonID, date: boolean) => {
      const off = isOff(x);
      const at = off && date ? kickedAt.get(x) : undefined;
      const gb = !date && topBytes.has(x) ? fmtBytes(topBytes.get(x) as number) : '';
      // Myself among them: there, but not to be turned away.
      const label = x === v.me ? spaced(`${name(x)} ${gb}`) : `${spaced(name(x))}${off ? '，已不让用，点一下就又让他用' : '，点一下就不让他用'}`;
      return `<button class="chip ${off ? 'blocked' : ''}" type="button" data-k="${esc(x)}" ${x === v.me ? 'disabled' : `aria-pressed="${off}"`} aria-label="${esc(label)}">${App.ball(x, off ? 'off' : '')}${esc(spaced(name(x)))}${gb ? `<span class="gb">${esc(gb)}</span>` : ''}${at ? `<span class="gb">${esc(day(at))}</span>` : ''}</button>`;
    };
    const others = Math.max(0, (m?.users || 0) - (m?.top || []).length);
    const top = cardTop.length ? `<div class="label">用得最多的</div><div class="people">${cardTop.map((x) => pchip(x, false)).join('')}</div><div class="hint">${others > 0 ? `还有 ${others} 个人。` : ''}点一下，就不让他用这个中转站</div>` : '';
    const offs = cardOff.length ? `<div class="label">不让用的</div><div class="people">${cardOff.map((x) => pchip(x, true)).join('')}</div>` : '';
    const detail =
      `<div class="card-foot"><button class="more" type="button">更多${G.ui('chevron', 14)}</button></div>` +
      `<div class="detail"><p>${esc(spaced(`地址 ${addrOf(p.r)} · 端口 ${p.r.derpPort}`))}<br />${esc(spaced(`每条连接限速 ${p.r.rateLimit ? fmtRate(p.r.rateLimit) : '不限'} · 同时最多几台设备：${p.v.capacity ? p.v.capacity + ' 台' : '不限'}`))}${p.v.default ? `<br />${esc('部署时就有的，所有局域网都先试它，不分设备')}` : ''}</p></div>`;
    const renamable = !p.v.default && (v.features || []).includes('rename');
    const el = sheet(id, head(orb(st), title(p.r), '公共中转站', renamable) + stat + segHTML + now + top + offs + detail);
    wireSeg(el, bar, id, () => again(true));
    for (const c of el.querySelectorAll<HTMLElement>('.people .chip')) c.addEventListener('click', () => void toggleKick(id, c.dataset.k as PersonID));
    const nb = el.querySelector<HTMLElement>('.name-edit');
    if (nb) nb.onclick = () => rename(nb, p.r.name || '', (to) => send('relay.rename', { relay: id, name: to }).catch((e) => failed(e, '没能改名，再试一次')), () => again(true));
  }

  // ---- "+ 加一台公共中转站" (as "用我自己的服务器") ------------------------------------------
  async function openAdd(): Promise<void> {
    const show = () => {
      const j = made ? null : waiting()[0];
      const left = (iso: string) => Math.max(1, Math.round((Date.parse(iso) - Date.now()) / 60000));
      const top = head(`<div class="orb add wait">${RING(40)}${G.ui('relayLine', 18)}</div>`, '加一台公共中转站', '装好后，它自己出现在这里');
      let html: string;
      if (made) {
        const just = Date.now() - made.at < 60000;
        html =
          top +
          `<div class="label">在服务器上运行这一行</div><div class="cmd" tabindex="0" aria-label="安装命令">${esc(made.command)}</div>` +
          `<div class="cmd-foot"><p class="hint">${just ? '1 小时内有效，只能用一次，要先装好 Docker' : esc(spaced(`还有 ${left(made.expiresAt)} 分钟有效，只能用一次`))}</p><button class="btn primary" type="button" data-act="copy">${G.ui('copy', 15)}复制</button></div>` +
          `<p class="say">刚装好的先当备用，点"多用"才分到设备，免得一下子接满流量</p>` +
          (just ? '' : `<div class="card-foot"><span></span><button class="out-btn" type="button" data-act="revoke">收回这行命令</button></div>`);
      } else if (j) {
        html =
          top +
          `<p class="say">命令只显示一次。还没装的话，收回它，再要一行。</p><div class="hint">${esc(spaced(`还有 ${left(j.expiresAt)} 分钟有效，只能用一次`))}</div>` +
          `<div class="card-foot"><span></span><button class="out-btn" type="button" data-act="revoke">收回这行命令</button></div>`;
      } else html = top + `<p class="say">正在生成命令…</p>`;
      const s = sheet('join', html);
      const b = s.querySelector('[data-act="copy"]') as HTMLButtonElement | null;
      b?.addEventListener('click', async () => {
        try {
          await navigator.clipboard.writeText(made!.command);
          b.innerHTML = `${G.ui('check', 15)}已复制`;
        } catch {
          const sel = getSelection();
          const range = document.createRange();
          range.selectNodeContents(s.querySelector('.cmd') as Node);
          sel?.removeAllRanges();
          sel?.addRange(range);
          b.innerHTML = '已选中，按 ⌘C';
        }
        b.animate([{ opacity: 0.4 }, { opacity: 1 }], { duration: 160, easing: 'ease-out' });
      });
      s.querySelector('[data-act="revoke"]')?.addEventListener('click', () => revokeCmd());
    };
    if (made || waiting().length) return show();
    show();
    try {
      const res = await ctx.op('relay.join', { public: true });
      const j = res.result?.relayJoin;
      if (!j) throw new ApiError(500, 'internal', '没拿到命令');
      made = { id: j.id, command: j.command, expiresAt: j.expiresAt, at: Date.now() };
    } catch (e) {
      if (gone) return;
      if (openKey === 'join') app.closeSheet();
      failed(e, '没能生成命令，再试一次');
      return;
    }
    if (gone) return;
    draw({ anim: true });
    if (openKey === 'join') show();
  }
  // Taken back: the cell goes at once; the server hears of it when the
  // toast goes without undo.
  function revokeCmd(): void {
    const id = made ? made.id : waiting()[0]?.id;
    if (!id) return;
    const was = made;
    made = null;
    revoking.add(id);
    if (openKey === 'join') app.closeSheet();
    draw({ anim: true });
    const back = () => {
      revoking.delete(id);
      made = was;
      draw({ anim: true });
    };
    hold(app, ctx, [['relay.revokeJoin', { join: id }]], '已收回这行命令', { parent: cv, x: TX, undo: back, failed: back });
  }

  // ---- Turning someone away, and back -------------------------------------------------------
  async function toggleKick(id: RelayID, x: PersonID): Promise<void> {
    const k = kicksOf(id).find((y) => y.person === x);
    if (k) return void unkick(id, k.id, x, false);
    await kick(id, x, false);
  }
  const nm = (x: PersonID) => App.PEOPLE[x]?.name || nameOf(v, x);
  async function kick(id: RelayID, x: PersonID, quiet: boolean): Promise<void> {
    const p = pubOf(id);
    if (!p) return;
    try {
      const res = await send('relay.kick', { relay: id, person: x });
      const kid = res.result?.kick;
      const m = month.get(id);
      if (m && kid) m.kicks = [...(m.kicks || []), { id: kid, person: x, at: new Date().toISOString() }];
      again();
      if (!quiet && kid) app.toast(spaced(`已不让${nm(x)}用${title(p.r)}`), { parent: cv, x: TX, undo: () => void unkick(id, kid, x, true) });
      void refresh();
    } catch (e) {
      failed(e, '没能做成，再试一次');
    }
  }
  async function unkick(id: RelayID, kid: string, x: PersonID, quiet: boolean): Promise<void> {
    const p = pubOf(id);
    try {
      await send('relay.unkick', { kick: kid });
    } catch (e) {
      failed(e, '没能做成，再试一次');
      return;
    }
    const m = month.get(id);
    if (m) m.kicks = (m.kicks || []).map((y) => (y.id === kid ? { ...y, liftedAt: new Date().toISOString() } : y));
    again();
    if (!quiet && p) app.toast(spaced(`又让${nm(x)}用${title(p.r)}了`), { parent: cv, x: TX, undo: () => void kick(id, x, true) });
    void refresh();
  }

  // ---- The numbers: now, and every 10 seconds while the page is open ---------------------------
  let timer: { cancel(): void } | null = null;
  async function refresh(): Promise<void> {
    try {
      const u = await api.operatorUsage();
      if (gone) return;
      usage = u;
      month.clear();
      for (const r of u.relays) month.set(r.relay, r);
      relabel();
      again();
    } catch {
      // Kept as it was: the next try comes in 10 seconds.
    }
  }
  const loop = () => {
    if (gone) return;
    void refresh();
    timer = D.after(10000, loop);
  };
  app.onSheetClose = () => {
    openKey = null;
    cardFor = null;
    mark();
  };

  draw({ still: true });
  loop();
  if (D.recording && new URLSearchParams(location.search).get('record') === 'ops') import('./rec-ops.ts').then(() => ((window as unknown as Record<string, unknown>).__ready = true));

  // ---- The server's word: a new one comes in as standby -----------------------------------------
  const off = ctx.onView((nv) => {
    follow(nv);
    v = nv;
    if (!v.operator) {
      // No more an operator: the page goes with the row.
      ctx.go('/relays', true);
      return;
    }
    for (const [id, w] of weightSent) {
      const rv = v.operator.relays.find((x) => x.id === id);
      if (!rv || rv.weight === w) weightSent.delete(id);
    }
    const ps = pubs();
    const fresh = ps.filter((p) => !known.has(p.r.id));
    known = new Set(ps.map((p) => p.r.id));
    if (made && !(v.operator.joins || []).some((j) => j.id === made!.id && !j.relay)) made = null;
    draw({ anim: true });
    for (const p of fresh) {
      const c = $(`.rcell[data-k="${CSS.escape(p.r.id)}"] .orb`);
      c?.animate([{ transform: 'scale(0.6)', opacity: 0 }, { transform: 'scale(1.08)', opacity: 1, offset: 0.55 }, { transform: 'none' }], { duration: 560, easing: 'cubic-bezier(0.3, 1.2, 0.5, 1)' });
      app.toast(spaced(`${title(p.r)}上线了${!p.v.default && weightOf(p) === 0 ? '，先当备用' : ''}`), { parent: cv, x: TX });
      if (openKey === 'join') openPub(p.r.id);
    }
    if (fresh.length) void refresh();
    else again();
  });

  return () => {
    gone = true;
    off();
    timer?.cancel();
    app.o.trace = (app.o.trace || []).filter((s: string) => !traced.includes(s));
    clear();
  };
}
