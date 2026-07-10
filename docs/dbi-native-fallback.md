# DynamoRIO / DBI native security wrapper

Shimmy can optionally launch native `file` or `rpc` workers under DynamoRIO. DBI is a transparent security wrapper around a process-backed worker; it is **not** an IO interface or worker protocol.

## Scope

Supported:

```text
FUNCTION_INTERFACE=file + DBI security ✅
FUNCTION_INTERFACE=rpc  + DBI security ✅
```

Rejected:

```text
FUNCTION_INTERFACE=wasm            + DBI security ❌
FUNCTION_INTERFACE=python-wasm     + DBI security ❌
FUNCTION_INTERFACE=reactor-python  + DBI security ❌
FUNCTION_INTERFACE=pyodide         + DBI security ❌
```

WASM and reactor evaluators execute inside the Shimmy process rather than as a separate native worker process. Pyodide has its own managed Node runner. Shimmy therefore fails closed if DBI security is enabled for any interface other than `file` or `rpc`.

## Command wrapping

Without DBI:

```text
<worker> <worker args...>
```

With DBI enabled:

```text
drrun [DBI options...] [-c client.so] -- <worker> <worker args...>
```

The original `FUNCTION_INTERFACE` and RPC transport remain unchanged.

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `FUNCTION_DBI_SECURITY_ENABLED` | unset / false | Enable the DBI security wrapper. Accepts Go boolean values such as `true`, `false`, `1`, and `0`. |
| `FUNCTION_DBI_CONFIG_PATH` | unset | Optional policy/config file consumed by the DBI client through the inherited environment. Shimmy verifies that it is readable before launch. |
| `FUNCTION_DBI_DRRUN` | `drrun` | DynamoRIO launcher path. |
| `FUNCTION_DBI_CLIENT` | unset | Optional DynamoRIO client `.so`. |
| `FUNCTION_DBI_OPTIONS` | unset | Extra whitespace-separated `drrun` options. |

Example transient file worker:

```bash
FUNCTION_INTERFACE=file \
FUNCTION_COMMAND=python3 \
FUNCTION_ARG=examples/lambda-feedback-adapter/run_lf_eval.py \
FUNCTION_DBI_SECURITY_ENABLED=true \
FUNCTION_DBI_DRRUN=/opt/dynamorio/bin64/drrun \
FUNCTION_DBI_CLIENT=/opt/shimmy-dbi/path_policy.so \
FUNCTION_DBI_CONFIG_PATH=/opt/shimmy-dbi/policy.yaml \
FUNCTION_DBI_OPTIONS='-logdir /tmp/drlogs' \
shimmy run
```

Example persistent RPC worker:

```bash
FUNCTION_INTERFACE=rpc \
FUNCTION_COMMAND=/opt/evaluator/worker \
FUNCTION_DBI_SECURITY_ENABLED=true \
FUNCTION_DBI_CLIENT=/opt/shimmy-dbi/path_policy.so \
shimmy run
```

## Fresh-state boundary

- `file`: each request receives a newly launched worker process. This is the recommended DBI final-fallback mode when strict process-level freshness matters.
- `rpc`: one instrumented worker process is reused. DynamoRIO does not restore native heap, globals, threads, file descriptors, or runtime state between requests.

DBI policy and worker lifecycle are separate concerns. Enabling the wrapper does not make persistent RPC state fresh.

## Security boundary

DynamoRIO provides interception and instrumentation hooks; it does not provide a complete policy automatically. The configured client must explicitly enforce the required filesystem, network, process, executable-memory, environment, and descriptor rules. Missing or unreadable configured policy files fail startup, but policy completeness still requires dedicated tests.

The verified 2026-07-10 Lambda probe covered a bounded exact-path `open/openat` denial returning `EPERM`. It did not prove a complete native sandbox or Lean evaluator compatibility.
