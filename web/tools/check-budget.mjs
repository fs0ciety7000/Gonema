// Checks the initial JS budget of the library route on a production build (dist/).
// Same measure as the "critical" bundle of e2e/perf.mjs, without a server or a browser:
// the JS that index.html references plus the chunks those import statically
// (lazy chunks, reached through import(), are not counted), gzip level 9.
//   npm run build && npm run budget
import { existsSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { gzipSync } from 'node:zlib';

const BUDGET_GZIP_BYTES = 200_000; // 200 KB, the budget of docs (web/e2e/fidelity.md)

const dist = fileURLToPath(new URL('../dist/', import.meta.url));
const indexHtml = join(dist, 'index.html');
if (!existsSync(indexHtml)) {
  console.error('dist/index.html not found: run `npm run build` first.');
  process.exit(2);
}

const html = readFileSync(indexHtml, 'utf8');
const queue = [...html.matchAll(/(?:src|href)="\.\/(assets\/[^"]+\.js)"/g)].map((m) => m[1]);
if (queue.length === 0) {
  console.error('index.html references no JS file: the build output changed shape.');
  process.exit(2);
}

// Static imports between chunks: `from"./x.js"` or `import"./x.js"` (not `import("./x.js")`).
const staticImport = /(?:\bfrom\s*|\bimport\s*)["']\.\/([^"']+\.js)["']/g;
const seen = new Set();
let total = 0;
const rows = [];
while (queue.length > 0) {
  const file = queue.shift();
  if (seen.has(file)) continue;
  seen.add(file);
  const buf = readFileSync(join(dist, file));
  const gz = gzipSync(buf, { level: 9 }).length;
  total += gz;
  rows.push({ file, raw: buf.length, gzip: gz });
  for (const m of buf.toString('utf8').matchAll(staticImport)) queue.push(`assets/${m[1]}`);
}

for (const r of rows) console.log(`${r.file}  raw ${r.raw} B  gzip ${r.gzip} B`);
console.log(`Initial JS (library route): ${total} B gzip, budget ${BUDGET_GZIP_BYTES} B`);
if (total > BUDGET_GZIP_BYTES) {
  console.error(`Over budget by ${total - BUDGET_GZIP_BYTES} B.`);
  process.exit(1);
}
