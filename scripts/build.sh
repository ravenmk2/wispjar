#!/usr/bin/env bash
# scripts/build.sh — local multi-platform build
set -euo pipefail

APP=wispjar
DIST=dist
VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS="-s -w -X main.version=${VERSION}"

platforms=(
  linux/amd64 linux/arm64
  darwin/amd64 darwin/arm64
  windows/amd64 windows/arm64
)

# embed scenario: ensure web/dist exists and is non-empty (the placeholder
# file is not version-controlled); skip if the frontend has been built so
# real artifacts are not overwritten
if [[ -d web && ! -e web/dist/index.html ]]; then
  mkdir -p web/dist
  printf '<!-- placeholder: run frontend build to replace -->\n' > web/dist/index.html
fi

mkdir -p "$DIST"
for p in "${platforms[@]}"; do
  os=${p%/*}
  arch=${p#*/}
  out="${DIST}/${APP}_${os}_${arch}"
  [[ $os == windows ]] && out="${out}.exe"
  echo "building ${os}/${arch} -> ${out}"
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -ldflags "$LDFLAGS" -o "$out" "./cmd/${APP}"
done
