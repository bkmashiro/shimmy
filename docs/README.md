# shimmy-wasm local docs

The canonical, current system documentation lives in `~/projects/shimmy-docs`:

- Current system + roadmap: `shimmy-docs/docs/wasm/current-system-roadmap-zh.md`
- Guest ABI: `shimmy-docs/docs/wasm/guest-abi.md`
- Snapshot/restore: `shimmy-docs/docs/wasm/snapshot-restore.md`
- Python backends: `shimmy-docs/docs/wasm/python-backends.md`
- Lambda Feedback capability matrix: `shimmy-docs/docs/wasm/lambda-feedback-capability-matrix.md`

This repository keeps only local runnable notes that belong next to scripts/examples:

| File | Purpose |
|---|---|
| [demo.md](demo.md) | One-command local WASM demo. |
| [demo-scenarios.md](demo-scenarios.md) | Multi-language/demo scenario matrix. |
| [python-examples.md](python-examples.md) | Plain Python / NumPy / SciPy route demos. |

Do not add new long-form architecture docs here unless they are also promoted to `shimmy-docs`.
