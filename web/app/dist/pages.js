// The pages drawn in the desktop's look (shell.ts and the design's App):
// the LAN canvas, scene 4's console, the relays, the deployment's page.
// main.ts imports this module once it has rendered, and draws these pages
// when it is in.
import { registerPage } from './main.js';
import { sharePage } from './share.js';
import { relaysPage } from './relays.js';
import { lanPage } from './lan.js';
import { minePage } from './mine.js';
import { sharedPage } from './shared.js';
import { operatePage } from './operate.js';
import { welcomePage } from './welcome.js';
import { consolePage } from './console.js';

registerPage('share', sharePage);
registerPage('relays', relaysPage);
registerPage('lan', lanPage);
registerPage('mine', minePage);
registerPage('shared', sharedPage);
registerPage('ops', operatePage);
registerPage('home', welcomePage);
registerPage('console', consolePage);
