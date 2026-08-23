import { readFileSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { verifyChain, type ChainReport } from './chain.js';
import type { LedgerEvent } from './api-schemas.js';

/**
 * Loader for golden event-sequence fixtures under tests/fixtures/events/.
 * The valid chain is the reference for I-07 verification; the tampered
 * variants each break exactly one rule so failures are diagnosable.
 */

const FIXTURE_DIR = join(dirname(fileURLToPath(import.meta.url)), '..', '..', 'fixtures', 'events');

export const VALID_FIXTURE = 'golden-chain.valid.json';
export const TAMPERED_FIXTURES = [
  'golden-chain.tampered-payload.json',
  'golden-chain.tampered-entry-hash.json',
  'golden-chain.tampered-prev-hash.json',
  'golden-chain.missing-middle.json',
] as const;

function readChain(name: string): LedgerEvent[] {
  return JSON.parse(readFileSync(join(FIXTURE_DIR, name), 'utf8')) as LedgerEvent[];
}

export function loadFixtureChain(name: string): LedgerEvent[] {
  return readChain(name);
}

export const verifyGoldenFixtures = {
  valid(): ChainReport {
    return verifyChain(readChain(VALID_FIXTURE));
  },
  tampered(name: (typeof TAMPERED_FIXTURES)[number]): ChainReport {
    return verifyChain(readChain(name));
  },
};
