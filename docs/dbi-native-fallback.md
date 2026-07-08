# DBI native fallback notes

Status: experimental compatibility path. WASM/Pyodide/reactor-python remain the fast/primary paths.

## DynamoRIO finding

The Lambda DynamoRIO smoke in `bkmashiro/shimmy-sandbox-prototypes` showed that DBI is not blocked outright on Lambda:

- minimal `drrun` package starts in Lambda;
- a DynamoRIO client can initialize for a packaged native probe;
- `dr_register_filter_syscall_event` + `dr_register_pre_syscall_event` can intercept `openat` and force `EPERM`;
- the existing syscall filter crash was a client bug (`dr_get_time(NULL)`) rather than a Lambda sandbox limitation.

The important API lesson is that pre/post syscall callbacks require a filter callback. Registering only `dr_register_pre_syscall_event` can make the client appear to load while `openat` passes through.

## Shimmy dispatcher path

`FUNCTION_INTERFACE=dbi` is a thin wrapper around the existing worker protocols. It rewrites the configured worker command to run under DynamoRIO and then uses the normal RPC or file adapter.

Environment variables:

| Variable | Default | Meaning |
|---|---|---|
| `FUNCTION_COMMAND` | required | Native worker command, e.g. `python3`. |
| `FUNCTION_DBI_DRRUN` | `drrun` | Path/name of DynamoRIO `drrun`. |
| `FUNCTION_DBI_CLIENT` | unset | Optional DynamoRIO client `.so`. |
| `FUNCTION_DBI_OPTIONS` | unset | Extra whitespace-separated `drrun` options. |
| `FUNCTION_DBI_TARGET_INTERFACE` | `rpc` | Underlying Shimmy worker protocol: `rpc` or `file`. |

Example:

```bash
FUNCTION_INTERFACE=dbi \
FUNCTION_COMMAND=python3 \
FUNCTION_ARG=examples/lambda-feedback-adapter/run_lf_eval.py \
FUNCTION_DBI_DRRUN=/opt/dynamorio/bin64/drrun \
FUNCTION_DBI_CLIENT=/opt/shimmy-dbi/open_log.so \
FUNCTION_DBI_OPTIONS='-logdir /tmp/drlogs' \
FUNCTION_DBI_TARGET_INTERFACE=file \
shimmy run
```

## Local Docker smoke

On the local Apple Silicon Mac, an AArch64 DynamoRIO build can run the existing Python Lambda Feedback adapter under an open-log client:

```text
[dr-open-log] client init pid=3020
[dr-open-log] saw open-family syscall 56
...
{"is_correct": true, "feedback": "Correct! 1.0 matches 1.0 within tolerance 1e-09.", "absolute_error": 0.0}
```

The x86_64 DynamoRIO build under Docker's amd64 emulation crashes before Python starts, even without a client, so that result is treated as a local emulation artifact rather than Lambda evidence. Lambda x86_64 still needs a direct Python-runtime smoke if we want x86 confidence.

## Remaining risk

This is now plausible for packaged native probes and for native AArch64 Docker Python. It is not yet proven for large dynamic runtimes such as Lean or Mathematica. The next checks should focus on Lambda x86 Python, dynamic loader compatibility, threads/signals, startup overhead, and policy coverage.
