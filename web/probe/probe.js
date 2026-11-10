// Measurement hooks: when the wasm arrived, when Go started, the first WebGL draw.
window.__t = {};
(() => {
  const t = window.__t;
  const draw = () => {
    if (t.firstDraw) { t.draws = (t.draws || 0) + 1; return; }
    t.firstDraw = performance.now(); t.draws = 1;
    requestAnimationFrame(() => { t.firstFrame = performance.now(); });
  };
  for (const C of [window.WebGL2RenderingContext, window.WebGLRenderingContext, window.CanvasRenderingContext2D]) {
    if (!C) continue;
    for (const m of ['drawArrays', 'drawElements', 'drawArraysInstanced', 'drawElementsInstanced', 'drawRangeElements', 'putImageData', 'drawImage']) {
      const f = C.prototype[m];
      if (!f) continue;
      C.prototype[m] = function (...a) { draw(); return f.apply(this, a); };
    }
  }
  // Frames that drew something, counted per animation frame.
  let last = 0; t.frames = 0;
  const tick = () => { if (t.draws && t.draws !== last) { t.frames++; last = t.draws; } requestAnimationFrame(tick); };
  requestAnimationFrame(tick);
})();
