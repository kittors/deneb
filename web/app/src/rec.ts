// Recording hooks (design/tools/record.mjs, DESIGN §11), loaded only with
// ?record: a drawn pointer that follows the recorder's pointer events, and
// __point, which turns a selector (and a point in it) into the viewport's
// coordinates, as design/demos/shared/demo.js has them.
const w = window as unknown as Record<string, unknown>;
document.body.classList.add('recording');
const cur = document.createElement('div');
cur.className = 'fake-cursor';
cur.innerHTML = `<span class="press"></span><svg width="22" height="24" viewBox="0 0 22 24"><path d="M5 2.5v17.2l4.3-4.1 2.9 6.4 2.9-1.3-2.8-6.2 6-.1z" fill="#111" stroke="#fff" stroke-width="1.4" stroke-linejoin="round"/></svg>`;
document.body.append(cur);
const press = cur.querySelector('.press') as HTMLElement;
const pos = (e: PointerEvent) => {
  cur.classList.add('seen');
  cur.style.transform = `translate(${e.clientX}px, ${e.clientY}px)`;
};
addEventListener('pointermove', pos, true);
addEventListener(
  'pointerdown',
  (e) => {
    pos(e);
    press.animate([{ opacity: 0, transform: 'scale(.5)' }, { opacity: 1, transform: 'scale(1)' }], { duration: 120, easing: 'ease-out', fill: 'forwards' });
  },
  true,
);
addEventListener('pointerup', () => press.animate([{ opacity: 1, transform: 'scale(1)' }, { opacity: 0, transform: 'scale(1.3)' }], { duration: 260, easing: 'ease-out', fill: 'forwards' }), true);
// find: a CSS selector, or "base|name|part" for data with ids the server
// made up: the first base element whose name (aria-label, title or text,
// spaces aside) starts with name, then part inside it (".dev|阿杰的树莓派|.tile").
const flat = (s: string | null | undefined) => (s || '').replace(/\s+/g, '');
w.__find = (sel: string): HTMLElement | null => {
  if (!sel.includes('|')) return document.querySelector(sel);
  const [base, name, part] = sel.split('|');
  const n = flat(name);
  for (const el of document.querySelectorAll(base) as NodeListOf<HTMLElement>) {
    if ([el.getAttribute('aria-label'), el.title, el.textContent].some((t) => flat(t).startsWith(n) && t)) return part ? el.querySelector(part) : el;
  }
  return null;
};
w.__point = (sel: string, x?: number, y?: number) => {
  const el = (w.__find as (s: string) => HTMLElement | null)(sel);
  if (!el) return null;
  const r = el.getBoundingClientRect();
  if (x == null) return { x: r.left + r.width / 2, y: r.top + r.height / 2 };
  const s = r.width / el.offsetWidth;
  return { x: r.left + x * s, y: r.top + (y as number) * s };
};
