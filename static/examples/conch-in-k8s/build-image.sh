#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"
test -x bin/conch && test -x bin/conchd && test -x bin/conch-init
test -f bin/conch-init.cpio.gz && test -x bin/stratovirt && test -f bin/vmlinux.bin
test -d erofs-utils-src

IMAGE=${IMAGE:-hub.conch.com:5000/conch/conch-engine:v0.1-x86_64}

docker build -f Dockerfile -t "$IMAGE" .
docker push "$IMAGE"
