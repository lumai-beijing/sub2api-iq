#!/usr/bin/env bash
set -euo pipefail

CADDYFILE="${CADDYFILE:-/etc/caddy/Caddyfile}"
SNIPPET="${SNIPPET:-/home/work/gpt-monitor/deploy/Caddyfile.snippet}"
DOMAIN="${DOMAIN:-gpt-monitor.lmu.ai}"

if [[ "${EUID}" -ne 0 ]]; then
  echo "Run as root: sudo $0" >&2
  exit 1
fi
if [[ ! -f "$CADDYFILE" || ! -f "$SNIPPET" ]]; then
  echo "Missing Caddyfile or snippet" >&2
  exit 1
fi
if ! command -v caddy >/dev/null 2>&1; then
  echo "caddy CLI is required" >&2
  exit 1
fi

if grep -qE "^[[:space:]]*${DOMAIN//./\\.}[[:space:]]*\{" "$CADDYFILE"; then
  echo "Caddy site already present: $DOMAIN"
  caddy validate --config "$CADDYFILE" --adapter caddyfile
  systemctl reload caddy
  exit 0
fi

backup="${CADDYFILE}.gpt-monitor.$(date +%Y%m%d%H%M%S).bak"
cp "$CADDYFILE" "$backup"
{
  printf '\n# ---- GPT-Pro Monitor ----\n'
  cat "$SNIPPET"
} >> "$CADDYFILE"

if ! caddy validate --config "$CADDYFILE" --adapter caddyfile; then
  cp "$backup" "$CADDYFILE"
  echo "Caddy validation failed; original config restored" >&2
  exit 1
fi
if ! systemctl reload caddy; then
  cp "$backup" "$CADDYFILE"
  systemctl reload caddy || true
  echo "Caddy reload failed; original config restored" >&2
  exit 1
fi
echo "Caddy site enabled: https://$DOMAIN (backup: $backup)"
