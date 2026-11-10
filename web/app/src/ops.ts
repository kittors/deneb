// Operations that wait for their toast (DESIGN.md §6.7, §6.11), as the
// desktop's holdFor: the page shows them done at once, the toast offers
// undo, and only once it goes without it (timed out, taken over by another
// toast, the page left) does the server hear of them, one after another.
// Undo is then nothing done. Used where the server has no way back
// (letting someone in, turning them away, freeing a seat, a LAN's mode).
import { ApiError } from './api.ts';
import type { Ctx } from './main.ts';
import type { OpType, OpArgs } from './model.ts';

export type HeldOp = [OpType, OpArgs];

export interface HoldOptions {
  dev?: { type: string; owner: string }; // the toast's tile
  undo?: () => void; // the page puts back what it showed
  failed?: () => void; // the server turned one down: the page puts it back
  sent?: () => void; // all of them went through
  x?: number;
  parent?: HTMLElement;
}

// hold shows text with undo and sends ops when the toast is gone.
export function hold(app: { toast: (t: string, o: object) => HTMLElement }, ctx: Ctx, ops: HeldOp[], text: string, o: HoldOptions = {}): void {
  let undone = false;
  app.toast(text, {
    dev: o.dev,
    x: o.x,
    parent: o.parent,
    undo: () => {
      undone = true;
      o.undo?.();
    },
    gone: async () => {
      if (undone) return;
      for (const [op, args] of ops) {
        try {
          await ctx.op(op, args);
        } catch (e) {
          o.failed?.();
          failed(app, e, '没能做成，再试一次', o);
          return;
        }
      }
      o.sent?.();
    },
  });
}

// failed says what the server said, or a plain line, in a toast.
export function failed(app: { toast: (t: string, o: object) => HTMLElement }, e: unknown, text: string, o: { x?: number; parent?: HTMLElement } = {}): void {
  app.toast(e instanceof ApiError ? e.message : text, { x: o.x, parent: o.parent });
}

// pop springs an element up and back, as a ball takes what was dropped on
// it (Bouncy's feel, in a keyframe the browser runs).
export function pop(el: Element | null, k = 1.22): void {
  el?.animate([{ transform: 'none' }, { transform: `scale(${k})`, offset: 0.3 }, { transform: 'none' }], { duration: 620, easing: 'cubic-bezier(0.3, 1.2, 0.5, 1)' });
}

