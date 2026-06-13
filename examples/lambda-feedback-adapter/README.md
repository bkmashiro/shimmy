# Lambda Feedback Python adapter shim

This directory contains a minimal adapter + compatibility shim used for local
compatibility testing against real `lambda-feedback` style evaluator packages.

- `lf_compat_adapter.py` implements a tiny loader/invoker/normalizer used by
  backend runners.
- `lf_toolkit/` is a **test-only** mock of `lf_toolkit` so fixtures can
  import `Result`, `Params`, `Preview`, `create_server`, and `run` in normal
  CPython without pulling the full toolkit package.

## Demo commands

```bash
# Adapter unit tests.
python3 -m pytest examples/lambda-feedback-adapter -q

# Direct local CPython invocation against a fixture package.
python3 examples/lambda-feedback-adapter/run_lf_eval.py \
  --root examples/lambda-feedback-fixtures/boilerplate-python \
  --eval-entrypoint evaluation_function.evaluation:evaluation_function \
  --preview-entrypoint evaluation_function.preview:preview_function \
  --method eval \
  --input '{"response":"2","answer":"2","params":{}}'

# Full local/Pyodide fixture smoke suite.
scripts/demo-lambda-feedback-fixtures.sh all
```

## Notes

`lf_toolkit.run()` is intentionally a no-op in this adapter slice. It is enough
for fixture imports and unit tests, but it is **not** a full runtime server.
