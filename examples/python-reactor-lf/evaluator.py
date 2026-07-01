"""Small Lambda Feedback-style evaluator for python-reactor benchmark smoke.

This file is intentionally pre-bundled as a single script so the generic PR can
exercise the existing reactor-python runtime without porting the old package
bundler and historical tests.
"""


def evaluation_function(response, answer, params=None):
    params = params or {}
    cases = params.get("cases") or []
    return {
        "is_correct": response == answer,
        "feedback": f"matched={response == answer}; response_len={len(response)}; cases={len(cases)}",
    }


def preview_function(response, answer=None, params=None):
    params = params or {}
    return {
        "preview": {
            "normalized": response.strip(),
            "params_keys": sorted(params.keys()),
        }
    }
