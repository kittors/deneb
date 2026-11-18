// M7-18 查外网地址的服务 (design/plan/stun-servers-20261118.md §4), recorded on
// the real page against the web's demo server (deneb-web -mock), as kittors,
// the deployment's operator: Cloudflare's to begin with; one added (the
// server looks it up first); one that cannot be looked up, and what it
// says; one taken off, and undo; the last one asks again before it goes,
// and the well says what happens with none; undo brings it back. Loaded only
// with ?record=stun.
import './rec.js';
import { settle, goJS, typing } from './rec-stunkit.js';

await settle('one', 1, true);

const rest = [1080, 640];
const CF = '.stun-row[data-k="stun.cloudflare.com:3478"]';
const NET = '.stun-row[data-k="stun.example.net:19302"]';
(window                                      ).RECORDING = {
  title: 'M7-18-查外网地址的服务',
  duration: 27800,
  actions: [
    { at: 700, shot: '一个', clean: true },
    // Over a row: its "去掉" shows.
    { at: 900, glide: { sel: CF }, dur: 800 },
    { at: 1900, shot: '指着一行' },
    // "加一个": pressed, then the field.
    { at: 2200, glide: { sel: '.stun-add' }, dur: 700 },
    { at: 3100, down: true },
    { at: 3160, shot: '按下加一个' },
    { at: 3240, up: true },
    { at: 3700, glide: rest, dur: 600 },
    ...typing(3800, 'stun.example.net:19302'),
    { at: 5600, shot: '写好了' },
    { at: 5800, glide: { sel: '.stun-go' }, dur: 600 },
    { at: 6600, down: true },
    { at: 6680, up: true },
    { at: 7300, shot: '正在查地址' },
    { at: 7700, js: goJS },
    { at: 8100, glide: rest, dur: 600 },
    { at: 9100, shot: '加好了', clean: true },
    // One that cannot be looked up: the server says so under the field.
    { at: 9300, glide: { sel: '.stun-add' }, dur: 700 },
    { at: 10100, down: true },
    { at: 10180, up: true },
    { at: 10500, glide: rest, dur: 600 },
    ...typing(10600, 'stun.nowhere.example'),
    { at: 12200, key: 'Enter' },
    { at: 13000, js: goJS },
    { at: 13800, shot: '查不到' },
    { at: 14000, glide: { sel: '.stun-cancel' }, dur: 700 },
    { at: 14800, down: true },
    { at: 14880, up: true },
    { at: 15100, glide: rest, dur: 500 },
    { at: 15800, shot: '收起来了', clean: true },
    // Off the list, with undo; and back.
    { at: 16000, glide: { sel: `${NET} .stun-rm` }, dur: 800 },
    { at: 17000, down: true },
    { at: 17080, up: true },
    { at: 17300, glide: rest, dur: 500 },
    { at: 18100, shot: '去掉了' },
    { at: 18300, glide: { sel: '.toast button' }, dur: 700 },
    { at: 19100, down: true },
    { at: 19180, up: true },
    { at: 19400, glide: rest, dur: 500 },
    { at: 20200, shot: '撤销了', clean: true },
    // Off again; then the last one, which asks again.
    { at: 20400, glide: { sel: `${NET} .stun-rm` }, dur: 700 },
    { at: 21200, down: true },
    { at: 21280, up: true },
    { at: 21900, glide: { sel: `${CF} .stun-rm` }, dur: 700 },
    { at: 22700, down: true },
    { at: 22780, up: true },
    { at: 23400, shot: '最后一个要再点一下' },
    { at: 23700, down: true },
    { at: 23780, up: true },
    { at: 24000, glide: rest, dur: 500 },
    { at: 24800, shot: '一个都没有' },
    { at: 25000, glide: { sel: '.toast button' }, dur: 700 },
    { at: 25800, down: true },
    { at: 25880, up: true },
    { at: 26100, glide: rest, dur: 500 },
    { at: 27200, shot: '又回来了', clean: true },
  ],
};
