// morph draws part of a page anew from markup, element by element, where
// it was drawn whole before (innerHTML) and every change showed in one
// frame (the 19th batch; the user, 11-18: "所有的内容的变化都得有微动效";
// design/plan/m7/links-and-web-changes.md §2.3):
//
//   - What the old and the new both have stays the element it was: its
//     attributes and classes are set to the new ones (their CSS transitions
//     carry the change), and it keeps its listeners, focus and hover.
//   - Words that change go one at a time (swapText, DESIGN §5 Swap): a
//     leaf's in place; where words sit among elements, the element's content
//     fades out whole and back in with the new.
//   - What is new fades in, after what goes has gone (--motion-swap-out);
//     what is gone fades out where it was, its room closing after it.
//
// Elements are matched by their key (data-id, data-k, data-key, data-i,
// data-l, data-who, data-ball), else by tag and first class in order. One
// that keep says to leave alone (a row being edited) is left as it is. A
// part's handlers must be idempotent: set as on* properties, or bound once.

                                                        
const swapText = (el             , text        )       => ((window                                     ).swapText)(el, text);

const ms = (name        )         => parseFloat(getComputedStyle(document.documentElement).getPropertyValue(name)) || 0;

// leaving: elements fading out on their way off, not matched again.
const leaving = new WeakSet      ();

const KEYS = ['data-id', 'data-k', 'data-key', 'data-i', 'data-l', 'data-who', 'data-ball'];

function keyOf(n      )                {
  if (!(n instanceof Element)) return null;
  for (const k of KEYS) {
    const v = n.getAttribute(k);
    if (v != null) return n.tagName + '|' + k + '=' + v;
  }
  return null;
}

function kindOf(n      )         {
  if (n instanceof Element) return n.tagName + '.' + (n.classList[0] || '');
  return '#' + n.nodeType;
}

                               
                                                        
                                  
 

export function morph(el             , html        , o               = {})       {
  const t = document.createElement('template');
  t.innerHTML = html;
  patchChildren(el, t.content, o);
}

function patchAttrs(old         , neu         )       {
  for (const a of Array.from(neu.attributes)) if (old.getAttribute(a.name) !== a.value) old.setAttribute(a.name, a.value);
  for (const a of Array.from(old.attributes)) if (!neu.hasAttribute(a.name)) old.removeAttribute(a.name);
}

// leafText is an element's words when it has nothing but words.
function leafText(el         )                {
  for (const c of Array.from(el.childNodes)) if (c.nodeType !== Node.TEXT_NODE) return null;
  return el.textContent || '';
}

function patch(old         , neu         , o              )       {
  if (o.keep && o.keep(old)) return;
  patchAttrs(old, neu);
  const a = leafText(old), b = leafText(neu);
  if (a != null && b != null) {
    // Words alone: one at a time, in place.
    if (a !== b && old instanceof HTMLElement) {
      if (a && b) swapText(old, b);
      else if (b) {
        // Words where there were none: they fade in.
        old.textContent = b;
        old.animate([{ opacity: 0 }, { opacity: 1 }], { duration: ms('--motion-swap-in'), easing: 'ease-out' });
      } else {
        // None where there were words: these fade out, then go.
        const out = old.animate([{ opacity: getComputedStyle(old).opacity }, { opacity: 0 }], { duration: ms('--motion-swap-out'), easing: 'ease-out', fill: 'forwards' });
        out.onfinish = () => {
          if (leafText(old) === a) old.textContent = '';
          out.cancel();
        };
      }
    } else if (a !== b) old.textContent = b;
    return;
  }
  patchChildren(old, neu, o);
}

