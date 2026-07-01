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

The file worker uses only three package-mode envs: `FUNCTION_LF_ROOT`,
`FUNCTION_LF_ENTRYPOINT`, and optional `FUNCTION_LF_PREVIEW_ENTRYPOINT` when
preview lives at a different function. Per-request fixture tests may also pass
`root` and `entrypoint` inside the request `params` object; those keys are
removed before calling the evaluator.

File-worker calls now run through the adapter's best-effort evaluator hygiene
context: per-request temporary cwd, cwd/`sys.path`/environment restore, evaluator
stdout/stderr capture, and cleanup of modules imported from the evaluator root.
This reduces accidental pollution in demos and CI. It is not a security sandbox;
malicious native Python evaluators still require a real isolation boundary.

See `docs/lambda-feedback-hygiene-roadmap.md` for the backend-independent
hygiene/compatibility follow-up track.
