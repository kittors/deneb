// Static server for the probe: serves app.wasm as application/wasm, brotli or
// gzip when the browser takes them (as a production CDN would), never cached.
import { createServer } from 'node:http';
import { readFileSync, existsSync, statSync } from 'node:fs';
import { join, extname } from 'node:path';
const root = process.argv[2], port = Number(process.argv[3] || 47610);
const types = { '.html': 'text/html; charset=utf-8', '.js': 'text/javascript', '.wasm': 'application/wasm', '.json': 'application/json', '.css': 'text/css', '.svg': 'image/svg+xml', '.png': 'image/png' };
createServer((req, res) => {
  let p = decodeURIComponent(new URL(req.url, 'http://x').pathname);
  if (p.endsWith('/')) p += 'index.html';
  const f = join(root, p);
  if (!f.startsWith(root) || !existsSync(f) || statSync(f).isDirectory()) { res.writeHead(404); return res.end('404'); }
  const ae = req.headers['accept-encoding'] || '';
  const h = { 'Content-Type': types[extname(f)] || 'application/octet-stream', 'Cache-Control': process.env.CACHE && f.endsWith('.wasm') ? 'public, max-age=31536000, immutable' : 'no-store' };
  let body = null;
  if (/\bbr\b/.test(ae) && existsSync(f + '.br')) { body = readFileSync(f + '.br'); h['Content-Encoding'] = 'br'; }
  else if (/\bgzip\b/.test(ae) && existsSync(f + '.gz')) { body = readFileSync(f + '.gz'); h['Content-Encoding'] = 'gzip'; }
  else body = readFileSync(f);
  h['Content-Length'] = body.length;
  res.writeHead(200, h); res.end(body);
}).listen(port, '127.0.0.1', () => console.log('serving', root, 'on', port));
