# Lambda Feedback Python bundle adapter

This tool turns a Python evaluator package into one self-contained script for Shimmy's Python Reactor. It embeds the evaluator modules and the small `lf_toolkit` compatibility layer, then exposes the Reactor's `evaluation_function` and optional `preview_function` entrypoints.

```bash
python3 tools/lf-bundle-python/lf_bundle_python.py \
  --root examples/lambda-feedback-fixtures/array-equal \
  --adapter-root tools/lf-bundle-python/adapter \
  --eval-entrypoint evaluation_function.evaluation:evaluation_function \
  --out /tmp/array-equal.bundle.py
```

Point `FUNCTION_WASM_PYTHON_SCRIPT` at the generated file when starting the `python-reactor` profile. The selected runtime artifact still determines which third-party imports are available. The compatibility tests use the `numpy-core` profile.

Run the cheap tests with:

```bash
python3 -m unittest discover -s tools/lf-bundle-python -p 'test_*.py' -v
go test ./internal/execution/wasm -run TestPythonReactorDispatcherLambdaFeedbackCompatibility -count=1
```

The Go test skips unless both `SHIMMY_PYTHON_REACTOR_WASM` and `SHIMMY_PYTHON_REACTOR_MANIFEST` point to a matching runtime bundle.
