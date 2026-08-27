// Copy/token lint suite (mission requirement): enforces the charter-derived
// copy guide mechanically.
//  - BANNED_PHRASES matches docs/plans/PRODUCT_UX_PLAN.md §4 T4 exactly, and
//    no rendered surface nor shipped string literal may contain one.
//  - Evidence glyphs stay one-meaning-per-glyph (§7): ✓ ○ ✕ ⟳ ⏹ only.
//  - The design-token layer (globals.css) defines the full mission palette so
//    components can never freehand risk/verdict colors again.
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { BANNED_PHRASES, findBannedPhrases } from './calibrated-copy';
import LandingPage from '@/app/page';

const SRC_ROOT = join(process.cwd(), 'src');

function listFiles(dir: string, exts: string[]): string[] {
  const out: string[] = [];
  for (const name of readdirSync(dir)) {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) out.push(...listFiles(path, exts));
    else if (exts.some((ext) => name.endsWith(ext))) out.push(path);
  }
  return out;
}

// Naive single-quoted-string extraction is enough: the codebase style keeps
// copy in '…' literals. Not a general parser — a linter-grade scanner would
// be an unapproved dependency; this is the stdlib-first equivalent (charter
// §4 native first).
function extractSingleQuoted(source: string): string[] {
  const literals = /'((?:[^'\\\n]|\\.)*)'/g;
  const found: string[] = [];
  let match: RegExpExecArray | null;
  while ((match = literals.exec(source)) !== null) found.push(match[1]);
  return found;
}

describe('copy guide — banned phrase ledger', () => {
  it('matches PRODUCT_UX_PLAN §4 T4 enumeration exactly', () => {
    expect([...BANNED_PHRASES].sort()).toEqual(
      ['all tests pass', 'ci green', 'fully verified', 'guaranteed', 'safe to merge'].sort(),
    );
  });

  it('finds every banned phrase inside arbitrary copy (detector works)', () => {
    expect(findBannedPhrases('This run is SAFE TO MERGE, guaranteed!')).toEqual([
      'guaranteed',
      'safe to merge',
    ]);
    expect(findBannedPhrases('No reachable dependency path')).toEqual([]);
  });

  it('no shipped string literal across src/** contains a banned phrase', () => {
    // The constants file + pre-existing fixture tests legitimately enumerate
    // or quote the banned list; enforcement targets PRODUCTION literals only,
    // so every *.test.* file is excluded by construction.
    const offenders: Array<{ file: string; literal: string }> = [];
    for (const file of listFiles(SRC_ROOT, ['.ts', '.tsx'])) {
      // Constants file owns the banned LIST; tests may quote it as fixtures.
      if (file.includes('.test.') || file.endsWith('calibrated-copy.ts')) continue;
      const source = readFileSync(file, 'utf8');
      for (const literal of extractSingleQuoted(source)) {
        if (findBannedPhrases(literal).length > 0) offenders.push({ file, literal });
      }
    }
    expect(offenders).toEqual([]);
  });

  it('rendered marketing surface carries no banned phrase', () => {
    process.env.NEXT_PUBLIC_CISYNC_GITHUB_APP_INSTALL_URL =
      'https://github.com/apps/cisync-test/installations/new';
    const html = renderToStaticMarkup(<LandingPage />);
    expect(findBannedPhrases(html)).toEqual([]);
  });
});

