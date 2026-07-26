# Clean Python runtime handoff gates

This document defines the consumer-side gates for replacing the frozen legacy
`python-reactor` artifact. It does **not** import, pin, or approve a replacement
artifact.

## Read-only producer audit

Source inspected on 2026-07-26:

```text
repository: bkmashiro/agent-python-runtime
branch:     main
HEAD seen:  b1e514f
```

The producer repository was being modified by another agent during the audit.
No files were written there. This repository carries only independent consumer
acceptance tooling and test vectors.

### Exact bundle reproducibility

The producer's controlled workflow builds the same commit on two independent
`ubuntu-24.04` jobs and compares the complete bundle. It does not ignore custom
sections, timestamps, notices, SBOM bytes, unknown files, or Wasm data-section
differences.

Evidence is intentionally mixed:

- run [`29962630062`](https://github.com/bkmashiro/agent-python-runtime/actions/runs/29962630062)
  at signed commit `770b37d17002f3107ebf920630d3fed0273281e4`
  reported `exact_match: true` for five bundle files and ten Wasm sections;
- run [`29982102694`](https://github.com/bkmashiro/agent-python-runtime/actions/runs/29982102694)
  at signed commit `5a8c5a8bdba01b88b2b43b58a5bb5de4a4baf274`
  again reported `exact_match: false`; equal-size Wasm section 11 had different
  SHA-256 values, and all artifact-derived bundle files consequently differed.

Therefore, bitwise reproducibility is a **per-candidate acceptance result**, not
a property that can be inherited from an earlier successful commit. The final
handoff commit must supply a new two-clean-build `exact_match: true` report.
Semantic equivalence, equal section sizes, or an ignore list is not sufficient.

Shimmy's independent comparator is:

```bash
python3 scripts/python-runtime-handoff/compare_bundles.py \
  /path/to/clean-build-one \
  /path/to/clean-build-two \
  --output /tmp/python-runtime-reproducibility.json
```

It verifies the complete file set, `SHA256SUMS`, manifest-to-artifact size and
digest, producer commit, `SOURCE_DATE_EPOCH`, and exact bytes. Wasm section
records are diagnostics only; any byte mismatch remains a failure.

### IEEE binary128 / NumPy long double

The producer contains three complementary controls:

1. commit `20cedf5` changed the NumPy Meson cross property from
   `IEEE_DOUBLE_LE` to `IEEE_QUAD_LE`, matching the measured 16-byte WebAssembly
   C ABI;
2. commit `124346f` linked wasi-sdk's
   `libc-printscan-long-double` instead of using a binary64 `strtold` wrapper;
3. runtime evidence in run
   [`29967618919`](https://github.com/bkmashiro/agent-python-runtime/actions/runs/29967618919)
   constructed `np.longdouble("1.0000000000000000000000000000000002")`,
   proved it remains greater than long-double one, and proved conversion to
   binary64 rounds to `1.0`.

The final artifact must repeat that proof through the final Shimmy consumer
path, not only through a producer-side link probe. The canary and strict result
validator are:

```text
scripts/python-runtime-handoff/float128_canary.py
scripts/python-runtime-handoff/validate_float128.py
```

The validator requires 16-byte storage, at least 112 explicit mantissa bits,
precision wider than binary64, preservation of the beyond-binary64 value, and
the expected narrowing behavior.

## ABI is not drop-in compatible yet

The frozen Shimmy loader and the inspected producer expose different contracts:

| Surface | Frozen Shimmy reactor | Inspected agent runtime |
|---|---|---|
| startup | `py_init`, optional `py_prepare` | `runtime_init`, `runtime_prepare` |
| request | `py_exec` or `evaluate` | `execute` |
| response | `resp_buf`, `resp_len` or length-prefixed `evaluate` result | agent-runtime ABI v1 response |
| custom imports | legacy `env` stubs | `agent_runtime_v1.host_call` |
| Python version observed | CPython 3.12-era artifact | CPython/WASI 3.14 producer |

Do not point the current loader at the new artifact and do not add compatibility
stubs to make it appear to work. Loader/Host adaptation, capability policy,
manifest verification, and removal of the legacy polyfill surface must be one
reviewed handoff slice.

## Atomic handoff checklist

The replacement may be accepted only when all of the following are available:

1. signed producer commit and clean source status;
2. two independent complete bundles for that exact commit;
3. Shimmy comparator report with `exact_match: true` and no validation errors;
4. artifact URL/tag, filename, byte size, and SHA-256;
5. manifest schema, ABI version, exact imports/exports, target, execution model,
   source lock, SBOM, notices, and WASI capabilities;
6. an explicit Shimmy Host/loader adaptation with no import-based routing;
7. real final-consumer tests for neutral Python, structured errors, timeout,
   cancellation, fresh state/replacement, and configured capabilities;
8. NumPy binary128 canary output accepted by `validate_float128.py` when the
   selected profile includes NumPy;
9. removal of the old Host bootstrap/polyfills, old artifact pin/LFS object,
   and frozen legacy CI in the same coherent slice;
10. full local and remote gates before any release or deployment claim.

Until then, the existing artifact, loader, ABI, entrypoint, resident path, and
LFS object remain frozen and unchanged.
