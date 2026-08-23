// Structure gate — enforces docs/REPO_STANDARDS §2/§5.
import { existsSync, readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';

const FORBIDDEN = [/\.(bak|orig)$/i, /(^|~)$/, /^\.DS_Store$/, /^tmp_/i, /^scratch_/i, /^output\./i];
const GO_FILE = /^[a-z0-9_]+(_test)?\.go$/;
const MIGRATION = /^\d{4}_[a-z0-9_]+\.(up|down)\.sql$/;
let errors = [];

function walk(dir, base = '') {
  for (const name of readdirSync(dir)) {
    const rel = join(base, name);
    if (FORBIDDEN.some((re) => re.test(name))) errors.push(`forbidden artifact: ${rel}`);
    const p = join(dir, name);
    if (statSync(p).isDirectory()) {
      if (name === 'node_modules' || name === '.git' || name === '.next') continue;
      walk(p, rel);
    } else if (name.endsWith('.go') && !name.endsWith('_generated.go')) {
      if (!GO_FILE.test(name)) errors.push(`bad Go filename (snake_case required): ${rel}`);
    }
  }
}

walk('.');

for (const svc of ['ingest', 'control-plane', 'runner-fleet', 'github-connector']) {
  const dir = `services/${svc}`;
  if (!existsSync(dir)) continue;
  if (!existsSync(`${dir}/cmd/${svc}/main.go`)) errors.push(`${dir}: missing cmd/${svc}/main.go`);
  if (!existsSync(`${dir}/go.mod`)) errors.push(`${dir}: missing go.mod`);
  if (!existsSync(`${dir}/Dockerfile`)) errors.push(`${dir}: missing Dockerfile`);
  const mdir = `${dir}/migrations`;
  if (existsSync(mdir)) {
    for (const f of readdirSync(mdir)) {
      if (f !== '.' && !MIGRATION.test(f)) errors.push(`${mdir}: bad migration name: ${f}`);
    }
  }
}

for (const req of ['docs/ARCHITECTURE.md', 'docs/INVARIANTS.md', 'packages/contracts/openapi.yaml', 'Makefile']) {
  if (!existsSync(req)) errors.push(`missing required file: ${req}`);
}

if (errors.length) {
  console.error('HYGIENE GATE FAILED:');
  for (const e of errors) console.error(' -', e);
  process.exit(1);
}
console.log(`hygiene OK (${new Date().toISOString()})`);
