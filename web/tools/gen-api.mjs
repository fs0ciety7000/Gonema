// Generates src/api/schema.d.ts from web/openapi.json (written by the Go side).
// openapi-typescript needs the TypeScript 5 JS API; TS 7 is native-only, so the
// generator lives in tools/openapi with its own TS 5 install.
import { execFileSync } from 'node:child_process';
import { existsSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const web = join(dirname(fileURLToPath(import.meta.url)), '..');
const tools = join(web, 'tools', 'openapi');
const input = process.argv[2] ?? join(web, 'openapi.json');
if (!existsSync(input)) {
  console.error('web/openapi.json is missing: generate it from the Go server first.');
  process.exit(1);
}
if (!existsSync(join(tools, 'node_modules'))) {
  execFileSync('npm', ['ci', '--no-audit', '--no-fund'], { cwd: tools, stdio: 'inherit' });
}
execFileSync(
  join(tools, 'node_modules', '.bin', 'openapi-typescript'),
  [input, '-o', join(web, 'src', 'api', 'schema.d.ts'), '--root-types', '--root-types-no-schema-prefix'],
  { stdio: 'inherit' },
);
