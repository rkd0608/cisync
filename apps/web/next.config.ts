import type { NextConfig } from 'next';

// WHY no rewrites here: /api/sauron/* is served by the route handler in
// src/app/api/sauron/[...path]/route.ts, which proxies to control-plane AND
// injects the admin bearer server-side (tokens must never reach the bundle).
// Config rewrites would shadow the handler and forward unauthenticated.
const nextConfig: NextConfig = {
  reactStrictMode: true,
};

export default nextConfig;
