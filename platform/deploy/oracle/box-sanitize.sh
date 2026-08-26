#!/usr/bin/env bash
# Kills every deploy-gremlin class we hit in W6 (run BEFORE compose up):
# 1. macOS AppleDouble junk breaks SQL migration name parsing
# 2. root-owned/0600 key files are unreadable by non-root containers
# 3. job-lease public key missing (never generated next to private key)
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
find "$HERE/../.." -name "._*" -delete 2>/dev/null || true
find "$HERE/../.." -name ".DS_Store" -delete 2>/dev/null || true
chmod 644 "$HERE"/dev-keys/* 2>/dev/null || true
for priv in "$HERE"/dev-keys/*.key; do
  [ -f "$priv" ] || continue
  pub="${priv%.key}.pub"
  [ -f "$pub" ] || openssl pkey -in "$priv" -pubout -out "$pub" && chmod 644 "$pub"
done
echo "box sanitized"
