# Historical python-reactor build notes

This directory is no longer the source of truth for building `python-reactor.wasm`.

Replacement source of truth:

- Repository: `bkmashiro/webassembly-language-runtimes`
- Workflow: `.github/workflows/build-python-reactor.yml`
- Frozen historical pin: `v1.0.14` (not approved for new deployments)
- Artifact SHA256: `78dcbb6d673351c0d3b776c42d2fb93b6f638cdc714d58072dece4b115edaa72`
- Historical exports: `py_init`, `py_prepare`, `evaluate`, `py_exec`, `alloc`, `dealloc`, `resp_buf`, `resp_len`

The files in this directory are retained only as historical/reference material from the earlier in-repo CPython 3.12 reactor experiment:

- `py_reactor.c` — older local reactor wrapper source.
- `ci-build.yml` — older workflow sketch against vmware-labs CPython 3.12/WASI SDK 20 assets.
- `artifacts/python-reactor.wasm` — historical Git LFS compatibility fixture; it is not a deployment source.

Do not update `ci-build.yml` or `py_reactor.c` when changing the real reactor artifact. Build a clean replacement in `webassembly-language-runtimes`, publish an immutable tagged release, and provide its SHA-256, exports, package/API manifest, no-polyfill evidence, and raw smoke results. Only after those gates pass should this repository update its pin, restore automatic reactor CI, or replace a 200+ MiB compatibility fixture required by the new ABI.
