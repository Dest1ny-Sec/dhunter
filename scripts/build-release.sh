#!/bin/bash
# build-release.sh — build cross-platform release tarballs for Dhunter
# Usage: ./scripts/build-release.sh [version]
#   version defaults to v0.3.5
set -euo pipefail

VERSION="${1:-v0.3.5}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DIST="$ROOT/dist-release"
mkdir -p "$DIST"

# Ensure frontend is built
echo "→ build frontend"
(cd "$ROOT/frontend" && npm run build 2>&1 | tail -3)

build_target() {
  local goos="$1" goarch="$2" ext="$3" outname="$4"
  echo "→ compile $outname ($goos/$goarch)"
  GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 \
    go build -trimpath -ldflags="-s -w" -o "$DIST/$outname" ./cmd/dhunter-server
  GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 \
    go build -trimpath -ldflags="-s -w" -o "$DIST/$outname-mcp" ./cmd/dhunter-mcp
  chmod +x "$DIST/$outname" "$DIST/$outname-mcp"
}

# macOS arm64 (Apple Silicon) — current dev platform
build_target darwin arm64 "" "dhunter-server-darwin-arm64"
# Linux x86_64 — server / Kali
build_target linux amd64 "" "dhunter-server-linux-amd64"

# Bundle each platform into a tarball
bundle() {
  local platform="$1" suffix="$2" server_bin="$3"
  local stage="$DIST/stage-$suffix"
  echo "→ bundle $suffix"
  rm -rf "$stage"
  mkdir -p "$stage/agents" "$stage/frontend" "$stage/configs" "$stage/scripts" "$stage/tools"
  cp -R "$ROOT/agents/." "$stage/agents/"
  cp -R "$ROOT/frontend/dist/." "$stage/frontend/"
  cp "$ROOT/configs/dhunter.yaml" "$stage/configs/"
  cp -R "$ROOT/scripts/." "$stage/scripts/"
  cp "$ROOT/DHUNTER_NOTES.md" "$stage/" 2>/dev/null || true
  cp "$ROOT/README.md" "$stage/"
  cp "$ROOT/LICENSE" "$stage/" 2>/dev/null || true
  cp "$DIST/$server_bin" "$stage/dhunter-server"
  chmod +x "$stage/dhunter-server"
  # MCP binary kept under tools/ with rename to keep the stage layout portable
  cp "$DIST/${server_bin}-mcp" "$stage/tools/dhunter-mcp"
  chmod +x "$stage/tools/dhunter-mcp"
  # strip out the .venv and __pycache__ that the user doesn't need
  rm -rf "$stage/agents/.venv" "$stage/agents/__pycache__"
  find "$stage/agents" -name "__pycache__" -type d -exec rm -rf {} + 2>/dev/null || true
  # tarball
  local tarball="$DIST/dhunter-$VERSION-$suffix.tar.gz"
  rm -f "$tarball"
  (cd "$DIST" && tar -czf "$tarball" -C "$stage" .)
  echo "  → $tarball ($(du -h "$tarball" | cut -f1))"
  rm -rf "$stage"
}

bundle darwin-arm64 darwin-arm64 dhunter-server-darwin-arm64
bundle linux-amd64  linux-amd64  dhunter-server-linux-amd64

# Cleanup intermediate binaries (keep the tarballs only)
rm -f "$DIST/dhunter-server-darwin-arm64" "$DIST/dhunter-server-darwin-arm64-mcp"
rm -f "$DIST/dhunter-server-linux-amd64"  "$DIST/dhunter-server-linux-amd64-mcp"

ls -lh "$DIST"
