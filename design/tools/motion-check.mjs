#!/usr/bin/env node
// Finds what a viewer would call jumps, jitter and flicker in a recording,
// from the page's own frame-by-frame log (trace.json, which record.mjs
// writes next to the frames). Each log entry is a visible box (after every
// transform) and its opacity, so this sees what the eye sees.
//
//   jump    a box teleports, spikes for one frame, stops dead from speed,
//           or starts at full speed from rest, while no hand holds it
//   pop     a tile, chip or area changes size by more than 8% (and 6px) in
//           one frame
//   jitter  it reverses direction 4 or more times within 0.6 s
//   flicker its opacity dips by 0.25 or more and comes back within 0.4 s
//   flip    a link changes its bend 2 or more times within 1.5 s
//   card    the open card's opacity dips (its entrance replayed), other
//           than stepping aside while something is on the move
//
// A box in the hand (or lifted, or shaking on purpose) is left out: the
// hand moves it. The first and last frames of a box's life are left out:
// that is its entrance or exit animation.
//
// usage: node design/tools/motion-check.mjs <trace.json> [<trace.json> ...]
import { readFileSync } from 'node:fs';
import { dirname, basename } from 'node:path';

const files = process.argv.slice(2);
let total = 0;
for (const file of files) {
  const frames = JSON.parse(readFileSync(file, 'utf8'));
  const issues = [];
  const add = (kind, key, f, note) => issues.push({ kind, key, t: frames[f].t, note });

  // ---- Boxes: nodes, names, chips, areas, list items ----------------------------
  const keys = new Set();
  for (const fr of frames) for (const k in fr.e) keys.add(k);
  for (const key of keys) {
    // Runs of consecutive frames where the box exists.
    const runs = [];
    let run = null;
    frames.forEach((fr, i) => {
      if (fr.e[key]) {
        if (!run) runs.push((run = []));
        run.push(i);
      } else run = null;
    });
    for (const r of runs) {
      const inner = r.slice(4, -4);
      const at = (i) => frames[i].e[key];
      const handled = (i) => {
        const v = at(i);
        return v[5] === 1 || v[6] === 1;
      };
      let lastJitterAt = -1e9;
      for (const i of inner) {
        if (handled(i) || handled(i - 1) || handled(i - 2)) continue;
        const [x, y, w] = at(i), [x1, y1, w1] = at(i - 1), [x2, y2] = at(i - 2);
        const v = Math.hypot(x - x1, y - y1), u = Math.hypot(x1 - x2, y1 - y2);
        // What the eye reads as a jump: a teleport; a one-frame spike that
        // snaps back; a dead stop from speed; full speed from rest. (A
        // quick spring start is none of these: its speed builds.)
        const next = at(i + 1) && !handled(i + 1) ? Math.hypot(at(i + 1)[0] - x, at(i + 1)[1] - y) : v;
        // (A long, fast spring flight or an ease-out start is not a jump:
        // its speed is high but changes smoothly.)
        if (v > 90) add('jump', key, i, `teleport ${v.toFixed(0)} px`);
        else if (v > u + 6 && next < v - 6) add('jump', key, i, `spike ${u.toFixed(1)}→${v.toFixed(1)}→${next.toFixed(1)} px/frame`);
        else if (u > 5 && v < 0.8) add('jump', key, i, `dead stop from ${u.toFixed(1)} px/frame`);
        else if (u < 0.3 && v > 30) add('jump', key, i, `instant start ${v.toFixed(1)} px/frame`);
        // Names have no box of their own: new words changing their width
        // are not a pop. Chips, tiles and areas are.
        // (A box's first half second is its entrance: a pop-in is meant.)
        if (!key.startsWith('name:') && i - r[0] > 30 && w1 > 4 && Math.abs(w / w1 - 1) > 0.08 && Math.abs(w - w1) > 6) add('pop', key, i, `size ${w1}→${w}`);
        // Direction reversals in the last 0.6 s.
        if (frames[i].t - lastJitterAt > 600) {
          let flips = 0, amp = 0;
          for (const axis of [0, 1]) {
            let prev = 0;
            for (let j = Math.max(r[0] + 1, i - 36); j <= i; j++) {
              if (!at(j) || !at(j - 1) || handled(j)) continue;
              const v = at(j)[axis] - at(j - 1)[axis];
              if (Math.abs(v) < 0.4) continue;
              if (prev && Math.sign(v) !== Math.sign(prev)) flips++;
              prev = v;
              amp = Math.max(amp, Math.abs(v));
            }
          }
          if (flips >= 4) {
            add('jitter', key, i, `${flips} reversals in 0.6 s`);
            lastJitterAt = frames[i].t;
          }
        }
      }
      // Opacity dips that come back: a blink.
      for (let n = 0; n < inner.length; n++) {
        const i = inner[n];
        const o = at(i)[4];
        let before = -1;
        for (let j = Math.max(r[0], i - 12); j < i; j++) before = Math.max(before, at(j)[4]);
        if (before - o < 0.25) continue;
        let back = false;
        for (let j = i + 1; j <= Math.min(r[r.length - 1], i + 24); j++) if (at(j)[4] >= before - 0.05) back = true;
        if (back && !handled(i)) {
          add('flicker', key, i, `opacity ${before}→${o}→back`);
          n += 24;
        }
      }
    }
  }

  // ---- Links changing their mind ------------------------------------------------------
  const links = new Set();
  for (const fr of frames) for (const k in fr.l) links.add(k);
  for (const k of links) {
    const changes = [];
    let prev = null;
    frames.forEach((fr, i) => {
      const v = fr.l[k];
      if (!v) return (prev = null);
      if (prev != null && Math.abs(v[0] - prev) > 0.02 && !fr.h) changes.push(i);
      prev = v[0];
    });
    for (let n = 1; n < changes.length; n++)
      if (frames[changes[n]].t - frames[changes[n - 1]].t < 1500) add('flip', 'link:' + k, changes[n], 'bend changed twice in 1.5 s');
  }

  // ---- The card replaying its entrance --------------------------------------------------
  // (Stepping aside while something is on the move, and coming back, is
  // a fade that is meant: the page marks those frames sheetAway.)
  frames.forEach((fr, i) => {
    if (i < 13 || fr.sheet == null || fr.sheet < 0 || fr.sheetAway) return;
    const prevOpen = frames.slice(i - 12, i).every((p) => p.sheet === 1);
    if (prevOpen && fr.sheet < 0.75) add('card', 'sheet', i, `opacity ${fr.sheet} while open`);
  });

  // ---- Report: one line per kind and thing, with the moments ------------------------------
  const groups = {};
  for (const it of issues) {
    const g = (groups[it.kind + ' ' + it.key] ||= []);
    if (!g.length || it.t - g[g.length - 1].t > 120) g.push(it);
  }
  const name = basename(dirname(file));
  const lines = Object.entries(groups).map(([k, g]) => `  ${k}: ${g.map((it) => `${(it.t / 1000).toFixed(2)}s (${it.note})`).join(', ')}`);
  console.log(`${name}: ${lines.length ? lines.length + ' problem(s)' : 'clean'}`);
  for (const l of lines) console.log(l);
  total += lines.length;
}
process.exit(total ? 1 : 0);
