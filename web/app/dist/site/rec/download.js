// The download page's recording (design/tools/record.mjs, 1440×900;
// site.js takes window.RECORDING from here by ?record): the download
// button hovered and pressed; a row's; 复制 → 已复制 → 复制; the folded help
// opened and closed, the page scrolled down to them first. Points in the
// viewport go through .top.
window.SITE_RECORDINGS = {
  default: {
    duration: 10000,
    // What the download site would say: 0.1.8's version, Deneb.dmg's size.
    data: { version: '0.1.8', sizes: 'Deneb.dmg:15095808' },
    actions: [
      { at: 0, move: { sel: '.top', x: 1200, y: 760 } },
      { at: 300, glide: { sel: '.mine .btn.primary' }, dur: 700, arc: 30 },
      { at: 1200, shot: '悬停-下载' },
      { at: 1400, down: true },
      { at: 1480, shot: '按下-下载' },
      { at: 1520, up: true },
      { at: 2000, glide: { sel: '.row .link' }, dur: 600 },
      { at: 2800, shot: '悬停-表里的下载' },
      { at: 3000, glide: { sel: '.top', x: 1200, y: 560 }, dur: 400 },
      { at: 3500, js: "__scroll(document.querySelector('.how').offsetTop - 240, 900)" },
      { at: 4300, glide: { sel: '[data-copy]' }, dur: 600, arc: 20 },
      { at: 5100, shot: '悬停-复制' },
      { at: 5300, down: true },
      { at: 5380, up: true },
      { at: 5600, shot: '已复制' },
      { at: 5800, glide: { sel: '.disclose' }, dur: 600, arc: 20 },
      { at: 6600, shot: '悬停-展开' },
      { at: 6800, down: true },
      { at: 6880, up: true },
      { at: 7500, shot: '展开了' },
      { at: 7600, shot: '复制变回来' },
      { at: 8400, down: true },
      { at: 8480, up: true },
      { at: 9100, shot: '收起了' },
    ],
  },
};
