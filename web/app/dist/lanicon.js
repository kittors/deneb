// A LAN's picture (design/plan/m7/switch-motion.md §5), as the desktop's
// (cmd/deneb/lanicon.go): one of the presets, cute pictures from Fluent
// Emoji's 3D set (MIT, THIRD_PARTY_NOTICES), served from /lan-icons/; or
// the owner's own, a 128×128 PNG in a rounded square. A server without the
// feature lanIcon, or a LAN without a picture: its members' balls, as before.
                                                    
import { esc } from './download.js';
import tokens from './tokens.js';

// The sizes, the desktop's (cmd/deneb/lanicon.go): in a row of the
// sidebar, beside the title.
const sizes = tokens.desktop['lanicon.go']                          ;
export const LAN_ICON_ROW = sizes.lanIconRow;
export const LAN_ICON_TITLE = sizes.lanIconTitle;

// The presets' names, as proto.LANIconPresets has them: the protocol's,
// never changed; a name this page does not know shows no picture.
export const LAN_ICON_PRESETS = [
  'fluent3d/cat', 'fluent3d/dog', 'fluent3d/fox', 'fluent3d/panda',
  'fluent3d/rabbit', 'fluent3d/frog', 'fluent3d/penguin', 'fluent3d/whale',
  'fluent3d/mushroom', 'fluent3d/cactus', 'fluent3d/rainbow', 'fluent3d/planet',
  'fluent3d/gamepad', 'fluent3d/house', 'fluent3d/sunflower', 'fluent3d/rocket',
];

// lanIcons reports whether the server gives LANs pictures.
export const lanIcons = (v                             )          => !!v && (v.features || []).includes('lanIcon');

// lanIconSrc is where a picture is: a preset's file, or the owner's own.
export function lanIconSrc(i                     )         {
  if (!i) return '';
  if (i.preset) return LAN_ICON_PRESETS.includes(i.preset) ? `/lan-icons/${i.preset.slice(i.preset.indexOf('/') + 1)}.png` : '';
  return i.image ? `data:image/png;base64,${i.image}` : '';
}

// lanIconHTML is the picture size DIPs big, or '' with none to show; the
// owner's own in a rounded square (.own).
export function lanIconHTML(i                     , size        )         {
  const src = lanIconSrc(i);
  if (!src) return '';
  return `<img class="lan-ic${i .preset ? '' : ' own'}" src="${esc(src)}" width="${size}" height="${size}" style="--s:${size}px" alt="" aria-hidden="true" draggable="false" />`;
}