function patchChildren(parent         , from            , o              )       {
  const all = Array.from(parent.childNodes);
  const olds = all.filter((n) => !leaving.has(n));
  const news = Array.from(from.childNodes);
  // Each new node's old one: by key, else the next of its kind.
  const keyed = new Map              ();
  for (const n of olds) {
    const k = keyOf(n);
    if (k && !keyed.has(k)) keyed.set(k, n);
  }
  const used = new Set      ();
  const pairs                        = [];
  let at = 0;
  for (const nn of news) {
    let match              = null;
    const k = keyOf(nn);
    if (k) {
      const m = keyed.get(k);
      if (m && !used.has(m)) match = m;
    } else {
      for (let i = at; i < olds.length; i++) {
        const c = olds[i];
        if (used.has(c) || keyOf(c)) continue;
        if (kindOf(c) === kindOf(nn)) {
          match = c;
          at = i + 1;
          break;
        }
      }
    }
    if (match) used.add(match);
    pairs.push([nn, match]);
  }
  // What goes stays where it showed until it is gone: after the node it
  // followed that stays, or first. Words not in an element go at once (the
  // element they are in fades, below).
  let words = false; // words among elements changed: the whole fades
  const after = new Map                     ();
  let anchor              = null;
  for (const n of all) {
    if (used.has(n)) {
      anchor = n;
      continue;
    }
    if (n instanceof Element) {
      const list = after.get(anchor) || [];
      list.push(n);
      after.set(anchor, list);
    } else {
      if (n.nodeType === Node.TEXT_NODE && (n        ).data.trim()) words = true;
      n.remove();
    }
  }
  const texts                   = [];
  const order         = [...(after.get(null) || [])];
  const entering            = [];
  for (const [nn, match] of pairs) {
    let node      ;
    if (match) {
      if (match.nodeType === Node.TEXT_NODE) {
        if ((match        ).data !== (nn        ).data) {
          if ((match        ).data.trim() || (nn        ).data.trim()) words = true;
          texts.push([match        , (nn        ).data]);
        }
      } else if (match instanceof Element) patch(match, nn           , o);
      node = match;
    } else {
      node = document.importNode(nn, true);
      if (node instanceof Element) entering.push(node);
      else if (node.nodeType === Node.TEXT_NODE && (node        ).data.trim()) words = true;
    }
    order.push(node);
    if (match) order.push(...(after.get(match) || []));
  }
  // In that order, moving only what is out of place.
  let ref = parent.firstChild;
  for (const n of order) {
    if (n === ref) {
      ref = ref.nextSibling;
      continue;
    }
    parent.insertBefore(n, ref);
  }
  for (const n of olds) if (!used.has(n) && n instanceof Element) exit(n);
  for (const e of entering) enter(e);
  if (!texts.length) return;
  if (words && parent instanceof HTMLElement && parent.isConnected) {
    // Words among elements: the content fades out whole, then in with the
    // new words (one at a time, as swapText does for a leaf).
    const out = parent.animate([{ opacity: getComputedStyle(parent).opacity }, { opacity: 0 }], { duration: ms('--motion-swap-out'), easing: 'ease-out', fill: 'forwards' });
    out.onfinish = () => {
      for (const [n, d] of texts) n.data = d;
      parent.animate([{ opacity: 0 }, { opacity: 1 }], { duration: ms('--motion-swap-in'), easing: 'ease-out' });
      out.cancel();
    };
  } else for (const [n, d] of texts) n.data = d;
}

const BLOCK = /^(block|flex|grid|list-item|table)$/;
const INLINE_BOX = /^(inline-block|inline-flex|inline-grid)$/;

// enter fades a new element in where it comes, once what goes is gone; its
// room opens meanwhile, a block's in height, a chip's or an icon's in
// width, so that what is beside it moves along rather than jumps.
function enter(el         )       {
  if (!(el instanceof HTMLElement || el instanceof SVGElement)) return;
  const out = ms('--motion-swap-out'), inn = ms('--motion-swap-in');
  el.animate([{ opacity: 0 }, { opacity: 0, offset: out / (out + inn) }, { opacity: 1 }], { duration: out + inn, easing: 'ease-out' });
  if (!el.isConnected) return;
  const cs = getComputedStyle(el);
  if (cs.position === 'absolute' || cs.position === 'fixed') return;
  const grow = { duration: out + inn, easing: 'cubic-bezier(0.3, 0.3, 0.2, 1)' };
  if (el instanceof HTMLElement && BLOCK.test(cs.display)) {
    const h = el.offsetHeight;
    if (h < 1) return;
    el.animate(
      [
        { height: '0px', marginTop: '0px', marginBottom: '0px', paddingTop: '0px', paddingBottom: '0px', overflow: 'hidden' },
        { height: h + 'px', marginTop: cs.marginTop, marginBottom: cs.marginBottom, paddingTop: cs.paddingTop, paddingBottom: cs.paddingBottom, overflow: 'hidden' },
      ],
      grow,
    );
  } else if (el instanceof SVGElement || INLINE_BOX.test(cs.display)) {
    const w = el.getBoundingClientRect().width;
    if (w < 1) return;
    // In a row with a gap, the gap beside it comes with it too: it starts
    // as nothing, the gap taken back by a margin.
    const gap = rowGap(el);
    const before = gap && el.previousElementSibling ? -gap : 0;
    const after = gap && !before && el.nextElementSibling ? -gap : 0;
    el.animate(
      [
        { width: '0px', minWidth: '0px', marginLeft: before + 'px', marginRight: after + 'px', paddingLeft: '0px', paddingRight: '0px', overflow: 'hidden' },
        { width: w + 'px', minWidth: '0px', marginLeft: cs.marginLeft, marginRight: cs.marginRight, paddingLeft: cs.paddingLeft, paddingRight: cs.paddingRight, overflow: 'hidden' },
      ],
      grow,
    );
  }
}

