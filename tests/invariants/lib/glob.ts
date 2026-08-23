/**
 * Minimal glob matcher mirroring the contract's repair `allowed_paths`
 * semantics (planner/glob.go): `**` spans path segments, `*` stays within
 * one segment, literals match exactly. WHY local: I-05 confinement is
 * property-tested against this independent implementation so a planner
 * regression cannot self-certify.
 */

export function globToRegExp(pattern: string): RegExp {
  let re = '';
  let i = 0;
  while (i < pattern.length) {
    const ch = pattern[i];
    if (ch === undefined) break;
    if (ch === '*' && pattern[i + 1] === '*') {
      re += '.*';
      i += 2;
      if (pattern[i] === '/') i += 1; // `**/` also matches zero segments
      continue;
    }
    if (ch === '*') {
      re += '[^/]*';
      i += 1;
      continue;
    }
    if (ch === '?') {
      re += '[^/]';
      i += 1;
      continue;
    }
    re += ch.replace(/[.+^${}()|[\]\\]/g, '\\$&');
    i += 1;
  }
  return new RegExp(`^${re}$`);
}

export interface PathConfinementVerdict {
  allowed: boolean;
  matchedGlob?: string;
  reason?: string;
}

/** Server-side gate rule (I-05): every touched path must hit an allowed
 *  glob, no prohibited glob, and no traversal escape. */
export function checkPathConfinement(
  path: string,
  allowedPaths: readonly string[],
  prohibitedPaths: readonly string[] = [],
): PathConfinementVerdict {
  if (path.includes('..') || path.startsWith('/')) {
    return { allowed: false, reason: 'path_traversal_escape' };
  }
  for (const banned of prohibitedPaths) {
    if (globToRegExp(banned).test(path)) return { allowed: false, matchedGlob: banned, reason: 'prohibited_path' };
  }
  for (const allowed of allowedPaths) {
    if (globToRegExp(allowed).test(path)) return { allowed: true, matchedGlob: allowed };
  }
  return { allowed: false, reason: 'outside_allowed_paths' };
}
