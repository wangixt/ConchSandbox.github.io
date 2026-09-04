#!/usr/bin/env bash
set -euo pipefail

CONCH_BIN=${CONCH_BIN:-/usr/local/bin/conch}
CONCH_CONFIG=${CONCH_CONFIG:-/etc/conch/config.yaml}
export CONCH_API_TIMEOUT=${CONCH_API_TIMEOUT:-30m}
TEMPLATE_NAME=${TEMPLATE_NAME:-hub.conch.com:5000/conch/openeuler-bzimage:24.03-lts-sp4}
SOURCE_IMAGE=${SOURCE_IMAGE:-hub.oepkgs.net/openeuler/openeuler:24.03-lts-sp4}
KERNEL=${KERNEL:-/opt/conch/vmlinux.bin}
INITRD=${INITRD:-/opt/conch/conch-init.cpio.gz}

args=(template create --config "$CONCH_CONFIG" --name "$TEMPLATE_NAME" \
  --source "$SOURCE_IMAGE" --kernel "$KERNEL" --initrd "$INITRD")
if [[ ${PLAIN_HTTP:-0} == 1 ]]; then args+=(--plain-http); fi
"$CONCH_BIN" "${args[@]}"

push_args=(template push --plain-http --config "$CONCH_CONFIG" "$TEMPLATE_NAME" "$TEMPLATE_NAME")
"$CONCH_BIN" "${push_args[@]}"
