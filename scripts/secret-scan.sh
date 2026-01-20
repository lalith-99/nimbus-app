#!/usr/bin/env bash
set -euo pipefail

# Scan for hardcoded secrets using gitleaks (runs via Docker).

if ! command -v docker &>/dev/null; then
  echo "error: docker required" >&2
  exit 1
fi

docker run --rm -v "$(pwd)":/repo ghcr.io/gitleaks/gitleaks:latest \
  detect --source /repo --no-git --redact
