# QEMU ultimate fallback

Shimmy can explicitly wrap an existing `file` or `rpc` evaluator in a small
QEMU VM. This path is for evaluators that require Linux process semantics and
cannot run through the normal native or WebAssembly paths.

QEMU is **not** an automatic retry mechanism. Shimmy enters this path only when
`FUNCTION_QEMU_ENABLED=true`; a failed native request is never replayed in a VM.

## Architecture

```text
Shimmy dispatcher
  -> shimmy-qemu-runner (one VM-backed worker)
    -> qemu-system-x86_64
      -> virtio-serial control channel
        -> shimmy-qemu-guest
          -> evaluator command
```

The runner preserves Shimmy's existing file/RPC interface. The host and guest
use a versioned, length-prefixed protocol with bounded frames. The VM root disk
is attached read-only. Networking defaults to `none` and requires the explicit
`user`/`inherit` profile to enable QEMU user networking.

## Required configuration

```bash
FUNCTION_INTERFACE=file                    # or rpc
FUNCTION_COMMAND=/opt/evaluator/worker
FUNCTION_QEMU_ENABLED=true
FUNCTION_QEMU_RUNNER=/opt/shimmy/bin/shimmy-qemu-runner
FUNCTION_QEMU_BINARY=/usr/bin/qemu-system-x86_64
FUNCTION_QEMU_ROOTFS=/opt/shimmy-qemu/evaluator.squashfs
FUNCTION_QEMU_IMAGE_MANIFEST=/opt/shimmy-qemu/manifest.json
FUNCTION_QEMU_ACCELERATOR=tcg              # or kvm; no automatic fallback
FUNCTION_QEMU_RESET_POLICY=lazy            # or off
```

The runner path, QEMU binary, root filesystem and manifest must be readable.
`FUNCTION_COMMAND`, its working directory and its arguments must refer to paths
that exist inside the guest image.

### Reset policy

- `lazy`: terminate the worker VM after every invocation. The next request boots
  a clean VM from the read-only image.
- `off`: retain the historical supervisor lifecycle; an RPC worker may keep its
  VM alive between requests.

AWS Lambda defaults to `lazy`. Other environments default to `off`. Set the
policy explicitly in production rather than relying on those defaults.

### Accelerator

`FUNCTION_QEMU_ACCELERATOR` is mandatory:

- `tcg`: portable software emulation;
- `kvm`: hardware acceleration, rejected if `/dev/kvm` cannot be opened.

Shimmy never silently changes KVM to TCG.

### Optional limits

| Variable | Default | Accepted range |
|---|---:|---:|
| `FUNCTION_QEMU_MEMORY_MB` | 512 | 1–32768 |
| `FUNCTION_QEMU_VCPUS` | 1 | 1–64 |
| `FUNCTION_QEMU_MAX_FRAME_BYTES` | 4 MiB | 1 KiB–64 MiB |
| `FUNCTION_QEMU_BOOT_TIMEOUT` | 60s | 10ms–10m |
| `FUNCTION_QEMU_SHUTDOWN_TIMEOUT` | 10s | 10ms–2m |
| `FUNCTION_QEMU_WORK_ROOT` | `/tmp/shimmy-qemu` | writable directory |
| `FUNCTION_QEMU_NETWORK_PROFILE` | `none` | `none`, `user`, `inherit` |

`inherit` and `user` both select QEMU user-mode networking; neither provides
host networking or a security guarantee beyond QEMU's own process boundary.

## Image manifest

The manifest is strict JSON. Unknown fields and trailing JSON values are
rejected. Every artifact is SHA-256 verified before VM startup.

```json
{
  "schema_version": 1,
  "architecture": "x86_64",
  "source_lock_sha256": "<optional 64-hex source-lock digest>",
  "kernel": {"path": "vmlinuz", "sha256": "<64 hex>"},
  "initrd": {"path": "initrd.img", "sha256": "<64 hex>"},
  "rootfs": {
    "path": "evaluator.squashfs",
    "sha256": "<64 hex>",
    "format": "raw"
  }
}
```

Artifact paths may be relative to the manifest. The configured
`FUNCTION_QEMU_ROOTFS` must resolve to the same file as the manifest rootfs.
The image must provide:

- an x86_64 kernel and initrd with virtio block, serial and console support;
- `/dev/virtio-ports/org.shimmy.control`;
- `shimmy-qemu-guest` started during boot;
- the evaluator command at the same path configured on the host.

## Security and compatibility boundary

- QEMU is an explicit compatibility/isolation fallback, not proof that an
  evaluator is safe.
- The root disk is read-only, but guest memory and temporary files remain
  mutable for the VM lifetime.
- Effective evaluator environment variables are forwarded into the guest;
  QEMU control variables are filtered. Do not place secrets in evaluator env
  unless the guest is intended to receive them.
- File and RPC payloads remain bounded by the configured frame size.
- Host paths are not mounted into the guest by this implementation.
- No request is replayed automatically after a crash or timeout.

## Validation

Unit and in-process bridge tests cover manifest validation, command
construction, protocol framing, stream lifecycle and file execution. A real
Linux/QEMU smoke can be run with externally built, checksummed image artifacts:

```bash
SHIMMY_QEMU_BINARY=/usr/bin/qemu-system-x86_64 \
SHIMMY_QEMU_ROOTFS=/path/evaluator.squashfs \
SHIMMY_QEMU_MANIFEST=/path/manifest.json \
scripts/e2e-qemu-fallback.sh
```
