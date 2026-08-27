import type { Metadata } from 'next';
import './globals.css';

export const metadata: Metadata = {
  title: 'CISYNC · change control',
  description:
    'Every agent-authored change, verified before merge — evidence dossiers, priority scheduling, bounded repair.',
};

// Root layout is intentionally bare: the marketing landing owns its full-bleed
// presentation while the console chrome lives in the (app) route group layout.
export default function RootLayout({
  children,
}: Readonly<{ children: React.ReactNode }>): React.ReactElement {
  return (
    <html lang="en">
      <body className="min-h-screen antialiased">{children}</body>
    </html>
  );
}