describe('design-token layer (globals.css)', () => {
  const css = readFileSync(join(SRC_ROOT, 'app', 'globals.css'), 'utf8');
  const declaration = (name: string): string | null => {
    const match = css.match(new RegExp(`${name.replace(/[-[\]{}()*+?.\\^$|]/g, '\\$&')}:\\s*([^;]+);`));
    return match === null ? null : (match[1]?.trim() ?? null);
  };

  it('defines the layered canvas + brand accent family', () => {
    for (const token of [
      '--color-canvas',
      '--color-surface',
      '--color-surface-raised',
      '--color-accent',
      '--color-accent-soft',
      '--color-signal',
      '--radius-card',
      '--font-display',
      '--font-mono',
    ]) {
      expect(declaration(token), `missing token ${token}`).not.toBeNull();
    }
    // Mission canvas spec anchors at the #0B0E14 family.
    expect(declaration('--color-canvas')).toMatch(/^#0b0e/i);
  });

  it('defines the four risk tokens with DISTINCT hues (low=slate · medium=amber · high=orange · critical=red)', () => {
    const risks = [
      '--color-risk-low',
      '--color-risk-medium',
      '--color-risk-high',
      '--color-risk-critical',
    ].map((token) => declaration(token));
    for (const value of risks) expect(value, 'risk token missing').not.toBeNull();
    expect(new Set(risks).size).toBe(4);
    // oklch hue angle is the trailing number: slate cold (~255), amber ~85,
    // orange ~55, red ~25. Range asserts the mapping without pinning format.
    const hue = (value: string): number => {
      const match = value.match(/([\d.]+)\s*\)\s*$/);
      return match === null ? NaN : Number(match[1]);
    };
    expect(hue(risks[0] as string)).toBeGreaterThan(200);
    expect(hue(risks[1] as string)).toBeGreaterThan(60);
    expect(hue(risks[1] as string)).toBeLessThan(100);
    expect(hue(risks[2] as string)).toBeGreaterThan(40);
    expect(hue(risks[2] as string)).toBeLessThan(70);
    expect(hue(risks[3] as string)).toBeGreaterThan(10);
    expect(hue(risks[3] as string)).toBeLessThan(40);
  });

  it('enforces focus-visible rings and a reduced-motion escape hatch globally', () => {
    expect(css).toContain(':focus-visible');
    expect(css).toContain('@media (prefers-reduced-motion: reduce)');
    expect(css).toContain('.card-hover'); // hover-elevate micro-interaction exists
    expect(css).toContain('.seq-flash'); // live seq pulse exists
  });

  it('risk pills reference TOKENS, never raw palette hexes', () => {
    const pillSource = readFileSync(join(SRC_ROOT, 'components', 'risk-pill.tsx'), 'utf8');
    for (const token of ['--color-risk-low', '--color-risk-medium', '--color-risk-high', '--color-risk-critical']) {
      expect(pillSource).toContain(token);
    }
    // Outline-only discipline: a fill-flood bg-current on the pill body would violate §7.
    expect(pillSource).not.toMatch(/bg-\[var\(--color-risk[^)]*\)\]/);
  });
});

describe('contrast tokens — zinc floor on data surfaces', () => {
  // Measured against the shipped canvas family (#0b0e14 canvas, #10141d
  // surface, #171c29 raised — WCAG relative-luminance math):
  //   zinc-600 #52525b → 2.20–2.50:1   FAIL
  //   zinc-500 #71717a → 3.52–4.00:1   FAIL (bump target rejected)
  //   zinc-400 #a1a1aa → 6.64–7.54:1   PASS ≥4.5 everywhere
  // So the rule "zinc-500 only where still ≥4.5:1" resolves to zinc-400 for
  // EVERY text token in this app — there is no surface where 500 passes.
  const TEXT_ZINC_LOW = /(^|\s|')((?:hover:)?text-zinc-(?:600|700))(?=\s|'|")/g;
  // Micro-label text below the floor is tolerated ONLY where the string is
  // incidental/decorative per WCAG 1.4.3 (marketing glyph block, whisper
  // footers) — each exemption carries a WHY at its usage site.
  const ALLOWLIST: ReadonlyArray<{ file: string; why: string }> = [
    {
      file: join('landing-social-proof.tsx'),
      why: 'aria-hidden placeholder logomark strip — decorative glyph block, not data',
    },
  ];

  it('never renders micro-label or data text in zinc-600/700 outside the documented exemptions', () => {
    const offenders: Array<{ file: string; match: string }> = [];
    for (const file of listFiles(SRC_ROOT, ['.ts', '.tsx'])) {
      if (file.includes('.test.')) continue;
      const rel = file.slice(SRC_ROOT.length + 1);
      if (ALLOWLIST.some((entry) => rel.endsWith(entry.file))) continue;
      const source = readFileSync(file, 'utf8');
      for (const match of source.matchAll(TEXT_ZINC_LOW)) {
        offenders.push({ file: rel, match: match[2] ?? String(match[0]) });
      }
    }
    expect(
      offenders.map((offender) => `${offender.file}: ${offender.match}`),
    ).toEqual([]);
  });

  it('documents the measured ladder that pins the bump decision', () => {
    // Guards against a future palette change silently invalidating the
    // zinc-400 choice above: re-run the luminance script if any hex moves.
    const css = readFileSync(join(SRC_ROOT, 'app', 'globals.css'), 'utf8');
    expect(css).toMatch(/--color-canvas:\s*#0b0e14/i);
  });
});