// rowGap is the gap between the items of the row el is in, if it is in a
// flex row or a grid.
function rowGap(el         )         {
  const p = el.parentElement;
  if (!p) return 0;
  const ps = getComputedStyle(p);
  const row = (ps.display === 'flex' || ps.display === 'inline-flex') && !ps.flexDirection.startsWith('column');
  return row || ps.display === 'grid' || ps.display === 'inline-grid' ? parseFloat(ps.columnGap) || 0 : 0;
}

// exit fades an element out where it was; then its room closes, a block's
// in height, a chip's or an icon's in width, and it goes.
function exit(el         )       {
  if (!(el instanceof HTMLElement || el instanceof SVGElement)) {
    el.remove();
    return;
  }
  leaving.add(el);
  if (el instanceof HTMLElement) {
    el.inert = true;
    el.style.pointerEvents = 'none';
  }
  el.setAttribute('aria-hidden', 'true');
  const cs = getComputedStyle(el);
  const block = el instanceof HTMLElement && BLOCK.test(cs.display) && cs.position !== 'absolute' && cs.position !== 'fixed';
  const box = !block && (el instanceof SVGElement || INLINE_BOX.test(cs.display)) && cs.position !== 'absolute' && cs.position !== 'fixed';
  const h = block ? (el               ).offsetHeight : 0;
  const w = box ? el.getBoundingClientRect().width : 0;
  // Its gap goes with it (as enter's).
  const gap = box ? rowGap(el) : 0;
  const gapBefore = gap && el.previousElementSibling ? -gap : 0;
  const gapAfter = gap && !gapBefore && el.nextElementSibling ? -gap : 0;
  const out = el.animate([{ opacity: cs.opacity }, { opacity: 0 }], { duration: ms('--motion-swap-out'), easing: 'ease-out', fill: 'forwards' });
  out.onfinish = () => {
    if (!el.isConnected || (!block && !box)) return void el.remove();
    const shut = { duration: ms('--motion-swap-in'), easing: 'ease-in-out', fill: 'forwards'             };
    const a = block
      ? el.animate(
          [
            { height: h + 'px', marginTop: cs.marginTop, marginBottom: cs.marginBottom, paddingTop: cs.paddingTop, paddingBottom: cs.paddingBottom, opacity: 0, overflow: 'hidden' },
            { height: '0px', marginTop: '0px', marginBottom: '0px', paddingTop: '0px', paddingBottom: '0px', opacity: 0, overflow: 'hidden' },
          ],
          shut,
        )
      : el.animate(
          [
            { width: w + 'px', minWidth: '0px', marginLeft: cs.marginLeft, marginRight: cs.marginRight, paddingLeft: cs.paddingLeft, paddingRight: cs.paddingRight, opacity: 0, overflow: 'hidden' },
            { width: '0px', minWidth: '0px', marginLeft: gapBefore + 'px', marginRight: gapAfter + 'px', paddingLeft: '0px', paddingRight: '0px', opacity: 0, overflow: 'hidden' },
          ],
          shut,
        );
    a.onfinish = () => el.remove();
  };
}
