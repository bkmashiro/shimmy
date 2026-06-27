# Lambda Feedback Python adapter shim

This directory contains a minimal adapter used by the Pyodide package-mode
example. It is not a full `lf_toolkit` replacement; it only provides the small
loader/invoker/normalizer surface needed by local compatibility tests.

- `lf_compat_adapter.py` loads `module:function` entrypoints.
- It calls `eval(response, answer, params)` and supports preview handlers with
  either `preview(response, params)` or `preview(response, answer, params)`.
- It normalizes dicts, dataclasses, Pydantic-style objects, and simple toolkit
  objects into JSON dictionaries.
