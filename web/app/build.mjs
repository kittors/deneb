#!/usr/bin/env node
// Builds the Web app with no dependencies: Node's own TypeScript stripping
// (module.stripTypeScriptTypes) turns src/*.ts into ES modules in dist/,
// with their imports pointed at the .js files; the desktop's tokens
// (web/tokens.json, web/tokens.css, generated from cmd/deneb) go along as
// tokens.js and tokens.css; src/*.css are joined into app.css. Type checking
// is tsc's (tsconfig.json, noEmit); this only strips the types.
//
// usage: node web/app/build.mjs
import { stripTypeScriptTypes } from 'node:module';
import { readFileSync, writeFileSync, mkdirSync, rmSync, readdirSync, copyFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

// Node marks the stripping experimental; the build says so once, here.
const warn = process.emitWarning;
process.emitWarning = (w, ...rest) => (String(w).includes('stripTypeScriptTypes') ? undefined : warn.call(process, w, ...rest));

const app = dirname(fileURLToPath(import.meta.url));
const src = join(app, 'src'), out = join(app, 'dist'), web = dirname(app);
rmSync(out, { recursive: true, force: true });
mkdirSync(out, { recursive: true });

const files = readdirSync(src).sort();
let n = 0;
for (const f of files) {
  if (!f.endsWith('.ts') || f.endsWith('.d.ts')) continue;
  let code = stripTypeScriptTypes(readFileSync(join(src, f), 'utf8'), { mode: 'strip' });
  // './x.ts' → './x.js', in static imports, re-exports and import().
  code = code.replace(/((?:from|import)\s*\(?\s*['"])(\.{1,2}\/[^'"]+?)\.ts(['"])/g, '$1$2.js$3');
  writeFileSync(join(out, f.replace(/\.ts$/, '.js')), code);
  n++;
}
const css = files.filter((f) => f.endsWith('.css')).map((f) => `/* ${f} */\n` + readFileSync(join(src, f), 'utf8')).join('\n');
writeFileSync(join(out, 'app.css'), css);
writeFileSync(join(out, 'tokens.js'), '// Generated from web/tokens.json (go run ./web/tokens/gen).\nexport default ' + readFileSync(join(web, 'tokens.json'), 'utf8').trim() + ';\n');
copyFileSync(join(web, 'tokens.css'), join(out, 'tokens.css'));
copyFileSync(join(app, 'index.html'), join(out, 'index.html'));
console.log(`built ${n} modules into ${out}`);
