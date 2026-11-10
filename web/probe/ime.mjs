// Chinese input method and screen reader check on a page of native UI.
import { open, sleep } from './cdp.mjs';
const [url, out, x, y] = [process.argv[2], process.argv[3], Number(process.argv[4]), Number(process.argv[5])];
const b = await open({ dark: false });
await b.S('Page.navigate', { url });
for (let i = 0; i < 200 && !(await b.ev('!!(window.__t && window.__t.firstFrame)')); i++) await sleep(50);
await sleep(800);
await b.shot(`${out}-0-start.png`);
const ta = () => b.ev(`(() => { const a = document.activeElement; const r = a.getBoundingClientRect(); const s = getComputedStyle(a); return { tag: a.tagName, x: Math.round(r.x), y: Math.round(r.y), w: Math.round(r.width), h: Math.round(r.height), opacity: s.opacity, value: a.value, pos: s.position, color: s.color, caret: s.caretColor } })()`);
await b.click(x, y); await sleep(600);
console.log('after click, focus:', JSON.stringify(await ta()));
await b.shot(`${out}-1-focused.png`);
// Composing 周末 with pinyin: the preedit first, then candidates, then commit.
await b.S('Input.imeSetComposition', { text: 'zhou', selectionStart: 4, selectionEnd: 4 }); await sleep(300);
console.log('composing zhou, focus:', JSON.stringify(await ta()));
await b.shot(`${out}-2-preedit-zhou.png`);
await b.S('Input.imeSetComposition', { text: 'zhou mo', selectionStart: 7, selectionEnd: 7 }); await sleep(300);
await b.shot(`${out}-3-preedit-zhoumo.png`);
await b.S('Input.insertText', { text: '周末' }); await sleep(300);
console.log('committed 周末, focus:', JSON.stringify(await ta()));
await b.shot(`${out}-4-committed.png`);
await b.S('Input.imeSetComposition', { text: 'ye', selectionStart: 2, selectionEnd: 2 }); await sleep(200);
await b.S('Input.imeSetComposition', { text: '夜', selectionStart: 1, selectionEnd: 1 }); await sleep(200);
await b.shot(`${out}-5-preedit-hanzi.png`);
await b.S('Input.insertText', { text: '夜' }); await sleep(300);
await b.shot(`${out}-6-committed2.png`);
console.log('final focus:', JSON.stringify(await ta()));
// Screen reader: what the accessibility tree exposes.
const ax = await b.S('Accessibility.getFullAXTree');
const named = ax.nodes.filter((n) => !n.ignored && (n.name?.value || '').trim()).map((n) => `${n.role?.value}:${n.name.value}`);
console.log('AX nodes:', ax.nodes.length, 'not ignored:', ax.nodes.filter((n) => !n.ignored).length, 'named:', named.length);
console.log('named sample:', JSON.stringify(named.slice(0, 40)));
const roles = {}; for (const n of ax.nodes.filter((n) => !n.ignored)) roles[n.role?.value] = (roles[n.role?.value] || 0) + 1;
console.log('roles:', JSON.stringify(roles));
await b.close();
