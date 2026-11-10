// Getting Deneb onto this computer (DESIGN.md §14.7): the download card,
// which the LAN page shows in the device card's place, and on a phone the
// dock at the bottom (there is no Deneb for phones, §14.6).
import { after } from './clock.js';

// The few glyphs these pages draw, as the desktop's (design/demos/v3/glyphs.js).
const UI                         = {
  close: '<path d="M7 7l10 10M17 7 7 17"/>',
  download: '<path d="M12 4.5v10.5M7.5 10.5 12 15l4.5-4.5"/><path d="M5 19.5h14"/>',
  copy: '<rect x="8.5" y="8.5" width="11" height="11" rx="2.2"/><path d="M15.5 8.5V6.2a1.7 1.7 0 0 0-1.7-1.7H6.2a1.7 1.7 0 0 0-1.7 1.7v7.6a1.7 1.7 0 0 0 1.7 1.7h2.3"/>',
  check: '<path d="m5.5 12.5 4 4 9-9"/>',
  chevron: '<path d="m9 6 6 6-6 6"/>',
};
export function icon(k        , size = 18)         {
  return `<svg viewBox="0 0 24 24" width="${size}" height="${size}" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${UI[k] ?? ''}</svg>`;
}
// The Deneb mark: a bright star, four rays with concave sides, the
// vertical pair the longer (DESIGN §15; the desktop's starGlyph).
export function star(size = 18, color = 'currentColor')         {
  return `<svg viewBox="0 0 24 24" width="${size}" height="${size}" aria-hidden="true"><path fill="${color}" d="M12,1.5 C12.21,4.07 12.27,6.03 13.3,8.03 C14.03,9.46 15.11,10.54 16.56,11.2 C17.94,11.83 19.09,11.87 20.67,12 C19.09,12.13 17.94,12.17 16.56,12.8 C15.11,13.46 14.03,14.54 13.3,15.97 C12.27,17.97 12.21,19.93 12,22.5 C11.79,19.93 11.73,17.97 10.7,15.97 C9.97,14.54 8.89,13.46 7.44,12.8 C6.06,12.17 4.91,12.13 3.33,12 C4.91,11.87 6.06,11.83 7.44,11.2 C8.89,10.54 9.97,9.46 10.7,8.03 C11.73,6.03 11.79,4.07 12,1.5Z"/></svg>`;
}

export function esc(s        )         {
  return s.replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]          );
}

// spaced puts a space between Chinese and Latin letters or digits, as the
// desktop writes names ("Mia 的 MacBook").
export function spaced(t        )         {
  return t.replace(/([一-鿿])([A-Za-z0-9])/g, '$1 $2').replace(/([A-Za-z0-9])([一-鿿])/g, '$1 $2');
}

                                                     

// The installers, where deneb-web serves them (-downloads). Until they are
// published (M6) a deployment points these elsewhere.
export const DOWNLOADS                                                    = {
  macos: { label: 'macOS 版', file: 'Deneb.dmg' },
  windows: { label: 'Windows 版', file: 'Deneb-Setup.exe' },
  linux: { label: 'Linux 版', file: 'deneb.deb' },
};
export const downloadURL = (p          )         => '/download/' + DOWNLOADS[p].file;

// detectPlatform tells which installer this browser's computer takes, or
// null on a phone or tablet (no Deneb for those yet).
export function detectPlatform(ua         = navigator.userAgent, touch         = navigator.maxTouchPoints || 0)                  {
  if (/Android|iPhone|iPad|iPod|Mobile/i.test(ua)) return null;
  // iPadOS asks for desktop pages as a Mac: a Mac with a touch screen is one.
  if (/Macintosh|Mac OS X/i.test(ua)) return touch > 1 ? null : 'macos';
  if (/Windows/i.test(ua)) return 'windows';
  if (/Linux|X11|CrOS/i.test(ua)) return 'linux';
  return null;
}

// pickExample names a device of someone else's in the LAN for the card's
// sentence: a NAS first (what people come for), then one that is online.
export function pickExample(devices                                                                         , me        )                     {
  const others = devices.filter((d) => d.owner !== me);
  const d = others.find((x) => x.kind === 'nas') ?? others.find((x) => x.online) ?? others[0];
  return d ? spaced(d.displayName) : undefined;
}

                                      
                  
                                                                                             
                                                                    
                                                       
                                                           
                             
                                     
                       
 

