// Builds the website (DESIGN.md §14.16) with no dependencies, into
// <out>/site, which deneb-web serves (cmd/deneb-web/site.go): the pages,
// with the parts they share put in (_top.html, _foot.html); the docs, from
// their Markdown (docs/*.md, in the order of docs/docs.json) into
// _doc.html; the styles, scripts, pictures and films as they are.
//
// In the pages and the docs a picture is {{pic:name|what it shows}} (or
// ![what it shows](name) in Markdown): img/light-<name>.webp, and
// img/dark-<name>.webp where the system is dark, at the size img/sizes.json
// gives (design/tools/site-images.py writes both); {{img:name|…}} is one
// picture for both, img/<name>.webp; {{dark:name|…}} the dark one only.
//
// usage: node web/app/build.mjs (which calls buildSite), or
//        node web/site/build.mjs [out]   (default web/app/dist)
import { readFileSync, writeFileSync, mkdirSync, readdirSync, copyFileSync, statSync, existsSync } from 'node:fs';
import { dirname, join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';

const esc = (s) => s.replace(/[&<>"]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' })[c]);

export function buildSite(site, out) {
  const dest = join(out, 'site');
  mkdirSync(join(dest, 'docs'), { recursive: true });
  const read = (f) => readFileSync(join(site, f), 'utf8');
  const sizes = existsSync(join(site, 'img', 'sizes.json')) ? JSON.parse(read('img/sizes.json')) : {};

  // A picture for the system's light or dark, at its size so the page does
  // not move when it comes.
  // real: at the window's own size, half the picture's (the docs'); else
  // as wide as its box lets it be (the pages').
  const pic = (name, alt, eager, real) => {
    const wh = sizes[name];
    if (!wh) throw new Error(`site: no picture ${name} (img/sizes.json; design/tools/site-images.py)`);
    // At its own size it is twice as dense as the page: said so, a browser
    // sizes it by itself.
    const [w, h] = real ? wh.map((v) => Math.round(v / 2)) : wh;
    const x = real ? ' 2x' : '';
    const load = eager ? 'fetchpriority="high"' : 'loading="lazy" decoding="async"';
    return `<picture><source media="(prefers-color-scheme: dark)" srcset="/site/img/dark-${name}.webp${x}" /><img src="/site/img/light-${name}.webp" srcset="/site/img/light-${name}.webp${x}" width="${w}" height="${h}" alt="${esc(alt)}" ${load} /></picture>`;
  };
  // One picture for both, as the film's cover.
  const img = (name, alt) => {
    const wh = sizes[name];
    if (!wh) throw new Error(`site: no picture ${name} (img/sizes.json; design/tools/site-images.py)`);
    return `<img src="/site/img/${name}.webp" width="${wh[0]}" height="${wh[1]}" alt="${esc(alt)}" loading="lazy" decoding="async" />`;
  };
  // The dark one only, whatever the system's: the near-black blocks'
  // (DESIGN §14.16), which show the dark window in light and dark alike.
  const dark = (name, alt) => {
    const wh = sizes[name];
    if (!wh) throw new Error(`site: no picture ${name} (img/sizes.json; design/tools/site-images.py)`);
    return `<img src="/site/img/dark-${name}.webp" width="${wh[0]}" height="${wh[1]}" alt="${esc(alt)}" loading="lazy" decoding="async" />`;
  };
  // The parts the pages share, and which link of the top bar is this page's.
  const fill = (html, here) =>
    html
      .replace(/\{\{> (\w+)\}\}/g, (_, p) => read(`_${p}.html`).trimEnd())
      .replace(/\{\{here\.(\w+)\}\}/g, (_, k) => (k === here ? ' class="here" aria-current="page"' : ''))
      .replace(/\{\{pic:([\w-]+)\|([^|}]+)(\|eager)?\}\}/g, (_, n, alt, eager) => pic(n, alt, !!eager))
      .replace(/\{\{img:([\w-]+)\|([^|}]*)\}\}/g, (_, n, alt) => img(n, alt))
      .replace(/\{\{dark:([\w-]+)\|([^|}]*)\}\}/g, (_, n, alt) => dark(n, alt));

  let pages = 0;
  for (const [f, here] of [['index.html', 'home'], ['download.html', 'download'], ['404.html', '']]) {
    writeFileSync(join(dest, f), fill(read(f), here));
    pages++;
  }

  // The docs: each its page, with all of them on the left and the one
  // before and after it at the foot.
  const docs = JSON.parse(read('docs/docs.json'));
  docs.forEach((d, i) => {
    const doc = markdown(read(`docs/${d.slug}.md`), pic);
    const toc = docs.map((o) => `<a href="/docs/${o.slug}"${o.slug === d.slug ? ' class="here" aria-current="page"' : ''}>${esc(o.title)}</a>`).join('\n        ');
    const prev = docs[i - 1], next = docs[i + 1];
    const pager =
      `<nav class="pager" aria-label="上一篇和下一篇">` +
      (prev ? `<a class="prev" href="/docs/${prev.slug}"><span>上一篇</span><b>${esc(prev.title)}</b></a>` : '<span></span>') +
      (next ? `<a class="next" href="/docs/${next.slug}"><span>下一篇</span><b>${esc(next.title)}</b></a>` : '<span></span>') +
      `</nav>`;
    const html = fill(read('_doc.html'), 'docs')
      .replace('{{title}}', esc(doc.title))
      .replace('{{description}}', esc(doc.intro))
      .replace('{{toc}}', toc)
      .replace('{{body}}', doc.html)
      .replace('{{pager}}', pager);
    writeFileSync(join(dest, 'docs', `${d.slug}.html`), html);
    pages++;
  });

  // The rest as it is: styles, scripts, pictures, films, the recordings'
  // plans.
  let files = 0;
  const copy = (from) => {
    for (const f of readdirSync(join(site, from))) {
      const p = join(from, f);
      if (statSync(join(site, p)).isDirectory()) {
        mkdirSync(join(dest, p), { recursive: true });
        copy(p);
      } else if (!f.startsWith('.')) {
        copyFileSync(join(site, p), join(dest, p));
        files++;
      }
    }
  };
  for (const f of ['site.css', 'site.js', 'theme.js']) {
    copyFileSync(join(site, f), join(dest, f));
    files++;
  }
  for (const d of ['img', 'media', 'rec']) {
    if (existsSync(join(site, d))) {
      mkdirSync(join(dest, d), { recursive: true });
      copy(d);
    }
  }
  return { pages, files, dest };
}

