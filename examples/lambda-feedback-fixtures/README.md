# Lambda Feedback compatibility fixtures

These small fixtures mirror common shapes from real Lambda Feedback evaluator
repositories while staying deterministic and dependency-light.

- `boilerplate-python`: toolkit-style `create_server()`, `@server.eval`,
  `@server.preview`, and `lf_toolkit.evaluation.Result`.
- `relative-preview`: package-relative imports plus a two-argument
  `preview_function(response, params)` variant.

They are compatibility fixtures, not production evaluators. The local adapter
uses the test-only `lf_toolkit` shim under `examples/lambda-feedback-adapter/`.
