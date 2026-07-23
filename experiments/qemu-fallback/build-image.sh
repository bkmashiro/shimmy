#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd -- "$SCRIPT_DIR/../.." && pwd)
OUTPUT_DIR=${OUTPUT_DIR:-"$REPO_ROOT/artifacts/qemu-fallback"}
BUILD_DIR=${BUILD_DIR:-"$REPO_ROOT/.cache/qemu-fallback-image"}
GUEST_BINARY=${GUEST_BINARY:-"$BUILD_DIR/shimmy-qemu-guest"}
EVALUATOR_BINARY=${EVALUATOR_BINARY:-"$BUILD_DIR/file-evaluator"}
RPC_EVALUATOR_BINARY=${RPC_EVALUATOR_BINARY:-"$BUILD_DIR/rpc-evaluator"}
BUSYBOX_BINARY=${BUSYBOX_BINARY:-/bin/busybox}
KERNEL_PATH=${KERNEL_PATH:-}
LOCK_FILE="$SCRIPT_DIR/sources.lock.json"

require_file() {
  if [[ ! -f "$1" ]]; then
    printf 'required file is missing: %s\n' "$1" >&2
    exit 1
  fi
}

for command_name in dracut go mksquashfs python3 sha256sum; do
  if ! command -v "$command_name" >/dev/null 2>&1; then
    printf 'required command is missing: %s\n' "$command_name" >&2
    exit 1
  fi
done

if [[ -z "$KERNEL_PATH" && -e /boot/vmlinuz ]]; then
  KERNEL_PATH=$(readlink -f /boot/vmlinuz)
fi
require_file "$KERNEL_PATH"
require_file "$BUSYBOX_BINARY"
require_file "$LOCK_FILE"