// markdown turns a doc into its page's article: the little Markdown the docs
// use. "# " the title, and the paragraph after it the doc's one line; "1. "
// a step, numbered as written, with what is indented three under it (a
// list, a picture, a paragraph) in the step; "## " a heading; "> " a note;
// "- " a list; ![what it shows](name) a picture; `code`, **bold** and
// [words](link) inside a line.
export function markdown(src, pic) {
  const lines = src.replace(/\r/g, '').split('\n');
  // deneb.app, as all demos write the site's address: site.js puts the
  // real one in where the docs are served.
  const inline = (t) =>
    esc(t)
      .replace(/deneb\.app/g, '<span class="host">deneb.app</span>')
      .replace(/`([^`]+)`/g, '<code>$1</code>')
      .replace(/\*\*([^*]+)\*\*/g, '<b>$1</b>')
      .replace(/\[([^\]]+)\]\(([^)\s]+)\)/g, '<a href="$2">$1</a>');
  const figure = (name, alt) => `<div class="shot">${pic(name, alt, false, true)}</div>`;
  let title = '', intro = '';
  const blocks = (ls, top) => {
    let html = '';
    for (let i = 0; i < ls.length; ) {
      const l = ls[i];
      let m;
      if (!l.trim()) {
        i++;
      } else if (top && (m = l.match(/^# (.+)$/))) {
        title = m[1];
        html += `<h1>${inline(m[1])}</h1>`;
        i++;
      } else if ((m = l.match(/^## (.+)$/))) {
        html += `<h2>${inline(m[1])}</h2>`;
        i++;
      } else if (top && (m = l.match(/^(\d+)\. (.+)$/))) {
        const under = [];
        for (i++; i < ls.length && (ls[i].startsWith('   ') || (!ls[i].trim() && (ls[i + 1] || '').startsWith('   '))); i++) under.push(ls[i].slice(3));
        html += `<div class="st"><span class="num">${m[1]}</span><p>${inline(m[2])}</p>${blocks(under, false)}</div>`;
      } else if ((m = l.match(/^!\[([^\]]*)\]\(([\w-]+)\)$/))) {
        html += figure(m[2], m[1]);
        i++;
      } else if (l.startsWith('>')) {
        const paras = [[]];
        for (; i < ls.length && ls[i].startsWith('>'); i++) {
          const t = ls[i].replace(/^> ?/, '');
          if (t.trim()) paras[paras.length - 1].push(t);
          else paras.push([]);
        }
        html += `<div class="note">${paras.filter((p) => p.length).map((p) => `<p>${inline(p.join(''))}</p>`).join('')}</div>`;
      } else if (l.startsWith('- ')) {
        const items = [];
        for (; i < ls.length && ls[i].startsWith('- '); i++) items.push(`<li>${inline(ls[i].slice(2))}</li>`);
        html += `<ul>${items.join('')}</ul>`;
      } else {
        const para = [];
        for (; i < ls.length && ls[i].trim() && !/^(#|>|- |\d+\. |!\[)/.test(ls[i]); i++) para.push(ls[i].trim());
        const t = para.join('');
        if (top && title && !intro) {
          intro = t;
          html += `<p class="intro">${inline(t)}</p>`;
        } else {
          html += `<p>${inline(t)}</p>`;
        }
      }
    }
    return html;
  };
  const html = blocks(lines, true);
  return { title, intro, html };
}

// Run by itself: build into the app's dist (or the folder given).
if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) {
  const site = dirname(fileURLToPath(import.meta.url));
  const out = process.argv[2] || join(dirname(site), 'app', 'dist');
  const r = buildSite(site, out);
  console.log(`built the site: ${r.pages} pages, ${r.files} files into ${relative(process.cwd(), r.dest) || r.dest}`);
}
