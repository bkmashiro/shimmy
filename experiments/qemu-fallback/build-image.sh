#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd -- "$SCRIPT_DIR/../.." && pwd)
OUTPUT_DIR=${OUTPUT_DIR:-"$REPO_ROOT/artifacts/qemu-fallback"}
BUILD_DIR=${BUILD_DIR:-"$REPO_ROOT/.cache/qemu-fallback-image"}
GUEST_BINARY=${GUEST_BINARY:-"$BUILD_DIR/shimmy-qemu-guest"}
EVALUATOR_BINARY=${EVALUATOR_BINARY:-"$BUILD_DIR/file-evaluator"}
BUSYBOX_BINARY=${BUSYBOX_BINARY:-/bin/busybox}
KERNEL_PATH=${KERNEL_PATH:-}
INITRD_PATH=${INITRD_PATH:-}
ROOTFS_SIZE_MB=${ROOTFS_SIZE_MB:-256}

require_file() {
  if [[ ! -f "$1" ]]; then
    printf 'required file is missing: %s\n' "$1" >&2
    exit 1
  fi
}

for command_name in go mkfs.ext4 qemu-img sha256sum; do
  if ! command -v "$command_name" >/dev/null 2>&1; then
    printf 'required command is missing: %s\n' "$command_name" >&2
    exit 1
  fi
done

if [[ -z "$KERNEL_PATH" && -e /vmlinuz ]]; then
  KERNEL_PATH=$(readlink -f /vmlinuz)
fi
if [[ -z "$INITRD_PATH" && -e /initrd.img ]]; then
  INITRD_PATH=$(readlink -f /initrd.img)
fi
require_file "$KERNEL_PATH"
require_file "$INITRD_PATH"
require_file "$BUSYBOX_BINARY"

rm -rf "$BUILD_DIR/rootfs"
mkdir -p "$BUILD_DIR/rootfs" "$OUTPUT_DIR"

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o "$GUEST_BINARY" ./cmd/shimmy-qemu-guest
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o "$EVALUATOR_BINARY" ./experiments/qemu-fallback/file-evaluator

ROOT="$BUILD_DIR/rootfs"
mkdir -p "$ROOT/bin" "$ROOT/dev" "$ROOT/proc" "$ROOT/sys" "$ROOT/run" "$ROOT/tmp" "$ROOT/usr/bin" "$ROOT/opt/evaluator"
install -m 0755 "$BUSYBOX_BINARY" "$ROOT/bin/busybox"
ln -s busybox "$ROOT/bin/sh"
install -m 0755 "$GUEST_BINARY" "$ROOT/usr/bin/shimmy-qemu-guest"
install -m 0755 "$EVALUATOR_BINARY" "$ROOT/opt/evaluator/file-evaluator"

cat >"$ROOT/init" <<'INIT'
#!/bin/busybox sh
set -eu
/bin/busybox mount -t devtmpfs devtmpfs /dev 2>/dev/null || true
/bin/busybox mount -t proc proc /proc 2>/dev/null || true
/bin/busybox mount -t sysfs sysfs /sys 2>/dev/null || true
/bin/busybox mount -t tmpfs -o mode=0755,nosuid,nodev tmpfs /run
/bin/busybox mount -t tmpfs -o mode=1777,nosuid,nodev tmpfs /tmp
/bin/busybox modprobe virtio_console 2>/dev/null || true

control_device=""
attempt=0
while [ "$attempt" -lt 400 ]; do
  if [ -c /dev/virtio-ports/org.shimmy.control ]; then
    control_device=/dev/virtio-ports/org.shimmy.control
    break
  fi
  for candidate in /dev/vport*p*; do
    if [ -c "$candidate" ]; then
      control_device=$candidate
      break 2
    fi
  done
  attempt=$((attempt + 1))
  /bin/busybox sleep 0.025
done

if [ -z "$control_device" ]; then
  printf 'shimmy qemu guest: virtio control device did not appear\n' >/dev/console
  /bin/busybox poweroff -f
fi

exec /usr/bin/shimmy-qemu-guest \
  --device "$control_device" \
  --work-root /run/shimmy \
  --max-frame-bytes 4194304
INIT
chmod 0755 "$ROOT/init"

RAW_ROOTFS="$BUILD_DIR/rootfs.raw"
QCOW_ROOTFS="$OUTPUT_DIR/evaluator.qcow2"
rm -f "$RAW_ROOTFS" "$QCOW_ROOTFS"
truncate -s "${ROOTFS_SIZE_MB}M" "$RAW_ROOTFS"
mkfs.ext4 -q -F -d "$ROOT" "$RAW_ROOTFS"
qemu-img convert -f raw -O qcow2 "$RAW_ROOTFS" "$QCOW_ROOTFS"
cp "$KERNEL_PATH" "$OUTPUT_DIR/vmlinuz"
cp "$INITRD_PATH" "$OUTPUT_DIR/initramfs.img"

kernel_sha=$(sha256sum "$OUTPUT_DIR/vmlinuz" | cut -d' ' -f1)
initrd_sha=$(sha256sum "$OUTPUT_DIR/initramfs.img" | cut -d' ' -f1)
rootfs_sha=$(sha256sum "$QCOW_ROOTFS" | cut -d' ' -f1)
cat >"$OUTPUT_DIR/manifest.json" <<MANIFEST
{
  "schema_version": 1,
  "architecture": "x86_64",
  "kernel": {"path": "vmlinuz", "sha256": "$kernel_sha"},
  "initrd": {"path": "initramfs.img", "sha256": "$initrd_sha"},
  "rootfs": {"path": "evaluator.qcow2", "sha256": "$rootfs_sha", "format": "qcow2"}
}
MANIFEST

printf 'QEMU fixture image written to %s\n' "$OUTPUT_DIR"
sha256sum "$OUTPUT_DIR/manifest.json" "$OUTPUT_DIR/vmlinuz" "$OUTPUT_DIR/initramfs.img" "$QCOW_ROOTFS"
