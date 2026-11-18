// M7-18 查外网地址的服务, the amounts (AGENTS §1.3): none, one, many (eight:
// "加一个" greyed, every kind of row: found, kept from before, none yet, an
// address, a long name), long names; recorded at each window size and
// theme, for the stills. Loaded only with ?record=stun-states.
import './rec.ts';
import { settle, sceneJS } from './rec-stunkit.ts';

await settle('empty', 0);

// What scrolls: the page in a wide window, the canvas in a narrow one.
const scroller = `[document.querySelector('.relays-page.ops-scroll'), document.querySelector('.win.full .canvas')].find((e) => e && getComputedStyle(e).overflowY === 'auto')`;
const bottom = `(${scroller}).scrollTop = 1e6`;
const top = `(${scroller}).scrollTop = 0`;
(window as unknown as Record<string, unknown>).RECORDING = {
  title: 'M7-18-查外网地址的服务-数据量',
  duration: 8000,
  actions: [
    { at: 400, shot: '没有', clean: true },
    { at: 600, js: sceneJS('one') },
    { at: 2000, shot: '一个', clean: true },
    { at: 2200, js: sceneJS('many') },
    { at: 3800, shot: '很多', clean: true },
    { at: 4000, js: bottom },
    { at: 4400, shot: '很多_拉到底', clean: true },
    { at: 4600, js: top },
    { at: 4800, js: sceneJS('long') },
    { at: 6400, shot: '名字很长', clean: true },
    { at: 6600, glide: { sel: '.stun-row:nth-child(2)' }, dur: 700 },
    { at: 7600, shot: '名字很长_指着' },
  ],
};
