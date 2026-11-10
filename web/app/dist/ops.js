// Operations that wait for their toast (DESIGN.md §6.7, §6.11), as the
// desktop's holdFor: the page shows them done at once, the toast offers
// undo, and only once it goes without it (timed out, taken over by another
// toast, the page left) does the server hear of them, one after another.
// Undo is then nothing done. Used where the server has no way back
// (letting someone in, turning them away, freeing a seat, a LAN's mode).
import { ApiError } from './api.js';
                                     
                                                 

                                      

                              
                                                            
                                                         
                                                                           
                                                
             
                       
 

// hold shows text with undo and sends ops when the toast is gone.
export function hold(app                                                  , ctx     , ops          , text        , o              = {})       {
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
export function failed(app                                                  , e         , text        , o                                       = {})       {
  app.toast(e instanceof ApiError ? e.message : text, { x: o.x, parent: o.parent });
}

// pop springs an element up and back, as a ball takes what was dropped on
// it (Bouncy's feel, in a keyframe the browser runs).
export function pop(el                , k = 1.22)       {
  el?.animate([{ transform: 'none' }, { transform: `scale(${k})`, offset: 0.3 }, { transform: 'none' }], { duration: 620, easing: 'cubic-bezier(0.3, 1.2, 0.5, 1)' });
}

