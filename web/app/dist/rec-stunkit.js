// What the 查外网地址的服务 recordings share (rec-stun.ts, rec-stun-states.ts):
// putting a scene in place on the demo's server before the clock starts, and
// typing a word a key at a time.
const h = { 'Content-Type': 'application/json' };

// scene asks the demo's server for one of its lists (cmd/deneb-web
// mock_stun.go); hold: its lookups wait for go().
export const sceneJS = (scene        , hold = false)         =>
  `fetch('/api/v1/_mock/stun', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: ${JSON.stringify(JSON.stringify({ scene, hold }))} })`;
export const goJS = `fetch('/api/v1/_mock/stun-go', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' })`;

const tick = () => new Promise((r) => setTimeout(r, 30));

// settle puts the scene in place and waits until the page shows it with
// rows rows, every animation run to its end: the recording starts still.
export async function settle(scene        , rows        , hold = false)                {
  await fetch('/api/v1/_mock/stun', { method: 'POST', headers: h, body: JSON.stringify({ scene, hold }) });
  for (let i = 0; i < 200; i++) {
    const n = document.querySelectorAll('.stun-row:not(.leaving)').length;
    const empty = !!document.querySelector('.stun-empty:not(.leaving)');
    if ((rows ? n === rows : empty) && !document.querySelector('.leaving')) break;
    finishAll();
    await tick();
  }
  for (let i = 0; i < 10; i++) {
    if (!finishAll()) break;
    await tick();
  }
}

// finishAll runs the page's animations with an end to it (not the rings'
// and spinners' that go on); it says whether there were any.
function finishAll()          {
  let any = false;
  for (const a of document.getAnimations()) {
    const end = a.effect?.getComputedTiming().endTime;
    if (typeof end === 'number' && isFinite(end) && a.playState !== 'finished') {
      a.finish();
      any = true;
    }
  }
  return any;
}

// typing turns a word into one action a character, every step ms from at.
export function typing(at        , word        , step = 70)                                 {
  return [...word].map((c, i) => ({ at: at + i * step, type: c }));
}
