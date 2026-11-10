// Recording hooks (design/tools/record.mjs, DESIGN §11), loaded only with
// ?record: a drawn pointer that follows the recorder's pointer events, and
// __point, which turns a selector (and a point in it) into the viewport's
// coordinates, as design/demos/shared/demo.js has them.
const w = window                                      ;
document.body.classList.add('recording');
const cur = document.createElement('div');
cur.className = 'fake-cursor';
cur.innerHTML = `<span class="press"></span><svg width="22" height="24" viewBox="0 0 22 24"><path d="M5 2.5v17.2l4.3-4.1 2.9 6.4 2.9-1.3-2.8-6.2 6-.1z" fill="#111" stroke="#fff" stroke-width="1.4" stroke-linejoin="round"/></svg>`;
document.body.append(cur);
const press = cur.querySelector('.press')               ;
const pos = (e              ) => {
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
w.__point = (sel        , x         , y         ) => {
  const el = document.querySelector(sel)                      ;
  if (!el) return null;
  const r = el.getBoundingClientRect();
  if (x == null) return { x: r.left + r.width / 2, y: r.top + r.height / 2 };
  const s = r.width / el.offsetWidth;
  return { x: r.left + x * s, y: r.top + (y          ) * s };
};
