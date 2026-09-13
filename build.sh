#!/usr/bin/env bash
# Build cpa-plugin-commandcode for CLIProxyAPI.
# glibc toolchain (Debian runtime): never alpine/musl. Needs go >= 1.26.
set -euo pipefail

SRC_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PLUGIN_NAME="commandcode"
PLUGIN_VERSION="${PLUGIN_VERSION:-0.3.2}"
OUT_DIR="${PLUGIN_OUT_DIR:-${SRC_DIR}/../../plugins/linux/amd64}"
GO_IMAGE="${PLUGIN_GO_IMAGE:-golang:1.26}"

echo "[build] source=${SRC_DIR}"
echo "[build] output=${OUT_DIR}"
mkdir -p "${OUT_DIR}"

docker run --rm \
  -v "${SRC_DIR}:/src" \
  -v "${OUT_DIR}:/out" \
  -v "${GOMODCACHE:-/tmp/gomodcache}:/go/pkg/mod" \
  -w /src \
  -e "CGO_ENABLED=1" \
  -e "GOFLAGS=-mod=mod" \
  -e "PLUGIN_NAME=${PLUGIN_NAME}" \
  -e "PLUGIN_VERSION=${PLUGIN_VERSION}" \
  "${GO_IMAGE}" \
  sh -ec '
    go mod tidy
    chmod -R u+w /src 2>/dev/null || true
    go vet ./...
    go test ./...
    go build -buildvcs=false -buildmode=c-shared \
      -ldflags "-s -w -X main.pluginVersion=${PLUGIN_VERSION} -X github.com/ahoo/cpa-plugin-commandcode.pluginVersion=${PLUGIN_VERSION}" \
      -o "/out/${PLUGIN_NAME}-v${PLUGIN_VERSION}.so" \
      ./cmd/commandcode
    rm -f "/out/${PLUGIN_NAME}-v${PLUGIN_VERSION}.h"
  '

printf '[build] ok: %s/%s-v%s.so\n' "${OUT_DIR}" "${PLUGIN_NAME}" "${PLUGIN_VERSION}"
