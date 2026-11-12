// @ts-nocheck
// design/demos/v3/glyphs.js as it is: the desktop's look, drawn as the design's demos draw it.
// Glyphs for the third design (DESIGN.md §3).
//
// Device glyphs are solid white shapes on a 24 grid, cut with even-odd
// holes, so they read at a glance on a colored tile, like the symbols on
// a phone's home screen. UI glyphs are 1.6px strokes in the text color.
(function () {
  const G = (window.G = window.G || {});
  const n = (v) => +v.toFixed(2);
  const rr = (x, y, w, h, r) =>
    `M${n(x + r)},${n(y)}H${n(x + w - r)}A${r},${r} 0 0 1 ${n(x + w)},${n(y + r)}V${n(y + h - r)}A${r},${r} 0 0 1 ${n(x + w - r)},${n(y + h)}H${n(x + r)}A${r},${r} 0 0 1 ${n(x)},${n(y + h - r)}V${n(y + r)}A${r},${r} 0 0 1 ${n(x + r)},${n(y)}Z`;
  const circ = (cx, cy, r) => `M${n(cx - r)},${n(cy)}a${r},${r} 0 1,0 ${n(2 * r)},0a${r},${r} 0 1,0 ${n(-2 * r)},0Z`;

  const DEV = {
    laptop: rr(3.5, 4.5, 17, 11.5, 2.2) + rr(5.3, 6.3, 13.4, 7.9, 0.8) + rr(1.5, 17, 21, 2.6, 1.3),
    desktop: rr(2.5, 3.5, 19, 12.6, 2.2) + rr(4.3, 5.3, 15.4, 9, 0.8) + rr(10.75, 16, 2.5, 2.9, 0.3) + rr(7.25, 18.6, 9.5, 1.9, 0.95),
    mini: rr(2.5, 8.5, 19, 8.5, 2.8) + circ(17.6, 12.75, 0.95),
    server: rr(3, 3.5, 18, 7.5, 2.2) + rr(3, 13, 18, 7.5, 2.2) + circ(17.1, 7.25, 1) + circ(17.1, 16.75, 1) + rr(6, 6.65, 6.5, 1.2, 0.6) + rr(6, 16.15, 6.5, 1.2, 0.6),
    nas: rr(5.8, 2.5, 12.4, 19, 2.8) + rr(8, 5, 8, 4.4, 1) + rr(8, 10.9, 8, 4.4, 1) + circ(12, 18.4, 0.95),
    phone: rr(6.8, 2, 10.4, 20, 2.9) + rr(8.4, 3.6, 7.2, 16.8, 1.6),
    console:
      'M7.2,7H16.8C20.4,7 22.4,10.4 22.8,14.4C23.1,18.4 21.1,20 19.4,19.4C18.2,19 17.3,17.5 16.4,16.1H7.6C6.7,17.5 5.8,19 4.6,19.4C2.9,20 0.9,18.4 1.2,14.4C1.6,10.4 3.6,7 7.2,7Z' +
      'M6.2,10.2h1.6v1.8h1.8v1.6h-1.8v1.8h-1.6v-1.8h-1.8v-1.6h1.8Z' +
      circ(16.1, 13.9, 1.05) +
      circ(18.5, 11.7, 1.05),
    board: rr(2.5, 5, 19, 14, 2.2) + rr(7.4, 9, 6.2, 6.2, 1) + rr(5.2, 6.4, 1.3, 1.3, 0.3) + rr(7.6, 6.4, 1.3, 1.3, 0.3) + rr(10, 6.4, 1.3, 1.3, 0.3) + rr(12.4, 6.4, 1.3, 1.3, 0.3) + rr(16.2, 10, 3.1, 2.8, 0.5) + rr(16.2, 14, 3.1, 2.8, 0.5),
    database:
      'M4,6.6a8,3.1 0 1,0 16,0a8,3.1 0 1,0 -16,0Z' +
      'M4,9.4v2.9a8,3.1 0 0,0 16,0v-2.9a8,3.1 0 0,1 -16,0Z' +
      'M4,15v2.9a8,3.1 0 0,0 16,0v-2.9a8,3.1 0 0,1 -16,0Z',
    relay: 'M2.5,12L7,7.5V10.4H17V7.5L21.5,12L17,16.5V13.6H7V16.5Z',
  };
  G.device = (k, size = 28) => `<svg viewBox="0 0 24 24" width="${size}" height="${size}" aria-hidden="true"><path fill-rule="evenodd" d="${DEV[k]}"/></svg>`;

  const UI = {
    home: '<path d="M4 10.5 12 4l8 6.5V19a1.5 1.5 0 0 1-1.5 1.5h-3.5v-5.5h-6v5.5H5.5A1.5 1.5 0 0 1 4 19z"/>',
    people: '<circle cx="9" cy="8.5" r="3.2"/><path d="M3.5 19.5c.6-3.2 2.8-5 5.5-5s4.9 1.8 5.5 5"/><circle cx="16.8" cy="9.5" r="2.6"/><path d="M15.6 14.7c2.4-.3 4.4 1.2 4.9 4.3"/>',
    inbox: '<path d="M4 13.5 6.2 6.2A1.5 1.5 0 0 1 7.6 5h8.8a1.5 1.5 0 0 1 1.4 1.2L20 13.5V18a1.5 1.5 0 0 1-1.5 1.5h-13A1.5 1.5 0 0 1 4 18z"/><path d="M4 13.5h4.5l1.2 2h4.6l1.2-2H20"/>',
    plus: '<path d="M12 5.5v13M5.5 12h13"/>',
    gear: '<path d="M4 7.5h9M17 7.5h3M4 16.5h3M11 16.5h9"/><circle cx="15" cy="7.5" r="2"/><circle cx="9" cy="16.5" r="2"/>',
    close: '<path d="M7 7l10 10M17 7 7 17"/>',
    folder: '<path d="M3.5 7.5A1.5 1.5 0 0 1 5 6h4.2l1.8 2H19a1.5 1.5 0 0 1 1.5 1.5v8A1.5 1.5 0 0 1 19 19H5a1.5 1.5 0 0 1-1.5-1.5z"/>',
    screen: '<rect x="3.5" y="4.5" width="17" height="11.5" rx="2"/><path d="M9 20h6M12 16v4"/>',
    globe: '<circle cx="12" cy="12" r="8.5"/><path d="M3.5 12h17M12 3.5c2.4 2.4 3.6 5.2 3.6 8.5s-1.2 6.1-3.6 8.5c-2.4-2.4-3.6-5.2-3.6-8.5S9.6 5.9 12 3.5z"/>',
    terminal: '<rect x="3.5" y="4.5" width="17" height="15" rx="2.5"/><path d="M7.5 9.5 10 12l-2.5 2.5M12.5 15h4"/>',
    chevron: '<path d="m9 6 6 6-6 6"/>',
    chevronLeft: '<path d="m15 6-6 6 6 6"/>',
    replay: '<path d="M4.5 12a7.5 7.5 0 1 0 2.2-5.3"/><path d="M4.5 4.5v3.2h3.2"/>',
    lock: '<rect x="5.5" y="10.5" width="13" height="9.5" rx="2.2"/><path d="M8.5 10.5V8a3.5 3.5 0 0 1 7 0v2.5"/>',
    sun: '<circle cx="12" cy="12" r="4"/><path d="M12 2.5v2M12 19.5v2M2.5 12h2M19.5 12h2M5.3 5.3l1.4 1.4M17.3 17.3l1.4 1.4M5.3 18.7l1.4-1.4M17.3 6.7l1.4-1.4"/>',
    moon: '<path d="M19.5 14.5A8 8 0 1 1 9.5 4.5a6.5 6.5 0 0 0 10 10z"/>',
    bolt: '<path d="M13 3 5 13.5h6L10.5 21 19 10.5h-6z"/>',
    // M7 (DESIGN §6.15): a link out of Deneb, deleting for good, a relay.
    external: '<path d="M9.5 6.5h8v8M17.5 6.5 7 17"/>',
    trash: '<path d="M5 7h14M10 7V5.5h4V7M7 7l.8 11.3A1.5 1.5 0 0 0 9.3 19.7h5.4a1.5 1.5 0 0 0 1.5-1.4L17 7M10.3 10.5v5.5M13.7 10.5v5.5"/>',
    arrows: '<path d="M5 9h13.5l-3-3M19 15H5.5l3 3"/>',
  };
  G.ui = (k, size = 18) =>
    `<svg viewBox="0 0 24 24" width="${size}" height="${size}" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${UI[k]}</svg>`;

  // The Deneb mark: a bright star, four rays with concave sides, the
  // vertical pair the longer (DESIGN §15; the desktop's starGlyph).
  G.star = (size = 18, color = 'currentColor') =>
    `<svg viewBox="0 0 24 24" width="${size}" height="${size}" aria-hidden="true"><path fill="${color}" d="M12,1.5 C12.21,4.07 12.27,6.03 13.3,8.03 C14.03,9.46 15.11,10.54 16.56,11.2 C17.94,11.83 19.09,11.87 20.67,12 C19.09,12.13 17.94,12.17 16.56,12.8 C15.11,13.46 14.03,14.54 13.3,15.97 C12.27,17.97 12.21,19.93 12,22.5 C11.79,19.93 11.73,17.97 10.7,15.97 C9.97,14.54 8.89,13.46 7.44,12.8 C6.06,12.17 4.91,12.13 3.33,12 C4.91,11.87 6.06,11.83 7.44,11.2 C8.89,10.54 9.97,9.46 10.7,8.03 C11.73,6.03 11.79,4.07 12,1.5Z"/></svg>`;
})();
