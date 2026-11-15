// The docs' recording (web/probe/record-web.mjs, 1440×900; site.js loads
// it by ?record): a link of the list hovered (安装 has no links in its words);
// the page scrolled to its foot,
// the top bar's line coming; 下一篇 hovered and pressed. Points in the
// viewport go through .top, which stays at its top.
window.SITE_RECORDINGS = {
  default: {
    duration: 7000,
    actions: [
      { at: 0, move: { sel: '.top', x: 1240, y: 760 } },
      { at: 300, glide: { sel: '.toc a:nth-of-type(3)' }, dur: 700, arc: 30 },
      { at: 1200, shot: '悬停-目录' },
      { at: 2400, glide: { sel: '.top', x: 1240, y: 600 }, dur: 400 },
      { at: 3000, js: '__scroll(document.documentElement.scrollHeight - innerHeight, 1300)' },
      { at: 4500, shot: '滚到底' },
      { at: 4700, glide: { sel: '.pager .next' }, dur: 700, arc: 30 },
      { at: 5600, shot: '悬停-下一篇' },
      { at: 5800, down: true },
      { at: 5880, shot: '按下-下一篇' },
      { at: 5920, up: true },
    ],
  },
};
