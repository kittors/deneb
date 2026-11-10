// The desktop's drawing as the design's demos have it (design/demos/v3:
// glyphs, links, the app frame with its canvas, drag, card and toast) runs
// on this app's clock: app.js and links.js read window.D, which here is
// clock.ts's, so the recorder's virtual clock drives them too. Imported
// before the v3-*.ts modules.
import { D } from './clock.ts';

const w = window as unknown as { D: unknown };
w.D = Object.assign(D, {
  theme: { get: (): string => document.documentElement.dataset.theme || 'light' },
});
