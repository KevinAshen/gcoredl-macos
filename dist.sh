#!/bin/bash

set -euo pipefail

cd "$(dirname "$0")"

mkdir -p dist

VERSION="${VERSION:-dev}"

_build() {
    local os="$1"
    local arch="$2"
    local suffix=""
    if [[ "$os" == "windows" ]]; then
        suffix=".exe"
    fi
    echo "building ${os}/${arch}"
    GOOS="$os" GOARCH="$arch" CGO_ENABLED=1 go build \
        -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
        -o "dist/gcoredl-${os}-${arch}${suffix}" .
}

_build darwin amd64
_build darwin arm64
