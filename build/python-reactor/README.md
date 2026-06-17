# Historical python-reactor build notes

This directory is no longer the source of truth for building `python-reactor.wasm`.

Current source of truth:

- Repository: `bkmashiro/webassembly-language-runtimes`
- Workflow: `.github/workflows/build-python-reactor.yml`
- Current release consumed by this branch: `v1.0.13`
- Artifact SHA256: `4c5fea0b3a6a31a54ea83f8f93a7c912627b4cf5fc6516ee8e50159bb7c04d4c`
- Required/current exports: `py_init`, `evaluate`, `py_exec`, `alloc`, `dealloc`, `resp_buf`, `resp_len`

The files in this directory are retained only as historical/reference material from the earlier in-repo CPython 3.12 reactor experiment:

- `py_reactor.c` — older local reactor wrapper source.
- `ci-build.yml` — older workflow sketch against vmware-labs CPython 3.12/WASI SDK 20 assets.
- `artifacts/python-reactor.wasm` — Git LFS copy of the current release artifact, kept for local scripts/tests that expect this path.

Do not update `ci-build.yml` or `py_reactor.c` when changing the real reactor artifact. Make the build change in `webassembly-language-runtimes`, publish a tagged release, then update the LFS artifact copies and docs in this repository.
