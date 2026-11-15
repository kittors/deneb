// The home page's recording (web/probe/record-web.mjs, 1440×900; site.js
// takes window.RECORDING from here by ?record): the first screen's button
// and link hovered; then the page scrolled block by block, the top bar
// frosting, each block's headline, line and picture rising in once, the
// loops running on the recorder's clock; the first step's 下载 hovered;
// back to the top. Points in the viewport go through .top, which stays at
// its top; a block's place is its offsetTop.
const at = (i) => `__scroll(document.querySelectorAll('main > section')[${i}].offsetTop, 1500)`;
window.SITE_RECORDINGS = {
  default: {
    duration: 28200,
    actions: [
      { at: 0, js: '__reveal()' },
      { at: 0, move: { sel: '.top', x: 1180, y: 760 } },
      { at: 400, glide: { sel: '.hero .btn.primary' }, dur: 700, arc: 30 },
      { at: 1300, shot: '悬停-下载' },
      { at: 1500, glide: { sel: '.hero .more' }, dur: 500 },
      { at: 2200, shot: '悬停-了解更多' },
      { at: 2400, glide: { sel: '.top', x: 1360, y: 560 }, dur: 500 },
      { at: 3000, js: at(1) },
      { at: 4700, shot: '拖进局域网' },
      { at: 5800, shot: '拖进局域网-拖着' },
      { at: 9200, shot: '拖进局域网-接上了' },
      { at: 9400, js: at(2) },
      { at: 11100, shot: '直接连接' },
      { at: 12200, js: at(3) },
      { at: 13900, shot: '分享给朋友' },
      { at: 15000, js: at(4) },
      { at: 16700, shot: '开放服务' },
      { at: 17800, js: at(5) },
      { at: 19500, shot: '自己的服务器' },
      { at: 20600, js: at(6) },
      { at: 22300, shot: '端到端加密' },
      { at: 23200, js: at(7) },
      { at: 24900, shot: '开始使用' },
      { at: 25000, glide: { sel: '.shot.get .btn' }, dur: 700, arc: 30 },
      { at: 25800, shot: '悬停-第一步下载' },
      { at: 26000, glide: { sel: '.top', x: 1360, y: 560 }, dur: 400 },
      { at: 26400, js: '__scroll(0, 1600)' },
      { at: 28100, shot: '回到顶上' },
    ],
  },
};
