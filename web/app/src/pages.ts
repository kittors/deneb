// The pages drawn in the desktop's look (shell.ts and the design's App):
// the LAN canvas, scene 4's console, the relays. main.ts imports this
// module once it has rendered, and draws these pages when it is in.
import { registerPage } from './main.ts';
import { sharePage } from './share.ts';
import { relaysPage } from './relays.ts';
import { lanPage } from './lan.ts';
import { minePage } from './mine.ts';
import { sharedPage } from './shared.ts';

registerPage('share', sharePage);
registerPage('relays', relaysPage);
registerPage('lan', lanPage);
registerPage('mine', minePage);
registerPage('shared', sharedPage);
