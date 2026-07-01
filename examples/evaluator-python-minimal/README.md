# Minimal Python evaluator

This is the smallest useful Python evaluator shape for Shimmy's Lambda
Feedback-compatible adapter.

## Functions

```python
def evaluation_function(response, answer, params=None):
    return {"is_correct": response.strip() == answer.strip(), "feedback": "..."}


def preview_function(response, answer=None, params=None):
    return {"preview": response.strip()}
```

Entrypoints for this example:

- eval: `evaluator:evaluation_function`
- preview: `evaluator:preview_function`

## Run through Shimmy locally

From the repository root:

```bash
go run . \
  --log-level error \
  --interface file \
  --command python3 \
  --arg examples/lambda-feedback-adapter/lf_file_worker.py \
  --env FUNCTION_LF_ROOT=$PWD/examples/evaluator-python-minimal \
  --env FUNCTION_LF_ENTRYPOINT=evaluator:evaluation_function \
  --env FUNCTION_LF_PREVIEW_ENTRYPOINT=evaluator:preview_function \
  serve \
  --host 127.0.0.1 \
  --port 8080
```

In another shell:

```bash
curl -fsS \
  -H 'content-type: application/json' \
  -d @examples/evaluator-python-minimal/test_request_eval.json \
  http://127.0.0.1:8080/

curl -fsS \
  -H 'content-type: application/json' \
  -H 'command: preview' \
  -d @examples/evaluator-python-minimal/test_request_preview.json \
  http://127.0.0.1:8080/
```

Expected shapes:

```json
{"command":"eval","result":{"feedback":"Correct.","is_correct":true}}
{"command":"preview","result":{"preview":"Preview: draft answer"}}
```

## Notes for evaluator authors

- Keep the evaluator as plain Python functions; do not start a server inside the
  evaluator package.
- Prefer local variables over module-level mutable state. WASM runtimes may
  restore state between requests, and file-worker runtimes may start fresh
  processes.
- Return JSON-compatible values only: dicts, lists, strings, numbers, booleans,
  and null.
- Use `params` for exercise-specific options. Shimmy reserves `_lf_root`,
  `_lf_entrypoint`, `root`, and `entrypoint` when routing per-request evaluator
  packages.