// maskEmail shows the first letter and the domain: l***@gmail.com.
export function maskEmail(email        )         {
  const at = email.indexOf('@');
  return at > 0 ? email[0] + '***' + email.slice(at) : email;
}

// downloadCard is the card's contents, in the device card's frame (the LAN
// page puts it in its sheet): the mark and "下载 Deneb", one sentence, the
// button for this computer's system, the others behind a small line, and
// after a download where to look next.
export function downloadCard(o                     )              {
  const el = document.createElement('div');
  el.className = 'dl-card';
  const platform = o.platform === undefined ? detectPlatform() : o.platform;
  let opened = false;
  let all = platform == null;
  const draw = () => {
    const mine = platform ?? 'macos';
    const btn = (p          , primary         ) =>
      `<a class="btn ${primary ? 'primary' : 'ghost'} dl-btn" href="${downloadURL(p)}" download data-p="${p}">${icon('download', 16)}下载 ${DOWNLOADS[p].label}</a>`;
    const buttons = all ? (['macos', 'windows', 'linux']              ).map((p) => btn(p, p === platform)).join('') : btn(mine, true) + `<button class="dl-more" type="button">也有 ${(['macos', 'windows', 'linux']              ).filter((p) => p !== mine).map((p) => DOWNLOADS[p].label).join('、')}</button>`;
    const what = o.what ?? `${o.lanName}里的设备`;
    const lead = o.example ? `装上以后，这台电脑就能用${what}，比如${o.example}。` : `装上以后，这台电脑就能用${what}。`;
    el.innerHTML =
      `<div class="top"><div class="dl-mark">${star(20, '#fff')}</div><div><b>下载 Deneb</b><span>装在这台电脑上</span></div>` +
      `<button class="icon-btn sheet-close" type="button" aria-label="关闭">${icon('close', 16)}</button></div>` +
      `<p class="dl-text">${esc(spaced(lead))}</p>` +
      `<div class="dl-buttons">${buttons}</div>` +
      (opened ? `<div class="dl-after" role="status">${esc(spaced(`装好打开以后，${o.email ? '用 ' + maskEmail(o.email) + ' ' : '用同一个邮箱'}登录，${o.after ?? '这台电脑会自己出现在这里'}。`))}</div>` : '');
    el.querySelector('.sheet-close')?.addEventListener('click', () => o.onClose?.());
    el.querySelector('.dl-more')?.addEventListener('click', () => {
      all = true;
      draw();
    });
    for (const a of el.querySelectorAll                   ('a.dl-btn')) {
      a.addEventListener('click', () => {
        const p = a.dataset.p            ;
        if (!opened) {
          opened = true;
          // After the browser has taken the click: the line appears.
          after(0, draw);
        }
        o.onDownload?.(p);
      });
    }
  };
  draw();
  return el;
}

// phoneDock is the pill at the bottom of the phone's pages: Deneb is for
// computers, and the link to send to one. Copied, the pill says so itself
// for 3 seconds (as the desktop's tray says what the hand holds), with no
// toast over the list.
export function phoneDock(link         = location.origin + '/download')              {
  const el = document.createElement('div');
  el.className = 'm-dock';
  const host = new URL(link).host;
  const idleText = `Deneb 装在电脑上用，<br />在电脑上打开 ${esc(host)} 下载`;
  const idleBtn = `${icon('copy', 15)}复制链接`;
  el.innerHTML = `<span class="m-dock-text" role="status">${idleText}</span><button class="btn primary" type="button">${idleBtn}</button>`;
  const text = el.querySelector('.m-dock-text')               ;
  const btn = el.querySelector('button')                     ;
  const swap = (node             , html        ) => {
    if (node.innerHTML === html) return;
    node.innerHTML = html;
    node.animate([{ opacity: 0 }, { opacity: 1 }], { duration: 160, easing: 'ease-out' });
  };
  let at = 0;
  btn.addEventListener('click', async () => {
    try {
      await navigator.clipboard.writeText(link);
    } catch {
      // No clipboard (an old browser, or not a secure page): the link is
      // in the words for the hand to copy.
      swap(text, `复制不了，记下这个地址：<br />${esc(link)}`);
      return;
    }
    const mine = ++at;
    swap(text, '已复制下载链接，<br />在电脑上打开就行');
    swap(btn, `${icon('check', 15)}已复制`);
    after(3000, () => {
      if (mine !== at) return;
      swap(text, idleText);
      swap(btn, idleBtn);
    });
  });
  return el;
}