readarray -t lock_values < <(python3 - "$LOCK_FILE" <<'PY'
import json
import sys
with open(sys.argv[1], encoding="utf-8") as handle:
    lock = json.load(handle)
if lock.get("schema_version") != 1 or lock.get("architecture") != "x86_64":
    raise SystemExit("unsupported QEMU source lock")
for key in ("source_date_epoch", "kernel_sha256", "fixture_alias_root"):
    print(lock[key])
PY
)
SOURCE_DATE_EPOCH=${lock_values[0]}
EXPECTED_KERNEL_SHA=${lock_values[1]}
FIXTURE_ALIAS_ROOT=${lock_values[2]}
if [[ "$FIXTURE_ALIAS_ROOT" != /* || "$FIXTURE_ALIAS_ROOT" == *".."* ]]; then
  printf 'unsafe fixture alias root: %s\n' "$FIXTURE_ALIAS_ROOT" >&2
  exit 1
fi

RAW_BUILD_DIR=$BUILD_DIR
if [[ -L "$RAW_BUILD_DIR" || -L "$RAW_BUILD_DIR/rootfs" ]]; then
  printf 'unsafe symlinked QEMU build root: %s\n' "$RAW_BUILD_DIR" >&2
  exit 1
fi
BUILD_DIR=$(python3 -c 'import os,sys; print(os.path.realpath(sys.argv[1]))' "$RAW_BUILD_DIR")
OUTPUT_DIR=$(python3 -c 'import os,sys; print(os.path.realpath(sys.argv[1]))' "$OUTPUT_DIR")
if [[ "$BUILD_DIR" == / || "$BUILD_DIR" == "$REPO_ROOT" ]]; then
  printf 'unsafe QEMU build root: %s\n' "$BUILD_DIR" >&2
  exit 1
fi

actual_kernel_sha=$(sha256sum "$KERNEL_PATH" | cut -d' ' -f1)
if [[ "$actual_kernel_sha" != "$EXPECTED_KERNEL_SHA" ]]; then
  printf 'kernel digest mismatch: got %s, want %s\n' "$actual_kernel_sha" "$EXPECTED_KERNEL_SHA" >&2
  exit 1
fi
KERNEL_VERSION=$(basename "$KERNEL_PATH")
KERNEL_VERSION=${KERNEL_VERSION#vmlinuz-}
if [[ ! -d "/lib/modules/$KERNEL_VERSION" ]]; then
  printf 'matching kernel modules are missing: /lib/modules/%s\n' "$KERNEL_VERSION" >&2
  exit 1
fi

rm -rf "$BUILD_DIR/rootfs"
mkdir -p "$BUILD_DIR/rootfs" "$OUTPUT_DIR"

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -trimpath -ldflags='-s -w -buildid=' -o "$GUEST_BINARY" ./cmd/shimmy-qemu-guest
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -trimpath -ldflags='-s -w -buildid=' -o "$EVALUATOR_BINARY" ./experiments/qemu-fallback/file-evaluator
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -trimpath -ldflags='-s -w -buildid=' -o "$RPC_EVALUATOR_BINARY" ./experiments/qemu-fallback/rpc-evaluator

ROOT="$BUILD_DIR/rootfs"
mkdir -p "$ROOT/bin" "$ROOT/dev" "$ROOT/proc" "$ROOT/sys" "$ROOT/run" "$ROOT/tmp" "$ROOT/usr/bin" "$ROOT/opt/evaluator"
install -m 0755 "$BUSYBOX_BINARY" "$ROOT/bin/busybox"
ln -s busybox "$ROOT/bin/sh"
install -m 0755 "$GUEST_BINARY" "$ROOT/usr/bin/shimmy-qemu-guest"
install -m 0755 "$EVALUATOR_BINARY" "$ROOT/opt/evaluator/file-evaluator"
install -m 0755 "$RPC_EVALUATOR_BINARY" "$ROOT/opt/evaluator/rpc-evaluator"
ALIAS_DIR="$ROOT$FIXTURE_ALIAS_ROOT"
mkdir -p "$ALIAS_DIR"
ln -s /opt/evaluator/file-evaluator "$ALIAS_DIR/file-evaluator"
ln -s /opt/evaluator/rpc-evaluator "$ALIAS_DIR/rpc-evaluator"
cat >"$ROOT/init" <<'INIT'
#!/bin/busybox sh
set -eu
/bin/busybox mount -t devtmpfs devtmpfs /dev 2>/dev/null || true
/bin/busybox mount -t proc proc /proc 2>/dev/null || true
/bin/busybox mount -t sysfs sysfs /sys 2>/dev/null || true
/bin/busybox mount -t tmpfs -o mode=0755,nosuid,nodev tmpfs /run 2>/dev/null || true
/bin/busybox mount -t tmpfs -o mode=1777,nosuid,nodev tmpfs /tmp 2>/dev/null || true
/bin/busybox ip link set lo up 2>/dev/null || true
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

python3 - "$ROOT" "$SOURCE_DATE_EPOCH" <<'PY'
import os
import sys
root = sys.argv[1]
epoch = int(sys.argv[2])
for current, directories, files in os.walk(root, topdown=False):
    for name in files + directories:
        os.utime(os.path.join(current, name), (epoch, epoch), follow_symlinks=False)
    os.utime(current, (epoch, epoch), follow_symlinks=False)
PY

printf 'QEMU rootfs input digests:\n'
sha256sum "$GUEST_BINARY" "$EVALUATOR_BINARY" "$RPC_EVALUATOR_BINARY" "$BUSYBOX_BINARY" "$ROOT/init"

FINAL_ROOTFS="$OUTPUT_DIR/evaluator.squashfs"
SORT_FILE="$BUILD_DIR/squashfs.sort"
rm -f "$FINAL_ROOTFS"
python3 - "$ROOT" "$SORT_FILE" <<'PY'
import os
import sys

root, destination = sys.argv[1:]
paths = []
for current, directories, files in os.walk(root):
    directories.sort()
    files.sort()
    for name in directories + files:
        paths.append(os.path.join(current, name))
with open(destination, "w", encoding="utf-8") as handle:
    for priority, path in enumerate(reversed(paths), start=1):
        handle.write(f"{path} {priority}\n")
PY
mksquashfs "$ROOT" "$FINAL_ROOTFS" \
  -noappend \
  -all-root \
  -all-time "$SOURCE_DATE_EPOCH" \
  -mkfs-time "$SOURCE_DATE_EPOCH" \
  -no-xattrs \
  -no-duplicates \
  -no-exports \
  -no-fragments \
  -no-progress \
  -processors 1 \
  -sort "$SORT_FILE" \
  -comp gzip
cp "$KERNEL_PATH" "$OUTPUT_DIR/vmlinuz"
SOURCE_DATE_EPOCH="$SOURCE_DATE_EPOCH" dracut \
  --force \
  --no-hostonly \
  --reproducible \
  "$OUTPUT_DIR/initramfs.img" \
  "$KERNEL_VERSION"
touch -d "@$SOURCE_DATE_EPOCH" "$OUTPUT_DIR/vmlinuz" "$OUTPUT_DIR/initramfs.img" "$FINAL_ROOTFS"

kernel_sha=$(sha256sum "$OUTPUT_DIR/vmlinuz" | cut -d' ' -f1)
initrd_sha=$(sha256sum "$OUTPUT_DIR/initramfs.img" | cut -d' ' -f1)
rootfs_sha=$(sha256sum "$FINAL_ROOTFS" | cut -d' ' -f1)
source_lock_sha=$(sha256sum "$LOCK_FILE" | cut -d' ' -f1)
cat >"$OUTPUT_DIR/manifest.json" <<MANIFEST
{
  "schema_version": 1,
  "architecture": "x86_64",
  "source_lock_sha256": "$source_lock_sha",
  "kernel": {"path": "vmlinuz", "sha256": "$kernel_sha"},
  "initrd": {"path": "initramfs.img", "sha256": "$initrd_sha"},
  "rootfs": {"path": "evaluator.squashfs", "sha256": "$rootfs_sha", "format": "raw"}
}
MANIFEST

printf 'QEMU fixture image written to %s\n' "$OUTPUT_DIR"
sha256sum "$OUTPUT_DIR/vmlinuz" "$OUTPUT_DIR/initramfs.img" "$FINAL_ROOTFS"
