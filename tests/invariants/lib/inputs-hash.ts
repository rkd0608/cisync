import { createHash } from 'node:crypto';

/**
 * Mirror of planner/inputs_hash.go HashInputs (I-02 reuse key).
 * WHY: the reuse-sensitivity property ("any output-affecting input change
 * ⇒ new key") is property-tested against this independent implementation.
 */

export interface InputsMaterial {
  baseSha: string;
  lockfiles: readonly string[];
  flags: readonly string[];
  toolchain: string;
}

function sorted(values: readonly string[]): string[] {
  return [...values].sort();
}

export function hashInputs(m: InputsMaterial): string {
  const material = [
    `base_sha=${m.baseSha}`,
    `lockfiles=${sorted(m.lockfiles).join(',')}`,
    `flags=${sorted(m.flags).join(',')}`,
    `toolchain=${m.toolchain}`,
  ].join('\n');
  return 'sha256:' + createHash('sha256').update(material, 'utf8').digest('hex');
}

/** The documented I-02 rule: reuse is legal only on FULL equality of every field. */
export function materialsEqualForReuse(a: InputsMaterial, b: InputsMaterial): boolean {
  const sameSet = (x: readonly string[], y: readonly string[]) =>
    x.length === y.length && sorted(x).every((v, i) => v === sorted(y)[i]);
  return (
    a.baseSha === b.baseSha &&
    a.toolchain === b.toolchain &&
    sameSet(a.lockfiles, b.lockfiles) &&
    sameSet(a.flags, b.flags)
  );
}
