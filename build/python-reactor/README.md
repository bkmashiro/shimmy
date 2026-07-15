# Historical python-reactor build notes

This directory is no longer the source of truth for building `python-reactor.wasm`.

Current source of truth:

- Repository: `bkmashiro/webassembly-language-runtimes`
- Workflow: `.github/workflows/build-python-reactor.yml`
- Current deployment pin: `v1.0.14`
- Artifact SHA256: `78dcbb6d673351c0d3b776c42d2fb93b6f638cdc714d58072dece4b115edaa72`
- Required/current exports: `py_init`, `py_prepare`, `evaluate`, `py_exec`, `alloc`, `dealloc`, `resp_buf`, `resp_len`

The files in this directory are retained only as historical/reference material from the earlier in-repo CPython 3.12 reactor experiment:

- `py_reactor.c` — older local reactor wrapper source.
- `ci-build.yml` — older workflow sketch against vmware-labs CPython 3.12/WASI SDK 20 assets.
- `artifacts/python-reactor.wasm` — historical Git LFS compatibility fixture; it is not the current deployment pin.

Do not update `ci-build.yml` or `py_reactor.c` when changing the real reactor artifact. Make the build change in `webassembly-language-runtimes`, publish a tagged release, then update the release pin and docs in this repository. CI and handoff smokes download the verified release; do not replace a 200+ MiB LFS fixture unless a compatibility test requires the new ABI.
