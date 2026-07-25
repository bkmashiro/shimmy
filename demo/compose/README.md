# Compact Shimmy demo stack

This is an experimental handoff stack for four bounded execution lanes. It is deliberately separate from the larger Lambda Feedback production images and does not contain Mathematica/Wolfram.

> The `python-reactor` lane retains a frozen historical artifact only for
> reproducibility. Do not use or publish that lane as a current deployment until
> a clean replacement passes the documented handoff. The generic and Pyodide
> lanes remain active demos.

| Service | Demonstrates | Image architecture |
|---|---|---|
| `generic` | Generic WASI evaluator, real HTTP path, and two-request snapshot reset | `linux/amd64`, `linux/arm64` |
| `python-reactor` | Plain Python evaluator on the pinned CPython-WASI reactor | `linux/amd64`, `linux/arm64` |
| `pyodide-scipy` | SciPy evaluator through Node/Pyodide | `linux/amd64`, `linux/arm64` |
| `dbi-lean` | Native Lean file worker wrapped by DynamoRIO and the bounded policy client | `linux/amd64` only |

The images are split by lane instead of forming one kitchen-sink image. Environment variables select already-packaged runtimes; they do not download dependencies at container startup.

## Local ARM64 use

Docker Desktop on Apple Silicon can build and run the first three services natively:

```bash
scripts/smoke-compose-demo.sh
```

The smoke builds the images, starts the services, sends real HTTP `eval` requests, checks generic WASM reset on two requests, checks the reactor result, proves the prepackaged SciPy stack loads with `--network none`, and runs a SciPy t-test through Pyodide.

DynamoRIO runtime behavior under amd64 emulation is not an acceptance gate. The script skips `dbi-lean` on ARM64. Build and run that lane on native `linux/amd64` through the manual GitHub Actions workflow.

To retain containers for inspection:

```bash
SHIMMY_DEMO_KEEP=1 scripts/smoke-compose-demo.sh
```

## Compose commands

```bash
# Three architecture-native lanes
docker compose -f demo/compose/compose.yaml up --build generic python-reactor pyodide-scipy

# Native x86_64 only
docker compose -f demo/compose/compose.yaml --profile dbi up --build dbi-lean
```

Default host ports are `18081` through `18084`, bound to `127.0.0.1` only; override them with `SHIMMY_DEMO_GENERIC_PORT`, `SHIMMY_DEMO_REACTOR_PORT`, `SHIMMY_DEMO_PYODIDE_PORT`, and `SHIMMY_DEMO_DBI_PORT`.

## Pinned inputs

- Python reactor (frozen historical demo input): `bkmashiro/webassembly-language-runtimes@v1.0.14`, SHA-256 `78dcbb6d673351c0d3b776c42d2fb93b6f638cdc714d58072dece4b115edaa72`.
- Pyodide: npm lockfile under `examples/eval-pyodide/` (`0.28.x`).
- Lean evaluator: `lambda-feedback/compareLeanTest@cb99cfc95748772d4ea67d63f3875c2d574660fc` with the checked-in Shimmy file-protocol adapter.
- DynamoRIO: `11.91.20545`, tar SHA-256 `db8b9f3d0dc14a469da1dc791e120a6cc6d968d2b18facb7bcaa471f4bde22da`, a minimal runtime tree, and a bounded evidence client. This lane is not a complete production sandbox.

## GitHub Actions and Docker Hub

Run **Build compact demo images** manually. Its first job always builds all four images on native GitHub-hosted x86_64 and runs `scripts/smoke-compose-demo.sh --no-build`.

Publishing is opt-in. Configure these repository secrets when credentials are available:

- `DOCKERHUB_USERNAME`
- `DOCKERHUB_TOKEN`

Then dispatch with `push_images=true` and a tag. The workflow publishes multi-architecture images for generic/reactor/Pyodide and an amd64-only DBI/Lean image:

```text
<username>/shimmy-demo-generic:<tag>
<username>/shimmy-demo-python-reactor:<tag>
<username>/shimmy-demo-pyodide-scipy:<tag>
<username>/shimmy-demo-dbi-lean:<tag>
```

Do not place credentials in Compose files, workflow inputs, build arguments, logs, or committed `.env` files.
