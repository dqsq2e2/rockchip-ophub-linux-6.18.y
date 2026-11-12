#!/usr/bin/env bash
set -euo pipefail
repo=$(cd "$(dirname "$0")/.." && pwd)
output=${1:?Usage: build-phx-r3588-app.sh OUTPUT}
output=$(realpath -m "$output")
build_time=$(date -u -d "@${SOURCE_DATE_EPOCH:-$(date +%s)}" +%Y.%m.%d)
(
    cd "$repo/rkdev"
    CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath \
        -ldflags="-s -w -X main.buildTime=$build_time" -o "$output" .
)
printf 'Recovery application version: %s\n' "$build_time"
