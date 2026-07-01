# Lambda Feedback compatibility fixtures

These small fixtures mirror common shapes from real Lambda Feedback evaluator
repositories while staying deterministic and dependency-light.

- `boilerplate-python`: toolkit-style `create_server()`, `@server.eval`,
  `@server.preview`, and `lf_toolkit.evaluation.Result`.
- `relative-preview`: package-relative imports plus a two-argument
  `preview_function(response, params)` variant.

They are compatibility fixtures, not production evaluators. The local adapter
uses the test-only `lf_toolkit` shim under `examples/lambda-feedback-adapter/`.

## Local adapter and Shimmy file-worker smoke

Exercise the backend-independent adapter directly:

```bash
scripts/demo-lambda-feedback-fixtures.sh all
```

Exercise the same fixtures through Shimmy's existing `FUNCTION_INTERFACE=file`
path:

```bash
scripts/demo-lambda-feedback-file-worker.sh all
```

The file worker is configured with `LF_EVAL_ROOT`, `LF_EVAL_ENTRYPOINT`, and
optionally command-specific `LF_EVAL_ENTRYPOINT` / `LF_PREVIEW_ENTRYPOINT` envs.
Per-request fixture tests may also pass `root` and `entrypoint` inside the
request `params` object; those keys are removed before calling the evaluator.
